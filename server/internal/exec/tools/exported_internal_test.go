package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools/toolstest"
)

// What mise gives is a value, whatever it holds: frontseat-action is told
// it as data, never as text a shell reads, and the root mise was asked
// in is the action's own.
// commanded is what command tells what starts an action, given what it
// is given.
func commanded(t *testing.T, own map[string]string, vars []given, within map[string]string, network bool) confine.Spec {
	t.Helper()
	encoded, err := command(own, vars, within, network)
	if err != nil {
		t.Fatal(err)
	}
	var spec confine.Spec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestWhatMiseGivesIsGivenAsItWasGiven(t *testing.T) {
	const asked = "/worker/build/1/root"
	values := map[string]string{
		"QUOTED":   `it's "said"`,
		"EXPANDED": `$HOME ${PATH} $(echo ran) ` + "`echo ran`",
		"LINES":    "one\ntwo",
		"EMPTY":    "",
		"ROOTED":   asked + "/.frontseat/mise/installs/go/1/bin:" + asked + "/x",
		"PATH":     asked + "/.frontseat/mise/installs/go/1/bin:/usr/bin:/bin",
	}
	printed := asked + "\n" + jsonOf(t, values)
	vars, err := exported([]byte(printed), map[string]exec.Held{".frontseat/mise/installs/go": {}})
	if err != nil {
		t.Fatal(err)
	}
	spec := commanded(t, nil, vars, nil, false)
	got := map[string]string{}
	for _, each := range spec.Environment {
		got[each.Name] = strings.Join(each.Parts, "/elsewhere")
	}
	for name, value := range values {
		if want := strings.ReplaceAll(value, asked, "/elsewhere"); got[name] != want {
			t.Errorf("%s is %q; mise gave %q", name, got[name], want)
		}
	}
}

// What is not an environment mise gives is refused, each beside a
// control that is one.
func TestWhatIsNoEnvironmentOfItsToolsIsRefused(t *testing.T) {
	const asked = "/worker/build/1/root"
	installs := map[string]exec.Held{".frontseat/mise/installs/go": {}}
	good := map[string]string{"PATH": asked + "/.frontseat/mise/installs/go/1/bin:/bin"}
	if _, err := exported([]byte(asked+"\n"+jsonOf(t, good)), installs); err != nil {
		t.Fatalf("the control was refused, so this test proves nothing: %v", err)
	}
	for name, printed := range map[string]string{
		"no root":                               "\n" + jsonOf(t, good),
		"nothing at all":                        "",
		"no environment":                        asked + "\nnot json",
		"a name the shell cannot set":           asked + "\n" + jsonOf(t, map[string]string{"PATH": good["PATH"], "A-B": "x"}),
		"a name that runs":                      asked + "\n" + jsonOf(t, map[string]string{"PATH": good["PATH"], "A=$(echo ran)": "x"}),
		"the tool not on the path":              asked + "\n" + jsonOf(t, map[string]string{"PATH": "/bin"}),
		"the tool of another root":              asked + "\n" + jsonOf(t, map[string]string{"PATH": "/other/.frontseat/mise/installs/go/1/bin:/bin"}),
		"a tree that only begins as the tool's": asked + "\n" + jsonOf(t, map[string]string{"PATH": asked + "/.frontseat/mise/installs/gopher/bin:/bin"}),
	} {
		if _, err := exported([]byte(printed), installs); err == nil {
			t.Errorf("%s was taken as an environment", name)
		}
	}
}

// jsonOf is an environment as mise env --json prints it.
func jsonOf(t *testing.T, values map[string]string) string {
	t.Helper()
	printed, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return string(printed)
}

// answering is a grid that installs at once and answers mise env with
// the environment given, said from the root it is asked in.
type answering map[string]string

func (a answering) Execute(_ context.Context, action exec.Action) (exec.Result, error) {
	if action.Label != "mise env" {
		return toolstest.Installed(action), nil
	}
	const asked = "/worker/build/7/root"
	values := map[string]string{}
	for name, value := range a {
		values[name] = strings.ReplaceAll(value, "@", asked)
	}
	printed, _ := json.Marshal(values)
	return exec.Result{Stdout: append([]byte(asked+"\n"), printed...)}, nil
}

// What mise names beneath the root it was asked in is placed in a
// layout where each place is: its data, home and temporary files where
// the layout keeps them, and the rest beneath the layout's root. A name
// that only begins as one of them is beneath the root; a path ends where
// what follows it is no part of a name.
func TestWhatMiseGivesIsPlacedInALayout(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[env]\nA = \"a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Provisioner{Platform: map[string]string{"OSFamily": "linux", "ISA": "aarch64"}, Executor: answering{
		"DATA":    "@/.frontseat/mise/installs/x/bin",
		"HOME_IN": "@/.frontseat/home/.cache",
		"TMP_IN":  "@/.frontseat/tmp",
		"NEAR":    "@/.frontseat/miserable",
		"PROJECT": "@/app/bin:@/.frontseat/mise/shims:/usr/bin",
		"PLAIN":   "nothing of the root",
		"OPTIONS": "-Duser.home=@/.frontseat/home -Dx=@/.frontseat/tmp,@/app",
	}}
	got, err := p.Environment(context.Background(), root, nil, "", nil, nil, Layout{Root: "/fix/work", Home: "/fix/home", Temporary: "/fix/tmp", Data: "/fix/mise"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DATA=/fix/mise/installs/x/bin",
		"HOME_IN=/fix/home/.cache",
		"NEAR=/fix/work/.frontseat/miserable",
		"OPTIONS=-Duser.home=/fix/home -Dx=/fix/tmp,/fix/work/app",
		"PLAIN=nothing of the root",
		"PROJECT=/fix/work/app/bin:/fix/mise/shims:/usr/bin",
		"TMP_IN=/fix/tmp",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("placed as\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Two environments are two answers: what a name holds is never read as
// part of the name, nor the other way about.
func TestTwoEnvironmentsAreNeverTakenForOne(t *testing.T) {
	if keyOf("/r", "repo", []string{"go"}, map[string]string{"A=B": "C"}) == keyOf("/r", "repo", []string{"go"}, map[string]string{"A": "B=C"}) {
		t.Error("A=B set to C and A set to B=C are taken for one environment")
	}
	// The control: the same environment, given in another order, is one.
	if keyOf("/r", "repo", []string{"go"}, map[string]string{"A": "1", "B": "2"}) != keyOf("/r", "repo", []string{"go"}, map[string]string{"B": "2", "A": "1"}) {
		t.Error("one environment is taken for two")
	}
}

// An action's own environment does not say where its home or temporary
// directory is: what every action runs with is set after it, as it was
// when a shell set it. What mise and what is given as written say come
// after, and a later one of a name replaces an earlier.
func TestAnActionsOwnEnvironmentDoesNotMoveItsHome(t *testing.T) {
	spec := commanded(t, map[string]string{"HOME": "/plugin-home", "TMPDIR": "/plugin-tmp", "OWN": "the plugin's"}, []given{{name: "OWN", parts: []string{"mise's"}}}, map[string]string{"WRITTEN": "as written"}, false)
	last := map[string]string{}
	for _, each := range spec.Environment {
		last[each.Name] = strings.Join(each.Parts, "/root")
	}
	for name, want := range map[string]string{"HOME": "/root/" + Home, "TMPDIR": "/root/" + home + "/tmp", "OWN": "mise's", "WRITTEN": "as written"} {
		if last[name] != want {
			t.Errorf("%s is %q, want %q", name, last[name], want)
		}
	}
}

// An action's tools are where the confiner looks for them: the two say
// the directory apart, since the confiner is of the standard library
// alone.
func TestTheToolsAreKeptWhereTheConfinerLooksForThem(t *testing.T) {
	if got := data + "/installs"; got != confine.MiseInstalls {
		t.Errorf("mise keeps the tools in %s, and the confiner looks in %s", got, confine.MiseInstalls)
	}
}

// A platform mise has no name of its own for is named as the grid names
// it: a lock that pins nothing for it then says which platform.
func TestAPlatformMiseDoesNotNameIsNamedAsTheGridNamesIt(t *testing.T) {
	for platform, want := range map[string]string{"aarch64": "linux-arm64", "x86-64": "linux-x64", "RISCV64": "linux-riscv64"} {
		if got := PlatformName(map[string]string{"OSFamily": "Linux", "ISA": platform}); got != want {
			t.Errorf("%s is named %q, want %q", platform, got, want)
		}
	}
}
