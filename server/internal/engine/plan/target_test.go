package plan

import "github.com/frontseat-dev/frontseat/server/internal/source/record"

import "github.com/frontseat-dev/frontseat/sdk/go/entity"

import (
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// workspace is two modules of one repository and one of another, each
// with a lint and a test.
func workspace() ([]Action, []record.Located) {
	var actions []Action
	for _, project := range []string{"platform/gateway", "platform/log", "services/greet"} {
		for _, name := range []string{"lint", "test"} {
			actions = append(actions, Action{
				Label:    model.Label{Path: project, Plugin: "go", Name: name},
				Declared: &pluginv1.Action{Name: name},
			})
		}
	}
	return actions, []record.Located{
		{Ref: entity.New("", "", "gateway"), Repository: "platform", Root: "gateway"},
		{Ref: entity.New("", "", "greet"), Repository: "services", Root: "greet"},
	}
}

func TestATargetPointsAtActions(t *testing.T) {
	cases := []struct {
		says          string
		targets, only []string
		want          string
	}{
		{"nothing is everything", nil, nil, "//platform/gateway:go.lint //platform/gateway:go.test //platform/log:go.lint //platform/log:go.test //services/greet:go.lint //services/greet:go.test"},
		{"a name is an entity", []string{"greet"}, nil, "//services/greet:go.lint //services/greet:go.test"},
		{"an entity named in full", []string{"component:default/greet"}, nil, "//services/greet:go.lint //services/greet:go.test"},
		{"a path is everything under it", []string{"platform/"}, []string{"lint"}, "//platform/gateway:go.lint //platform/log:go.lint"},
		{"a file is its project", []string{"platform/log/log.go"}, []string{"test"}, "//platform/log:go.test"},
		{"an action as it is printed", []string{"//platform/log:go.test"}, nil, "//platform/log:go.test"},
		{"several are all of them", []string{"greet", "platform/log"}, []string{"test"}, "//platform/log:go.test //services/greet:go.test"},
		{"the workspace itself", []string{"."}, []string{"test"}, "//platform/gateway:go.test //platform/log:go.test //services/greet:go.test"},
	}
	for _, c := range cases {
		actions, entities := workspace()
		pointsAt(t, actions, entities, c.says, c.targets, c.only, c.want)
	}
}

// pointsAt fails the test, saying says, unless the targets, of the actions
// only, point at the actions whose labels are want, in order.
func pointsAt(t *testing.T, actions []Action, entities []record.Located, says string, targets, only []string, want string) {
	t.Helper()
	selected, err := Pointed(actions, entities, targets, only)
	if err != nil {
		t.Errorf("%s: %v", says, err)
		return
	}
	var labels []string
	for _, action := range selected {
		labels = append(labels, action.Label.String())
	}
	if got := strings.Join(labels, " "); got != want {
		t.Errorf("%s: %v only %v points at\n  %s\nwant\n  %s", says, targets, only, got, want)
	}
}

// A project's tests may be pieces, each an action. Whoever asks for
// tests is given every piece, and the label one test of the project
// would have points at them all; a piece is pointed at by its own.
func TestTestsInPiecesAreAskedForAndPointedAtTogether(t *testing.T) {
	var actions []Action
	for _, name := range []string{"lint", "test-build", "test/.", "test/internal/ledger"} {
		declared := &pluginv1.Action{Name: name, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}
		if strings.HasPrefix(name, "test/") {
			declared.Kind = &pluginv1.Action_Test{Test: &pluginv1.Test{}}
		}
		actions = append(actions, Action{Label: model.Label{Path: "services/greet", Plugin: "go", Name: name}, Declared: declared})
	}
	// A project in a directory whose name ends as the label of a
	// project's tests does, and a piece whose own name does.
	for _, name := range []string{"test/.", "test/pkg.test/sub"} {
		actions = append(actions, Action{Label: model.Label{Path: "odd.test/svc", Plugin: "go", Name: name}, Declared: &pluginv1.Action{Name: name, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}}})
	}
	// A project with a piece cut into shards, beside a piece whose name
	// begins as the first's does.
	for _, name := range []string{"test/.", "test/internal/ledger@1of2", "test/internal/ledger@2of2", "test/internal/ledgers"} {
		actions = append(actions, Action{Label: model.Label{Path: "sharded", Plugin: "go", Name: name}, Declared: &pluginv1.Action{Name: name, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}}})
	}
	// A project whose tests are one action, beside it.
	actions = append(actions, Action{Label: model.Label{Path: "platform/log", Plugin: "go", Name: "test"}, Declared: &pluginv1.Action{Name: "test", Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}}})
	for _, c := range []struct {
		says          string
		targets, only []string
		want          string
	}{
		{"tests asked for are every piece and every whole", nil, []string{"test"}, "//services/greet:go.test/. //services/greet:go.test/internal/ledger //odd.test/svc:go.test/. //odd.test/svc:go.test/pkg.test/sub //sharded:go.test/. //sharded:go.test/internal/ledger@1of2 //sharded:go.test/internal/ledger@2of2 //sharded:go.test/internal/ledgers //platform/log:go.test"},
		{"the label of a project's tests is each piece", []string{"//services/greet:go.test"}, nil, "//services/greet:go.test/. //services/greet:go.test/internal/ledger"},
		{"a piece by its own label", []string{"//services/greet:go.test/internal/ledger"}, nil, "//services/greet:go.test/internal/ledger"},
		{"what builds the tests is no test", nil, []string{"test-build"}, "//services/greet:go.test-build"},
		{"a piece cut into shards is each of its shards", []string{"//sharded:go.test/internal/ledger"}, nil, "//sharded:go.test/internal/ledger@1of2 //sharded:go.test/internal/ledger@2of2"},
		{"a shard by its own label", []string{"//sharded:go.test/internal/ledger@2of2"}, nil, "//sharded:go.test/internal/ledger@2of2"},
		{"the tests of a project with shards are every piece and shard", []string{"//sharded:go.test"}, nil, "//sharded:go.test/. //sharded:go.test/internal/ledger@1of2 //sharded:go.test/internal/ledger@2of2 //sharded:go.test/internal/ledgers"},
	} {
		pointsAt(t, actions, nil, c.says, c.targets, c.only, c.want)
	}
	// What begins as a piece's label and is none points at nothing, and
	// a piece is no name to ask for.
	for _, c := range []struct{ targets, only []string }{
		// The label of the tests of another project, another plugin and
		// no project; and what is no label and begins as one does.
		{[]string{"//services:go.test"}, nil},
		{[]string{"//services/greet:rust.test"}, nil},
		{[]string{"//services/greet:go.test/"}, nil},
		{[]string{"//services/greet.test"}, nil},
		{[]string{"//services/greet:go.test/internal"}, nil},
		{[]string{"//services/greet:go.lint"}, []string{"test/."}},
		{[]string{"//services/greet:go.tes"}, nil},
		{[]string{"//odd.test"}, nil},
		// A piece that is not cut into shards has no shards to point at
		// by a longer name, and a shard that is not there is none.
		{[]string{"//sharded:go.test/internal"}, nil},
		{[]string{"//sharded:go.test/internal/ledger@3of2"}, nil},
		{[]string{"//sharded:go.test/internal/ledger@"}, nil},
		{[]string{"//odd.test/svc:go.test/pkg.test"}, nil},
	} {
		if selected, err := Pointed(actions, nil, c.targets, c.only); err == nil {
			t.Errorf("%v only %v points at %d actions, and is refused nowhere", c.targets, c.only, len(selected))
		}
	}
}

