package life

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

// Files is the file in a daemon's state where what it knew of the
// directory's files is saved when it ends, for the daemon after it: read
// once the state is taken, and written before it is let go of.
const Files = "files"

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
	// Version is the program the command that starts it took it for
	// (Made.Program): a daemon that finds itself another does not start.
	// Empty, nothing is held to it.
	Version string
	// Idle is how long it has nothing to do before it ends itself;
	// zero, three hours.
	Idle time.Duration
	// Layers is the directory beneath which each repository something
	// else mounted in the directory it builds has its layer, by the
	// repository's name (build.Config.Layers): a command that starts it
	// in a workspace's sandbox names it. Empty, no layer is read.
	Layers string
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
	read, err := Read(o.Settings)
	if err != nil {
		return err
	}
	// The command that started it said what program it took this for,
	// and will look for a daemon that says that: one that finds itself
	// another, its file replaced between the two, would be found by none.
	if o.Version != "" && o.Version != read.Made.Program {
		return errors.New("this program is not the one the command that started it read: its file was replaced in between, and the next command starts the daemon of the one there now")
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
	// The directory is taken before anything of the daemon is made in
	// it, and let go of after everything of it has ended: of two daemons
	// started at once one is refused here, and the one that starts where
	// another ended finds nothing of it.
	held, err := Take(o.State)
	if err != nil {
		return err
	}
	defer held.Release()
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
	made, err := build.New(ctx, build.Config{Workspace: directory, System: chosen.System(), Plugins: read.Modules, Grid: read.Settings.Grid, Platform: read.Settings.Platform, Telemetry: signals, Builds: filepath.Join(o.State, Builds), Files: filepath.Join(o.State, Files), Program: read.Made.Program, Given: read.Settings.Given, Mounted: alone, Log: os.Stderr, Layers: o.Layers})
	if err != nil {
		return err
	}
	idle := o.Idle
	if idle == 0 {
		idle = idleFor
	}
	// The build is closed by the daemon's life, once it serves no more
	// and before the directory is let go of: its runs joined, and what it
	// keeps in the directory released.
	return held.Keep(ctx, Life{State: o.State, Said: Info{Version: read.Made.Program, Settings: read.Made.Settings, Directory: directory, Environment: Environment(read.Settings.Given)}, Handler: made.Handler, Busy: made.Busy, Idle: idle, Ended: made.Close})
}

// apart is a context that is part of no trace. A daemon is started by a
// command and outlives it: what it serves afterwards is each caller's
// own, a trace of its own or the one the caller says, and never a part
// of the command that happened to start the daemon, whose trace would
// otherwise hold every build the daemon ever made.
func apart(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.SpanContext{})
}

// Made is what a daemon is made of, each a digest of content: a command
// that finds a daemon made of anything else starts another.
type Made struct {
	// Program is this program: its own file's content, so that a program
	// built again is another, whatever version it says.
	Program string
	// Settings is what the daemon was told and what that has it load:
	// the settings file's bytes, every plugin module they load, by its
	// name and its content, and the authorities the grid is known by, if
	// the settings name any.
	Settings string
}

// Loaded is a daemon's settings as they were read, with what they load
// and what that makes a daemon.
type Loaded struct {
	Settings settings.Settings
	// Modules are the plugins' modules, in the order of
	// Settings.Plugins: the bytes the digest was made of, which are the
	// ones a daemon loads.
	Modules [][]byte
	Made    Made
}

// Read reads a daemon's settings from a file, with what they load, and
// says what a daemon of them is made of. The command line asks it of
// every command, and a daemon when it starts: the one function that says
// what a daemon is. Everything is identified by its content, read
// whole, and by nothing a file system says of it: a file written again
// in place, at the same size and time, is another. The settings are read
// once, and the bytes parsed are the bytes digested, as a plugin's
// module loaded is the one digested. What cannot be read is an error
// that names its path, never left out. The grid is known by the
// authorities' bytes digested here, and their file is not read again.
func Read(path string) (Loaded, error) {
	raw, err := settings.Bytes(path)
	if err != nil {
		return Loaded{}, err
	}
	read, err := settings.Parse(raw, path)
	if err != nil {
		return Loaded{}, err
	}
	digest := sha256.New()
	part := func(name string, content []byte) {
		fmt.Fprintf(digest, "%d:%s:%d:", len(name), name, len(content))
		digest.Write(content)
	}
	part("settings", raw)
	loaded := Loaded{Settings: read}
	for _, plugin := range read.Plugins {
		module, err := os.ReadFile(plugin)
		if err != nil {
			return Loaded{}, fmt.Errorf("a plugin the build's settings load: %w", err)
		}
		part("plugin "+filepath.Base(plugin), module)
		loaded.Modules = append(loaded.Modules, module)
	}
	if read.Grid.Authorities != "" {
		known, err := os.ReadFile(read.Grid.Authorities)
		if err != nil {
			return Loaded{}, fmt.Errorf("the authorities the build's settings name: %w", err)
		}
		part("authorities", known)
		loaded.Settings.Grid.Known = known
	}
	program, err := Program()
	if err != nil {
		return Loaded{}, err
	}
	loaded.Made = Made{Program: program, Settings: hex.EncodeToString(digest.Sum(nil))}
	return loaded, nil
}

// Program is what this program is: the digest of its own file's
// content.
func Program() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("this program's own file: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("this program's own file, %s: %w", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
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
