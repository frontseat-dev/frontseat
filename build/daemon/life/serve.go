package life

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/frontseat-dev/frontseat/build/daemon/build"
	"github.com/frontseat-dev/frontseat/build/daemon/settings"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/common/vcs/chosen"
	"github.com/frontseat-dev/frontseat/core/telemetry"
)

// Builds is the directory beneath a daemon's state its builds are kept
// in, named by the form they are kept in: builds kept in another form,
// by a daemon from before a change to how a build is written, are not
// read, and are the operator's to delete. It moves when a kept build
// would otherwise be read as something it was not.
const Builds = "builds-3"

// idleFor is how long a build daemon has nothing to do before it ends
// itself, where it is told no other: what it knew of its files is kept,
// so the daemon after it reads only what changed. Bazel's server waits
// three hours.
const idleFor = 3 * time.Hour

// Options are what a build daemon is started with, by the command that
// would ask it.
type Options struct {
	// Directory is the directory it builds, and State its state
	// directory, which is that directory's alone.
	Directory, State string
	// Settings is the file it is told its plugins and its grid in.
	Settings string
	// Version is the program it is, as the command that starts it says.
	Version string
	// Idle is how long it has nothing to do before it ends itself;
	// zero, three hours.
	Idle time.Duration
}

// Serve is a build daemon for a directory: it serves the build API on
// the loopback, to whoever presents its token, over that directory, its
// plugins and its grid, and says where in its state directory (Told). It
// knows no control plane and no workspace's name: a directory, and what
// its settings tell it. It ends when the context does, or once it has
// had nothing to do for long enough.
func Serve(ctx context.Context, o Options) error {
	if o.State == "" {
		return errors.New("the build daemon was given no directory to keep its state in")
	}
	read, digest, err := Read(o.Settings)
	if err != nil {
		return err
	}
	directory, err := filepath.Abs(o.Directory)
	if err != nil {
		return err
	}
	// What it starts begins in the directory it builds.
	if err := os.Chdir(directory); err != nil {
		return fmt.Errorf("the directory to build: %w", err)
	}
	alone, err := Alone(directory)
	if err != nil {
		return err
	}
	signals, stop, err := telemetry.Setup(ctx, "frontseat")
	defer func() {
		if err := telemetry.Ended(stop); err != nil {
			fmt.Fprintln(os.Stderr, "frontseat: telemetry:", err)
		}
	}()
	if err != nil {
		return err
	}
	ctx = apart(signals.Context(ctx))
	var plugins [][]byte
	for _, path := range read.Plugins {
		module, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		plugins = append(plugins, module)
	}
	made, err := build.New(ctx, build.Config{Workspace: directory, System: chosen.System(), Plugins: plugins, Grid: read.Grid, Platform: read.Platform, Telemetry: signals, Builds: filepath.Join(o.State, Builds), Given: read.Given, Mounted: alone})
	if err != nil {
		return err
	}
	defer made.Close()
	idle := o.Idle
	if idle == 0 {
		idle = idleFor
	}
	return Keep(ctx, Life{State: o.State, Said: Info{Version: o.Version, Settings: digest, Directory: directory, Environment: Environment(read.Given)}, Handler: made.Handler, Busy: made.Busy, Idle: idle})
}

// apart is a context that is part of no trace. A daemon is started by a
// command and outlives it: what it serves afterwards is each caller's
// own, a trace of its own or the one the caller says, and never a part
// of the command that happened to start the daemon, whose trace would
// otherwise hold every build the daemon ever made.
func apart(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.SpanContext{})
}

// Read reads a daemon's settings from a file, and returns with them what
// the file was when it was read: a daemon whose file has changed since
// is not the one a command would start.
func Read(path string) (settings.Settings, string, error) {
	read, err := settings.Read(path)
	if err != nil {
		return settings.Settings{}, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return settings.Settings{}, "", fmt.Errorf("the build's settings: %w", err)
	}
	sum := sha256.Sum256(raw)
	return read, hex.EncodeToString(sum[:]), nil
}

// Environment is what the variables a publish may be given are where
// this is asked, as one word that says none of them: a daemon has those
// it was started with, and a command given another token for its forge,
// or none, is not served by it. A variable that is not set is not one
// set to nothing.
func Environment(given []string) string {
	digest := sha256.New()
	for _, name := range given {
		value, set := os.LookupEnv(name)
		fmt.Fprintf(digest, "%d:%s=%t:%d:%s\n", len(name), name, set, len(value), value)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// Alone says whether a directory is one repository the build was given,
// as a job's checkout is, and not a workspace of several: a workspace
// says what it holds in its manifest, which whatever fills it writes,
// and a checkout has none.
func Alone(directory string) (bool, error) {
	_, err := os.Stat(filepath.Join(directory, filepath.FromSlash(manifest.File)))
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	}
	return false, fmt.Errorf("the directory to build: %w", err)
}
