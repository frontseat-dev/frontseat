// Package fix runs a check's fix where the checkout is. A check runs on
// the grid and only reports; its fix, gofmt -w where the check is
// gofmt -l, never runs there. A run asked to fix runs it here, in the
// daemon's own sandbox, which is what confines it: over a copy of the
// check's inputs, with the same tool the check runs with on the grid,
// brought from the grid by its digest, and with the environment the
// check ran with, as mise said it on the grid. What the fix changed of the
// files it may change is then a change over what the run read, for the
// snapshot to apply or refuse; the copy is thrown away. Nothing of the
// fix is an action of the grid, and nothing it does is kept.
package fix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	execport "github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// Runner runs fixes for one run.
type Runner struct {
	// Grid brings a tool's tree here.
	Grid execport.Materializing
	// Tools is the run's provisioner: what it was answered for each tool
	// is what the fix runs with.
	Tools *tools.Provisioner
	// Kept is the directory tool trees are kept in here, by digest, for
	// as long as the daemon runs.
	Kept string
}

// Run runs an action's fix over a copy of its inputs and returns what it
// changed of the files it may change: each a change over the file as
// the run read it. A fix that ends with a non-zero exit code is an
// error, with what it printed.
func (r Runner) Run(ctx context.Context, snapshot *source.Snapshot, action plan.Action) (changes []source.Change, err error) {
	ctx, span := telemetry.Start(ctx, "fix")
	defer func() { telemetry.End(span, err) }()
	command := action.Declared.GetFix()
	if len(command.GetArguments()) == 0 {
		return nil, fmt.Errorf("%s has no fix", action.Label)
	}
	repository := snapshot.Paths[action.Repository]
	given, err := snapshot.Select(ctx, action.Repository, action.Sources)
	if err != nil {
		return nil, err
	}
	// The copy is kept where the daemon keeps a fix's work, which the
	// daemon after it clears: nothing is left in the system's temporary
	// directory by a daemon that died here.
	if err := os.MkdirAll(r.Kept, 0o755); err != nil {
		return nil, err
	}
	copied, err := os.MkdirTemp(r.Kept, "fix-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(copied)
	root := filepath.Join(copied, "work")
	// The inputs, as the run read them. What a generated source claims
	// is not the check's to see, here as on the grid.
	for _, file := range given {
		if !action.AcceptsSnapshot(action.Repository, file) {
			continue
		}
		if err := r.copy(ctx, snapshot, action.Repository, repository, file, root); err != nil {
			return nil, err
		}
	}
	// The files above the project its tool reads for the workspace, and
	// the files arranged for the check, as the check had them.
	for _, file := range action.WorkspaceFiles {
		if err := r.copy(ctx, snapshot, action.Repository, repository, file, root); err != nil {
			return nil, err
		}
	}
	for _, file := range action.Arranged {
		at := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			return nil, err
		}
		perm := os.FileMode(0o644)
		if file.Executable {
			perm = 0o755
		}
		if err := os.WriteFile(at, file.Content, perm); err != nil {
			return nil, err
		}
	}
	// The tools, as the grid holds them, where mise keeps its own. They
	// are the workers' tools, built for the workers' platform: a daemon
	// on another platform cannot run them, and says so rather than run
	// something else.
	data, home, tmp := filepath.Join(copied, "mise"), filepath.Join(copied, "home"), filepath.Join(copied, "tmp")
	for _, dir := range []string{data, home, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	named := action.Declared.GetTools()
	installs := map[string]string{}
	if len(named) > 0 {
		if workers, here := tools.PlatformName(r.Tools.Platform), tools.Here(); workers != here {
			return nil, &OtherPlatform{Label: action.Label.String(), Tools: named, Workers: workers, Daemon: here}
		}
		for _, tool := range named {
			tree, installed, err := r.Tools.Installed(ctx, snapshot.Root, snapshot, repository, tool)
			if err != nil {
				return nil, err
			}
			kept, err := r.materialize(ctx, tree)
			if err != nil {
				return nil, fmt.Errorf("the tool %s: %w", tool, err)
			}
			if err := os.MkdirAll(filepath.Join(data, "installs"), 0o755); err != nil {
				return nil, err
			}
			if err := os.Symlink(kept, filepath.Join(data, "installs", installed)); err != nil {
				return nil, err
			}
			installs[tool] = filepath.Join(data, "installs", installed)
		}
	}
	// The fix runs with what its check ran with: what mise said, on the
	// grid and confined there, that the tools and the repository's
	// mise.toml put in the check's environment, placed here. Nothing the
	// repository says is asked of mise where the daemon is; the daemon's
	// sandbox is what confines the fix.
	checked, err := r.Tools.Environment(ctx, snapshot.Root, snapshot, repository, named, action.Declared.GetCommand().GetEnvironment(), tools.Layout{Root: root, Home: home, Temporary: tmp, Data: data})
	if err != nil {
		return nil, fmt.Errorf("the fix of %s: %w", action.Label, err)
	}
	env, err := withTools(Environment(home, tmp), checked, installs)
	if err != nil {
		return nil, fmt.Errorf("the fix of %s: %w", action.Label, err)
	}
	// What the action is given as written (plan.Action.Environment) its
	// fix is given too, held to what its check is held to: never under a
	// name mise, the check's command or the fix's own already gives.
	gives := make([]string, 0, len(checked))
	for _, line := range checked {
		name, _, _ := strings.Cut(line, "=")
		gives = append(gives, name)
	}
	if err := tools.Within(action.Label.String(), action.Environment, gives, action.Declared.GetCommand().GetEnvironment(), command.GetEnvironment()); err != nil {
		return nil, fmt.Errorf("the fix of %s: %w", action.Label, err)
	}
	// The fix, in its project, with that environment and its own, and
	// nothing of the daemon's: a tool that is not in the tree it was
	// given is not found elsewhere.
	env = append(env, environment(command.GetEnvironment())...)
	env = append(env, environment(action.Environment)...)
	program, err := found(command.GetArguments()[0], env, data, len(action.Declared.GetTools()) > 0)
	if err != nil {
		return nil, fmt.Errorf("the fix of %s: %w", action.Label, err)
	}
	run := exec.CommandContext(ctx, program, command.GetArguments()[1:]...)
	run.Dir = filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(action.ProjectRoot))
	// The project is there for the fix even where the check reads
	// nothing of it: what the fix makes there is judged like the rest.
	if err := os.MkdirAll(run.Dir, 0o755); err != nil {
		return nil, err
	}
	run.Env = env
	if out, err := run.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("the fix of %s: %w\n%s", action.Label, err, strings.TrimSpace(string(out)))
	}
	return r.changed(ctx, snapshot, action, root, given)
}

