package runner

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	bbrunner "github.com/buildbarn/bb-remote-execution/pkg/proto/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
)

// Confined is the runner of a runner sandbox: what the worker asks to run
// each action, over Buildbarn's runner protocol, in place of bb_runner.
// It runs a command only where it begins with Frontseat's confiner, as
// the daemon writes every action's (frontseat-action, of a form installed
// at /bb), held to the action's own input root: any other command, as
// any client of the grid may send, is refused, never run as it is. It
// runs one action at a time, and keeps whatever an action starts beneath
// it (a subreaper), so that it ends all of it before the next, whatever
// the action's confiner did or had done to it.
type Confined struct {
	bbrunner.UnimplementedRunnerServer

	// Build is the slot's build directory, what the worker's paths are
	// relative to (/slot/build).
	Build string
	// Confiners is what a confiner's path begins with, its form after it
	// (confine.Confiners where it is empty).
	Confiners string
	// Temporaries is where a confiner makes an action's own temporary
	// directory (confine.Temporaries where it is empty).
	Temporaries string

	// one runs one action at a time.
	one sync.Mutex
}

// ServeConfined serves the runner on a unix socket at listen until ctx
// ends. The runner is a subreaper from its start.
func ServeConfined(ctx context.Context, listen string, r *Confined) error {
	if err := subreaper(); err != nil {
		return err
	}
	if err := os.Remove(listen); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	l, err := net.Listen("unix", listen)
	if err != nil {
		return err
	}
	server := grpc.NewServer()
	bbrunner.RegisterRunnerServer(server, r)
	go func() {
		<-ctx.Done()
		server.Stop()
	}()
	return server.Serve(l)
}

// CheckReadiness answers that a path of the build directory the worker
// names is there, as bb_runner does.
func (r *Confined) CheckReadiness(_ context.Context, request *bbrunner.CheckReadinessRequest) (*emptypb.Empty, error) {
	at, err := r.within(request.GetPath())
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(at); err != nil {
		return nil, status.Errorf(codes.Internal, "the path is not in the build directory: %v", cause(err))
	}
	return &emptypb.Empty{}, nil
}

// Run runs an action's command, where it is one the runner runs (confined),
// with the environment the request gives and nothing else, writing its
// output where the request says, and ends everything it left before it
// answers.
func (r *Confined) Run(ctx context.Context, request *bbrunner.RunRequest) (*bbrunner.RunResponse, error) {
	r.one.Lock()
	defer r.one.Unlock()
	// What an action before it left, were the runner ever to have been
	// left anything, is ended before it starts, and so is what it leaves.
	r.clear()
	defer r.clear()
	root, err := r.within(request.GetInputRootDirectory())
	if err != nil {
		return nil, err
	}
	dir, err := within(root, request.GetWorkingDirectory())
	if err != nil {
		return nil, err
	}
	confiners := r.Confiners
	if confiners == "" {
		confiners = confine.Confiners
	}
	if err := confined(confiners, request.GetArguments(), dir, root); err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	// The confiner finds its root from where it runs, as the system says
	// it is: a link in the input tree that the working directory leads
	// through would take it elsewhere than the root checked above.
	if err := unlinked(root, dir); err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	stdout, err := r.output(request.GetStdoutPath())
	if err != nil {
		return nil, err
	}
	defer stdout.Close()
	stderr, err := r.output(request.GetStderrPath())
	if err != nil {
		return nil, err
	}
	defer stderr.Close()
	command := exec.CommandContext(ctx, request.GetArguments()[0], request.GetArguments()[1:]...)
	command.Dir = dir
	command.Env = make([]string, 0, len(request.GetEnvironmentVariables()))
	for name, value := range request.GetEnvironmentVariables() {
		if name != "PWD" {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	// Where it runs, as the runner put it there, never as the request says.
	command.Env = append(command.Env, "PWD="+dir)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "starting the action: %v", err)
	}
	if err := command.Wait(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, status.Errorf(codes.Internal, "waiting for the action: %v", err)
		}
	}
	return &bbrunner.RunResponse{ExitCode: int64(command.ProcessState.ExitCode())}, nil
}

// clear ends every process an action left, and then removes every
// action's temporary directory: one action runs at a time, so none is a
// running action's, and one a killed action could not empty is not read
// by the next.
func (r *Confined) clear() {
	end()
	temporaries := r.Temporaries
	if temporaries == "" {
		temporaries = confine.Temporaries
	}
	confine.Clear(temporaries)
}

// confined returns why a command is not one the runner runs: one that
// begins with a confiner installed with the runner, of a form (its 64
// hexadecimal digits) at the confiners' path, and no other program; then
// a spec; then the way up from where it runs to its root, which is the
// action's input root. What the confiner is told is its own to check;
// what reaches it is a command the confiner confines, to the root it was
// given.
func confined(confiners string, arguments []string, dir, root string) error {
	if len(arguments) < 4 || !strings.HasPrefix(arguments[0], confiners) || !form.MatchString(strings.TrimPrefix(arguments[0], confiners)) {
		return errors.New("an action's command begins with Frontseat's confiner, and this one does not: it is not run")
	}
	if !strings.HasPrefix(arguments[1], "{") {
		return errors.New("an action's confiner is given a spec, and this one is not: it is not run")
	}
	up := arguments[2]
	for _, step := range strings.Split(up, "/") {
		if step != ".." && step != "." {
			return errors.New("the way up to the action's root is not .. or . alone: it is not run")
		}
	}
	if filepath.Join(dir, up) != root {
		return errors.New("the way up from where the action runs leads elsewhere than its input root: it is not run")
	}
	return nil
}

// form is a confiner's form as its name holds it.
var form = regexp.MustCompile("^[0-9a-f]{64}$")

// unlinked returns why a directory beneath a root is not reached from it
// as it is written: a link on the way leads elsewhere.
func unlinked(root, dir string) error {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "the action's input root: %v", err)
	}
	reached, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "where the action runs: %v", err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || reached != filepath.Join(real, rel) {
		return errors.New("where the action runs is reached through a link, which leads elsewhere than its input root: it is not run")
	}
	return nil
}

// within is a path the worker gives, beneath the build directory.
func (r *Confined) within(path string) (string, error) {
	return within(r.Build, path)
}

// within is a path beneath a directory, refused where it would leave it.
func within(dir, path string) (string, error) {
	if path == "" {
		return dir, nil
	}
	if !filepath.IsLocal(path) {
		return "", status.Error(codes.InvalidArgument, "a path the request names is not beneath its directory")
	}
	return filepath.Join(dir, path), nil
}

// cause is why the system refused a path, without the path: a client of
// the grid wrote it, and a refusal does not repeat it.
func cause(err error) error {
	var at *fs.PathError
	if errors.As(err, &at) {
		return at.Err
	}
	return err
}

// output opens an output file of the action's, made anew in the build
// directory, as bb_runner makes one: never one that is there already.
func (r *Confined) output(path string) (*os.File, error) {
	at, err := r.within(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(at, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND|syscall.O_NOFOLLOW, 0o666)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "opening an output file of the action's: %v", cause(err))
	}
	return file, nil
}