func TestWhatPointsAtNothingIsRefusedWithTheNearest(t *testing.T) {
	cases := []struct {
		targets, only []string
		says          string
	}{
		{[]string{"gatewya"}, nil, "there is no entity gatewya in the workspace; did you mean gateway?"},
		{[]string{"payments"}, nil, "there is no entity payments in the workspace"},
		{[]string{"platform/notes/"}, nil, "nothing is built at platform/notes"},
		{[]string{"//platform/log:go.tset"}, nil, "there is no action //platform/log:go.tset; did you mean //platform/log:go.test?"},
		{nil, []string{"tset"}, "no action is named tset; did you mean test?"},
	}
	for _, c := range cases {
		actions, entities := workspace()
		_, err := Pointed(actions, entities, c.targets, c.only)
		if err == nil || err.Error() != c.says {
			t.Errorf("%v only %v: %v, want %q", c.targets, c.only, err, c.says)
		}
	}
}

// A shared fetch is labelled with its repository's directory, and is the
// project of no file there and the action of no directory: it is brought
// by what needs it. The control: the same label, not shared, is.
func TestASharedActionIsPointedAtOnlyByItsLabel(t *testing.T) {
	for _, shared := range []bool{false, true} {
		actions, entities := workspace()
		declared := &pluginv1.Action{Name: "fetch/example.com/dep@v1.0.0", Kind: &pluginv1.Action_Fetch{Fetch: &pluginv1.Fetch{}}}
		if shared {
			declared.Kind = &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{}}
		}
		actions = append(actions, Action{Label: model.Label{Path: "platform", Plugin: "go", Name: "fetch/example.com/dep@v1.0.0"}, Declared: declared})
		for _, target := range []string{"platform/", "platform/notes.md"} {
			selected, err := Pointed(actions, entities, []string{target}, nil)
			fetched := false
			for _, action := range selected {
				fetched = fetched || action.Label.Name == "fetch/example.com/dep@v1.0.0"
			}
			if err == nil && fetched == shared {
				t.Errorf("shared %v: %s points at the fetch: %v", shared, target, fetched)
			}
		}
		selected, err := Pointed(actions, entities, []string{"//platform:go.fetch/example.com/dep@v1.0.0"}, nil)
		if err != nil || len(selected) != 1 {
			t.Errorf("shared %v: its label points at %v, %v; want it", shared, selected, err)
		}
	}
}

