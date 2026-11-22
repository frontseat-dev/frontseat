// Package here makes a command ready to run where the daemon is: a
// check's fix, or what publishes a release, neither of which is an
// action of the grid. It runs with the tools an action of the grid runs
// with, brought from the grid by their digests, and with the
// environment mise gives for them, as mise said it on the grid, placed
// here; nothing of the daemon's own environment reaches it but what its
// caller hands it by name. The daemon's sandbox is what confines it.
package here

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// Runner makes commands ready for one run.
type Runner struct {
	// Grid brings a tool's tree here.
	Grid exec.Materializing
	// Tools is the run's provisioner: what it was answered for each tool
	// is what the command runs with.
	Tools *tools.Provisioner
	// Kept is the directory tool trees are kept in here, by digest, for
	// as long as the daemon runs.
	Kept string
}

// Asked is a command to make ready.
type Asked struct {
	// What is what runs, as a mistake names it: "the fix of //a:b";
	// Label the label of its action alone.
	What, Label string
	// Repository is the path of its repository in the workspace.
	Repository string
	// Tools are the tools it runs with, as the repository pins them.
	Tools []string
	// Variables are what mise is told of the action of the grid it runs
	// as: that action's own environment.
	Variables map[string]string
	// Command is what runs.
	Command *pluginv1.Command
	// Given is what it is given as written, held to no name mise, the
	// action of the grid or the command already gives.
	Given map[string]string
	// Root is where the workspace, as much as it reads of it, is laid
	// out; Scratch a directory of its own, for its tools, home and
	// temporary files, which its caller removes.
	Root, Scratch string
}

// Ready returns the environment a command runs with and the program it
// names, as that environment's path finds it.
func (r Runner) Ready(ctx context.Context, snapshot *source.Snapshot, asked Asked) (env []string, program string, err error) {
	root, repository, command := asked.Root, asked.Repository, asked.Command
	// The tools, as the grid holds them, where mise keeps its own. They
	// are the workers' tools, built for the workers' platform: a daemon
	// on another platform cannot run them, and says so rather than run
	// something else.
	data, home, tmp := filepath.Join(asked.Scratch, "mise"), filepath.Join(asked.Scratch, "home"), filepath.Join(asked.Scratch, "tmp")
	for _, dir := range []string{data, home, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, "", err
		}
	}
	named := asked.Tools
	installs := map[string]string{}
	if len(named) > 0 {
		if err := tools.RunsHere(asked.What+" runs with "+strings.Join(named, " and ")+", the workers'", r.Tools.Platform); err != nil {
			return nil, "", err
		}
		for _, tool := range named {
			tree, installed, err := r.Tools.Installed(ctx, snapshot.Root, snapshot, repository, tool)
			if err != nil {
				return nil, "", err
			}
			kept, err := r.materialize(ctx, tree)
			if err != nil {
				return nil, "", fmt.Errorf("the tool %s: %w", tool, err)
			}
			if err := os.MkdirAll(filepath.Join(data, "installs"), 0o755); err != nil {
				return nil, "", err
			}
			if err := os.Symlink(kept, filepath.Join(data, "installs", installed)); err != nil {
				return nil, "", err
			}
			installs[tool] = filepath.Join(data, "installs", installed)
		}
	}
	// It runs with what an action of the grid runs with: what mise
	// said, on the grid and confined there, that the tools and the
	// repository's mise.toml put in the environment, placed here.
	// Nothing the repository says is asked of mise where the daemon is;
	// where the daemon runs is what confines it.
	checked, err := r.Tools.Environment(ctx, snapshot.Root, snapshot, repository, named, asked.Variables, tools.Layout{Root: root, Home: home, Temporary: tmp, Data: data})
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", asked.What, err)
	}
	env, err = withTools(tools.Local(home, tmp), checked, installs)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", asked.What, err)
	}
	// What the action is given as written (plan.Action.Environment) it
	// is given too, held to what an action of the grid is held to: never
	// under a name mise, that action or the command already gives.
	gives := make([]string, 0, len(checked))
	for _, line := range checked {
		name, _, _ := strings.Cut(line, "=")
		gives = append(gives, name)
	}
	if err := tools.Within(asked.Label, asked.Given, gives, asked.Variables, command.GetEnvironment()); err != nil {
		return nil, "", fmt.Errorf("%s: %w", asked.What, err)
	}
	// The command, with that environment and its own, and nothing of
	// the daemon's: a tool that is not in the tree it was
	// given is not found elsewhere.
	env = append(env, tools.Assignments(command.GetEnvironment())...)
	env = append(env, tools.Assignments(asked.Given)...)
	if len(command.GetArguments()) == 0 {
		return nil, "", fmt.Errorf("%s runs nothing", asked.What)
	}
	program, err = found(command.GetArguments()[0], env, data, len(named) > 0)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", asked.What, err)
	}
	return env, program, nil
}

