package fix_test

import (
	"context"
	"errors"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/server/internal/exec/fix"
	"github.com/frontseat-dev/frontseat/server/internal/exec/here"
	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
)

// A fix that runs with the workers' tools is refused, by name, where the
// daemon is on another platform: nothing else is run in their place.
func TestAFixWithToolsOfAnotherPlatformIsRefused(t *testing.T) {
	other := map[string]string{"OSFamily": "linux", "ISA": "x86-64"}
	if tools.Here() == tools.PlatformName(other) {
		other = map[string]string{"OSFamily": "linux", "ISA": "aarch64"}
	}
	snapshot := sourcetest.Taken(t, nil)
	action := plan.Action{
		Label:    model.Label{Path: "app", Plugin: "go", Name: "format"},
		Declared: &pluginv1.Action{Name: "format", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"gofmt", "-w", "."}}, Rewrites: []string{"**/*.go"}}}, Tools: []string{"go"}},
	}
	runner := fix.Runner{Tools: &tools.Provisioner{Platform: other}, Kept: t.TempDir()}
	_, err := runner.Run(context.Background(), snapshot, action)
	var refused *here.OtherPlatform
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), runtime.GOOS) || !strings.Contains(err.Error(), "go") {
		t.Fatalf("a fix with another platform's tools was not refused by name: %v", err)
	}
}

// A fix's environment is the system's programs and its own home, and
// nothing of the daemon's: a tool is found in the tree it was given or
// not at all.
func TestAFixRunsWithNothingOfTheDaemonsEnvironment(t *testing.T) {
	t.Setenv("PATH", "/nowhere/of/the/daemon:"+t.TempDir())
	t.Setenv("INJECTED", "yes")
	env := here.Environment("/h", "/t")
	for _, line := range env {
		if strings.Contains(line, "nowhere") || strings.HasPrefix(line, "INJECTED=") {
			t.Fatalf("the daemon's environment reached the fix: %q", line)
		}
	}
	for _, want := range []string{"PATH=/usr/local/sbin:", "HOME=/h", "TMPDIR=/t"} {
		if !strings.Contains(strings.Join(env, "\n"), want) {
			t.Fatalf("the fix's environment lacks %q: %v", want, env)
		}
	}
}

// A fix runs over what its check ran over: the project's inputs, the
// workspace files above it, and the files arranged for it.
func TestAFixIsGivenItsChecksWorkspaceAndArrangedFiles(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"Cargo.lock": "lock\n", "crates/hello/f.txt": "before\n"})
	action := plan.Action{
		Label:          model.Label{Path: "crates/hello", Plugin: "custom", Name: "own"},
		ProjectRoot:    "crates/hello",
		Sources:        []pattern.Group{{Directory: "crates/hello", Patterns: []string{"f.txt"}}},
		WorkspaceFiles: []string{"Cargo.lock"},
		Arranged:       []plan.Arranged{{Path: "Cargo.toml", Content: []byte("narrowed\n")}},
		Declared:       &pluginv1.Action{Name: "own", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", "cat ../../Cargo.lock ../../Cargo.toml > f.txt"}}, Rewrites: []string{"f.txt"}}}, Inputs: []*pluginv1.Input{{Kind: &pluginv1.Input_Source{Source: "f.txt"}}}, Command: &pluginv1.Command{Arguments: []string{"true"}}},
	}
	changes, err := fix.Runner{Tools: &tools.Provisioner{}, Kept: t.TempDir()}.Run(context.Background(), snapshot, action)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || string(changes[0].Content) != "lock\nnarrowed\n" {
		t.Fatalf("the fix did not see the workspace and arranged files: %+v", changes)
	}
}