// An entity with several directories is everything built in each of
// them: pointing at it builds all it is, never the first alone.
func TestAnEntityOfSeveralDirectoriesIsAllOfThem(t *testing.T) {
	actions, entities := workspace()
	entities = append(entities,
		record.Located{Ref: entity.New("", "", "gateway"), Repository: "platform", Root: "log"},
		record.Located{Ref: entity.New("api", "", "menu"), Repository: "platform", Root: "docs"},
		record.Located{Ref: entity.New("api", "", "menu"), Repository: "platform", Root: "more"},
	)
	selected, err := Pointed(actions, entities, []string{"gateway"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, action := range selected {
		labels = append(labels, action.Label.String())
	}
	if got, want := strings.Join(labels, " "), "//platform/gateway:go.lint //platform/gateway:go.test //platform/log:go.lint //platform/log:go.test"; got != want {
		t.Errorf("gateway points at\n  %s\nwant\n  %s", got, want)
	}
	// One whose directories build nothing says so of all of them.
	if _, err := Pointed(actions, entities, []string{"api:menu"}, nil); err == nil || !strings.Contains(err.Error(), "nothing is built for api:menu, at platform/docs and platform/more") {
		t.Errorf("an entity nothing is built for: %v", err)
	}
}

// programs is the workspace with a program in one of its modules, which
// reads what that module's lint left, and a compile beside it.
func programs() ([]Action, []record.Located) {
	actions, entities := workspace()
	lint := model.Label{Path: "services/greet", Plugin: "go", Name: "lint"}
	actions = append(actions, Action{
		Label:    model.Label{Path: "services/greet", Plugin: "go", Name: "run/greet"},
		Declared: &pluginv1.Action{Name: "run/greet", Kind: &pluginv1.Action_Program{Program: &pluginv1.Program{}}},
		Needs:    []model.Label{lint},
	})
	return actions, entities
}

// A program is no part of a build: nothing that points at where it is
// builds it, and pointed at by itself it says how it is run.
func TestNoTargetOfABuildPointsAtAProgram(t *testing.T) {
	for _, targets := range [][]string{nil, {"."}, {"greet"}, {"services/"}, {"services/greet/main.go"}} {
		actions, entities := programs()
		selected, err := Pointed(actions, entities, targets, nil)
		if err != nil {
			t.Fatalf("%v: %v", targets, err)
		}
		if len(selected) == 0 {
			t.Errorf("%v points at nothing, so this proves nothing", targets)
		}
		for _, action := range selected {
			if action.IsProgram() {
				t.Errorf("%v points at the program %s", targets, action.Label)
			}
		}
	}
	actions, entities := programs()
	_, err := Pointed(actions, entities, []string{"//services/greet:go.run/greet"}, nil)
	if err == nil || !strings.Contains(err.Error(), "frontseat run //services/greet:go.run/greet") {
		t.Errorf("a build pointed at a program: %v", err)
	}
	// Its name is no name of a build's action to keep: a build that
	// would keep nothing is refused, and not run as one that passed.
	if _, err := Pointed(actions, entities, nil, []string{"run/greet"}); err == nil || !strings.Contains(err.Error(), "no action is named run/greet") {
		t.Errorf("only the program's name: %v", err)
	}
}

// Running is one program, by its label, and what it needs before it.
func TestRunningIsOneProgramAndWhatItNeeds(t *testing.T) {
	actions, _ := programs()
	runs, program, err := Program(actions, "//services/greet:go.run/greet")
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, action := range runs {
		labels = append(labels, action.Label.String())
	}
	if got := strings.Join(labels, " "); got != "//services/greet:go.lint //services/greet:go.run/greet" || program.Label.Name != "run/greet" {
		t.Errorf("running runs %s and runs %s", got, program.Label)
	}
	for target, want := range map[string]string{
		"//services/greet:go.lint":     "is a step of a build, and no program",
		"//services/greet:go.run/gret": "there is no program //services/greet:go.run/gret; did you mean //services/greet:go.run/greet?; what can be run: //services/greet:go.run/greet",
		"greet":                        "there is no program greet; what can be run: //services/greet:go.run/greet",
		"services/":                    "there is no program services/",
		"":                             "there is no program",
	} {
		if _, _, err := Program(actions, target); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("running %q: %v, want %q", target, err, want)
		}
	}
	plain, _ := workspace()
	if _, _, err := Program(plain, "//services/greet:go.run/greet"); err == nil || !strings.Contains(err.Error(), "nothing in the workspace is a program") {
		t.Errorf("a workspace with no program: %v", err)
	}
}

func TestDaemonRunRefusesPreparedSourcesAndToolsByName(t *testing.T) {
	for _, prepared := range []string{"source", "tool"} {
		actions, _ := programs()
		program := &actions[len(actions)-1]
		if prepared == "source" {
			program.Sources = []pattern.Group{{Directory: program.ProjectRoot, Patterns: []string{"config.json"}}}
		} else {
			program.Declared.Tools = []string{"caddy"}
		}
		if _, _, err := Program(actions, program.Label.String()); err == nil || !strings.Contains(err.Error(), "prepared source or tools") {
			t.Errorf("daemon run accepted %s without its preparation: %v", prepared, err)
		}
	}
}

// A mistake says what can be run, and of many, the first.
func TestWhatCanBeRunIsSaidUpToAFew(t *testing.T) {
	var actions []Action
	for i := range 25 {
		name := "run/p" + string(rune('a'+i))
		actions = append(actions, Action{Label: model.Label{Path: "m", Plugin: "go", Name: name}, Declared: &pluginv1.Action{Name: name, Kind: &pluginv1.Action_Program{Program: &pluginv1.Program{}}}})
	}
	_, _, err := Program(actions, "//m:go.run/none")
	if err == nil || !strings.Contains(err.Error(), "//m:go.run/pa, ") || !strings.Contains(err.Error(), "and 5 more") || strings.Contains(err.Error(), "run/py") {
		t.Errorf("of twenty-five programs: %v", err)
	}
}

// A name a person types finds its entity in whatever namespace its
// catalog put it; one that two namespaces hold is refused, naming both,
// and the one written in full is found.
func TestANameFindsItsEntityInAnyNamespaceAndAnAmbiguousNameIsRefused(t *testing.T) {
	actions, _ := workspace()
	entities := []record.Located{
		{Ref: entity.New("component", "frontseat", "gateway"), Repository: "platform", Root: "gateway"},
		{Ref: entity.New("component", "frontseat", "greet"), Repository: "services", Root: "greet"},
		{Ref: entity.New("component", "other", "greet"), Repository: "platform", Root: "log"},
	}
	pointsAt(t, actions, entities, "a name in a namespace of its own", []string{"gateway"}, []string{"lint"}, "//platform/gateway:go.lint")
	pointsAt(t, actions, entities, "a name written in full", []string{"component:frontseat/greet"}, []string{"lint"}, "//services/greet:go.lint")
	_, err := Pointed(actions, entities, []string{"greet"}, nil)
	if err == nil || !strings.Contains(err.Error(), "component:frontseat/greet") || !strings.Contains(err.Error(), "component:other/greet") {
		t.Errorf("a name two namespaces hold: %v, want it refused naming both", err)
	}
}

// The packages of a project are asked for and pointed at together, as
// the pieces of its tests are: by "package", and by the label one
// package of it would have. An action named so that is no package is
// none of them; the control is each by its own label and name.
func TestTheBuildsPackagesAreAskedForAndPointedAtTogether(t *testing.T) {
	var actions []Action
	for _, name := range []string{"compile", "package/greet-linux-amd64", "package/greet-darwin-arm64"} {
		declared := &pluginv1.Action{Name: name, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}
		if strings.HasPrefix(name, "package/") {
			declared.Kind = &pluginv1.Action_Package{Package: &pluginv1.Package{}}
		}
		actions = append(actions, Action{Label: model.Label{Path: "services/greet", Plugin: "go", Name: name}, Declared: declared})
	}
	actions = append(actions, Action{Label: model.Label{Path: "services/greet", Plugin: "cargo", Name: "wasm"}, Declared: &pluginv1.Action{Name: "wasm", Kind: &pluginv1.Action_Package{Package: &pluginv1.Package{}}}})
	for _, c := range []struct {
		says          string
		targets, only []string
		want          string
	}{
		{"asked for by package", nil, []string{"package"}, "//services/greet:go.package/greet-linux-amd64 //services/greet:go.package/greet-darwin-arm64"},
		{"pointed at by the label a package would have", []string{"//services/greet:go.package"}, nil, "//services/greet:go.package/greet-linux-amd64 //services/greet:go.package/greet-darwin-arm64"},
		{"control: one by its label", []string{"//services/greet:go.package/greet-linux-amd64"}, nil, "//services/greet:go.package/greet-linux-amd64"},
		{"control: a package its tool names is asked for by that name", nil, []string{"wasm"}, "//services/greet:cargo.wasm"},
	} {
		got, err := Pointed(actions, nil, c.targets, c.only)
		if err != nil {
			t.Errorf("%s: %v", c.says, err)
			continue
		}
		var labels []string
		for _, action := range got {
			labels = append(labels, action.Label.String())
		}
		if strings.Join(labels, " ") != c.want {
			t.Errorf("%s: %v, want %s", c.says, labels, c.want)
		}
	}
}
