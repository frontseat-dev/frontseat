// Package runner is frontseat-runner: the runner pool of the grid's
// runner sandboxes (Pool), the runner in each (Confined), and the runner
// of the workers that run tests with services. A worker calls the last as
// it calls any Buildbarn runner (buildbarn.runner.Runner), and for each
// action it makes, through the sandbox API over the OpenShell gateway, a
// sandbox for each service the action's root names
// (frontseat.sandbox.v1alpha1.RunnerSandbox, at
// .frontseat/runtime/runner.binpb), in dependency order, then one for the
// test. Each holds only a private loopback, and is granted the endpoints
// it may reach by routes: frontseat-action's relays between its loopback
// and Unix sockets in directories the runner shares, which the sandbox
// runs before its own command, in its one workload (SandboxSpec.wrapper).
// The test's sandbox runs Frontseat's runner (Confined) over the action's
// directory and nothing else of the worker's. Once each service is ready
// the runner hands the test sandbox's runner the request as it came,
// returns its answer, and removes dependents before dependencies.
//
// What the action's root says is the action's, and is checked here as
// the daemon checked it: whatever wrote the file, an image is named by
// its registry and digest, pulled through the proxy where one is
// configured, a name is a container's, and nothing is handed to a runtime
// as anything else.
//
// It is told by its flags (frontseat-runner --help), which Config holds.
package runner

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/frontseat-dev/frontseat/common/confine"
	runnerv1 "github.com/frontseat-dev/frontseat/common/runner/runnerapi/frontseat/runner/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
	"github.com/frontseat-dev/frontseat/core/httpserver"
	"github.com/frontseat-dev/frontseat/core/image"
	"github.com/frontseat-dev/frontseat/core/page"
	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// runnerSandbox is where in an action's root what its sandbox holds is,
// as frontseat.sandbox.v1alpha1.RunnerSandbox says.
const runnerSandbox = testservice.RunnerDescription

// label marks every runner sandbox this runner makes, so that one left
// by a runner that ended is found and removed when the next starts.
const label = "dev.frontseat.runner-sandbox"

type runner struct {
	sandboxes               sandboxv1alpha1connect.SandboxServiceClient
	worker, host, image, bb string
	// own is the directory, beneath the worker's, that only the worker
	// may enter: the runner's socket and each sandbox's own are there.
	own string
	// registries maps a registry an image may come from to where the
	// proxy serves it.
	registries map[string]string
	// removing are the removals of sandboxes that answered, which the
	// runner waits for before it ends.
	removing  sync.WaitGroup
	admitOnce sync.Once
	admission *semaphore.Weighted
	creations creations
}

// Config is what the runner is told.
type Config struct {
	// Socket is where it listens, in a directory of its own in the
	// worker's, which only the worker may enter.
	Socket string
	// Worker is the worker's directory as the runner sees it, and Host
	// the same as the container runtime sees it.
	Worker, Host string
	// Image is the image actions run in.
	Image string
	// Gateway is the OpenShell gateway that makes every sandbox, as a URL,
	// and Identity the directory of what it knows this runner by;
	// Runtime is its container runtime's command line (podman), asked
	// what an image is started with.
	Gateway, Identity, Runtime string
	// BB is the directory the runner and the confiners are in, as the
	// container runtime sees it.
	BB string
	// Registries maps each registry an image may come from to the
	// proxy's address for it (docker.io: localhost:8081/dockerhub); empty
	// where there is no proxy, and images are pulled from their registry.
	Registries map[string]string
	// State is the directory the runner keeps what is its own in: the
	// sandbox service's records and the socket it is asked on.
	State string
}