// withTools is a command's environment with what its tools and
// repository put in it, and without what the repository unsets (a name
// alone), each tool held to being on its path in the tree it was given:
// a program of the system's by the same name is not it.
func withTools(env, given []string, installs map[string]string) ([]string, error) {
	merged := map[string]string{}
	for _, line := range append(env, given...) {
		name, value, set := strings.Cut(line, "=")
		if !set {
			delete(merged, name)
			continue
		}
		merged[name] = value
	}
	for tool, install := range installs {
		if !slices.ContainsFunc(filepath.SplitList(merged["PATH"]), func(dir string) bool { return beneath(install, dir) }) {
			return nil, fmt.Errorf("the tool %s is not in the tree it was given: nothing of it is on the path mise gives", tool)
		}
	}
	return tools.Assignments(merged), nil
}

// found returns a program as the command's own path finds it: in one of
// its directories, executable, and nowhere else. One that runs with tools
// runs a program of theirs: one found in the system's directories by the
// same name is not it, and is refused.
func found(name string, env []string, data string, withTools bool) (string, error) {
	if strings.Contains(name, string(os.PathSeparator)) {
		return "", fmt.Errorf("the program %q is named by a path: a program is named as its tools or the system provide it", name)
	}
	var searched string
	for _, line := range env {
		if path, is := strings.CutPrefix(line, "PATH="); is {
			searched = path
		}
	}
	for _, dir := range filepath.SplitList(searched) {
		at := filepath.Join(dir, name)
		info, err := os.Stat(at)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			continue
		}
		if withTools && !beneath(filepath.Join(data, "installs"), at) {
			return "", fmt.Errorf("the program %s is %s, which is not of the tools it was given", name, at)
		}
		return at, nil
	}
	return "", fmt.Errorf("the program %s is not on its path", name)
}

// beneath reports whether a path is a directory or beneath it, each
// taken as it is on the disk, any link on the way followed: mise names
// a tool's tree by where the tree is, and the daemon by the link it made.
func beneath(directory, at string) bool {
	if _, err := exec.Within(directory, at); err == nil {
		return true
	}
	// A directory of links to trees: beneath one of them is beneath it.
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if _, err := exec.Within(filepath.Join(directory, entry.Name()), at); err == nil {
			return true
		}
	}
	return false
}

// materializing is one tree at a time for each tree: two commands that
// need one tool wait for each other, and the second finds it there.
// Those that need different tools do not wait for each other.
var materializing = struct {
	sync.Mutex
	trees map[string]*sync.Mutex
}{trees: map[string]*sync.Mutex{}}

// materialize brings a tool's tree here, once for as long as the daemon
// runs, and returns where it is. The tree is whole or not there: it is
// brought beside its place and moved into it, so one a daemon died
// under is never taken for whole.
func (r Runner) materialize(ctx context.Context, tree exec.Digest) (string, error) {
	if !tree.Valid() {
		return "", fmt.Errorf("the tool's tree is named by %q, which is not a digest", tree.Hash)
	}
	name := fmt.Sprintf("%s-%d", tree.Hash, tree.Size)
	materializing.Lock()
	lock := materializing.trees[name]
	if lock == nil {
		lock = &sync.Mutex{}
		materializing.trees[name] = lock
	}
	materializing.Unlock()
	lock.Lock()
	defer lock.Unlock()
	kept := filepath.Join(r.Kept, "tools")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		return "", err
	}
	at := filepath.Join(kept, name)
	if _, err := os.Stat(at); err == nil {
		return at, nil
	}
	// Its own to delete: a name beneath the daemon's own directory that
	// this function alone makes, for this tree, whose lock is held: what
	// is there is what a daemon that died bringing it left.
	partial := at + ".partial"
	if err := os.RemoveAll(partial); err != nil {
		return "", err
	}
	if err := r.Grid.Materialize(ctx, exec.Held{Digest: tree, Directory: true}, partial); err != nil {
		return "", err
	}
	if err := os.Rename(partial, at); err != nil {
		return "", err
	}
	return at, nil
}