// withTools is a fix's environment with what its check's tools and
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
			return nil, fmt.Errorf("the tool %s is not in the tree the fix was given: nothing of it is on the path mise gives", tool)
		}
	}
	return environment(merged), nil
}

// found returns a program as the fix's own path finds it: in one of its
// directories, executable, and nowhere else. A fix that runs with tools
// runs a program of theirs: one found in the system's directories by the
// same name is not it, and is refused.
func found(name string, env []string, data string, withTools bool) (string, error) {
	if strings.Contains(name, string(os.PathSeparator)) {
		return "", fmt.Errorf("the program %q is named by a path: a fix names a program its tools or the system provide", name)
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
			return "", fmt.Errorf("the program %s is %s, which is not of the tools the fix was given", name, at)
		}
		return at, nil
	}
	return "", fmt.Errorf("the program %s is not on the fix's path", name)
}

// beneath reports whether a path is a directory or beneath it, each
// taken as it is on the disk, any link on the way followed: mise names
// a tool's tree by where the tree is, and the daemon by the link it made.
func beneath(directory, at string) bool {
	led, err := filepath.EvalSymlinks(at)
	if err != nil {
		return false
	}
	within, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(within, led)
	if err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return true
	}
	// A directory of links to trees: beneath one of them is beneath it.
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		target, err := filepath.EvalSymlinks(filepath.Join(directory, entry.Name()))
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(target, led); err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return true
		}
	}
	return false
}

// Environment is what a fix runs with before what its check's tools and
// repository put in it: the system's programs, and a home and scratch
// space of its own; nothing of the daemon's.
func Environment(home, tmp string) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + home, "TMPDIR=" + tmp,
	}
}

// environment is a command's own environment, as name=value, sorted.
func environment(given map[string]string) []string {
	out := make([]string, 0, len(given))
	for name, value := range given {
		out = append(out, name+"="+value)
	}
	slices.Sort(out)
	return out
}

// OtherPlatform is a fix that runs with tools, where the daemon is on a
// platform other than the workers': their tools are not its to run.
type OtherPlatform struct {
	Label           string
	Tools           []string
	Workers, Daemon string
}

func (e *OtherPlatform) Error() string {
	return fmt.Sprintf("the fix of %s runs with %s, the workers' %s, and the daemon is on %s: a fix runs where the daemon's platform is the workers'", e.Label, strings.Join(e.Tools, " and "), e.Workers, e.Daemon)
}

