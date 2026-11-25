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
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
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
	o, err := absolute(o)
	if err != nil {
		return err
	}
	// What the daemon is made of is looked at as it is read: a write
	// after shows to whoever waits for what a build read to change, and
	// the command that waits starts the daemon of it as it is then.
	program, err := os.Executable()
	if err != nil {
		return err
	}
	made, read, err := madeAndRead(ctx, time.Now(), o.Settings, program, nil)
	if err != nil {
		return err
	}
	// The command that started it said what program it took this for,
	// and will look for a daemon that says that: one that finds itself
	// another, its file replaced between the two, would be found by none.
	if o.Version != "" && o.Version != read.Made.Program {
		return errors.New("this program is not the one the command that started it read: its file was replaced in between, and the next command starts the daemon of the one there now")
	}
	directory := o.Directory
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
	daemon, err := build.New(ctx, build.Config{Workspace: directory, System: chosen.System(), Plugins: read.Modules, Grid: read.Settings.Grid, Platform: read.Settings.Platform, Telemetry: signals, Builds: filepath.Join(o.State, Builds), Files: filepath.Join(o.State, Files), Program: read.Made.Program, Given: read.Settings.Given, Mounted: alone, Log: os.Stderr, Made: made})
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
	return held.Keep(ctx, Life{State: o.State, Said: Info{Version: read.Made.Program, Settings: read.Made.Settings, Directory: directory, Environment: Environment(read.Settings.Given)}, Handler: daemon.Handler, Busy: daemon.Busy, Yields: daemon.Yields, Idle: idle, Ended: daemon.Close})
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
// once, and the bytes parsed are the bytes digested; the directory of
// plugins is listed once, and each plugin's module loaded is the one
// digested. What cannot be read is an error that names its path, never
// left out. The authorities are read once, as the settings are parsed:
// the grid is checked against, known by and identified by those very
// bytes, and their file is not read again.
func Read(path string) (Loaded, error) {
	program, err := os.Executable()
	if err != nil {
		return Loaded{}, err
	}
	return readOf(path, program, nil, nil)
}

// readOf is Read, of this program's own file at a path (Made.Program),
// each file read through made (source.Looks.Read), which holds it as it
// was read, and the directory of plugins held as it was listed
// (source.Looks.Lists); a nil made holds nothing. meanwhile, where it
// is given, is called once the settings are read and before what they
// name is: a test's way in between.
//
// Each part is a file of its own, by the path it is read by: a path
// that is two parts (the grid's authorities named as the settings, the
// settings among the plugins, this program's own file one of them) is
// refused, by which parts it is, before the second is read. Read twice,
// the file would be held to both readings, and one written between them
// would hold what one used and not the other, answering every wait for
// as long as the daemon lived (source.Looks.Await).
func readOf(path, program string, made *source.Looks, meanwhile func()) (Loaded, error) {
	parts := map[string]string{}
	part := func(file, role string) error {
		if was, named := parts[file]; named {
			return fmt.Errorf("%s and %s are one file: each part of a daemon is a file of its own", was, role)
		}
		parts[file] = role
		return nil
	}
	if err := part(path, "the build's settings"); err != nil {
		return Loaded{}, err
	}
	var raw []byte
	opened := false
	told, err := made.Read(path, func(file io.Reader) (err error) {
		opened = true
		raw, err = settings.From(file, path)
		return err
	})
	if err != nil {
		if !opened {
			err = fmt.Errorf("the build's settings: %w", err)
		}
		return Loaded{}, err
	}
	if meanwhile != nil {
		meanwhile()
	}
	// The authorities are read as the settings are parsed, once: the
	// bytes the grid is checked against are those it is known by and
	// the daemon is identified by.
	authorities := ""
	read, err := settings.Parse(raw, path, func(file string) ([]byte, error) {
		if err := part(file, "the grid's authorities"); err != nil {
			return nil, err
		}
		var known []byte
		hash, err := made.Read(file, func(r io.Reader) (err error) {
			known, err = build.Authorities(r, file)
			return err
		})
		authorities = hash
		return known, err
	})
	if err != nil {
		return Loaded{}, err
	}
	made.Lists(read.PluginDirectory, settings.Module)
	// Each part by the SHA-256 of what was read of it, which reading it
	// says: nothing is hashed twice.
	digest := sha256.New()
	said := func(name, hash string) {
		fmt.Fprintf(digest, "%d:%s:%s\n", len(name), name, hash)
	}
	said("settings", told)
	loaded := Loaded{Settings: read}
	for _, plugin := range read.Plugins {
		if err := part(plugin, "a plugin"); err != nil {
			return Loaded{}, err
		}
		module, hash, err := whole(made, plugin)
		if err != nil {
			return Loaded{}, fmt.Errorf("a plugin the build's settings load: %w", err)
		}
		said("plugin "+filepath.Base(plugin), hash)
		loaded.Modules = append(loaded.Modules, module)
	}
	if read.Grid.Authorities != "" {
		said("authorities", authorities)
	}
	// This program's own file is read only to say what it is: its
	// digest is the hash of what was read, all of it, so that a file
	// written as it was read is said by the bytes read (source.Looks.Read).
	if err := part(program, "this program's own file"); err != nil {
		return Loaded{}, err
	}
	itself, err := made.Read(program, func(r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	})
	if err != nil {
		return Loaded{}, fmt.Errorf("this program's own file: %w", err)
	}
	loaded.Made = Made{Program: itself, Settings: hex.EncodeToString(digest.Sum(nil))}
	return loaded, nil
}

