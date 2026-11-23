// Package tools puts the tools actions run with on the grid. A tool is
// what its repository pins with mise, and mise is what fetches it: one
// action, the only kind that reaches the network, runs mise against the
// tool's pins, cut from the repository's (pins.For), and leaves the
// tool's installation as a directory. That directory is then a tree in the root of every action
// that names the tool, which runs offline, in a home of its own, with
// what mise says the tool and the repository's mise.toml put in the
// environment: asked of mise once for the run, by an action laid out as
// the action's root is, and not at every action. An action so sees its
// tools as it sees its sources: as inputs, the same on every worker.
//
// A worker therefore needs frontseat-confine (common/confine),
// mise, and what a fetch's command runs to place what the asset service
// fetched for a download (exec/download): the shell, tar, and the
// programs that cut and check a part of one.
//
// Whatever a worker runs for a repository is confined: an action, and
// the install of a tool the repository pins. What confines it is the
// runner sandbox it runs in, one action at a time, which holds its root
// and the system's programs and libraries. An action that is not a fetch
// is sent to sandboxes that reach nothing; a fetch and an install are
// sent to those that reach the network (Spec.Network, stage's network
// property), as the sandbox's policy says.
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/pins"
	"github.com/frontseat-dev/frontseat/common/confine"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
	"github.com/frontseat-dev/frontseat/core/telemetry"
)

// home is the directory of an action's root that holds what frontseat
// puts there: the tools, and the action's own home and scratch space.
const home = ".frontseat"

// Home is an action's home, relative to its root: where a tool keeps
// what is its own, such as what it fetched.
const Home = home + "/home"

// temporary is where mise keeps its temporary files beneath an action's
// root, and data where it keeps its own, the tools among it
// (confine.MiseInstalls).
const (
	temporary = home + "/tmp"
	data      = home + "/mise"
)

// Downloads is where what a fetch downloaded is placed in its root, each
// by its digest.
const Downloads = home + "/downloads"

// Path is where the system's programs are found, on a worker and where
// the daemon runs what it runs itself.
const Path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// worker is what every action, and every install of a tool, is given of
// the worker's: where its programs are, and how mise is to run there.
var worker = []confine.Variable{
	{Name: "PATH", Parts: []string{Path}},
	{Name: "MISE_YES", Parts: []string{"1"}},
	{Name: "MISE_TRUSTED_CONFIG_PATHS", Parts: []string{"/"}},
}

// beneath is a value naming a path beneath the action's root, which is
// known only on the worker.
func beneath(at string) []string { return []string{"", "/" + at} }

// installing is how a tool a repository pins is installed, in a root
// that holds the repository's pin files and nothing else. A repository's
// pins can name a step of its own to run once a tool is installed, and a
// repository is not the worker's to trust: so mise, and whatever it
// runs, is confined to the root, as an action is. It has the network, to
// fetch with. mise is held to the lock: what it fetches has the checksum
// the lock holds, and a lock it cannot read, or one that does not pin
// the tool, is a failure. Without that a mise older than the lock's
// format reads no lock and installs what it is served.
var installing = mustEncode(confine.Spec{
	Home:      ".home",
	Temporary: ".tmp",
	Network:   true,
	Environment: append([]confine.Variable{
		{Name: "MISE_DATA_DIR", Parts: beneath(".mise")},
		{Name: "MISE_LOCKED", Parts: []string{"1"}},
	}, worker...),
})

// base is what every action's command runs with, after its own
// environment and before what mise and what it is given as written say:
// its home, its temporary directory and mise's own beneath its root, and
// mise offline; the confiner then gives the command a temporary
// directory of its own beside the root, in place of this one, which mise
// keeps. Its own environment names none of them for it.
var base = append([]confine.Variable{
	{Name: "HOME", Parts: beneath(Home)},
	{Name: "TMPDIR", Parts: beneath(temporary)},
	{Name: "MISE_DATA_DIR", Parts: beneath(data)},
	{Name: "MISE_OFFLINE", Parts: []string{"1"}},
	{Name: "MISE_EXEC_AUTO_INSTALL", Parts: []string{"0"}},
}, worker...)

