// Package publish runs what publishes a release (the action kind Publish) where
// the daemon is: its tool's own way of sending a release somewhere, a
// forge's command line or a registry's client, with the tools the
// repository pins, brought from the grid. It is given its unit's
// artifacts, brought from the grid into one directory with a file of
// their sha256s beside them, and the variables of where the daemon runs
// that it names and the daemon was told it may be given, which is how
// its tool is handed a token. Nothing of it is an action of the grid,
// and nothing is kept of it but whether it passed.
package publish

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	execport "github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/here"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/common/confine"
	"github.com/frontseat-dev/frontseat/core/plain"
)

// Checksums is the file of its artifacts' sha256s a publish is given
// beside them, as sha256sum writes and reads one.
const Checksums = "checksums.txt"

// Runner runs what publishes for one release.
type Runner struct {
	// Grid brings a tool's tree, and an artifact, here.
	Grid execport.Materializing
	// Tools is the provisioner its tools are asked of.
	Tools *tools.Provisioner
	// Kept is the directory its work is kept in while it runs, and tool
	// trees for as long as the daemon does.
	Kept string
	// Given are the variables of where the daemon runs that a publish
	// may be given: those the daemon was told. One a publish names that
	// is not among them is not given.
	Given []string
}

// Artifact is a file a unit ships, as the grid holds it.
type Artifact struct {
	Name string
	Held execport.Held
}

// Release is what a publish is given of the unit it publishes.
type Release struct {
	Version    pluginhost.Version
	Artifacts  []Artifact
	Notes      string
	Remote     string
	Prerelease bool
}

// Within is how long one publish may take: a tool that never answers
// would hold the daemon's one release for as long as it runs.
const Within = 30 * time.Minute

// printed is how much of what a publish printed is said of it: its end.
const printed = 64 << 10

// Named holds an artifact's name to one a publish can be given: a
// file's own name, which reaches a command line, so plain text of one
// part that is no option, and not the file of sums. It says why not,
// never repeating a name that is no plain text.
func Named(name string) error {
	switch {
	case !plain.Text(name) || name != filepath.Base(name) || name == "." || name == "..":
		return fmt.Errorf("an artifact has a name that is no plain file name")
	case strings.HasPrefix(name, "-") || strings.HasPrefix(name, "."):
		return fmt.Errorf("the artifact %s begins as an option or a hidden file does", name)
	case name == Checksums:
		return fmt.Errorf("an artifact is named %s, which is the file of sums a publish is given beside them", Checksums)
	}
	return nil
}

