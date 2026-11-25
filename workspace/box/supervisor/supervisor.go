// Package supervisor is a box's supervisor, the program frontseat-box
// beneath its main: the one process of a workspace's sandbox that works
// for the control plane. It holds the sandbox's lock, fills the
// workspace's directory as it is told over the source API, and serves
// the shell. It knows no build: a build needs the directory, and not
// whoever fills it.
package supervisor

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/frontseat-dev/frontseat/common/record/buildfile"
	"github.com/frontseat-dev/frontseat/core/httpserver"
	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/core/telemetry"
	"github.com/frontseat-dev/frontseat/workspace/box/source"
)

// program is the supervisor's program, as it names itself.
const program = "frontseat-box"

// Config is what a supervisor is told.
type Config struct {
	// Source is the address it serves the source API on.
	Source string
	// Shell is the address it serves SSH on, to AuthorizedKey, a line of
	// authorized_keys; with no key, no shell is served.
	Shell, AuthorizedKey string
	// Workspace is the workspace's directory, and Name the workspace it
	// holds, which the control plane that made the sandbox says.
	Workspace, Name string
	// Lock is the sandbox's lock; empty, where every program of the
	// sandbox finds it (ports.Lock).
	Lock string
	// Build is what a build in the workspace is told, as the control
	// plane said it: the grid, and the plugins of the box. With a grid,
	// it is written to Settings, which is where a build there reads it
	// (ports.Settings where empty); with none, what is there is removed.
	Build    buildfile.File
	Settings string
}

// Supervisor is a box's supervisor, holding its sandbox's lock and
// listening.
type Supervisor struct {
	held   *source.Workspace
	told   net.Listener
	closes []func()
}

// Open makes this the one supervisor of its sandbox, standing in the
// workspace's directory, and has it listen: Serve serves, and Close lets
// go of everything where it is never served.
func Open(config Config) (*Supervisor, error) {
	// The workspace's name is told by whoever made the sandbox: the
	// command line knows a box by the workspace its lock names, and one
	// of no name would be found by none.
	if config.Name == "" {
		return nil, errors.New("the supervisor was told no workspace to hold: it is given --name, by the control plane that made its sandbox")
	}
	if config.Lock == "" {
		config.Lock = ports.Lock
	}
	if err := os.MkdirAll(config.Workspace, 0o755); err != nil {
		return nil, fmt.Errorf("the workspace's directory: %w", err)
	}
	s := &Supervisor{}
	release, err := Sole(config.Lock, config.Name)
	if err != nil {
		return nil, err
	}
	s.closes = append(s.closes, release)
	// What a build is told is written before anything is served, so that
	// the first command in the workspace finds it: whole, in place of
	// what a supervisor before this one wrote.
	if err := config.tell(); err != nil {
		s.Close()
		return nil, err
	}
	if config.AuthorizedKey != "" {
		closeShell, err := Shell(program, config.Shell, config.Workspace, config.AuthorizedKey)
		if closeShell != nil {
			s.closes = append(s.closes, closeShell)
		}
		if err != nil {
			s.Close()
			return nil, err
		}
	}
	if s.held, err = source.New(config.Workspace, false); err != nil {
		s.Close()
		return nil, err
	}
	s.closes = append(s.closes, s.held.Close)
	if s.told, err = net.Listen("tcp", config.Source); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// tell writes what a build in the workspace is told where a build reads
// it, or, told no grid, removes what is there: a sandbox told none
// builds on none, and not on the grid a supervisor before this one was
// told. The file is the supervisor's own to delete: it is the one
// program that writes it, at the sandbox's one path for it, and there is
// one supervisor in a sandbox. A path that is no file, and one that
// cannot be written or removed, fails the start, saying which it is; so
// do instances its build would refuse, and instances told with no grid.
func (config Config) tell() error {
	path := config.Settings
	if path == "" {
		path = ports.Settings
	}
	if there, err := os.Lstat(path); err == nil && !there.Mode().IsRegular() {
		return fmt.Errorf("what a build in the workspace is told is kept in %s, which is no file (%s): it is the supervisor's own file", path, there.Mode().Type())
	}
	// What a build in the workspace would be refused when it reads its
	// settings, the supervisor is refused now, by the same check
	// (--instance is grid.instance, --reads grid.reads, --grid
	// grid.address): instances told with no grid among it, which would
	// otherwise start it as told none.
	if err := config.Build.Grid.CheckInstances(); err != nil {
		return fmt.Errorf("what a build in the workspace is told (--instance is grid.instance, --reads grid.reads, --grid grid.address): %w", err)
	}
	if config.Build.Grid.Address == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("what a build in the workspace was told before, in %s: %w", path, err)
		}
		return nil
	}
	// A build asks every action for the platform of its grid's workers,
	// so that an action is of the image it runs in: told a grid and
	// nothing of its workers, a build would guess this machine's.
	if len(config.Build.Platform) == 0 {
		return errors.New("the supervisor was told a grid and nothing of what its workers are: it is given --platform, as the control plane that made its sandbox says them")
	}
	if err := buildfile.Write(path, config.Build); err != nil {
		return fmt.Errorf("what a build in the workspace is told, in %s: %w", path, err)
	}
	return nil
}