// whole is what a file holds, read whole through made (readOf), and
// the SHA-256 of it, in hex.
func whole(made *source.Looks, file string) ([]byte, string, error) {
	var content []byte
	hash, err := made.Read(file, func(r io.Reader) (err error) {
		content, err = io.ReadAll(r)
		return err
	})
	return content, hash, err
}

// madeAndRead reads what a daemon of a file of settings is made of
// (Read) as it looks at it, at a time, the clock's now, for whoever
// waits for what a build read to change, and holds the look to the very
// bytes read (source.Looks.Read): each part is looked at as it is
// opened, before it is read, so a write after shows, and nothing is
// waited for; one that had not settled is read again once it has, and
// answered only where it does not hold what the daemon read, as is a
// part written within the moment its times are told in, at its size,
// and a part that looks otherwise: one written again with what it held
// is no change (source.Looks.Await).
// What it is made of is said by one reading: the settings read once,
// the directory of plugins listed once and held as it was listed
// (source.Looks.Lists), each part read once; so nothing the daemon used
// goes unheld. Of this program's own file (program), that is what the
// daemon read of it, which says what program it is (Made.Program).
// Not held: what runs is what the system loaded when the daemon
// started, before the look, and that is not read; the file replaced
// between and put back before the look leaves the daemon running a
// program none of this saw. meanwhile is readOf's. It returns when the
// context ends, with its error.
func madeAndRead(ctx context.Context, at time.Time, told, program string, meanwhile func()) (*source.Looks, Loaded, error) {
	made, err := source.Beside(ctx, at)
	if err != nil {
		return nil, Loaded{}, err
	}
	loaded, err := readOf(told, program, made, meanwhile)
	if err != nil {
		return nil, Loaded{}, err
	}
	return made, loaded, nil
}

// absolute is what a daemon is started with, each path made whole from
// where it was started: it then runs in the directory it builds, and
// whatever it holds by a path is held wherever it runs.
func absolute(o Options) (Options, error) {
	for _, path := range []*string{&o.Directory, &o.State, &o.Settings} {
		whole, err := filepath.Abs(*path)
		if err != nil {
			return Options{}, err
		}
		*path = whole
	}
	return o, nil
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
