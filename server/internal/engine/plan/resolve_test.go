package plan

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
)

// leaving is an action of a project, by plugin and name, with what it
// leaves and what it reads of others.
func leaving(path, plugin, name string, outputs []string, reads ...Reference) Action {
	declared := &pluginv1.Action{Name: name}
	for _, output := range outputs {
		declared.Outputs = append(declared.Outputs, &pluginv1.Output{Path: output})
	}
	return Action{Label: model.Label{Path: path, Plugin: plugin, Name: name}, Repository: "lab", ProjectRoot: path, Declared: declared, References: reads}
}

func sharedLeaving(path, plugin, name string, outputs ...string) Action {
	action := leaving(path, plugin, name, outputs)
	action.Declared.Shared = true
	return action
}

func catalogRef(at, written string) Reference {
	ref, err := model.ParseReference(at, written)
	if err != nil {
		panic(err)
	}
	return Reference{Reference: ref, File: "lab/" + at + "/catalog-info.yaml"}
}

func pluginRef(at, plugin, written string) Reference {
	ref, err := model.ParseReference(at, written)
	if err != nil {
		panic(err)
	}
	return Reference{Reference: ref, Plugin: plugin, Shared: "lab"}
}

// What a reference names is an action, for all of it, or one output, for
// that alone; the same thing named twice, or an output beside its
// action, is read once.
func TestAReferenceResolvesToOneReadAndOneNeed(t *testing.T) {
	write := leaving("lab/a", "custom", "write", []string{"made/", "spare/"})
	compile := leaving("lab/a", "go", "compile", []string{"~/go/bin/"})
	fetch := leaving("lab", "go", "fetch/x.com/m@v1", []string{"~/go/pkg/mod/x.com/m@v1/"})
	fetch.Declared.Shared = true
	read := leaving("lab/a", "custom", "read", nil,
		catalogRef("lab/a", ":made/"), catalogRef("lab/a", ":made/"), catalogRef("lab/a", ":spare/"),
		catalogRef("lab/a", ":go.compile"), catalogRef("lab/a", ":~/go/bin/"))
	other := leaving("lab/b", "custom", "read", nil, catalogRef("lab/b", "//lab/a:made/"), catalogRef("lab/b", "//lab/a:custom.write"))
	built := leaving("lab/a", "go", "test", nil, pluginRef("lab/a", "go", ":compile"), pluginRef("lab/a", "go", ":fetch/x.com/m@v1"))
	actions := []Action{write, compile, fetch, read, other, built}

	if err := Resolve(actions); err != nil {
		t.Fatal(err)
	}
	want := map[string][]Read{
		"//lab/a:custom.read": {{From: write.Label, Repository: "lab", ProjectRoot: "lab/a", Output: "made"}, {From: write.Label, Repository: "lab", ProjectRoot: "lab/a", Output: "spare"}, {From: compile.Label, Repository: "lab", ProjectRoot: "lab/a"}},
		"//lab/b:custom.read": {{From: write.Label, Repository: "lab", ProjectRoot: "lab/a"}},
		"//lab/a:go.test":     {{From: compile.Label, Repository: "lab", ProjectRoot: "lab/a"}, {From: fetch.Label, Repository: "lab", ProjectRoot: "lab"}},
	}
	for _, action := range actions {
		if got, expected := fmt.Sprint(action.Reads), fmt.Sprint(want[action.Label.String()]); got != expected {
			t.Errorf("%s reads %s, want %s", action.Label, got, expected)
		}
		var needs []string
		for _, need := range action.Needs {
			needs = append(needs, need.String())
		}
		var expected []string
		for _, read := range want[action.Label.String()] {
			if len(expected) == 0 || expected[len(expected)-1] != read.From.String() {
				expected = append(expected, read.From.String())
			}
		}
		if fmt.Sprint(needs) != fmt.Sprint(expected) {
			t.Errorf("%s needs %v, want %v", action.Label, needs, expected)
		}
	}
}

