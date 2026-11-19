// Command frontseatd is the daemon. It always runs in a sandbox: it
// populates a workspace, evaluates it with plugins, runs actions on a
// grid and serves the build API.
//
// It runs nothing itself. The grid it is given speaks the Remote
// Execution API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/frontseat-dev/frontseat/server/internal/build"
	"github.com/frontseat-dev/frontseat/server/internal/httpserver"
	"github.com/frontseat-dev/frontseat/server/internal/source"
	"github.com/frontseat-dev/frontseat/server/telemetry"

	"github.com/frontseat-dev/frontseat/local/ports"
)

// modules collects a flag that may be given more than once.
type modules []string

func (m *modules) String() string     { return fmt.Sprint(*m) }
func (m *modules) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "frontseatd:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listen := flag.String("listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Daemon)), "the address to serve the build API on")
	sourceAddress := flag.String("source", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Source)), "the address to serve the source API on: what the control plane tells the daemon to hold, which no route of the control plane's leads a caller to")
	workspace := flag.String("workspace", ".", "the workspace's directory")
	state := flag.String("state", "/var/lib/frontseat", "the directory the daemon keeps what is its own in: its runs, for the daemon that comes after this one (what each did and what its actions printed, the newest of them)")
	name := flag.String("name", "", "the name of the workspace it manages, which makes it its sandbox's daemon: the control plane that made the sandbox says it")
	var plugins modules
	flag.Var(&plugins, "plugin", "a plugin's wasm module; give it once for each plugin")
	pluginDirectory := flag.String("plugins", "", "a directory of plugins: every <name>.wasm in it is loaded, as if each were given by --plugin")
	grid := flag.String("grid", "", "the address of the grid actions run on, host:port")
	instance := flag.String("instance", "", "the instance name asked of the grid")
	assets := flag.String("assets", "", "the address of the grid's asset service, host:port, which fetches what a shared fetch downloads")
	var rewrites modules
	flag.Var(&rewrites, "rewrite", "which proxy of the grid's stands in for a public site a download names, as a regular expression over the download's URL, a space, and the proxy's address, with $1 and so on; give it once for each")
	var properties modules
	flag.Var(&properties, "platform", "a property of the workers actions run on, as name=value; give it once for each (default: Linux, on this machine's architecture; a sandbox's daemon, given --name, is to be told)")
	shellAddress := flag.String("shell", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Shell)), "the address to serve SSH on, to the key --authorized-key gives; with none, no shell is served")
	authorized := flag.String("authorized-key", "", "the key let into the shell, as a line of authorized_keys: a sandbox a person works in")
	flag.Parse()
	// What the grid's client library logs (glog) goes to standard error,
	// never to a file of its own: it names one by the time and this
	// process's id, and a daemon started again in the same second, as a
	// sandbox resumed starts one, finds its own and aborts.
	if err := flag.Set("logtostderr", "true"); err != nil {
		return err
	}

	// The workspace's name is told by whoever made the sandbox, and with
	// it that this is the sandbox's daemon. Whatever it was told, of its
	// name or its state, a second daemon in the sandbox is refused,
	// naming the workspace the first manages: a sandbox holds one daemon.
	if *state == "" {
		return fmt.Errorf("--state names no directory: the daemon keeps its runs and its lock there")
	}
	plugins, err := withDirectory(plugins, *pluginDirectory)
	if err != nil {
		return err
	}
	if err := stood(workspace, plugins, state); err != nil {
		return err
	}
	release, err := sole(ports.Lock, *name)
	if err != nil {
		return err
	}
	defer release()
	if *name != "" {
		if err := toldPlatform(properties); err != nil {
			return err
		}
	}
	platform, err := platformOf(properties)
	var rewritten map[string]string
	if err == nil {
		rewritten, err = rewritesOf(rewrites)
	}
	if err == nil && *authorized != "" {
		var closeShell func()
		closeShell, err = serveShell(*shellAddress, *workspace, *authorized)
		if closeShell != nil {
			defer closeShell()
		}
	}
	if err == nil {
		err = serve(ctx, *listen, *sourceAddress, *workspace, filepath.Join(*state, "builds"), plugins, build.Grid{Address: *grid, Instance: *instance, Assets: *assets, Rewrites: rewritten}, platform)
	}
	return err
}

// toldPlatform refuses a sandbox's daemon that was told nothing of the
// platform of its grid's workers: it asks every action for that
// platform, so that an action is of the image it runs in, and a guess
// would be this machine's.
func toldPlatform(properties []string) error {
	if len(properties) == 0 {
		return fmt.Errorf("the sandbox was not told what the workers of its grid are: it was given no --platform")
	}
	return nil
}

// withDirectory is the plugins given one by one, and after them every
// <name>.wasm of a directory of plugins, by name. A directory named that
// holds none is refused: a daemon with none of the plugins it was
// pointed at would refuse every repository later, and say less.
func withDirectory(plugins []string, directory string) ([]string, error) {
	if directory == "" {
		return plugins, nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("--plugins %s: %w", directory, err)
	}
	var found []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".wasm") {
			found = append(found, filepath.Join(directory, entry.Name()))
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("--plugins %s holds no <name>.wasm", directory)
	}
	return append(plugins, found...), nil
}