// Source is the address the source API is served on.
func (s *Supervisor) Source() string { return s.told.Addr().String() }

// Serve serves the source API until the context ends, and then lets go
// of everything the supervisor holds: what fills the workspace is
// stopped, the shell closed, and the sandbox's lock released.
func (s *Supervisor) Serve(ctx context.Context) error {
	defer s.Close()
	signals, stop, err := telemetry.Setup(ctx, program)
	defer func() {
		if err := telemetry.Ended(stop); err != nil {
			fmt.Fprintln(os.Stderr, program+": telemetry:", err)
		}
	}()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: serving the source API on %s\n", program, s.Source())
	return httpserver.Serve(signals.Context(ctx), s.told, signals.Handler(source.Serve(s.held), "source API"))
}

// Close lets go of what the supervisor holds, the last taken first. A
// listener Serve was never given is closed.
func (s *Supervisor) Close() {
	if s.told != nil {
		_ = s.told.Close()
	}
	for i := len(s.closes) - 1; i >= 0; i-- {
		s.closes[i]()
	}
	s.closes = nil
}

// ErrUsage is what Run returns where the supervisor was asked wrongly: a
// flag that is none of its own, or what follows its flags. It has said
// so on standard error, and the program ends with status 2.
var ErrUsage = errors.New(program + " was asked wrongly")

// Run is the supervisor, given the arguments after its name: it serves
// until the context ends. Its flags are a set of its own.
func Run(ctx context.Context, args []string) error {
	config, err := asked(args)
	if err != nil || config == nil {
		return err
	}
	if err := Stood(&config.Workspace, nil); err != nil {
		return err
	}
	s, err := Open(*config)
	if err != nil {
		return err
	}
	return s.Serve(ctx)
}