// Serve runs the runner until ctx ends, as it is told, and returns once
// what it answered is removed.
func Serve(ctx context.Context, config Config) error {
	r := &runner{worker: config.Worker, host: config.Host, image: config.Image, bb: config.BB, registries: config.Registries}
	socket, state := config.Socket, config.State
	for name, given := range map[string]string{"--socket": socket, "--worker": r.worker, "--worker-host": r.host, "--image": r.image, "--bb": r.bb, "--state": state, "--gateway": config.Gateway, "--identity": config.Identity, "--runtime": config.Runtime} {
		if given == "" {
			return fmt.Errorf("%s is not given", name)
		}
	}
	r.own = filepath.Dir(socket)
	if rel, err := filepath.Rel(r.worker, r.own); err != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)) {
		return fmt.Errorf("--socket is to be in a directory of its own in %s", r.worker)
	}
	// The runner's own directory is the worker's (root's): no other user
	// lists it, and its socket is root's alone. Others may pass through
	// it, as the gateway's rootless runtime must, to bind each test's
	// directories, which are named by 128 random bits and bound into no
	// sandbox but the test's own.
	if err := os.Mkdir(r.own, 0o711); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	// Anyone may add to the worker's directory: what is there by the
	// name is used only where it is a directory of root's, not a link
	// another put there.
	switch info, err := os.Lstat(r.own); {
	case err != nil:
		return fmt.Errorf("the runner's own directory: %w", err)
	case !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != 0:
		return fmt.Errorf("%s is not a directory of root's", r.own)
	}
	if err := os.Chmod(r.own, 0o711); err != nil {
		return err
	}

	// The sandbox API, served here over the OpenShell gateway, is how a
	// runner sandbox and each service's are made: it is reached on a
	// socket of this process's own, as any client reaches it.
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	sandboxes, err := sandbox.New(sandbox.Options{PublicURL: "http://frontseat-runner", State: state, Gateway: &sandbox.Gateway{
		Address: config.Gateway, Identity: config.Identity, Runtime: config.Runtime, ReadAll: true, HostVolumes: true,
	}})
	if err != nil {
		return err
	}
	r.creations.next = sandboxes.Handler()
	api := filepath.Join(state, "sandbox.sock")
	os.Remove(api)
	apiListener, err := net.Listen("unix", api)
	if err != nil {
		return err
	}
	serving, stopServing := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- httpserver.Serve(serving, apiListener, &r.creations) }()
	defer func() {
		stopServing()
		<-served
	}()
	dial := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", api)
	}}}
	r.sandboxes = sandboxv1alpha1connect.NewSandboxServiceClient(connect.NewClient(connecthttp.NewTransport(dial, "http://frontseat-runner")))
	if err := r.sweep(ctx); err != nil {
		return err
	}

	os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	// The worker's alone, root's, to ask.
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	server := grpc.NewServer(grpc.ForceServerCodec(raw{}), grpc.UnknownServiceHandler(r.handle))
	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()
	log.Printf("frontseat-runner: serving on %s, making runner sandboxes of %s", socket, r.image)
	err = server.Serve(listener)
	// What answered is removed before the runner ends.
	r.removing.Wait()
	return err
}

// labelled are the sandboxes that carry a label of a value, every page
// of them.
func (r *runner) labelled(ctx context.Context, label, value string) ([]*sandboxv1.Sandbox, error) {
	return page.All(func(token string) ([]*sandboxv1.Sandbox, string, error) {
		page, err := r.sandboxes.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{Labels: map[string]string{label: value}, PageToken: token})
		return page.GetSandboxes(), page.GetNextPageToken(), err
	})
}

// sweep removes the runner sandboxes a runner that ended left.
func (r *runner) sweep(ctx context.Context) error {
	remaining, err := r.labelled(ctx, label, "1")
	if err != nil {
		return fmt.Errorf("finding sandboxes left: %w", err)
	}
	slices.SortFunc(remaining, func(a, b *sandboxv1.Sandbox) int {
		return strings.Compare(b.GetSpec().GetLabels()["dev.frontseat.runner-order"], a.GetSpec().GetLabels()["dev.frontseat.runner-order"])
	})
	for _, s := range remaining {
		if s.GetSpec().GetLabels()[label] != "1" {
			return fmt.Errorf("restart cleanup encountered an unowned sandbox")
		}
		if _, err := r.sandboxes.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: s.GetId()}); err != nil {
			return fmt.Errorf("removing %s, left: %w", s.GetId(), err)
		}
	}
	// The runner's private directory is root-owned and inaccessible to actions.
	// Only attempt directories use these random 128-bit names. Remove them only
	// after every recorded sandbox was removed; keep sockets and unknown entries.
	entries, err := os.ReadDir(r.own)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 32 {
			continue
		}
		if _, err := hex.DecodeString(entry.Name()); err != nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.own, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// raw passes a message on as it came.
type raw struct{}

func (raw) Marshal(v any) ([]byte, error)   { return *(v.(*[]byte)), nil }
func (raw) Unmarshal(d []byte, v any) error { *(v.(*[]byte)) = append([]byte(nil), d...); return nil }
func (raw) Name() string                    { return "proto" }

func (r *runner) handle(_ any, stream grpc.ServerStream) error {
	method, _ := grpc.MethodFromServerStream(stream)
	var request []byte
	if err := stream.RecvMsg(&request); err != nil {
		return err
	}
	switch method {
	case "/buildbarn.runner.Runner/CheckReadiness":
		empty := []byte{}
		return stream.SendMsg(&empty)
	case "/buildbarn.runner.Runner/Run":
		answer, err := r.run(stream.Context(), request)
		if err != nil {
			return err
		}
		return stream.SendMsg(&answer)
	}
	return status.Errorf(codes.Unimplemented, "frontseat-runner serves no %s", method)
}

