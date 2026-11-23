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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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
	flags := flag.NewFlagSet(program, flag.ContinueOnError)
	var config Config
	flags.StringVar(&config.Source, "source", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Source)), "the address to serve the source API on: what the control plane tells the box to hold, which no route of the control plane's leads a caller to")
	flags.StringVar(&config.Workspace, "workspace", ".", "the workspace's directory")
	flags.StringVar(&config.Name, "name", "", "the name of the workspace it holds: the control plane that made the sandbox says it")
	flags.StringVar(&config.Shell, "shell", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Shell)), "the address to serve SSH on, to the key --authorized-key gives; with none, no shell is served")
	flags.StringVar(&config.AuthorizedKey, "authorized-key", "", "the key let into the shell, as a line of authorized_keys: a sandbox a person works in")
	flags.SetOutput(os.Stderr)
	switch err := flags.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return nil
	case err != nil:
		return ErrUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(flags.Output(), "%q is no flag of %s, which takes flags alone\n", flags.Arg(0), program)
		flags.Usage()
		return ErrUsage
	}
	if err := Stood(&config.Workspace, nil); err != nil {
		return err
	}
	s, err := Open(config)
	if err != nil {
		return err
	}
	return s.Serve(ctx)
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
			return nil, fmt.Errorf("a daemon already manages the workspace %s in this sandbox: a sandbox holds one daemon", managed)
		}
		return nil, fmt.Errorf("a daemon already runs in this sandbox: a sandbox holds one daemon")
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