// asked is what a supervisor is told by its arguments; nil where it was
// asked only what its flags are, and ErrUsage where it was asked wrongly,
// which it has said.
func asked(args []string) (*Config, error) {
	flags := flag.NewFlagSet(program, flag.ContinueOnError)
	var config Config
	flags.StringVar(&config.Source, "source", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Source)), "the address to serve the source API on: what the control plane tells the box to hold, which no route of the control plane's leads a caller to")
	flags.StringVar(&config.Workspace, "workspace", ".", "the workspace's directory")
	flags.StringVar(&config.Name, "name", "", "the name of the workspace it holds: the control plane that made the sandbox says it")
	flags.StringVar(&config.Shell, "shell", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Shell)), "the address to serve SSH on, to the key --authorized-key gives; with none, no shell is served")
	flags.StringVar(&config.AuthorizedKey, "authorized-key", "", "the key let into the shell, as a line of authorized_keys: a sandbox a person works in")
	flags.StringVar(&config.Build.Grid.Address, "grid", "", "the address of the grid a build in the workspace runs its actions on, host:port: it is written where a build there reads its settings")
	flags.StringVar(&config.Build.Grid.Assets, "assets", "", "the address of the grid's asset service, host:port")
	flags.StringVar(&config.Build.Grid.Instance, "instance", "", "the grid's instance a build in the workspace looks its actions up in first and runs them under; none, the grid's default")
	flags.Func("reads", "another instance of the grid a build in the workspace looks an action up in, after its own, before running it; give it once for each, in the order they are looked in", func(instance string) error {
		// Held, with --instance, to what a build would be refused when the
		// supervisor starts (Config.tell), by the build's own check.
		config.Build.Grid.Reads = append(config.Build.Grid.Reads, instance)
		return nil
	})
	flags.StringVar(&config.Build.Plugins, "plugins", "", "the directory of the box's plugins: every <name>.wasm in it is loaded by a build")
	flags.Func("platform", "a property of the grid's workers, as name=value; give it once for each", func(property string) error {
		name, value, found := strings.Cut(property, "=")
		if !found || name == "" {
			return errors.New("want name=value")
		}
		if _, twice := config.Build.Platform[name]; twice {
			return fmt.Errorf("%s is given twice", name)
		}
		if config.Build.Platform == nil {
			config.Build.Platform = map[string]string{}
		}
		config.Build.Platform[name] = value
		return nil
	})
	flags.SetOutput(os.Stderr)
	switch err := flags.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return nil, nil
	case err != nil:
		return nil, ErrUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(flags.Output(), "%q is no flag of %s, which takes flags alone\n", flags.Arg(0), program)
		flags.Usage()
		return nil, ErrUsage
	}
	return &config, nil
}

// Sole makes this program the one of its sandbox that manages its
// workspace, and returns what lets go of that. A sandbox holds one and
// one workspace: a second is refused, naming the workspace the first
// manages.
func Sole(path, workspace string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("the sandbox's lock: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the sandbox's lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		held, _ := io.ReadAll(io.LimitReader(file, 256))
		_ = file.Close()
		if managed := strings.TrimSpace(string(held)); managed != "" {
			return nil, fmt.Errorf("a supervisor already manages the workspace %s in this sandbox: a sandbox holds one supervisor, and one workspace", managed)
		}
		return nil, fmt.Errorf("a supervisor already runs in this sandbox: a sandbox holds one supervisor, and one workspace")
	}
	said := []byte(workspace + "\n")
	if _, err = file.WriteAt(said, 0); err == nil {
		err = file.Truncate(int64(len(said)))
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("the sandbox's lock: %w", err)
	}
	return func() { _ = file.Close() }, nil
}

// Stood has a program stand in its workspace: the workspace's directory
// becomes the directory it works in, and every path it was told beside
// where it was started is made whole first, so that it is still found.
func Stood(workspace *string, paths []string, others ...*string) error {
	whole := func(path *string) error {
		if *path == "" {
			return nil
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = absolute
		return nil
	}
	for _, each := range append([]*string{workspace}, others...) {
		if err := whole(each); err != nil {
			return err
		}
	}
	for i := range paths {
		if err := whole(&paths[i]); err != nil {
			return err
		}
	}
	if err := os.Chdir(*workspace); err != nil {
		return fmt.Errorf("the workspace's directory: %w", err)
	}
	return nil
}

// Shell lets the people whose keys the sandbox was given in over SSH,
// for the program named, and returns what closes it. A sandbox that was
// given none serves none.
func Shell(program, address, directory, authorized string) (func(), error) {
	editor, err := source.NewShell(source.ShellConfig{Address: address, Directory: directory, AuthorizedKeys: authorized})
	if err != nil || editor == nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "%s: serving SSH on %s\n", program, editor.Address())
	reported := make(chan struct{})
	go func() {
		defer close(reported)
		if err := editor.Wait(); err != nil {
			fmt.Fprintln(os.Stderr, program+": ssh:", err)
		}
	}()
	return func() { _ = editor.Close(); <-reported }, nil
}
