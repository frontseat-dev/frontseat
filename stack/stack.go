// Package stack brings the local stack up or down from a checkout
// (frontseat-stack up, frontseat-stack down; mise start and mise stop):
// one container of the stack's image (stack/image), which holds the grid,
// the OpenShell gateway, the control plane and every sandbox, on the
// ports core/ports says, on this machine's loopback alone.
//
// Up builds the box from the checkout every time, and the stack's image
// on it, each named by what it holds (box.Build, box.Stack), never by a
// tag another checkout may move; starts the container of that image,
// keeping its volume; hands the box to the container's Podman, which
// runs it for each workspace; and lays out the owner's token where the
// command line finds it.
//
// A stack is one checkout's: it runs what that checkout built. So Up does
// not start over a stack another checkout started, which would take it
// from under that checkout's work. Stopping one asks nothing of who
// started it.
package stack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/frontseat-dev/frontseat/stack/box"
	"github.com/frontseat-dev/frontseat/stack/internal/poll"
	"github.com/frontseat-dev/frontseat/stack/local"
)

// Name is the stack's container's, and Volume the one volume it keeps
// everything in, which outlives it.
const (
	Name   = local.Container
	Volume = "frontseat-local"
)

// checkoutLabel is the label the container carries the checkout that
// started it under.
const checkoutLabel = "frontseat.dev/checkout"

// token is where the control plane keeps its owner's token, in the
// stack's state.
const token = "/var/lib/frontseat/controlplane/owner/token"

// Stack is the local stack of a checkout.
type Stack struct {
	// Root is the checkout's: what the box and the stack's image are
	// built from.
	Root string
	// Plugins is the directory holding the plugins the box holds, each
	// a <name>.wasm (dist/plugins, which mise run build makes).
	Plugins string
	// Log is where it says what it does.
	Log io.Writer

	// docker runs the container runtime's command and returns what it
	// printed; build builds the box, and image the stack's image on it,
	// each returning its tag; load hands the box to the container's
	// Podman. Unset, they are the docker command, box.Build, box.Stack
	// and docker save piped into the container.
	docker func(ctx context.Context, arguments ...string) (string, error)
	build  func(ctx context.Context) (string, error)
	image  func(ctx context.Context, box string) (string, error)
	load   func(ctx context.Context, box string) error
}

// Up brings the stack up from this checkout, running the box built from
// it, and lays out the owner's token where the command line finds it.
func (s Stack) Up(ctx context.Context) error {
	if err := s.Ready(ctx); err != nil {
		return err
	}
	built, err := s.box(ctx)
	if err != nil {
		return err
	}
	image, err := s.stackImage(ctx, built)
	if err != nil {
		return err
	}
	here, err := s.checkout()
	if err != nil {
		return err
	}
	// The container is made anew of the image built: what it keeps is on
	// its volume, which it is made with again. The stack is asked again
	// first: another checkout may have started one while this one built.
	if err := s.Ready(ctx); err != nil {
		return err
	}
	if _, err := s.run(ctx, "rm", "--force", Name); err != nil {
		return err
	}
	kept, err := os.MkdirTemp("", "frontseat-local-")
	if err != nil {
		return err
	}
	// The directory is this call's own, made just above; the runtime has
	// read the profile once the container is made.
	defer os.RemoveAll(kept)
	profile, err := Profile(kept)
	if err != nil {
		return err
	}
	c := Container{Name: Name, Image: image, Volume: Volume, Publish: true, Labels: map[string]string{checkoutLabel: here}}
	if _, err := s.run(ctx, c.Arguments(profile)...); err != nil {
		return err
	}
	if err := s.give(ctx, built); err != nil {
		return err
	}
	return s.layOut(ctx)
}

// Down brings the stack down, whichever checkout started it. Its volume
// is kept.
func (s Stack) Down(ctx context.Context) error {
	if _, err := s.answers(ctx); err != nil {
		return err
	}
	// By its name, so that it stops whoever started it, and a stack whose
	// checkout is gone.
	_, err := s.run(ctx, "rm", "--force", Name)
	return err
}

