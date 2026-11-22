// Package daemon is the daemon's program, frontseatd, beneath its main:
// it reads the daemon's flags, makes it the one daemon of its sandbox,
// and serves the build API and the source API over the workspace, the
// plugins and the grid it was told.
//
// It runs nothing itself. The grid it is given speaks the Remote
// Execution API.
package daemon

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/server/internal/build"
	"github.com/frontseat-dev/frontseat/server/internal/httpserver"
	"github.com/frontseat-dev/frontseat/server/internal/source"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// modules collects a flag that may be given more than once.
type modules []string

func (m *modules) String() string     { return fmt.Sprint(*m) }
func (m *modules) Set(v string) error { *m = append(*m, v); return nil }

// Release is the release a daemon was built at, as its main was linked.
type Release struct {
	// Version is its version, and Commit the commit it was built from;
	// a build that is no release has no commit.
	Version, Commit string
}

// String is what the daemon says of the release it is.
func (r Release) String() string {
	if r.Commit == "" {
		return "frontseatd " + r.Version
	}
	return "frontseatd " + r.Version + " (" + r.Commit + ")"
}

// Run is the daemon, given the arguments after its name: it serves
// until the context ends. Its flags are a set of its own: nothing a
// library registers with the process's flags is one of them.
func Run(ctx context.Context, args []string, built Release) error {
	flags := flag.NewFlagSet("frontseatd", flag.ContinueOnError)
	listen := flags.String("listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Daemon)), "the address to serve the build API on")
	sourceAddress := flags.String("source", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Source)), "the address to serve the source API on: what the control plane tells the daemon to hold, which no route of the control plane's leads a caller to")
	workspace := flags.String("workspace", ".", "the workspace's directory")
	checkout := flags.Bool("checkout", false, "the workspace's directory is a checkout the daemon was given, as a job's is: one repository, which it builds and releases from as it is, bringing nothing into it; its tags are pushed to where the checkout says it was brought from; it is given with --name, by which the command line finds it (default: the daemon brings what it is told to hold, and is given none)")
	state := flags.String("state", "/var/lib/frontseat", "the directory the daemon keeps what is its own in: its runs, for the daemon that comes after this one (what each did and what its actions printed, the newest of them)")
	name := flags.String("name", "", "the name of the workspace it manages, which makes it its sandbox's daemon: the control plane that made the sandbox says it")
	var plugins modules
	flags.Var(&plugins, "plugin", "a plugin's wasm module; give it once for each plugin")
	pluginDirectory := flags.String("plugins", "", "a directory of plugins: every <name>.wasm in it is loaded, as if each were given by --plugin")
	grid := flags.String("grid", "", "the address of the grid actions run on, host:port")
	instance := flags.String("instance", "", "the instance name asked of the grid")
	gridTLS := flags.Bool("grid-tls", false, "ask the grid and its asset service over TLS, known by the system's authorities, where it asks no token (default: in the clear, as the local stack's grid is asked; a workspace's sandbox is told of no other)")
	gridCA := flags.String("grid-ca", "", "a PEM file of the authorities the grid and its asset service are known by, in place of the system's; with it they are asked over TLS")
	gridToken := flags.String("grid-token", "", "a file holding the bearer token the grid admits this daemon by, a JSON Web Token: read again for every call, so whoever was given the token replaces the file, whole and by a rename, when it is renewed; sent to the grid and to its asset service (--assets), and only where --grid-ca names the authorities they are known by: the system's are chosen by the environment")
	assets := flags.String("assets", "", "the address of the grid's asset service, host:port, which fetches what a shared fetch downloads; with --grid-token it is told the token too")
	var rewrites modules
	flags.Var(&rewrites, "rewrite", "which proxy of the grid's stands in for a public site a download names, as a regular expression over the download's URL, a space, and the proxy's address, with $1 and so on; give it once for each: the first that matches a download, in the order given, is taken")
	var given modules
	flags.Var(&given, "publish-environment", "a variable of where the daemon runs that what publishes a release may be given, by its name, as GH_TOKEN: how a forge's or a registry's own tool is handed its token; give it once for each (default: none, and what publishes is given nothing of where the daemon runs)")
	var properties modules
	flags.Var(&properties, "platform", "a property of the workers actions run on, as name=value; give it once for each (default: Linux, on this machine's architecture; a sandbox's daemon, given --name, is to be told)")
	shellAddress := flags.String("shell", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Shell)), "the address to serve SSH on, to the key --authorized-key gives; with none, no shell is served")
	authorized := flags.String("authorized-key", "", "the key let into the shell, as a line of authorized_keys: a sandbox a person works in")
	saysVersion := flags.Bool("version", false, "say which release of frontseat this is, and do nothing else")
	if asked, err := parsed(flags, args); err != nil || !asked {
		return err
	}
	if *saysVersion {
		fmt.Println(built)
		return nil
	}
	// What the grid's client library logs (glog) goes to standard error,
	// never to a file of its own: it names one by the time and this
	// process's id, and a daemon started again in the same second, as a
	// sandbox resumed starts one, finds its own and aborts. The library
	// registers its flags with the process's, which the daemon never
	// parses: it is set there, and is no flag of the daemon's.
	if err := flag.Set("logtostderr", "true"); err != nil {
		return err
	}
	if *state == "" {
		return fmt.Errorf("--state names no directory: the daemon keeps its runs there")
	}
	plugins, err := withDirectory(plugins, *pluginDirectory)
	if err != nil {
		return err
	}
	if err := stood(workspace, plugins, state); err != nil {
		return err
	}
	// The workspace's name is told by whoever made the sandbox, and with
	// it that this is the sandbox's daemon. Whatever it was told, of its
	// name or its state, a second daemon in the sandbox is refused,
	// naming the workspace the first manages: a sandbox holds one daemon.
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
	if err := handed(*checkout, *name); err != nil {
		return err
	}
	platform, err := platformOf(properties)
	if err != nil {
		return err
	}
	rewritten, err := rewritesOf(rewrites)
	if err != nil {
		return err
	}
	if *authorized != "" {
		closeShell, err := serveShell(*shellAddress, *workspace, *authorized)
		if closeShell != nil {
			defer closeShell()
		}
		if err != nil {
			return err
		}
	}
	asked, err := gridOf(*grid, *instance, *assets, rewritten, *gridTLS, *gridCA, *gridToken)
	if err != nil {
		return err
	}
	return serve(ctx, *listen, *sourceAddress, *workspace, filepath.Join(*state, "builds"), plugins, asked, platform, given, *checkout)
}