// A fix runs with what its check runs with: what its repository's
// mise.toml puts in the environment, a value and a directory of its own
// on the path, placed in the fix's copy. The control is the repository
// without it, whose fix is given neither.
func TestAFixRunsWithWhatItsRepositorysMiseTomlPutsInTheEnvironment(t *testing.T) {
	grid := reapitest.Dial(t)
	said := "said " + time.Now().Format(time.RFC3339Nano)
	fixed := func(t *testing.T, config string) string {
		t.Helper()
		snapshot := sourcetest.Taken(t, map[string]string{"mise.toml": config, "app/f.txt": "before\n"})
		action := plan.Action{
			Label:       model.Label{Path: "app", Plugin: "custom", Name: "own"},
			ProjectRoot: "app",
			Sources:     []pattern.Group{{Directory: "app", Patterns: []string{"f.txt"}}},
			Declared:    &pluginv1.Action{Name: "own", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", `printf '%s\n%s\n' "$FROM_THE_REPOSITORY" "$PATH" > f.txt`}}, Rewrites: []string{"f.txt"}}}, Inputs: []*pluginv1.Input{{Kind: &pluginv1.Input_Source{Source: "f.txt"}}}, Command: &pluginv1.Command{Arguments: []string{"true"}}},
		}
		runner := fix.Runner{Tools: &tools.Provisioner{Executor: grid, Platform: testgrid.Platform()}, Kept: t.TempDir()}
		changes, err := runner.Run(context.Background(), snapshot, action)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 {
			t.Fatalf("the fix changed %d files, want f.txt", len(changes))
		}
		return string(changes[0].Content)
	}

	if without := fixed(t, "[settings]\nexperimental = false\n"); strings.Contains(without, "said ") || strings.Contains(without, "/work/bin") {
		t.Fatalf("a repository that says nothing of the environment gave its fix %q, so this test proves nothing", without)
	}
	with := fixed(t, "[env]\nFROM_THE_REPOSITORY = \""+said+"\"\n_.path = [\"./bin\"]\n")
	lines := strings.Split(with, "\n")
	if lines[0] != said {
		t.Errorf("the fix was given FROM_THE_REPOSITORY=%q, want %q", lines[0], said)
	}
	if len(lines) < 2 || !strings.Contains(lines[1], "/work/bin:") {
		t.Errorf("the fix's path is %q, want the repository's ./bin in the fix's own copy first", lines[1:])
	}
}

// A name the repository's mise.toml puts in the environment is not
// given to a fix as written as well: refused by name, as its check
// refuses it on the grid, beside a name mise does not give.
func TestAFixIsNotGivenWhatMiseSets(t *testing.T) {
	grid := reapitest.Dial(t)
	snapshot := sourcetest.Taken(t, map[string]string{"mise.toml": "[env]\nFROM_THE_REPOSITORY = \"said\"\n", "app/f.txt": "before\n"})
	fixed := func(given map[string]string) ([]source.Change, error) {
		action := plan.Action{
			Label: model.Label{Path: "app", Plugin: "custom", Name: "own"}, ProjectRoot: "app", Environment: given,
			Sources:  []pattern.Group{{Directory: "app", Patterns: []string{"f.txt"}}},
			Declared: &pluginv1.Action{Name: "own", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", `printf '%s' "$GIVEN" > f.txt`}}, Rewrites: []string{"f.txt"}}}, Inputs: []*pluginv1.Input{{Kind: &pluginv1.Input_Source{Source: "f.txt"}}}, Command: &pluginv1.Command{Arguments: []string{"true"}}},
		}
		return fix.Runner{Tools: &tools.Provisioner{Executor: grid, Platform: testgrid.Platform()}, Kept: t.TempDir()}.Run(context.Background(), snapshot, action)
	}
	if changes, err := fixed(map[string]string{"GIVEN": "configured"}); err != nil || len(changes) != 1 || string(changes[0].Content) != "configured" {
		t.Fatalf("a name mise does not give was not given: %+v, %v", changes, err)
	}
	if changes, err := fixed(map[string]string{"FROM_THE_REPOSITORY": "configured"}); err == nil || !strings.Contains(err.Error(), "mise sets FROM_THE_REPOSITORY") {
		t.Fatalf("given what mise sets, the fix ran: %+v, %v", changes, err)
	}
}