// Ready returns where this checkout may not bring the stack up: the
// runtime does not answer, or another checkout started the stack that
// is up. It is asked before anything is built (mise start).
func (s Stack) Ready(ctx context.Context) error {
	if _, err := s.answers(ctx); err != nil {
		return err
	}
	here, err := s.checkout()
	if err != nil {
		return err
	}
	said, err := s.run(ctx, "ps", "-a", "--filter", "name=^"+Name+"$", "--format", `{{.Label "`+checkoutLabel+`"}}`)
	if err != nil {
		return err
	}
	if from := strings.TrimSpace(said); from != "" && from != here {
		return fmt.Errorf("the local stack was started from %s, not from %s: stop it first (mise stop, from any checkout), then start it from here", from, here)
	}
	return nil
}

// checkout is this checkout's directory, whatever links lead to it.
func (s Stack) checkout() (string, error) {
	return filepath.EvalSymlinks(s.Root)
}

// answers returns the container runtime's architecture, or that it does
// not answer.
func (s Stack) answers(ctx context.Context) (string, error) {
	architecture, err := s.run(ctx, local.ArchitectureAsked()...)
	if err != nil {
		return "", fmt.Errorf("the container runtime does not answer: start it, and run this again: %w", err)
	}
	return strings.TrimSpace(architecture), nil
}

// box is the box the stack runs: built from this checkout, every time,
// and named by what it holds, so that no tag another checkout moves
// puts another tree's box under this stack.
func (s Stack) box(ctx context.Context) (string, error) {
	if s.build != nil {
		return s.build(ctx)
	}
	plugins, err := box.PluginsIn(s.Plugins)
	if err != nil {
		return "", err
	}
	return box.Build{Root: s.Root, Plugins: plugins, Log: s.Log}.Run(ctx)
}

// stackImage is the stack's image, built on the box.
func (s Stack) stackImage(ctx context.Context, built string) (string, error) {
	if s.image != nil {
		return s.image(ctx, built)
	}
	return box.Stack{Root: s.Root, Box: built, Log: s.Log}.Run(ctx)
}

// give hands the box to the container's Podman, which runs it for each
// workspace (box.Give).
func (s Stack) give(ctx context.Context, built string) error {
	if s.load != nil {
		return s.load(ctx, built)
	}
	return box.Give(ctx, built)
}

// layOut lays out the owner's token where the command line finds it:
// what the control plane knows the person at this machine by, minted by
// it the first time it starts, in its own state. It is that person's
// alone: whoever holds it may do anything the control plane does. It is
// never said.
func (s Stack) layOut(ctx context.Context) error {
	minted, err := poll.Tries(ctx, 120, time.Second, func() bool {
		_, err := s.run(ctx, "exec", Name, "test", "-s", token)
		return err == nil
	})
	if err != nil {
		return err
	}
	if !minted {
		return fmt.Errorf("the control plane minted no owner's token in two minutes: docker logs %s", Name)
	}
	read, err := s.run(ctx, "exec", Name, "cat", token)
	if err != nil {
		return errors.New("reading the owner's token the control plane minted failed")
	}
	held, err := local.TokenFile()
	if err != nil {
		return err
	}
	server, err := local.ServerFile()
	if err != nil {
		return err
	}
	kept := filepath.Dir(held)
	if err := os.MkdirAll(kept, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(kept, 0o700); err != nil {
		return err
	}
	// Whole or as it was: a command reading it meanwhile reads one or
	// the other.
	laying := filepath.Join(kept, ".token")
	if err := os.Remove(laying); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(laying, []byte(read), 0o600); err != nil {
		return err
	}
	// The token is the stack's, and is presented to the stack's control
	// plane: what named another server (frontseat/local/server) is not
	// left beside it.
	if err := os.Remove(server); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(laying, held)
}

// run runs the container runtime's command.
func (s Stack) run(ctx context.Context, arguments ...string) (string, error) {
	if s.docker != nil {
		return s.docker(ctx, arguments...)
	}
	command := exec.CommandContext(ctx, "docker", arguments...)
	var printed, said bytes.Buffer
	command.Stdout = &printed
	command.Stderr = &said
	if err := command.Run(); err != nil {
		return printed.String(), fmt.Errorf("docker %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(said.String()))
	}
	return printed.String(), nil
}