// described reads what the action's root says its sandbox holds, within
// that root, and checks it.
func (r *runner) described(root string) (*runnerv1.RunnerSandbox, error) {
	within, err := os.OpenRoot(filepath.Join(r.worker, "build", filepath.FromSlash(root)))
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "the action's root: %v", err)
	}
	defer within.Close()
	f, err := within.Open(runnerSandbox)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "the action's root says nothing of a runner sandbox (%s): %v", runnerSandbox, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, mostDescribed+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > mostDescribed {
		return nil, status.Errorf(codes.InvalidArgument, "%s is more than %d bytes", runnerSandbox, mostDescribed)
	}
	var described runnerv1.RunnerSandbox
	if err := proto.Unmarshal(raw, &described); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%s: %v", runnerSandbox, err)
	}
	if err := r.checkDescription(&described); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%s: %v", runnerSandbox, err)
	}
	return &described, nil
}

// mostDescribed is the most a runner sandbox's file may hold.
const mostDescribed = 1 << 20

// proxied is an image as the proxy serves it, where one is configured:
// named by its registry and its digest, from a registry the proxy stands
// in front of, which the runtime pulls it from and from nowhere else.
// Where none is, the image as written, by its digest.
func (r *runner) proxied(written string) (string, error) {
	// What was written is not repeated: any client of the grid writes
	// it, and what image.Read refuses it with repeats none of it.
	pinned, err := image.Read(written)
	if err != nil {
		return "", fmt.Errorf("a service's image %w", err)
	}
	if len(r.registries) == 0 {
		// No proxy is configured: the gateway's runtime pulls the image
		// from its registry, by its digest.
		return written, nil
	}
	proxy, ok := r.registries[pinned.Registry]
	if !ok {
		return "", errors.New("the image is of a registry the proxy does not stand in front of")
	}
	return proxy + "/" + pinned.Path + "@" + pinned.Digest, nil
}

func (r *runner) running(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	waited, err := r.sandboxes.WaitSandbox(ctx, &sandboxv1.WaitSandboxRequest{SandboxId: id})
	if err != nil {
		return err
	}
	switch s := waited.GetSandbox(); s.GetState() {
	case sandboxv1.Sandbox_STATE_RUNNING:
		return nil
	case sandboxv1.Sandbox_STATE_FAILED:
		return fmt.Errorf("it failed: %s", s.GetFailure())
	default:
		return fmt.Errorf("it is %s", s.GetState())
	}
}

// failed says why a sandbox failed, where it has: one of its containers
// ended, and is waited on no longer.
func (r *runner) failed(ctx context.Context, id string) error {
	now, err := r.sandboxes.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: id})
	if err != nil {
		return err
	}
	switch now.GetSandbox().GetState() {
	case sandboxv1.Sandbox_STATE_FAILED:
		return fmt.Errorf("the sandbox failed: %s", now.GetSandbox().GetFailure())
	case sandboxv1.Sandbox_STATE_ENDING:
		return fmt.Errorf("the sandbox is ending")
	}
	return nil
}

// ready holds until each of a service's conditions holds, in order, or
// its time is up, or the sandbox has failed. A port is reached, and asked
// over HTTP, from here, through the socket of the service's route to it,
// which its sandbox's routes relay to its loopback; a command runs in the
// service's sandbox. Its image need hold nothing to check it with.
func (a *attempt) ready(ctx context.Context, sb string, s *runnerv1.RunnerService) error {
	said := s.GetService()
	deadline := time.Now().Add(said.GetTimeout().AsDuration())
	for _, check := range said.GetReady() {
		port := said.GetPorts()[testservice.PortOf(check)]
		socket := filepath.Join(a.mine, "routes", s.GetName(), strconv.Itoa(int(port)), "socket")
		var try func() error
		switch c := check.GetCheck().(type) {
		case *typev1.ServiceReady_Port:
			try = func() error { return reached(ctx, socket) }
		case *typev1.ServiceReady_Http:
			// The expression is read once, not at every try.
			matches, err := regexp.Compile(c.Http.GetBody())
			if err != nil {
				return err
			}
			try = func() error { return answered(ctx, socket, c.Http.GetPath(), matches) }
		case *typev1.ServiceReady_Command:
			args := c.Command.GetArguments()
			if s.Program != nil {
				var err error
				if args, err = programReadiness(s.Program, args); err != nil {
					return err
				}
			}
			try = func() error {
				answer, err := a.r.sandboxes.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: sb, Arguments: args, Timeout: durationpb.New(10 * time.Second)})
				if err != nil {
					return err
				}
				if answer.GetExitCode() != 0 {
					return fmt.Errorf("the readiness command: exit %d: %s", answer.GetExitCode(), strings.TrimSpace(string(answer.GetOutput())))
				}
				return nil
			}
		default:
			return fmt.Errorf("a readiness check of no kind this runner knows")
		}
		err := poll.Until(ctx, 200*time.Millisecond, func() (bool, error) {
			err := try()
			if err == nil {
				return true, nil
			}
			if failed := a.r.failed(ctx, sb); failed != nil {
				return false, failed
			}
			if time.Now().After(deadline) {
				return false, fmt.Errorf("not ready after %s: %v", said.GetTimeout().AsDuration(), err)
			}
			return false, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// reached says whether a service answers a connection to a port, through
// the socket of its route: the route relays a connection on only where the
// service accepted it, and closes it where nothing listens. One the service
// holds open, or speaks first on, is reached.
func reached(ctx context.Context, socket string) error {
	dialing, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := confine.DialSocket(dialing, socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		return err
	}
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil
		}
		return fmt.Errorf("the port closed the connection: nothing listens on it yet")
	}
	return nil
}