// serveShell lets the people whose keys the sandbox was given in over
// SSH. A sandbox that was given none serves none.
func serveShell(address, directory, authorized string) (func(), error) {
	editor, err := source.NewShell(source.ShellConfig{Address: address, Directory: directory, AuthorizedKeys: authorized})
	if err != nil || editor == nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "frontseatd: serving SSH on %s\n", editor.Address())
	reported := make(chan struct{})
	go func() {
		defer close(reported)
		if err := editor.Wait(); err != nil {
			fmt.Fprintln(os.Stderr, "frontseatd: ssh:", err)
		}
	}()
	return func() { _ = editor.Close(); <-reported }, nil
}

// rewritesOf reads which proxy stands in for each public site a download
// names, from the --rewrite flags: a regular expression, a space and the
// proxy's address, which holds none. A pattern given twice is refused,
// and so is one that is no regular expression, at the start rather than
// at the first download.
func rewritesOf(given []string) (map[string]string, error) {
	if len(given) == 0 {
		return nil, nil
	}
	rewrites := map[string]string{}
	for _, each := range given {
		space := strings.LastIndex(each, " ")
		if space <= 0 || space == len(each)-1 {
			return nil, fmt.Errorf("--rewrite %q: want a regular expression, a space and the proxy's address", each)
		}
		pattern, address := each[:space], each[space+1:]
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("--rewrite: %w", err)
		}
		if _, twice := rewrites[pattern]; twice {
			return nil, fmt.Errorf("--rewrite gives %s twice", pattern)
		}
		rewrites[pattern] = address
	}
	return rewrites, nil
}

// platformOf reads the workers' platform from the daemon's flags, or
// where none is given, Linux on this machine's architecture.
func platformOf(properties []string) (map[string]string, error) {
	if len(properties) == 0 {
		isa := map[string]string{"arm64": "aarch64", "amd64": "x86-64"}[runtime.GOARCH]
		return map[string]string{"OSFamily": "linux", "ISA": isa}, nil
	}
	platform := map[string]string{}
	for _, property := range properties {
		name, value, found := strings.Cut(property, "=")
		if !found || name == "" {
			return nil, fmt.Errorf("--platform %s: want name=value", property)
		}
		if _, twice := platform[name]; twice {
			return nil, fmt.Errorf("--platform gives %s twice", name)
		}
		platform[name] = value
	}
	return platform, nil
}

func serve(ctx context.Context, listen, sourceAddress, workspace, runs string, modules []string, config build.Grid, platform map[string]string) error {
	signals, stop, err := telemetry.Setup(ctx, "frontseatd")
	defer func() {
		if err := telemetry.Ended(stop); err != nil {
			fmt.Fprintln(os.Stderr, "frontseatd: telemetry:", err)
		}
	}()
	if err != nil {
		return err
	}
	ctx = signals.Context(ctx)
	var modulesBytes [][]byte
	for _, path := range modules {
		module, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		modulesBytes = append(modulesBytes, module)
	}
	daemon, err := build.New(ctx, build.Config{Workspace: workspace, Plugins: modulesBytes, Grid: config, Platform: platform, Telemetry: signals, Builds: runs})
	if err != nil {
		return err
	}
	defer daemon.Close()
	fmt.Fprintf(os.Stderr, "frontseatd: running actions on the grid at %s\n", config.Address)
	for _, name := range daemon.Plugins {
		fmt.Fprintf(os.Stderr, "frontseatd: loaded the %s plugin\n", name)
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	told, err := net.Listen("tcp", sourceAddress)
	if err != nil {
		_ = listener.Close()
		return err
	}
	fmt.Fprintf(os.Stderr, "frontseatd: serving %s on %s, and its source API on %s\n", workspace, listen, sourceAddress)
	// The build API and the source API, each on its own listener: when
	// one stops, so does the other, and both are joined.
	serving, cancel := context.WithCancel(ctx)
	defer cancel()
	sourced := make(chan error, 1)
	go func() {
		defer cancel()
		sourced <- httpserver.Serve(serving, told, daemon.Source)
	}()
	err = httpserver.Serve(serving, listener, daemon.Handler)
	cancel()
	return errors.Join(err, <-sourced)
}

// sole makes this daemon the one of its sandbox, and returns what lets go of that. A sandbox holds one daemon and one
// workspace: a second daemon is refused, naming the workspace the first
// manages.
func sole(path, workspace string) (func(), error) {
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

// stood has the daemon stand in its workspace: the workspace's directory
// becomes the directory it works in, and every path it was told beside
// where it was started is made whole first, so that it is still found.
func stood(workspace *string, plugins []string, others ...*string) error {
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
	for i := range plugins {
		if err := whole(&plugins[i]); err != nil {
			return err
		}
	}
	if err := os.Chdir(*workspace); err != nil {
		return fmt.Errorf("the workspace's directory: %w", err)
	}
	return nil
}