// ErrUsage is what Run returns where the daemon was asked wrongly: a
// flag that is none of its own, or what follows its flags. The flag
// package has said so on standard error, with the daemon's flags, as it
// does for every program here, and the program ends with status 2,
// which whoever starts it tells from a daemon that failed.
var ErrUsage = errors.New("frontseatd was asked wrongly")

// parsed reads the daemon's flags from its arguments, and says whether
// the daemon was asked to do anything: asked for its flags (--help), it
// is told them and asked nothing else. What is no flag of the daemon's
// is refused, and so is what follows the flags, each said on standard
// error with the daemon's flags, as the flag package says it.
func parsed(flags *flag.FlagSet, args []string) (bool, error) {
	flags.SetOutput(os.Stderr)
	switch err := flags.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return false, nil
	case err != nil:
		return false, ErrUsage
	}
	if err := strays(flags.Args()); err != nil {
		fmt.Fprintln(flags.Output(), err)
		flags.Usage()
		return false, ErrUsage
	}
	return true, nil
}

// gridOf is the grid the flags name and how it is asked, refused where
// the daemon could not ask so: found at the start, and not at the first
// action. The asset service is only ever the one --assets names, and is
// asked as the grid is, its token included.
func gridOf(address, instance, assets string, rewrites []build.Rewrite, tls bool, authorities, token string) (build.Grid, error) {
	grid := build.Grid{Address: address, Instance: instance, Assets: assets, Rewrites: rewrites, TLS: tls, Authorities: authorities, Token: token}
	return grid, grid.Check()
}

// strays refuses what follows the flags. A flag that takes no value ends
// them where a value is given it, and every flag after it would be left
// unread: the daemon would ask its grid otherwise than it was told to.
func strays(rest []string) error {
	if len(rest) == 0 {
		return nil
	}
	return fmt.Errorf("%q is no flag, and what follows it would not be read: a flag that takes no value, as --grid-tls, is given none", rest[0])
}

// handed refuses a daemon given a checkout and no name: the command
// line knows it runs beside a daemon by the workspace the sandbox's lock
// names, and one of no name would be found by none. A job names its
// own, as its repository.
func handed(checkout bool, name string) error {
	if checkout && name == "" {
		return fmt.Errorf("--checkout was given with no --name: the command line finds the daemon beside it by the workspace it names, so a job names its own, as its repository")
	}
	return nil
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
// proxy's address, which holds none, kept in the order given: the first
// that matches a download is taken. A pattern given twice is refused,
// and so is one that is no regular expression, at the start rather than
// at the first download.
func rewritesOf(given []string) ([]build.Rewrite, error) {
	var rewrites []build.Rewrite
	for _, each := range given {
		space := strings.LastIndex(each, " ")
		if space <= 0 || space == len(each)-1 {
			return nil, fmt.Errorf("--rewrite %q: want a regular expression, a space and the proxy's address", each)
		}
		pattern, address := each[:space], each[space+1:]
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("--rewrite: %w", err)
		}
		if slices.ContainsFunc(rewrites, func(earlier build.Rewrite) bool { return earlier.From == pattern }) {
			return nil, fmt.Errorf("--rewrite gives %s twice", pattern)
		}
		rewrites = append(rewrites, build.Rewrite{From: pattern, To: address})
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

func serve(ctx context.Context, listen, sourceAddress, workspace, runs string, modules []string, config build.Grid, platform map[string]string, given []string, checkout bool) error {
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
	daemon, err := build.New(ctx, build.Config{Workspace: workspace, Plugins: modulesBytes, Grid: config, Platform: platform, Telemetry: signals, Builds: runs, Given: given, Mounted: checkout})
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