// What a reference names is one thing, or it is refused, naming the
// reader, the reference and the catalog file; a plugin's names only its
// own plugin's actions.
func TestAReferenceThatNamesNotOneThingIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		actions []Action
		says    string
	}{
		"nothing": {[]Action{leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":made/"))},
			"//lab/a:custom.read reads :made/, which names no action and nothing an action leaves"},
		"another project nobody has": {[]Action{leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", "//lab/z:custom.write"))},
			"//lab/a:custom.read reads //lab/z:custom.write, which names no action and nothing an action leaves"},
		"both": {[]Action{leaving("lab/a", "custom", "write", []string{"custom.write"}), leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":custom.write"))},
			"//lab/a:custom.read reads :custom.write, which names both an action and what an action leaves"},
		"leaves nothing": {[]Action{leaving("lab/a", "go", "test", nil), leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":go.test"))},
			"//lab/a:custom.read reads :go.test, which leaves nothing: an action reads what another left, and waits for nothing else"},
		"another plugin's action": {[]Action{leaving("lab/a", "custom", "write", []string{"made/"}), leaving("lab/a", "go", "test", nil, pluginRef("lab/a", "go", ":write"))},
			"//lab/a:go.test reads :write, which names no action and nothing an action leaves"},
		"another plugin's output": {[]Action{leaving("lab/a", "custom", "write", []string{"made/"}), leaving("lab/a", "go", "test", nil, pluginRef("lab/a", "go", ":made/"))},
			"//lab/a:go.test reads :made/, which names no action and nothing an action leaves"},
		"both, by a directory's spelling": {[]Action{leaving("lab/a", "custom", "write", []string{"custom.write"}), leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":custom.write/"))},
			"//lab/a:custom.read reads :custom.write/, which names both an action and what an action leaves"},
		"both, from a plugin, by a directory's spelling": {[]Action{leaving("lab/a", "go", "test", []string{"out/"}), leaving("lab/a", "go", "other", []string{"test"}), leaving("lab/a", "go", "lint", nil, pluginRef("lab/a", "go", ":test/"))},
			"//lab/a:go.lint reads :test/, which names both an action and what an action leaves"},
		"an output two actions leave": {[]Action{leaving("lab/a", "custom", "write", []string{"made/"}), leaving("lab/a", "custom", "copy", []string{"made/"}), leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":made/"))},
			"//lab/a:custom.read reads :made/, which more than one action leaves: //lab/a:custom.write and //lab/a:custom.copy. Name the action whose it is"},
		"an output two plugins' actions leave": {[]Action{leaving("lab/a", "go", "compile", []string{"target/"}), leaving("lab/a", "cargo", "compile", []string{"target/"}), leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":target/"))},
			"//lab/a:custom.read reads :target/, which more than one action leaves: //lab/a:go.compile and //lab/a:cargo.compile. Name the action whose it is"},
		"an output two shared fetches leave": {[]Action{sharedLeaving("lab", "cargo", "fetch/a@1", "~/.cargo/config.toml"), sharedLeaving("lab", "cargo", "fetch/b@1", "~/.cargo/config.toml"), leaving("lab/a", "cargo", "compile", nil, pluginRef("lab/a", "cargo", ":~/.cargo/config.toml"))},
			"//lab/a:cargo.compile reads :~/.cargo/config.toml, which more than one action leaves: //lab:cargo.fetch/a@1 and //lab:cargo.fetch/b@1. Name the action whose it is"},
		"a project's action by a shared one's path": {[]Action{leaving("lab", "go", "compile", []string{"~/go/bin/"}), leaving("lab/a", "go", "test", nil, pluginRef("lab/a", "go", ":compile"))},
			"//lab/a:go.test reads :compile, which names no action and nothing an action leaves"},
	} {
		t.Run(name, func(t *testing.T) {
			err := Resolve(c.actions)
			var mistake *model.Mistake
			if !errors.As(err, &mistake) || mistake.Message != c.says {
				t.Fatalf("Resolve = %v, want %q", err, c.says)
			}
			if c.actions[len(c.actions)-1].References[0].File != "" && mistake.File == "" {
				t.Errorf("the mistake names no file")
			}
		})
	}
}