// sets says whether what runs an action sets name itself: frontseat-confine
// (HOME, TMPDIR, PATH, PWD and mise's MISE_*).
func sets(name string) bool {
	switch name {
	case "HOME", "TMPDIR", "PATH", "PWD":
		return true
	}
	return strings.HasPrefix(name, "MISE_")
}

// Within refuses what an action is given as written (within) where it
// could not be given as it is: under a name that is no variable's, one
// that what runs and confines the action sets itself, one that mise
// gives for its tools or its repository's mise.toml (mise, by name), or
// one the action is given already (own). It is the one check, for an
// action of the grid and for a fix, which runs where the daemon is.
func Within(label string, within map[string]string, mise []string, own ...map[string]string) error {
	for _, name := range sortedNames(within) {
		if !testservice.Exportable(name) {
			// What was written may hold a value: it is not repeated.
			return fmt.Errorf("%s must be given what is no name of a variable", label)
		}
		if sets(name) {
			return fmt.Errorf("%s must be given %s as it is, and what runs and confines it sets %s itself", label, name, name)
		}
		for _, each := range own {
			if _, also := each[name]; also {
				return fmt.Errorf("%s must be given %s as it is, and is given it twice", label, name)
			}
		}
		if slices.Contains(mise, name) {
			return fmt.Errorf("%s must be given %s as it is, and mise sets %s for its tools or its repository's mise.toml", label, name, name)
		}
	}
	return nil
}

// command is how an action's command runs, as frontseat-confine is told:
// confined to its root, with its own environment (own), then what every
// action runs with (base), then what its tools and its repository's
// mise.toml put in the environment (given), then what the action is
// given as written (within), and with no network unless it fetches
// (network). A later one of a name replaces an earlier: what mise gives
// is the repository's word on what the action's own environment said. What a fetch's network holds is not this one's to say: a
// worker is on a network that holds the proxy and nothing else.
//
// The environment is mise's, asked once for the run (environ), and not
// asked again at every action: mise x around each command cost every
// action a process of its own.
//
// All of it is set inside what confines the action, by frontseat-confine
// just before the command runs: a repository's mise.toml says what a path holds
// and what a variable is, and a plugin what its command is given, and
// nothing either says reaches what confines the action, nor where that is
// found.
func command(own map[string]string, given []given, within map[string]string, network bool) (string, error) {
	env := owned(own, len(given)+len(within))
	for _, each := range given {
		env = append(env, confine.Variable{Name: each.name, Parts: each.parts})
	}
	for _, name := range sortedNames(within) {
		env = append(env, confine.Variable{Name: name, Parts: []string{within[name]}})
	}
	return confine.Encode(confine.Spec{Home: Home, Temporary: temporary, Network: network, Environment: env})
}

// asking is how mise is asked for the environment of some tools and of
// the repository's mise.toml, in a root laid out as an action's is and in
// the repository's directory, confined as an action is, with the
// action's own environment (own), which a repository's mise.toml may
// build on: the root it ran in, on its first line, then mise's answer.
func asking(own map[string]string) (string, error) {
	return confine.Encode(confine.Spec{Home: Home, Temporary: temporary, SayRoot: true, Environment: owned(own, 0)})
}

// owned is what every command an action runs by is first given: the
// action's own environment, by name, then the base every action has,
// with room for more after them.
func owned(own map[string]string, more int) []confine.Variable {
	env := make([]confine.Variable, 0, len(own)+len(base)+more)
	for _, name := range sortedNames(own) {
		env = append(env, confine.Variable{Name: name, Parts: []string{own[name]}})
	}
	return append(env, base...)
}

// commands are the two an action of an environment runs by, as
// frontseat-confine is told: with no network, and as a fetch.
type commands struct {
	run, fetch string
	// given is what they set, as mise gave it.
	given []given
}