// without is text with each of the values left out, and what each
// becomes trimmed or line by line: the longest first, so that one value
// that begins another does not leave the other's end.
func without(text string, values []string) string {
	var secrets []string
	for _, value := range values {
		for _, each := range append([]string{value, strings.TrimSpace(value)}, strings.Split(value, "\n")...) {
			if each = strings.TrimSpace(each); each != "" && !slices.Contains(secrets, each) {
				secrets = append(secrets, each)
			}
		}
		if value != "" && !slices.Contains(secrets, value) {
			secrets = append(secrets, value)
		}
	}
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	var pairs []string
	for _, each := range secrets {
		pairs = append(pairs, each, "…")
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// Run runs an action that publishes, for a unit. It is an error where
// it could not be run, took longer than Within or ended with a non-zero
// exit code, with the end of what it printed, whatever it was given of
// where the daemon runs left out. Whatever it started is ended with it.
func (r Runner) Run(ctx context.Context, snapshot *source.Snapshot, action plan.Action, of Release) error {
	what := "the publish " + action.Label.String()
	ctx, cancel := context.WithTimeout(ctx, Within)
	defer cancel()
	if err := os.MkdirAll(r.Kept, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(r.Kept, "publish-")
	if err != nil {
		return err
	}
	// What it was given is its own to delete: a directory made here,
	// for this run, which nothing else names.
	defer os.RemoveAll(work)
	given, root := filepath.Join(work, "given"), filepath.Join(work, "work")
	for _, dir := range []string{given, root} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	artifacts := slices.Clone(of.Artifacts)
	slices.SortFunc(artifacts, func(a, b Artifact) int { return strings.Compare(a.Name, b.Name) })
	var files []string
	var sums strings.Builder
	for i, each := range artifacts {
		if err := Named(each.Name); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		switch {
		case i > 0 && artifacts[i-1].Name == each.Name:
			return fmt.Errorf("%s: two artifacts are named %s", what, each.Name)
		case each.Held.Directory:
			return fmt.Errorf("%s: the artifact %s is a directory", what, each.Name)
		case !each.Held.Digest.Valid():
			return fmt.Errorf("%s: the artifact %s is held by what is no sha256", what, each.Name)
		}
		if err := r.Grid.Materialize(ctx, each.Held, filepath.Join(given, each.Name)); err != nil {
			return fmt.Errorf("%s: the artifact %s could not be brought: %w", what, each.Name, err)
		}
		fmt.Fprintf(&sums, "%s  %s\n", each.Held.Digest.Hash, each.Name)
		files = append(files, filepath.Join(given, each.Name))
	}
	if len(artifacts) > 0 {
		if err := os.WriteFile(filepath.Join(given, Checksums), []byte(sums.String()), 0o644); err != nil {
			return err
		}
		files = append(files, filepath.Join(given, Checksums))
		slices.Sort(files)
	}
	notes := filepath.Join(work, "notes.md")
	if err := os.WriteFile(notes, []byte(of.Notes), 0o644); err != nil {
		return err
	}
	filled := pluginhost.FillPublish(action.Declared, of.Version, pluginhost.Published{Artifacts: files, Notes: notes, Remote: of.Remote, Prerelease: of.Prerelease})
	command := filled.GetCommand()
	env, program, err := here.Runner{Grid: r.Grid, Tools: r.Tools, Kept: r.Kept}.Ready(ctx, snapshot, here.Asked{
		What: what, Label: action.Label.String(), Repository: snapshot.Paths[action.Repository], Tools: action.Declared.GetTools(),
		Variables: command.GetEnvironment(), Command: command, Given: action.Environment,
		Root: root, Scratch: work,
	})
	if err != nil {
		return err
	}
	// What it names of where the daemon runs, and the daemon was told
	// it may be given, as it is there. Each is kept out of what is said
	// of the run: a tool that prints its token is not repeated.
	var secrets []string
	for _, name := range action.Declared.GetPublish().GetEnvironment() {
		if !slices.Contains(r.Given, name) {
			continue
		}
		if value, set := os.LookupEnv(name); set {
			env = append(env, name+"="+value)
			secrets = append(secrets, value)
		}
	}
	// What it prints goes to a file, not through the daemon: something
	// it left running holds no pipe the daemon would wait on, and what
	// is said of it is its end, however much it printed.
	said, err := os.Create(filepath.Join(work, "printed"))
	if err != nil {
		return err
	}
	defer said.Close()
	run := exec.CommandContext(ctx, program, command.GetArguments()[1:]...)
	run.Dir, run.Env, run.Stdout, run.Stderr = given, env, said, said
	// It is a group of its own, ended whole: when its time is up or the
	// daemon ends, and once it has ended, whatever it left running.
	run.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	run.Cancel = func() error { return confine.SignalGroup(run.Process.Pid, syscall.SIGKILL) }
	failed := run.Run()
	if run.Process != nil {
		_ = confine.SignalGroup(run.Process.Pid, syscall.SIGKILL)
	}
	if failed == nil {
		return nil
	}
	end, cut, from := make([]byte, printed), "", int64(0)
	if info, err := said.Stat(); err == nil && info.Size() > printed {
		cut, from = "… ", info.Size()-printed
	}
	n, _ := said.ReadAt(end, from)
	if ctx.Err() == context.DeadlineExceeded {
		failed = fmt.Errorf("it took longer than %s and was ended", Within)
	}
	return fmt.Errorf("%s: %w\n%s%s", what, failed, cut, strings.TrimSpace(without(strings.ToValidUTF8(string(end[:n]), ""), secrets)))
}