// What a reference resolves to does not depend on the order the
// workspace's actions come in: an output two actions leave is refused
// whichever is first, and a plugin's reference finds its own plugin's
// output beside another plugin's of the same path.
func TestResolvingDoesNotDependOnTheOrderOfActions(t *testing.T) {
	write, copied := leaving("lab/a", "custom", "write", []string{"made/"}), leaving("lab/a", "custom", "copy", []string{"made/"})
	for _, actions := range [][]Action{
		{write, copied, leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":made/"))},
		{copied, write, leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":made/"))},
		{leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":made/")), copied, write},
	} {
		if err := Resolve(actions); err == nil {
			t.Errorf("an output two actions leave was resolved to one of them: %v", actions[len(actions)-1].Needs)
		}
	}
	mine, theirs := leaving("lab/a", "go", "generate", []string{"target/"}), leaving("lab/a", "cargo", "compile", []string{"target/"})
	for _, actions := range [][]Action{
		{mine, theirs, leaving("lab/a", "go", "compile", nil, pluginRef("lab/a", "go", ":target/"))},
		{theirs, mine, leaving("lab/a", "go", "compile", nil, pluginRef("lab/a", "go", ":target/"))},
	} {
		if err := Resolve(actions); err != nil {
			t.Fatal(err)
		}
		if needs := actions[2].Needs; len(needs) != 1 || needs[0] != mine.Label {
			t.Errorf("go.compile needs %v, want its own plugin's generate", needs)
		}
	}
	// One place under two spellings, a file's and a directory's, is one
	// place: two actions that leave there are refused, and one is found
	// by either spelling.
	file, directory := leaving("lab/a", "custom", "write", []string{"made"}), leaving("lab/a", "custom", "copy", []string{"made/"})
	for _, written := range []string{":made", ":made/"} {
		if err := Resolve([]Action{file, directory, leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", written))}); err == nil {
			t.Errorf("%s was resolved though two actions leave there", written)
		}
		one := []Action{directory, leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", written))}
		if err := Resolve(one); err != nil || len(one[1].Reads) != 1 || one[1].Reads[0].Output != "made" {
			t.Errorf("%s: %v, %v", written, err, one[1].Reads)
		}
	}
	// A slash at the end changes nothing of what is named: an action too
	// is found with one.
	slashed := []Action{directory, leaving("lab/a", "custom", "read", nil, catalogRef("lab/a", ":custom.copy/"))}
	if err := Resolve(slashed); err != nil || len(slashed[1].Reads) != 1 || !slashed[1].Reads[0].All() {
		t.Errorf(":custom.copy/: %v, %v", err, slashed[1].Reads)
	}
	// A lone repository's shared actions are labelled with the empty path.
	lone := []Action{sharedLeaving("", "go", "fetch/x.com/m@v1", "~/go/pkg/mod/x.com/m@v1/"), leaving("hello", "go", "compile", nil, Reference{Reference: model.Reference{Path: "hello", Rest: "fetch/x.com/m@v1", Written: ":fetch/x.com/m@v1"}, Plugin: "go", Shared: ""})}
	if err := Resolve(lone); err != nil || len(lone[1].Needs) != 1 {
		t.Errorf("a lone repository's shared fetch was not found: %v, %v", err, lone[1].Needs)
	}
}

// BenchmarkResolveALock measures ten modules that each lock four hundred
// modules: every one of a module's three building actions names each
// shared fetch, twelve thousand references in all. It is the shape that
// multiplies: the cost must be of the references, not of the references
// times the fetches.
func BenchmarkResolveALock(b *testing.B) {
	const modules, fetches = 10, 400
	baseline := make([]Action, 0, fetches+3*modules)
	refs := make([]string, fetches)
	for i := range fetches {
		name := fmt.Sprintf("fetch/x.com/m%04d@v1", i)
		baseline = append(baseline, sharedLeaving("lab", "go", name, "~/go/pkg/mod/"+name+"/", "~/go/config"))
		refs[i] = ":" + name
	}
	for i := range modules {
		at := fmt.Sprintf("lab/module-%04d", i)
		reads := make([]Reference, len(refs))
		for j, ref := range refs {
			reads[j] = pluginRef(at, "go", ref)
		}
		baseline = append(baseline, leaving(at, "go", "compile", []string{"~/go/bin/"}, reads...), leaving(at, "go", "test", nil, reads...), leaving(at, "go", "lint", nil, reads...))
	}
	resolving(b, baseline)
}

// resolving resolves a fresh copy of baseline, for as long as the
// benchmark loops.
func resolving(b *testing.B, baseline []Action) {
	b.Helper()
	actions := make([]Action, len(baseline))
	for b.Loop() {
		for i := range baseline {
			actions[i] = baseline[i]
			actions[i].Reads, actions[i].Needs = nil, nil
		}
		if err := Resolve(actions); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkResolve measures a thousand modules, each compiled after a
// shared fetch and read by an action of its entity: what every build of
// a repository of that size resolves.
func BenchmarkResolve(b *testing.B) {
	baseline := make([]Action, 0, 3001)
	baseline = append(baseline, leaving("lab", "go", "fetch/x.com/m@v1", []string{"~/go/pkg/mod/x.com/m@v1/"}))
	baseline[0].Declared.Shared = true
	for i := range 1000 {
		at := fmt.Sprintf("lab/module-%04d", i)
		baseline = append(baseline,
			leaving(at, "go", "compile", []string{"~/go/bin/"}, pluginRef(at, "go", ":fetch/x.com/m@v1")),
			leaving(at, "custom", "run", nil, catalogRef(at, ":go.compile"), catalogRef(at, ":~/go/bin/")))
	}
	resolving(b, baseline)
}

// Measured 2026-11-09 with `mise exec -- go test -run '^$' -bench
// '^BenchmarkResolve' -benchmem -count=3 ./build/internal/engine/plan`
// in server/: 825–904 us and 4024 allocations for a thousand modules
// and three thousand references; 4.3–4.7 ms and 640 allocations for
// ten modules that each name four hundred shared fetches from three
// actions, twelve thousand references. Before references were looked
// up, when each was searched for among the repository's actions, an
// independent review measured that second shape at 134 ms and 103 MB,
// and four times the fetches at 2.0 s: the budget holds the shape that
// multiplies. The time budgets leave the tenfold room for scheduler
// noise that the budgets beside them do; the allocations are held close.
func TestBudgetOfResolve(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 10 * time.Millisecond, Allocs: 4100}, BenchmarkResolve)
}

func TestBudgetOfResolvingALock(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 40 * time.Millisecond, Allocs: 700}, BenchmarkResolveALock)
}