// given is a variable mise gave, its value cut where it named the root
// it was asked in: what stands between one cut and the next follows the
// root, a path beneath it. One with no value is one the repository's
// mise.toml unsets.
type given struct {
	name  string
	parts []string
}

// sortedNames is the names of an environment, in order.
func sortedNames(variables map[string]string) []string {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mustEncode is a spec made once, of values this package holds.
func mustEncode(spec confine.Spec) string {
	encoded, err := confine.Encode(spec)
	if err != nil {
		panic(err)
	}
	return encoded
}

// Provisioner puts tools on a grid, for one run. It asks the grid for a
// tool once and holds where it is from then on, so it is made anew for
// each run: the grid is what knows whether it still holds a tool, and
// may let one go between two runs.
type Provisioner struct {
	// Executor runs the action that fetches a tool.
	Executor exec.Executor
	// Platform is the platform of the workers the tools are for.
	Platform map[string]string

	// installs are the trees of the tools installed for the run, each
	// installed once however many actions ask for it at once, and none
	// holding up the install of another.
	installs shared[exec.Digest]
	// environs are the commands an action of some tools of a repository
	// runs by, with what mise puts in their environment, asked of mise
	// once for the run.
	environs shared[commands]

	// mu guards asked and given, and is held across nothing that waits.
	mu sync.Mutex
	// asked is what the run was answered for each tool of each
	// repository: its pins are read once, as the run's files are.
	asked map[string]asked
	// given are the pins each repository gives an action of some tools,
	// made once for the run: the run's files do not change under it.
	given map[string][2][]byte
}

// Join stops whatever the provisioner still has the grid doing and
// returns once it has stopped: an install or an ask of mise that none
// waits for any more is told to stop when the last gives up, and goes on
// until its call to the grid returns. A run joins its provisioner before
// it ends, so that the grid it asks is closed only after nothing of the
// run asks it.
func (p *Provisioner) Join() {
	p.installs.join()
	p.environs.join()
}

// asked is a tool as a run was answered for it: its tree, and the name
// of the directory mise keeps it in.
type asked struct {
	tree      exec.Digest
	installed string
}

// Use makes an action run confined to its root, with the tools it names,
// and with no network unless it is a fetch. Each tool's
// tree is put in its root, the pins of those tools are written where the
// repository's are (pins.For), or, for an action that reads the
// repository's pins itself, those are its inputs whole; and its command
// runs with what mise says those tools and the repository's mise.toml
// put in the environment (environ). The repository is the
// directory, relative to the action's Root, of the repository the action
// belongs to.
//
// An action that names no tool is confined all the same: what it may
// reach does not depend on what it runs with. It is given the
// environment of its repository's mise.toml, as one that names tools is.
//
// A tool the repository's lock holds no checksum of, for the workers'
// platform, is refused: nothing would say what the action ran with.
//
// The action's own Environment is set first, and what mise gives after
// it, so a name both set is mise's; mise is asked with it, as a
// repository's mise.toml may build on it. within is what the action is
// given as written, set after what mise gives. One that what runs the
// action sets itself (sets), or that mise gives for its tools or its
// repository's mise.toml, is refused by name, never replaced without a
// word. All of it is set by frontseat-confine just before it runs the
// action, and none of it is in the environment of what starts and
// confines it: the action is returned with no environment, and all of
// it is in its command, so in its key.
func (p *Provisioner) Use(ctx context.Context, action exec.Action, repository string, tools []string, fetch bool, within map[string]string) (exec.Action, error) {
	if err := Within(action.Label, within, nil, action.Environment); err != nil {
		return exec.Action{}, err
	}
	held := make(map[string]exec.Held, len(action.Held)+len(tools))
	for at, each := range action.Held {
		held[at] = each
	}
	for _, tool := range tools {
		tree, installed, err := p.Installed(ctx, action.Root, action.Files, repository, tool)
		if err != nil {
			return exec.Action{}, err
		}
		held[path.Join(confine.MiseInstalls, installed)] = exec.Held{Digest: tree, Directory: true}
	}
	action.Held = held
	given, err := p.environ(ctx, action.Root, action.Files, repository, tools, held, action.Environment)
	if err != nil {
		return exec.Action{}, err
	}
	gives := make([]string, 0, len(given.given))
	for _, each := range given.given {
		gives = append(gives, each.name)
	}
	if err := Within(action.Label, within, gives); err != nil {
		return exec.Action{}, err
	}
	if len(tools) > 0 {
		pinned := [2]string{path.Join(repository, pins.Files[0]), path.Join(repository, pins.Files[1])}
		read := false
		for _, at := range pinned {
			// Nothing else written for the action stands in their place.
			if _, over := action.Written[at]; over {
				return exec.Action{}, fmt.Errorf("%s writes %s, which is where the repository's tool pins are given", action.Label, at)
			}
			read = read || slices.Contains(action.Inputs, at)
		}
		if read {
			// An action that reads the repository's pins itself is
			// given them whole: it is given what it reads.
			for _, at := range pinned {
				if !slices.Contains(action.Inputs, at) {
					action.Inputs = append(action.Inputs, at)
				}
			}
		} else {
			// The pins of its tools, where the repository's are: what its
			// environment was asked of mise by, and nothing of another
			// tool's or of a task, which the action never runs with.
			config, lock, err := p.pinsOf(action.Root, action.Files, repository, tools)
			if err != nil {
				return exec.Action{}, err
			}
			written := make(map[string]exec.Written, len(action.Written)+len(pins.Files))
			for at, each := range action.Written {
				written[at] = each
			}
			written[pinned[0]] = exec.Written{Content: config}
			written[pinned[1]] = exec.Written{Content: lock}
			action.Written = written
		}
	}
	up, err := filepath.Rel(filepath.FromSlash("/"+action.WorkingDirectory), "/")
	if err != nil {
		return exec.Action{}, err
	}
	spec := given.run
	if fetch {
		spec = given.fetch
	}
	if len(within) > 0 {
		if spec, err = command(action.Environment, given.given, within, fetch); err != nil {
			return exec.Action{}, err
		}
	}
	// What the action is given is in what frontseat-confine is told, and
	// nothing of it in the environment of what starts and confines it.
	action.Arguments = confine.Arguments(spec, filepath.ToSlash(up), action.Arguments)
	action.Environment = nil
	return action, nil
}

// environ returns the commands an action of a repository that names
// some tools runs by, with what mise puts in their environment, asked of
// mise once for the run: by an action laid out as the action's root is,
// the tools' trees where the action has them and the pins it is given
// (pinsOf), which hold the repository's [env], with the action's own
// environment, and in the repository's directory, where its mise.toml
// is: what mise.toml says of a path or of the directory it is asked in
// is said of the repository's. An action that names no tool is given
// the [env] alone; a repository with no mise.toml gives it nothing.
// Actions that ask at once wait for one answer.
//
// mise names a tool by where it is, in the root it was asked in; each
// action's root is elsewhere, so a value is cut where it names that
// root, and frontseat-confine joins it by the action's own. Each tool must be on the path mise gives, in its own tree,
// as a fix's are (fix.toolsEnvironment).
func (p *Provisioner) environ(ctx context.Context, root string, files exec.Files, repository string, tools []string, held map[string]exec.Held, variables map[string]string) (commands, error) {
	key := keyOf(root, repository, tools, variables)
	if given, there := p.environs.known(key); there {
		return given, nil
	}
	return p.environs.do(ctx, key, func(asking context.Context) (commands, error) {
		return p.ask(asking, root, files, repository, tools, held, variables)
	})
}

// keyOf is what environ knows an answer by: the root, the repository,
// the tools and the environment, its names in order, made in one go.
// Each is ended by a NUL, which no name or value of an environment holds:
// no two are read as the same.
func keyOf(root, repository string, tools []string, variables map[string]string) string {
	var room [16]string
	names := room[:0]
	for name := range variables {
		names = append(names, name)
	}
	slices.Sort(names)
	size := len(root) + len(repository) + 2
	for _, tool := range tools {
		size += len(tool) + 1
	}
	for _, name := range names {
		size += len(name) + len(variables[name]) + 2
	}
	var key strings.Builder
	key.Grow(size)
	key.WriteString(root)
	key.WriteByte(0)
	key.WriteString(repository)
	key.WriteByte(0)
	for _, tool := range tools {
		key.WriteString(tool)
		key.WriteByte(0)
	}
	for _, name := range names {
		key.WriteString(name)
		key.WriteByte(0)
		key.WriteString(variables[name])
		key.WriteByte(0)
	}
	return key.String()
}

// ask asks mise, on the grid, what environ answers.
func (p *Provisioner) ask(ctx context.Context, root string, files exec.Files, repository string, tools []string, held map[string]exec.Held, variables map[string]string) (commands, error) {
	installs := map[string]exec.Held{}
	for at, each := range held {
		if strings.HasPrefix(at, confine.MiseInstalls+"/") {
			installs[at] = each
		}
	}
	config, lock, err := p.pinsOf(root, files, repository, tools)
	if len(tools) == 0 && errors.Is(err, fs.ErrNotExist) {
		config, lock, err = nil, nil, nil
	}
	if err != nil {
		return commands{}, err
	}
	var vars []given
	if config != nil {
		up, err := filepath.Rel(filepath.FromSlash("/"+repository), "/")
		if err != nil {
			return commands{}, err
		}
		spec, err := asking(variables)
		if err != nil {
			return commands{}, err
		}
		ask := exec.Action{
			Label:            strings.TrimSpace("mise env " + strings.Join(tools, " ")),
			Root:             root,
			Written:          map[string]exec.Written{path.Join(repository, pins.Files[0]): {Content: config}, path.Join(repository, pins.Files[1]): {Content: lock}},
			Held:             map[string]exec.Held{},
			WorkingDirectory: repository,
			Arguments:        confine.Arguments(spec, filepath.ToSlash(up), []string{"mise", "env", "--json"}),
			Platform:         p.Platform,
		}
		for at, each := range installs {
			ask.Held[at] = each
		}
		spanned, span := telemetry.Start(ctx, "environment", attribute.StringSlice("tools", tools))
		done, err := p.Executor.Execute(spanned, ask)
		telemetry.End(span, err)
		if err != nil {
			return commands{}, err
		}
		if done.ExitCode != 0 {
			return commands{}, fmt.Errorf("%s exited %d: %s", ask.Label, done.ExitCode, strings.TrimSpace(string(done.Stderr)))
		}
		if vars, err = exported(done.Stdout, installs); err != nil {
			return commands{}, fmt.Errorf("%s: %w", ask.Label, err)
		}
		// What the repository unsets, mise does not name: it is unset as
		// mise x unset it, where mise gave it no value.
		unset, err := pins.Unset(config)
		if err != nil {
			return commands{}, err
		}
		for _, name := range unset {
			if !slices.ContainsFunc(vars, func(each given) bool { return each.name == name }) {
				vars = append(vars, given{name: name})
			}
		}
	}
	run, err := command(variables, vars, nil, false)
	if err != nil {
		return commands{}, err
	}
	fetch, err := command(variables, vars, nil, true)
	if err != nil {
		return commands{}, err
	}
	return commands{run: run, fetch: fetch, given: vars}, nil
}

// exported reads what asking printed: each variable mise gave, by name,
// its value cut where it names the root mise was asked in. Each tool's
// tree must be on the path it gives.
func exported(printed []byte, installs map[string]exec.Held) ([]given, error) {
	asked, answer, found := bytes.Cut(printed, []byte("\n"))
	if !found || len(asked) == 0 {
		return nil, fmt.Errorf("it printed no root: %q", printed)
	}
	var answered map[string]string
	if err := json.Unmarshal(answer, &answered); err != nil {
		return nil, fmt.Errorf("mise's answer is no environment: %w", err)
	}
	names := make([]string, 0, len(answered))
	for name := range answered {
		if !testservice.Exportable(name) {
			return nil, fmt.Errorf("mise gives %q, which is no variable's name", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for at := range installs {
		if !OnPath(answered["PATH"], string(asked)+"/"+at, lexically) {
			return nil, fmt.Errorf("%s is not on the path mise gives", at)
		}
	}
	vars := make([]given, 0, len(names))
	for _, name := range names {
		vars = append(vars, given{name: name, parts: strings.Split(answered[name], string(asked))})
	}
	return vars, nil
}

// OnPath reports whether a tool's tree is on a PATH mise gives: one of
// its directories is the tree or beneath it, as beneath tells where the
// path is read (by its text on the grid, on the disk where the daemon is).
// A program of the system's by the same name is not the tool.
func OnPath(path, tree string, beneath func(tree, dir string) bool) bool {
	return slices.ContainsFunc(filepath.SplitList(path), func(dir string) bool { return beneath(tree, dir) })
}

// lexically reports whether a directory is a tree or beneath it by its
// text alone, as a path mise printed elsewhere is read.
func lexically(tree, dir string) bool {
	return dir == tree || strings.HasPrefix(dir, tree+"/")
}

// Layout is where a directory laid out as an action's root is, and where
// what an action keeps beneath its root is kept there: its home, its
// temporary files and mise's own. A fix is run so, where the daemon is.
type Layout struct {
	Root, Home, Temporary, Data string
}

// ended reports whether what follows a path ends its name: nothing, or
// what no file name holds, such as /, : or a space.
func ended(rest string) bool {
	if rest == "" {
		return true
	}
	c := rest[0]
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '+' || c == '@' || c >= 0x80)
}

// Environment returns what mise puts in the environment of an action of
// a repository that names some tools and runs with some variables, as
// name=value, the same that the run's actions of those tools are given
// (environ), placed in a layout: what mise named beneath the root it was
// asked in is beneath the layout's own. A variable the repository unsets
// is its name alone. A fix so runs with what its check ran with.
func (p *Provisioner) Environment(ctx context.Context, root string, files exec.Files, repository string, tools []string, variables map[string]string, layout Layout) ([]string, error) {
	held := make(map[string]exec.Held, len(tools))
	for _, tool := range tools {
		tree, installed, err := p.Installed(ctx, root, files, repository, tool)
		if err != nil {
			return nil, err
		}
		held[path.Join(confine.MiseInstalls, installed)] = exec.Held{Digest: tree, Directory: true}
	}
	answered, err := p.environ(ctx, root, files, repository, tools, held, variables)
	if err != nil {
		return nil, err
	}
	beneath := []struct{ within, at string }{
		{"/" + data, layout.Data},
		{"/" + Home, layout.Home},
		{"/" + temporary, layout.Temporary},
		{"", layout.Root},
	}
	out := make([]string, 0, len(answered.given))
	for _, each := range answered.given {
		if each.parts == nil {
			out = append(out, each.name)
			continue
		}
		var value strings.Builder
		value.WriteString(each.parts[0])
		for _, part := range each.parts[1:] {
			for _, place := range beneath {
				if rest, is := strings.CutPrefix(part, place.within); is && (place.within == "" || ended(rest)) {
					value.WriteString(place.at + rest)
					break
				}
			}
		}
		out = append(out, each.name+"="+value.String())
	}
	return out, nil
}

// PlatformName is the name mise gives the workers' platform, as the lock
// pins a tool for it: linux-arm64. An architecture mise has no name of
// its own for is named as the platform names it, so that a lock that
// pins nothing for it says which.
func PlatformName(platform map[string]string) string {
	arch, named := map[string]string{"aarch64": "arm64", "x86-64": "x64"}[platform["ISA"]]
	if !named {
		arch = strings.ToLower(platform["ISA"])
	}
	return strings.ToLower(platform["OSFamily"]) + "-" + arch
}

// DaemonPlatform is the daemon's platform, named as PlatformName names
// the workers': linux-arm64. What runs where the daemon is, a fix or a
// program, runs what the workers installed or built only where the two
// are the same.
func DaemonPlatform() string {
	arch, named := map[string]string{"arm64": "arm64", "amd64": "x64"}[runtime.GOARCH]
	if !named {
		arch = runtime.GOARCH
	}
	return runtime.GOOS + "-" + arch
}

// OtherPlatform is what the workers installed or built, asked to run
// where the daemon is, on another platform than theirs: it is not the
// daemon's to run.
type OtherPlatform struct {
	// What is what was asked to run and what it has of the workers', as
	// a refusal says it before it names their platform.
	What            string
	Workers, Daemon string
}

func (e *OtherPlatform) Error() string {
	return fmt.Sprintf("%s %s, and the daemon is on %s: it runs where the daemon's platform is the workers'", e.What, e.Workers, e.Daemon)
}

// RunsHere refuses what the workers installed or built for a platform
// where the daemon is not on it (OtherPlatform).
func RunsHere(what string, platform map[string]string) error {
	if workers, daemon := PlatformName(platform), DaemonPlatform(); workers != daemon {
		return &OtherPlatform{What: what, Workers: workers, Daemon: daemon}
	}
	return nil
}

// Local is what a command the daemon runs where it is, a fix or a
// program, runs with before anything else: the system's programs, and a
// home and scratch space of its own; nothing of the daemon's.
func Local(home, tmp string) []string {
	return []string{"PATH=" + Path, "HOME=" + home, "TMPDIR=" + tmp}
}

// Assignments returns variables as a command's environment is written,
// name=value, sorted.
func Assignments(variables map[string]string) []string {
	out := make([]string, 0, len(variables))
	for name, value := range variables {
		out = append(out, name+"="+value)
	}
	slices.Sort(out)
	return out
}

// Installed returns a tool of a repository as the run was answered for
// it, asking where it has not been asked: a tree the grid holds, and the
// name of the directory mise keeps it in. An action is given it in its
// root, and a fix runs with it where the checkout is, the same tool the
// check runs with on the grid.
func (p *Provisioner) Installed(ctx context.Context, root string, files exec.Files, repository, tool string) (exec.Digest, string, error) {
	key := root + "\x00" + repository + "\x00" + tool
	p.mu.Lock()
	known, there := p.asked[key]
	p.mu.Unlock()
	if there {
		return known.tree, known.installed, nil
	}
	tree, installed, err := p.pinned(ctx, root, files, repository, tool)
	if err != nil {
		return exec.Digest{}, "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.asked == nil {
		p.asked = map[string]asked{}
	}
	p.asked[key] = asked{tree: tree, installed: installed}
	return tree, installed, nil
}

// pinned returns a tool's installation as a tree the grid holds, fetching
// it where the grid has not kept it, and the name of the directory mise
// keeps it in. A plugin names a tool; the repository's lock says what it
// is pinned as, which is the tool's own name or that name with what
// provides it, and mise is asked for it by that.
func (p *Provisioner) pinned(ctx context.Context, root string, files exec.Files, repository, tool string) (tree exec.Digest, installed string, err error) {
	config, lock, err := p.pinsOf(root, files, repository, []string{tool})
	if err != nil {
		return exec.Digest{}, "", err
	}
	sum := sha256.New()
	fmt.Fprintf(sum, "%d %d\n", len(config), len(lock))
	sum.Write(config)
	sum.Write(lock)
	named, err := pins.Named(lock, tool)
	if err != nil {
		return exec.Digest{}, "", fmt.Errorf("the tool %s: %w", tool, err)
	}
	if err := pins.Locked(lock, tool, PlatformName(p.Platform)); err != nil {
		return exec.Digest{}, "", fmt.Errorf("the tool %s: %w", tool, err)
	}
	// mise names a tool's directory after what it is pinned as, with what
	// would not be a file name made one.
	installed = strings.NewReplacer(":", "-", "/", "-").Replace(named)
	key := named + " " + hex.EncodeToString(sum.Sum(nil))

	tree, err = p.installs.do(ctx, key, func(fetching context.Context) (exec.Digest, error) {
		return p.install(fetching, root, repository, tool, named, installed, config, lock)
	})
	return tree, installed, err
}

// install fetches a tool's installation on the grid and returns it as a
// tree the grid holds. It holds no lock: it lasts as long as the fetch
// does, and the other tools of the run are installed meanwhile.
func (p *Provisioner) install(ctx context.Context, root, repository, tool, named, installed string, config, lock []byte) (tree exec.Digest, err error) {
	// The action's root is the tool's pins and nothing else, at no path of
	// the repository's: repositories that pin the tool alike share it.
	ctx, span := telemetry.Start(ctx, "tool", attribute.String("tool", named))
	defer func() { telemetry.End(span, err) }()
	output := path.Join(".mise", "installs", installed)
	fetch := exec.Action{
		Label:     "mise install " + named,
		Root:      filepath.Join(root, filepath.FromSlash(repository)),
		Written:   map[string]exec.Written{pins.Files[0]: {Content: config}, pins.Files[1]: {Content: lock}},
		Arguments: confine.Arguments(installing, ".", []string{"mise", "install", named}),
		Outputs:   []string{output},
		// It fetches the tool, so it is sent where the network is reached.
		Platform: exec.Reaching(p.Platform),
	}
	done, err := p.Executor.Execute(ctx, fetch)
	if err != nil {
		return exec.Digest{}, err
	}
	if done.ExitCode != 0 {
		return exec.Digest{}, fmt.Errorf("the tool %s: mise install exited %d: %s", tool, done.ExitCode, strings.TrimSpace(string(done.Stderr)))
	}
	left, there := done.Outputs[output]
	if !there || !left.Directory {
		return exec.Digest{}, fmt.Errorf("the tool %s: mise install left nothing at %s", tool, output)
	}
	return left.Digest, nil
}

// pinsOf returns the pins an action of a repository that runs with some
// tools is given (pins.For), made once for the run. The repository's pins
// are the run's files, read as the run reads any other: what a tool is
// found by is what the run began with.
func (p *Provisioner) pinsOf(root string, files exec.Files, repository string, tools []string) (config, lock []byte, err error) {
	key := root + "\x00" + repository + "\x00" + strings.Join(tools, "\x00")
	p.mu.Lock()
	known, there := p.given[key]
	p.mu.Unlock()
	if there {
		return known[0], known[1], nil
	}
	if config, lock, err = pinsFor(root, files, repository, tools); err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.given == nil {
		p.given = map[string][2][]byte{}
	}
	p.given[key] = [2][]byte{config, lock}
	return config, lock, nil
}

// pinsFor reads a repository's pins and cuts them to some tools.
func pinsFor(root string, files exec.Files, repository string, tools []string) (config, lock []byte, err error) {
	var content [2][]byte
	for i, file := range pins.Files {
		at := filepath.Join(root, filepath.FromSlash(repository), file)
		if files != nil {
			content[i], err = files.Content(at)
		} else {
			content[i], err = os.ReadFile(at)
		}
		if err != nil && i == 1 && len(tools) == 0 && errors.Is(err, fs.ErrNotExist) {
			// A mise.toml of no tool locks nothing: its [env] needs no lock.
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("the tools %s: their repository pins them in %s: %w", strings.Join(tools, ", "), strings.Join(pins.Files, " and "), err)
		}
	}
	return pins.For(content[0], content[1], tools)
}