// copy writes a file of the snapshot into the copy, as the run read it.
func (r Runner) copy(ctx context.Context, snapshot *source.Snapshot, name, repository, file, root string) error {
	content, found, err := snapshot.Read(ctx, name, file)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, _, executable, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(repository), filepath.FromSlash(file)))
	if err != nil {
		return err
	}
	at := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if executable {
		perm = 0o755
	}
	return os.WriteFile(at, content, perm)
}

// materializing is one tree at a time for each tree: two fixes that
// need one tool wait for each other, and the second finds it there.
// Fixes that need different tools do not wait for each other.
var materializing = struct {
	sync.Mutex
	trees map[string]*sync.Mutex
}{trees: map[string]*sync.Mutex{}}

// materialize brings a tool's tree here, once for as long as the daemon
// runs, and returns where it is. The tree is whole or not there: it is
// brought beside its place and moved into it, so one a daemon died
// under is never taken for whole.
func (r Runner) materialize(ctx context.Context, tree execport.Digest) (string, error) {
	if !hex64(tree.Hash) {
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
	partial := at + ".partial"
	if err := os.RemoveAll(partial); err != nil {
		return "", err
	}
	if err := r.Grid.Materialize(ctx, execport.Held{Digest: tree, Directory: true}, partial); err != nil {
		return "", err
	}
	if err := os.Rename(partial, at); err != nil {
		return "", err
	}
	return at, nil
}

// hex64 says whether a string is a SHA-256 digest as the grid prints
// one: sixty-four lowercase hexadecimal digits, and so a plain file name.
func hex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// changed compares the files the fix may change, in the copy, with what
// the run read: a difference in content or in whether a file may be run
// is a replacement, a file gone a deletion, a file new a creation.
func (r Runner) changed(ctx context.Context, snapshot *source.Snapshot, action plan.Action, root string, given []string) ([]source.Change, error) {
	repository := snapshot.Paths[action.Repository]
	project := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(action.ProjectRoot))
	may := func(file string) bool {
		return pattern.Within(action.ProjectRoot, file, action.Rewrites()) && action.AcceptsSnapshot(action.Repository, file)
	}
	// A file that is new is a creation where the check's inputs would
	// have selected it: a fix creates within what its check reads.
	given_ := func(file string) bool {
		return slices.ContainsFunc(action.Sources, func(group pattern.Group) bool { return group.Accepts(file) })
	}
	var changes []source.Change
	// What was arranged for the check is the host's, and no change of
	// the fix's.
	arranged := map[string]bool{}
	for _, file := range action.Arranged {
		arranged[path.Clean(file.Path)] = true
	}
	wasThere := map[string]bool{}
	for _, file := range given {
		if !may(file) {
			continue
		}
		wasThere[file] = true
		was, _, executable, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(repository), filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		at := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(file))
		info, err := os.Lstat(at)
		switch {
		case os.IsNotExist(err):
			changes = append(changes, source.Change{Path: file, Kind: source.Delete, From: was})
			continue
		case err != nil:
			return nil, err
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("the fix of %s left %s as something other than a file", action.Label, file)
		}
		content, err := os.ReadFile(at)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(content)
		now := info.Mode()&0o100 != 0
		if hex.EncodeToString(sum[:]) != was || now != executable {
			changes = append(changes, source.Change{Path: file, Kind: source.Replace, Content: content, Executable: now, From: was})
		}
	}
	// What is new beneath the project and may be changed is created.
	err := filepath.WalkDir(project, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if at != project && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(project, at)
		if err != nil {
			return err
		}
		file := path.Join(action.ProjectRoot, filepath.ToSlash(rel))
		if wasThere[file] || arranged[file] || !may(file) || !given_(file) {
			return nil
		}
		content, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		changes = append(changes, source.Change{Path: file, Kind: source.Create, Content: content, Executable: info.Mode()&0o100 != 0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(changes, func(a, b source.Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

// Changeable returns the files an action's fix may change: its inputs
// that its rewrite patterns select, sorted. It is what two actions of a
// run must not share.
func Changeable(ctx context.Context, snapshot *source.Snapshot, action plan.Action) ([]string, error) {
	if len(action.Rewrites()) == 0 || action.Declared.GetFix() == nil || !action.IsCheck() {
		return nil, nil
	}
	given, err := snapshot.Select(ctx, action.Repository, action.Sources)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, file := range given {
		if pattern.Within(action.ProjectRoot, file, action.Rewrites()) && action.AcceptsSnapshot(action.Repository, file) {
			out = append(out, path.Join(action.Repository, file))
		}
	}
	return out, nil
}