// saidOf is how much of what a service answered a failed check repeats.
const saidOf = 512

// answered says whether a service answers a request for a path with
// success and a body the expression matches, over HTTP through the socket
// of its route.
func answered(ctx context.Context, socket, path string, matches *regexp.Regexp) error {
	// One connection for one request, closed with it: a check tried again
	// and again keeps nothing open.
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return confine.DialSocket(ctx, socket)
	}}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://service"+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	said, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode >= 400 || !matches.Match(said) {
		// The beginning of what it answered says why; the rest is the
		// service's to keep. A character cut in two at the end is left out.
		return fmt.Errorf("GET %s: %s: %s", path, response.Status, strings.TrimSpace(strings.ToValidUTF8(string(said[:min(len(said), saidOf)]), "")))
	}
	return nil
}

func (r *runner) listening(ctx context.Context, sb, socket string) error {
	deadline := time.Now().Add(time.Minute)
	return poll.Until(ctx, 50*time.Millisecond, func() (bool, error) {
		if info, err := os.Lstat(socket); err == nil && info.Mode().Type() == os.ModeSocket {
			return true, nil
		}
		if err := r.failed(ctx, sb); err != nil {
			return false, err
		}
		if time.Now().After(deadline) {
			return false, errors.New("its socket did not appear within a minute")
		}
		return false, nil
	})
}

// paths is what a RunRequest names of the build directory.
type paths struct {
	// directory is the one directory of the build directory it is in.
	directory string
	// root is its input root.
	root string
}

// pathsOf reads the paths a RunRequest (buildbarn.runner.RunRequest)
// names: stdout_path, stderr_path, input_root_directory,
// temporary_directory and server_logs_directory, fields 4 to 8, each
// relative to the build directory. The worker names all five in one
// directory of it (local_build_executor.go), and so must every request:
// one that leaves out a path, names one outside the directory, or is not
// well formed is refused, since the sandbox is given that directory alone.
func pathsOf(request []byte) (paths, error) {
	found := map[protowire.Number]string{}
	for b := request; len(b) > 0; {
		number, kind, n := protowire.ConsumeTag(b)
		if n < 0 {
			return paths{}, errors.New("a malformed request")
		}
		b = b[n:]
		if kind == protowire.BytesType && number >= 4 && number <= 8 {
			v, m := protowire.ConsumeString(b)
			if m < 0 {
				return paths{}, errors.New("a malformed request")
			}
			found[number] = v
			b = b[m:]
			continue
		}
		m := protowire.ConsumeFieldValue(number, kind, b)
		if m < 0 {
			return paths{}, errors.New("a malformed request")
		}
		b = b[m:]
	}
	directory, _, _ := strings.Cut(found[4], "/")
	if directory == "" || directory == "." || directory == ".." || strings.ContainsAny(directory, `\`) {
		return paths{}, errors.New("the request's output is in no directory of the build directory")
	}
	for number := protowire.Number(4); number <= 8; number++ {
		p, ok := found[number]
		if !ok {
			return paths{}, fmt.Errorf("the request names no field %d", number)
		}
		if !strings.HasPrefix(p, directory+"/") || path.Clean(p) != p || strings.Contains(p, "..") {
			return paths{}, fmt.Errorf("the path of field %d is outside the request's directory, or not written plainly", number)
		}
	}
	return paths{directory: directory, root: found[6]}, nil
}
