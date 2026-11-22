package pluginhost_test

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
)

// sound is an answer that holds to the contract: one project under the
// root asked about, with an input set, an action that fetches and one
// that reads the set and what the fetch left.
func sound() *pluginv1.AnalyzeResponse {
	return &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Root: "services", Projects: []*pluginv1.Project{{
		Root:     "services/hello",
		Kind:     "go-module",
		Name:     "example.com/hello",
		Manifest: "services/hello/go.mod",
		Provides: []*pluginv1.Provide{{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/hello"}, Sources: "sources"}},
		Requires: []*pluginv1.Requirement{{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/greet"}, Origin: &pluginv1.Origin{Path: "services/hello/go.mod", Line: 5}}},
		InputSets: []*pluginv1.InputSet{
			{Name: "sources", Inputs: []*pluginv1.Input{pluginhost.SourceInput("go.mod"), pluginhost.SourceInput("**/*.go"), pluginhost.SourceInput("!**/testdata/**")}},
		},
		Actions: []*pluginv1.Action{
			{Name: "fetch", Command: &pluginv1.Command{Arguments: []string{"go", "mod", "download"}}, Inputs: []*pluginv1.Input{pluginhost.SourceInput("go.mod")}, Outputs: []*pluginv1.Output{{Path: "~/go/pkg/mod/"}}, Tools: []string{"go"}, Kind: &pluginv1.Action_Fetch{Fetch: &pluginv1.Fetch{}}},
			{Name: "compile", Command: &pluginv1.Command{Arguments: []string{"go", "build"}}, Inputs: append([]*pluginv1.Input{pluginhost.SetInput("sources"), pluginhosttest.OutputInput("fetch")}, pluginhosttest.AllDependencies(".frontseat/go/{name}")), Outputs: []*pluginv1.Output{{Path: "bin/"}}, Tools: []string{"go"}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
		},
	}}}}}
}

// What a plugin answers becomes paths in a workspace and in the root of
// an action. A plugin is a module someone else wrote: its answer is held
// to the contract before any of it is used.
func TestAnAnswerThatBreaksTheContractIsRefused(t *testing.T) {
	roots := []string{"services"}
	if err := pluginhost.CheckAnalysis("go", roots, sound()); err != nil {
		t.Fatalf("an answer that holds to the contract was refused, so this test proves nothing: %v", err)
	}
	project := func(a *pluginv1.AnalyzeResponse) *pluginv1.Project { return a.GetAnalyses()[0].GetProjects()[0] }

	for name, change := range map[string]func(*pluginv1.AnalyzeResponse){
		"a missing requirement origin":     func(a *pluginv1.AnalyzeResponse) { project(a).Requires[0].Origin = nil },
		"an origin outside the repository": func(a *pluginv1.AnalyzeResponse) { project(a).Requires[0].Origin.Path = "../go.mod" },
		"an origin with no line":           func(a *pluginv1.AnalyzeResponse) { project(a).Requires[0].Origin.Line = 0 },
		"a cyclic input set": func(a *pluginv1.AnalyzeResponse) {
			project(a).InputSets[0].Inputs = []*pluginv1.Input{pluginhost.SetInput("sources")}
		},
		"a project outside the root asked about": func(a *pluginv1.AnalyzeResponse) { project(a).Root = "platform/gateway" },
		"a project out of the repository":        func(a *pluginv1.AnalyzeResponse) { project(a).Root = "services/../../elsewhere" },
		"two projects in one directory": func(a *pluginv1.AnalyzeResponse) {
			a.Analyses[0].Projects = append(a.Analyses[0].Projects, proto.Clone(project(a)).(*pluginv1.Project))
		},
		"a manifest out of the repository": func(a *pluginv1.AnalyzeResponse) { project(a).Manifest = "../go.mod" },
		"a manifest by its full path":      func(a *pluginv1.AnalyzeResponse) { project(a).Manifest = "/etc/passwd" },
		"an input set with no name":        func(a *pluginv1.AnalyzeResponse) { project(a).InputSets[0].Name = "" },
		"one input set twice": func(a *pluginv1.AnalyzeResponse) {
			project(a).InputSets = append(project(a).InputSets, project(a).InputSets[0])
		},
		"an input out of the project": func(a *pluginv1.AnalyzeResponse) {
			project(a).InputSets[0].Inputs = []*pluginv1.Input{pluginhost.SourceInput("../**/*.go")}
		},
		"an input by its full path": func(a *pluginv1.AnalyzeResponse) {
			project(a).Actions[0].Inputs = []*pluginv1.Input{pluginhost.SourceInput("/etc/**")}
		},
		"an input that is no pattern": func(a *pluginv1.AnalyzeResponse) {
			project(a).Actions[0].Inputs = []*pluginv1.Input{pluginhost.SourceInput("[a-")}
		},
		"an input set nobody declared": func(a *pluginv1.AnalyzeResponse) {
			project(a).Actions[1].Inputs = []*pluginv1.Input{pluginhost.SetInput("nowhere")}
		},
		"a set of an input set nobody declared": func(a *pluginv1.AnalyzeResponse) {
			project(a).InputSets[0].Inputs = []*pluginv1.Input{pluginhost.SetInput("nowhere")}
		},
		"an output out of the project":             func(a *pluginv1.AnalyzeResponse) { project(a).Actions[1].Outputs[0].Path = "../bin/" },
		"an output by its full path":               func(a *pluginv1.AnalyzeResponse) { project(a).Actions[1].Outputs[0].Path = "/usr/local/bin/" },
		"an output out of the action's home":       func(a *pluginv1.AnalyzeResponse) { project(a).Actions[0].Outputs[0].Path = "~/../../escaped/" },
		"an output with no path":                   func(a *pluginv1.AnalyzeResponse) { project(a).Actions[1].Outputs[0].Path = "" },
		"what is handed placed out of the project": func(a *pluginv1.AnalyzeResponse) { project(a).Actions[1].Inputs[2].GetDependency().Into = "../{name}" },
		"what is handed placed by a full path": func(a *pluginv1.AnalyzeResponse) {
			project(a).Actions[1].Inputs[2].GetDependency().Into = "/tmp/{name}"
		},
		"sources that are no input set":  func(a *pluginv1.AnalyzeResponse) { project(a).Provides[0].Sources = "nowhere" },
		"a coordinate with no ecosystem": func(a *pluginv1.AnalyzeResponse) { project(a).Provides[0].Coordinate.Ecosystem = "" },
		"a requirement with no name":     func(a *pluginv1.AnalyzeResponse) { project(a).Requires[0].Coordinate.Name = "" },
		"a tool with no name":            func(a *pluginv1.AnalyzeResponse) { project(a).Actions[0].Tools = []string{""} },
	} {
		answer := sound()
		change(answer)
		if err := pluginhost.CheckAnalysis("go", roots, answer); err == nil {
			t.Errorf("an answer with %s was taken", name)
		}
	}
}

// Independent review found that raw root spellings bypassed the uniqueness
// check. These are distinct coordinates, so provider conflicts cannot hide it.
func TestProjectRootAliasesCannotDuplicateIdentity(t *testing.T) {
	for _, roots := range [][2]string{{"", "."}, {"pkg", "sub/../pkg"}, {"pkg", "./pkg"}, {"pkg", "pkg/"}, {"pkg", "pkg/."}, {"pkg", "pkg"}, {"pkg", "other"}} {
		var projects []*pluginv1.Project
		for i, root := range roots {
			projects = append(projects, &pluginv1.Project{Root: root, Name: "fixture", Kind: "fixture", Manifest: "manifest.toml", Provides: []*pluginv1.Provide{{Coordinate: &pluginv1.Coordinate{Ecosystem: "fixture", Name: []string{"one", "two"}[i]}}}})
		}
		err := pluginhost.CheckAnalysis("fixture", []string{""}, &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Projects: projects}}})
		if roots[1] == "other" {
			if err != nil {
				t.Fatalf("distinct directory control: %v", err)
			}
		} else if err == nil {
			t.Errorf("accepted two spellings of one project: %q", roots)
		}
	}
}

// What a plugin writes for its tool is written into the root of an
// action, at a path of the project's.
func TestAFileAPluginWritesIsAFileOfItsProject(t *testing.T) {
	answer := func(path string) *pluginv1.ArrangeResponse {
		return &pluginv1.ArrangeResponse{Files: []*pluginv1.File{{Path: path, Content: []byte("go 1.27\n")}}}
	}
	if err := pluginhost.CheckArrangement("go", "services/hello", answer("services/hello/go.work")); err != nil {
		t.Fatalf("a file of the project was refused: %v", err)
	}
	for name, path := range map[string]string{
		"of another project":    "services/greet/go.work",
		"out of the repository": "services/hello/../../../go.work",
		"by its full path":      "/etc/go.work",
		"with no path":          "",
		"that is the project":   "services/hello",
	} {
		if err := pluginhost.CheckArrangement("go", "services/hello", answer(path)); err == nil {
			t.Errorf("a file %s was taken", name)
		}
	}
	if err := pluginhost.CheckArrangement("go", "", answer("go.work")); err != nil {
		t.Errorf("a file of a project at the repository's root was refused: %v", err)
	}
}

// shared is sound with a shared fetch of one module, which the compile
// reads all of, beside the project's own actions.
func shared() *pluginv1.AnalyzeResponse {
	a := sound()
	p := a.GetAnalyses()[0].GetProjects()[0]
	p.Actions = append(p.Actions, &pluginv1.Action{
		Name: "fetch/github.com/spf13/cobra@v1.8.1", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: "go.mod", Content: []byte("module fetch.invalid\n")}, {Path: "go.sum", Content: []byte("github.com/spf13/cobra v1.8.1 h1:x=\n")}}}}, Tools: []string{"go"},
		Command: &pluginv1.Command{Arguments: []string{"go", "mod", "download", "github.com/spf13/cobra@v1.8.1"}},
		Outputs: []*pluginv1.Output{{Path: "~/go/pkg/mod/"}},
	})
	p.Actions[1].Inputs = append(p.Actions[1].Inputs, pluginhosttest.OutputInput("fetch/github.com/spf13/cobra@v1.8.1"))
	return a
}

// lastAction is the last action of the first project an answer says:
// where these tests' answers keep their shared fetch.
func lastAction(a *pluginv1.AnalyzeResponse) *pluginv1.Action {
	actions := a.GetAnalyses()[0].GetProjects()[0].GetActions()
	return actions[len(actions)-1]
}

// refused fails, in a subtest of the name given, where a plugin's answer
// made sound and then changed is accepted.
func refused(t *testing.T, name, plugin string, roots []string, sound func() *pluginv1.AnalyzeResponse, change func(*pluginv1.AnalyzeResponse)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		a := sound()
		change(a)
		if err := pluginhost.CheckAnalysis(plugin, roots, a); err == nil {
			t.Error("accepted")
		} else {
			t.Logf("refused: %v", err)
		}
	})
}

// A shared action belongs to no project, so nothing of a project may
// reach it: no source, nothing handed, nothing it waits for, nothing it
// leaves in a project. Only a fetch is shared, and only a shared action
// is given files.
func TestASharedActionIsHeldToWhatNoProjectGivesIt(t *testing.T) {
	roots := []string{"services"}
	if err := pluginhost.CheckAnalysis("go", roots, shared()); err != nil {
		t.Fatalf("a sound shared fetch was refused, so this test proves nothing: %v", err)
	}
	fetch := lastAction
	// rename names the fetch, and what reads it, so that a name is
	// refused for itself and not for a reference left behind.
	rename := func(a *pluginv1.AnalyzeResponse, name string) {
		fetch(a).Name = name
		a.GetAnalyses()[0].GetProjects()[0].GetActions()[1].Inputs = []*pluginv1.Input{pluginhost.SetInput("sources"), pluginhosttest.OutputInput("fetch"), pluginhosttest.OutputInput(name)}
	}
	for name, change := range map[string]func(*pluginv1.AnalyzeResponse){
		"a shared action that reads source": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).Inputs = []*pluginv1.Input{pluginhost.SourceInput("go.mod")}
		},
		"a shared action that is handed things": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).Inputs = []*pluginv1.Input{pluginhosttest.AllDependencies(".frontseat/go/{name}")}
		},
		"a shared action that reads another": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).Inputs = []*pluginv1.Input{pluginhosttest.OutputInput("fetch")}
		},
		"a shared action that leaves in project": func(a *pluginv1.AnalyzeResponse) { fetch(a).Outputs[0].Path = "bin/" },
		"a shared action named as a project's":   func(a *pluginv1.AnalyzeResponse) { rename(a, "download") },
		"a shared name of nothing":               func(a *pluginv1.AnalyzeResponse) { rename(a, "fetch/") },
		"a shared name with an empty part":       func(a *pluginv1.AnalyzeResponse) { rename(a, "fetch/a//b") },
		"a shared name that climbs":              func(a *pluginv1.AnalyzeResponse) { rename(a, "fetch/a/../b") },
		"a shared name that ends in a slash":     func(a *pluginv1.AnalyzeResponse) { rename(a, "fetch/a/") },
		"a shared name with a space":             func(a *pluginv1.AnalyzeResponse) { rename(a, "fetch/a b") },
		"a shared name with a colon":             func(a *pluginv1.AnalyzeResponse) { rename(a, "fetch/a:b") },
		"a shared name of an upper-case kind":    func(a *pluginv1.AnalyzeResponse) { rename(a, "Fetch/a") },
		"a project's action with a slash":        func(a *pluginv1.AnalyzeResponse) { fetch(a).Kind = &pluginv1.Action_Fetch{Fetch: &pluginv1.Fetch{}} },
		"a file out of its directory":            func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[0].Path = "../go.mod" },
		"a file by its full path":                func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[0].Path = "/go.mod" },
		"a file with no name":                    func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[0].Path = "" },
		"one file twice":                         func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[1].Path = "./go.mod" },
		"a file in its home":                     func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[0].Path = ".frontseat/home/x" },
		"a file that is a directory":             func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[1].Path = "go.mod/x" },
		"an output of its whole home":            func(a *pluginv1.AnalyzeResponse) { fetch(a).Outputs[0].Path = "~/." },
		"a name longer than the contract": func(a *pluginv1.AnalyzeResponse) {
			rename(a, "fetch/"+strings.Repeat("a", pluginhost.SharedNameAtMost))
		},
		"files larger than the contract": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Files[0].Content = make([]byte, pluginhost.SharedFilesAtMost+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := shared()
			change(a)
			if err := pluginhost.CheckAnalysis("go", roots, a); err == nil {
				t.Errorf("%s was not refused", name)
			}
		})
	}
	// The control for the size: exactly as much as the contract allows.
	a := shared()
	fetch(a).GetSharedFetch().Files = []*pluginv1.File{{Path: "go.sum", Content: make([]byte, pluginhost.SharedFilesAtMost)}}
	if err := pluginhost.CheckAnalysis("go", roots, a); err != nil {
		t.Errorf("files exactly as large as allowed were refused: %v", err)
	}
	// The control for a directory: files in a directory of their own.
	a = shared()
	fetch(a).GetSharedFetch().Files = []*pluginv1.File{{Path: "sub/go.sum"}, {Path: "sub/go.mod"}}
	if err := pluginhost.CheckAnalysis("go", roots, a); err != nil {
		t.Errorf("files in a directory were refused: %v", err)
	}
	// The control for the length: as long a name as the contract allows.
	a = shared()
	fetch(a).Name = "fetch/" + strings.Repeat("a", pluginhost.SharedNameAtMost-len("fetch/"))
	a.GetAnalyses()[0].GetProjects()[0].GetActions()[1].Inputs = []*pluginv1.Input{pluginhost.SetInput("sources"), pluginhosttest.OutputInput("fetch"), pluginhosttest.OutputInput(fetch(a).Name)}
	if err := pluginhost.CheckAnalysis("go", roots, a); err != nil {
		t.Errorf("a name as long as allowed was refused: %v", err)
	}
	// The control for the name: everything a Go module and version holds.
	a = shared()
	fetch(a).Name = "fetch/gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405+incompatible~x_y"
	a.GetAnalyses()[0].GetProjects()[0].GetActions()[1].Inputs = []*pluginv1.Input{pluginhost.SetInput("sources"), pluginhosttest.OutputInput("fetch"), pluginhosttest.OutputInput(fetch(a).Name)}
	if err := pluginhost.CheckAnalysis("go", roots, a); err != nil {
		t.Errorf("a module's name was refused: %v", err)
	}
}

// downloading is a sound shared fetch that downloads: one crate by its
// URL and sha256, unpacked into its home, with the checksum file cargo
// reads beside it, and no command or tool of its own.
func downloading() *pluginv1.AnalyzeResponse {
	a := sound()
	p := a.GetAnalyses()[0].GetProjects()[0]
	p.Actions = append(p.Actions, &pluginv1.Action{
		Name: "fetch/itoa@1.0.18", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: "~/.cargo/vendor/itoa-1.0.18/.cargo-checksum.json", Content: []byte(`{"files":{}}`)}}, Downloads: []*pluginv1.Download{{Url: "https://static.crates.io/crates/itoa/1.0.18/download", Sha256: strings.Repeat("8f", 32), Into: "~/.cargo/vendor/"}}}}, Outputs: []*pluginv1.Output{{Path: "~/.cargo/vendor/itoa-1.0.18/"}},
	})
	p.Actions[1].Inputs = append(p.Actions[1].Inputs, pluginhosttest.OutputInput("fetch/itoa@1.0.18"))
	return a
}

// A shared fetch that downloads runs nothing of its own: the host unpacks
// what the asset service fetched. Each download is a public https address,
// a hex sha256 and a directory of the fetch's home; its files may be of
// its home too. Only a shared fetch downloads.
func TestASharedDownloadIsHeldToTheContract(t *testing.T) {
	roots := []string{"services"}
	if err := pluginhost.CheckAnalysis("cargo", roots, downloading()); err != nil {
		t.Fatalf("a sound download was refused, so this test proves nothing: %v", err)
	}
	fetch := lastAction
	for name, change := range map[string]func(*pluginv1.AnalyzeResponse){
		"a download with a command of its own": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).Command = &pluginv1.Command{Arguments: []string{"tar", "xzf", "x"}}
		},
		"a download that names a tool": func(a *pluginv1.AnalyzeResponse) { fetch(a).Tools = []string{"rust"} },
		"a download over http": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "http://static.crates.io/crates/itoa/1.0.18/download"
		},
		"a download of a file":    func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Downloads[0].Url = "file:///etc/passwd" },
		"a download with no host": func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Downloads[0].Url = "https:///crates/itoa" },
		"a download with a user": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://me:secret@static.crates.io/crates/itoa/1.0.18/download"
		},
		"a download that is no address": func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Downloads[0].Url = "::not a url" },
		"a download with a query": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://static.crates.io/crates/a?x=/1.0.0/download"
		},
		"a download with an escape": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://static.crates.io/crates/%2e%2e/1.0.0/download"
		},
		"a download with an escaped letter": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://static.crates.io/crates/it%6fa/1.0.18/download"
		},
		"a download that climbs": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://static.crates.io/crates/../../download"
		},
		"a download with a dot step": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://static.crates.io/crates/./itoa/1.0.18/download"
		},
		"a download with a slash twice": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://static.crates.io/crates//itoa/1.0.18/download"
		},
		"a download with no path": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Url = "https://static.crates.io"
		},
		"a checksum that is too short": func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Downloads[0].Sha256 = "8f42" },
		"a checksum in upper case": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Sha256 = strings.Repeat("8F", 32)
		},
		"a checksum with a prefix": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads[0].Sha256 = "sha256:" + strings.Repeat("8f", 32)[7:]
		},
		"unpacked into the project": func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Downloads[0].Into = "vendor/" },
		"unpacked above its home":   func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Downloads[0].Into = "~/../tools/" },
		"unpacked by a full path":   func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Downloads[0].Into = "/tmp/" },
		"the same address twice": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads = append(fetch(a).GetSharedFetch().Downloads, fetch(a).GetSharedFetch().Downloads[0])
		},
		"a file of its home that leaves it": func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[0].Path = "~/../.cargo/config.toml" },
		"a file that is its home":           func(a *pluginv1.AnalyzeResponse) { fetch(a).GetSharedFetch().Files[0].Path = "~/" },
		"a file of its home given twice": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Files = append(fetch(a).GetSharedFetch().Files, &pluginv1.File{Path: "~/.cargo/vendor/itoa-1.0.18/./.cargo-checksum.json"})
		},
		"a download that reads source": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).Inputs = []*pluginv1.Input{pluginhost.SourceInput("Cargo.lock")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := downloading()
			change(a)
			if err := pluginhost.CheckAnalysis("cargo", roots, a); err == nil {
				t.Errorf("%s was not refused", name)
			}
		})
	}
	// The controls: two downloads of one fetch into one directory, and a
	// shared fetch that runs a tool and downloads nothing, as before.
	a := downloading()
	fetch(a).GetSharedFetch().Downloads = append(fetch(a).GetSharedFetch().Downloads, &pluginv1.Download{Url: "https://static.crates.io/crates/cfg-if/1.0.0/download", Sha256: strings.Repeat("ba", 32), Into: "~/.cargo/vendor/"})
	if err := pluginhost.CheckAnalysis("cargo", roots, a); err != nil {
		t.Errorf("two downloads into one directory were refused: %v", err)
	}
	if err := pluginhost.CheckAnalysis("go", roots, shared()); err != nil {
		t.Errorf("a shared fetch that runs its tool was refused: %v", err)
	}
}

// What an action says it writes is a plain path beneath its project or
// in its home, one spelling for one place and never the whole of either:
// it is a name others read it by. The control is each plain form.
func TestAnOutputIsWrittenPlainly(t *testing.T) {
	for _, plain := range []string{"made", "made/", "made/deep/word.txt", "out.tar.gz", "~/go/bin/", "~/.cargo/config.toml", ".hidden/", "a..b", "~/.frontseat/x", ".frontseatx/", "x/.frontseat/"} {
		if err := pluginhost.CheckOutputs("the action write", []string{plain}); err != nil {
			t.Errorf("%q was refused: %v", plain, err)
		}
	}
	for _, other := range []string{"", ".", "./", "..", "../x", "/", "/abs", "~", "~/", "~/.", "~/..", "~/../x", "~/x/..", "./x", "x/../y", "x/./y", "x//y", "made//", "a\\b", "a\x00b", "x/..", ".frontseat", ".frontseat/", ".frontseat/home/x", ".frontseat/downloads/x"} {
		err := pluginhost.CheckOutputs("the action write", []string{other})
		if err == nil || !strings.Contains(err.Error(), "which is not a plain path beneath the project or the action's home") {
			t.Errorf("%q: err = %v, want it refused", other, err)
		}
	}
}

// No project is in .frontseat, where an action's root holds the action's
// home: its outputs would sit where "~/…" does, one place under two
// spellings. The control is a directory whose name only begins or ends
// the same.
func TestNoProjectIsWhereAnActionsHomeIs(t *testing.T) {
	answer := func(root string) *pluginv1.AnalyzeResponse {
		return &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Projects: []*pluginv1.Project{{Root: root, Name: "a", Kind: "go-module", Manifest: root + "/go.mod",
			Actions: []*pluginv1.Action{{Name: "compile", Command: &pluginv1.Command{Arguments: []string{"true"}}, Outputs: []*pluginv1.Output{{Path: "home/x"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}}}}}}}
	}
	for _, allowed := range []string{"a", ".frontseatx", "x/.frontseat", ".hidden"} {
		if err := pluginhost.CheckAnalysis("go", nil, answer(allowed)); err != nil {
			t.Errorf("a project at %s was refused: %v", allowed, err)
		}
	}
	for _, refused := range []string{".frontseat", ".frontseat/home", ".frontseat/home/x", "./.frontseat", ".frontseat/"} {
		err := pluginhost.CheckAnalysis("go", nil, answer(refused))
		if err == nil || !strings.Contains(err.Error(), "it is in .frontseat") {
			t.Errorf("a project at %s: err = %v, want it refused", refused, err)
		}
	}
}

// cutting is a sound shared fetch that cuts parts out of what it
// downloads: an apk's control and data sections, each by its offset, its
// length and the checksum an apk lock holds of it, kept in its home, with
// a key beside them as a file.
func cutting() *pluginv1.AnalyzeResponse {
	a := sound()
	p := a.GetAnalyses()[0].GetProjects()[0]
	p.Actions = append(p.Actions, &pluginv1.Action{
		Name: "fetch/packages.wolfi.dev/os/aarch64/busybox-1.37.0-r50", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: "~/.cache/apk/keys/wolfi.rsa.pub", Content: []byte("key")}}, Downloads: []*pluginv1.Download{{Url: "https://packages.wolfi.dev/os/aarch64/busybox-1.37.0-r50.apk", Parts: []*pluginv1.Part{
			{Offset: 0, Length: 982, Sha1: strings.Repeat("1a", 20), Path: "~/.cache/apk/busybox/control.tar.gz"},
			{Offset: 982, Length: 1301, Sha256: strings.Repeat("2b", 32), Path: "~/.cache/apk/busybox/data.tar.gz"},
		}}}}}, Outputs: []*pluginv1.Output{{Path: "~/.cache/apk/"}},
	})
	p.Actions[1].Inputs = append(p.Actions[1].Inputs, pluginhosttest.OutputInput("fetch/packages.wolfi.dev/os/aarch64/busybox-1.37.0-r50"))
	return a
}

// A download cut into parts has no checksum or directory of the whole,
// and each part is a run of bytes, one checksum of it, and a file of the
// fetch's home that nothing else of the fetch is.
func TestADownloadCutIntoPartsIsHeldToTheContract(t *testing.T) {
	roots := []string{"services"}
	if err := pluginhost.CheckAnalysis("cargo", roots, cutting()); err != nil {
		t.Fatalf("a sound download in parts was refused, so this test proves nothing: %v", err)
	}
	download := func(a *pluginv1.AnalyzeResponse) *pluginv1.Download {
		return lastAction(a).GetSharedFetch().Downloads[0]
	}
	fetch := lastAction
	for name, change := range map[string]func(*pluginv1.AnalyzeResponse){
		"a checksum of the whole beside parts":    func(a *pluginv1.AnalyzeResponse) { download(a).Sha256 = strings.Repeat("8f", 32) },
		"a directory to unpack into beside parts": func(a *pluginv1.AnalyzeResponse) { download(a).Into = "~/.cache/" },
		"a part of no bytes":                      func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Length = 0 },
		"a part that starts past any file":        func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Offset = 1<<40 + 1 },
		"a part whose end wraps": func(a *pluginv1.AnalyzeResponse) {
			download(a).Parts[0].Offset, download(a).Parts[0].Length = 1<<40, 1<<64-1
		},
		"a part with no checksum": func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Sha1 = "" },
		"a part with two checksums": func(a *pluginv1.AnalyzeResponse) {
			download(a).Parts[0].Sha256 = strings.Repeat("2b", 32)
		},
		"a sha1 that is a sha256":      func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Sha1 = strings.Repeat("1a", 32) },
		"a sha1 in upper case":         func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Sha1 = strings.Repeat("1A", 20) },
		"a sha256 that is a sha1":      func(a *pluginv1.AnalyzeResponse) { download(a).Parts[1].Sha256 = strings.Repeat("2b", 20) },
		"a sha256 with a prefix":       func(a *pluginv1.AnalyzeResponse) { download(a).Parts[1].Sha256 = "sha256-" + strings.Repeat("2b", 32) },
		"a checksum that is a command": func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Sha1 = "$(touch /tmp/x)" },
		"a part kept in the project":   func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Path = "control.tar.gz" },
		"a part kept above its home":   func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Path = "~/../control.tar.gz" },
		"a part kept by a full path":   func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Path = "/etc/passwd" },
		"a part kept as its home":      func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Path = "~/" },
		"a part kept as a directory":   func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Path = "~/.cache/apk/busybox/" },
		"two parts kept as one file": func(a *pluginv1.AnalyzeResponse) {
			download(a).Parts[1].Path = "~/.cache/apk/busybox/./control.tar.gz"
		},
		"a part kept as a file the fetch is given": func(a *pluginv1.AnalyzeResponse) {
			download(a).Parts[0].Path = "~/.cache/apk/keys/wolfi.rsa.pub"
		},
		"a part kept beneath a file the fetch is given": func(a *pluginv1.AnalyzeResponse) {
			download(a).Parts[0].Path = "~/.cache/apk/keys/wolfi.rsa.pub/control.tar.gz"
		},
		"more parts than are allowed": func(a *pluginv1.AnalyzeResponse) {
			for i := range pluginhost.PartsAtMost {
				download(a).Parts = append(download(a).Parts, &pluginv1.Part{Offset: uint64(i), Length: 1, Sha1: strings.Repeat("1a", 20), Path: fmt.Sprintf("~/extra/%d", i)})
			}
		},
		"one download unpacked and another cut": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).GetSharedFetch().Downloads = append(fetch(a).GetSharedFetch().Downloads, &pluginv1.Download{Url: "https://static.crates.io/crates/itoa/1.0.18/download", Sha256: strings.Repeat("8f", 32), Into: "~/.cargo/vendor/"})
		},
		"a command beside parts": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).Command = &pluginv1.Command{Arguments: []string{"apk", "fetch"}}
		},
	} {
		refused(t, name, "cargo", roots, cutting, change)
	}
	// Refused for what the case is of, and not by a check that happens
	// to stand behind the one it tries.
	for said, change := range map[string]func(*pluginv1.AnalyzeResponse){
		"keeps two parts as": func(a *pluginv1.AnalyzeResponse) {
			download(a).Parts[1].Path = download(a).Parts[0].Path
		},
		"written plainly":  func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Path = "~/.cache/apk/busybox/" },
		"no output of its": func(a *pluginv1.AnalyzeResponse) { download(a).Parts[0].Path = "~/elsewhere/control.tar.gz" },
		"also given as a file": func(a *pluginv1.AnalyzeResponse) {
			download(a).Parts[0].Path = "~/.cache/apk/keys/wolfi.rsa.pub"
		},
	} {
		t.Run(said, func(t *testing.T) {
			a := cutting()
			change(a)
			if err := pluginhost.CheckAnalysis("cargo", roots, a); err == nil || !strings.Contains(err.Error(), said) {
				t.Errorf("err = %v; want it to say %q", err, said)
			}
		})
	}
}

// A refusal never repeats what it refused: an address or a name a
// plugin wrote may hold a value. The answers refused here are refused
// for that value alone; the sound answers beside them are not.
func TestARefusedDownloadOrNameIsNotRepeated(t *testing.T) {
	roots := []string{"services"}
	for _, sound := range []func() *pluginv1.AnalyzeResponse{downloading, running} {
		if err := pluginhost.CheckAnalysis("go", roots, sound()); err != nil {
			t.Fatalf("a sound answer was refused, so this test proves nothing: %v", err)
		}
	}
	for name, c := range map[string]struct {
		sound  func() *pluginv1.AnalyzeResponse
		change func(*pluginv1.Action)
	}{
		"a download with a user and a password": {downloading, func(a *pluginv1.Action) {
			a.GetSharedFetch().Downloads[0].Url = "https://me:v4lue@static.crates.io/crates/itoa/1.0.18/download"
		}},
		"a download with a value as its user": {downloading, func(a *pluginv1.Action) {
			a.GetSharedFetch().Downloads[0].Url = "https://v4lue@static.crates.io/crates/itoa/1.0.18/download"
		}},
		"a download that is no address": {downloading, func(a *pluginv1.Action) { a.GetSharedFetch().Downloads[0].Url = "::v4lue" }},
		"a download with a value as its query": {downloading, func(a *pluginv1.Action) {
			a.GetSharedFetch().Downloads[0].Url = "https://static.crates.io/crates/itoa/1.0.18/download?sig=v4lue"
		}},
		"a program given a name with a value": {running, func(a *pluginv1.Action) {
			a.Command.Environment["NAME=v4lue"] = "y"
		}},
	} {
		a := c.sound()
		c.change(lastAction(a))
		err := pluginhost.CheckAnalysis("go", roots, a)
		if err == nil || strings.Contains(err.Error(), "v4lue") {
			t.Errorf("%s: %v; want it refused without what it holds", name, err)
		}
	}
}

// running is a sound answer with a program: what the compile left,
// run as it is.
func running() *pluginv1.AnalyzeResponse {
	a := sound()
	p := a.GetAnalyses()[0].GetProjects()[0]
	p.Actions = append(p.Actions, &pluginv1.Action{
		Name:    "run/hello",
		Command: &pluginv1.Command{Arguments: []string{"bin/hello", "--serve"}, Environment: map[string]string{"GREETING": "hello"}},
		Inputs:  []*pluginv1.Input{pluginhosttest.OutputInput("compile")},
		Kind:    &pluginv1.Action_Program{Program: &pluginv1.Program{}},
	})
	return a
}

// A program is no step of a build, and is held to being one: named for
// what it runs, a program another action left, reading only what
// actions left. What a run may say that nothing serves yet is refused
// by name, so that none of it is taken and ignored.
func TestAnActionThatIsAProgramIsHeldToWhatOneIs(t *testing.T) {
	roots := []string{"services"}
	if err := pluginhost.CheckAnalysis("go", roots, running()); err != nil {
		t.Fatalf("a sound program was refused, so this test proves nothing: %v", err)
	}
	// A program in the action's home is one too.
	home := running()
	home.GetAnalyses()[0].GetProjects()[0].GetActions()[2].Command.Arguments = []string{"~/go/bin/hello"}
	if err := pluginhost.CheckAnalysis("go", roots, home); err != nil {
		t.Errorf("a program in the action's home was refused: %v", err)
	}
	for says, c := range map[string]struct {
		change func(*pluginv1.Action)
		want   string
	}{
		"a build's name":                   {func(a *pluginv1.Action) { a.Name = "serve" }, "not named run"},
		"a name with a directory":          {func(a *pluginv1.Action) { a.Name = "run/cmd/hello" }, "not named run"},
		"a name that is a parent":          {func(a *pluginv1.Action) { a.Name = "run/.." }, "not named run"},
		"a name with a space":              {func(a *pluginv1.Action) { a.Name = "run/a b" }, "not named run"},
		"an output":                        {func(a *pluginv1.Action) { a.Outputs = []*pluginv1.Output{{Path: "out/"}} }, "leaves something"},
		"what its project requires":        {func(a *pluginv1.Action) { a.Inputs = append(a.Inputs, pluginhosttest.AllDependencies("deps/{name}")) }, "not served yet"},
		"no program":                       {func(a *pluginv1.Action) { a.Command = nil }, "names no program"},
		"nothing read":                     {func(a *pluginv1.Action) { a.Inputs = nil }, "reads nothing"},
		"a program of the system's":        {func(a *pluginv1.Action) { a.Command.Arguments = []string{"sh", "-c", "id"} }, "not a plain path"},
		"a program by a full path":         {func(a *pluginv1.Action) { a.Command.Arguments = []string{"/bin/sh"} }, "not a plain path"},
		"a program above it":               {func(a *pluginv1.Action) { a.Command.Arguments = []string{"../../bin/sh"} }, "not a plain path"},
		"a program above its home":         {func(a *pluginv1.Action) { a.Command.Arguments = []string{"~/../sh"} }, "not a plain path"},
		"a program not spelled plainly":    {func(a *pluginv1.Action) { a.Command.Arguments = []string{"bin/./hello"} }, "not a plain path"},
		"a path of its own":                {func(a *pluginv1.Action) { a.Command.Environment["PATH"] = "/x" }, "sets PATH"},
		"a home of its own":                {func(a *pluginv1.Action) { a.Command.Environment["HOME"] = "/x" }, "sets HOME"},
		"a temporary directory of its own": {func(a *pluginv1.Action) { a.Command.Environment["TMPDIR"] = "/x" }, "sets TMPDIR"},
		"a name that sets another":         {func(a *pluginv1.Action) { a.Command.Environment["HOME=/x"] = "y" }, "no name of a variable"},
		"an empty name":                    {func(a *pluginv1.Action) { a.Command.Environment[""] = "y" }, "no name of a variable"},
		"a name with a nul":                {func(a *pluginv1.Action) { a.Command.Environment["A\x00B"] = "y" }, "no name of a variable"},
		"a name past the longest":          {func(a *pluginv1.Action) { a.Name = "run/" + strings.Repeat("a", pluginhost.SharedNameAtMost) }, "more than the"},
	} {
		a := running()
		c.change(a.GetAnalyses()[0].GetProjects()[0].GetActions()[2])
		err := pluginhost.CheckAnalysis("go", roots, a)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("a program with %s: %v, want it refused saying %q", says, err, c.want)
		}
	}
	// Service preparation accepts source patterns, input sets and pinned tools.
	for _, input := range []*pluginv1.Input{pluginhost.SourceInput("**/*.html"), pluginhost.SetInput("sources")} {
		a := running()
		project := a.Analyses[0].Projects[0]
		action := project.Actions[2]
		action.Inputs = append(action.Inputs, input)
		action.Tools = []string{"go"}
		action.Command.Arguments = []string{"go", "run", "."}
		if err := pluginhost.CheckAnalysis("go", roots, a); err != nil {
			t.Errorf("prepared program inputs: %v", err)
		}
	}
	// Two programs of one name are one name twice.
	twice := running()
	p := twice.GetAnalyses()[0].GetProjects()[0]
	p.Actions = append(p.Actions, p.Actions[2])
	if err := pluginhost.CheckAnalysis("go", roots, twice); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("two programs of one name: %v", err)
	}
}

// pieced is a sound answer whose project's tests are two pieces, each
// run in its package's directory, with the program another action left.
func pieced() *pluginv1.AnalyzeResponse {
	a := sound()
	p := a.GetAnalyses()[0].GetProjects()[0]
	p.Actions = append(p.Actions,
		&pluginv1.Action{Name: "test-build", Command: &pluginv1.Command{Arguments: []string{"go", "test", "-c"}}, Inputs: []*pluginv1.Input{pluginhost.SetInput("sources")}, Outputs: []*pluginv1.Output{{Path: "_tests/0/hello.test"}, {Path: "_tests/0/ledger.test"}}, Tools: []string{"go"}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
		&pluginv1.Action{Name: "test/.", Command: &pluginv1.Command{Arguments: []string{"./_tests/0/hello.test"}}, Inputs: []*pluginv1.Input{pluginhosttest.OutputInput("_tests/0/hello.test"), pluginhost.SourceInput("testdata/**")}, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}},
		&pluginv1.Action{Name: "test/internal/ledger", Command: &pluginv1.Command{Arguments: []string{"../../_tests/0/ledger.test"}, Directory: "internal/ledger"}, Inputs: []*pluginv1.Input{pluginhosttest.OutputInput("_tests/0/ledger.test"), pluginhost.SourceInput("internal/ledger/testdata/**")}, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}},
	)
	return a
}

// A project's tests may be pieces, each named "test", a slash and what
// piece it is, and a command may run beneath its project. Both are a
// plugin's word about paths, and are held to the project.
func TestTestsInPiecesAndWhereACommandRunsAreHeldToTheContract(t *testing.T) {
	roots := []string{"services"}
	if err := pluginhost.CheckAnalysis("go", roots, pieced()); err != nil {
		t.Fatalf("sound pieces of a project's tests were refused, so this test proves nothing: %v", err)
	}
	actions := func(a *pluginv1.AnalyzeResponse) []*pluginv1.Action {
		return a.GetAnalyses()[0].GetProjects()[0].GetActions()
	}
	// The last action is the piece that runs beneath the project.
	piece := func(a *pluginv1.AnalyzeResponse) *pluginv1.Action { return actions(a)[len(actions(a))-1] }
	for name, change := range map[string]struct {
		change func(*pluginv1.AnalyzeResponse)
		says   string
	}{
		"a piece that is no test":    {func(a *pluginv1.AnalyzeResponse) { piece(a).Kind = &pluginv1.Action_Build{Build: &pluginv1.Build{}} }, "is no test"},
		"an action of no kind":       {func(a *pluginv1.AnalyzeResponse) { piece(a).Kind = nil }, "says no kind"},
		"a piece of nothing":         {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/" }, "nor test, a slash"},
		"a piece that climbs":        {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/.." }, "nor test, a slash"},
		"a piece written two ways":   {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal/../internal/ledger" }, "nor test, a slash"},
		"a piece with an empty part": {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal//ledger" }, "nor test, a slash"},
		"a piece with a space":       {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/a b" }, "nor test, a slash"},
		"a piece with a colon":       {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/a:b" }, "nor test, a slash"},
		"a piece of another kind":    {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "lint/internal/ledger" }, "nor test, a slash"},
		"a piece longer than the contract": {func(a *pluginv1.AnalyzeResponse) {
			piece(a).Name = "test/" + strings.Repeat("a", pluginhost.SharedNameAtMost)
		}, "more than the"},
		"a name with an @ that is no shard's": {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal/ledger@x" }, "is no shard's"},
		"a shard that is none of its many":    {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal/ledger@3of2" }, "is no shard's"},
		"a shard of one":                      {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal/ledger@1of1" }, "is no shard's"},
		"a shard numbered from nothing":       {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal/ledger@0of2" }, "is no shard's"},
		"a shard written with a zero before":  {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal/ledger@01of2" }, "is no shard's"},
		"a shard of a shard":                  {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/internal/ledger@1of2@1of2" }, "is no shard's"},
		"a shard of no piece":                 {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/@1of2" }, "is no shard's"},
		"an action idle without what it reads, that reads nothing of another": {func(a *pluginv1.AnalyzeResponse) {
			piece(a).GetTest().IdleWithoutInputs, piece(a).Inputs = true, []*pluginv1.Input{pluginhost.SourceInput("internal/ledger/testdata/**")}
		}, "reads no output another action may pass without leaving"},
		"an action idle without an output its action always leaves": {func(a *pluginv1.AnalyzeResponse) { piece(a).GetTest().IdleWithoutInputs = true }, "reads no output another action may pass without leaving"},
		"an action idle without all an action leaves": {func(a *pluginv1.AnalyzeResponse) {
			piece(a).GetTest().IdleWithoutInputs, piece(a).Inputs = true, []*pluginv1.Input{pluginhosttest.OutputInput("test-build")}
		}, "reads no output another action may pass without leaving"},
		// With an output that may not be left in the project, beside it.
		"an action idle without all an action leaves, one of them optional": {func(a *pluginv1.AnalyzeResponse) {
			actions(a)[2].Outputs[0].Optional = true
			piece(a).GetTest().IdleWithoutInputs, piece(a).Inputs = true, []*pluginv1.Input{pluginhosttest.OutputInput("test-build")}
		}, "reads no output another action may pass without leaving"},
		"an action idle without an output always left, beside one that may not be": {func(a *pluginv1.AnalyzeResponse) {
			actions(a)[2].Outputs[0].Optional = true
			piece(a).GetTest().IdleWithoutInputs = true
		}, "reads no output another action may pass without leaving"},
		"an action that may be idle and says it leaves something": {func(a *pluginv1.AnalyzeResponse) {
			for _, output := range actions(a)[2].Outputs {
				output.Optional = true
			}
			piece(a).GetTest().IdleWithoutInputs, piece(a).Outputs = true, []*pluginv1.Output{{Path: "internal/ledger/report.txt"}}
		}, "an action that may not run leaves nothing"},
		"one piece twice": {func(a *pluginv1.AnalyzeResponse) { piece(a).Name = "test/." }, "twice"},
		"the whole of the tests beside pieces of them": {func(a *pluginv1.AnalyzeResponse) {
			p := a.GetAnalyses()[0].GetProjects()[0]
			p.Actions = append(p.Actions, &pluginv1.Action{Name: "test", Command: &pluginv1.Command{Arguments: []string{"go", "test"}}, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}})
		}, "never both"},
		"pieces after the whole of the tests": {func(a *pluginv1.AnalyzeResponse) {
			p := a.GetAnalyses()[0].GetProjects()[0]
			p.Actions = append([]*pluginv1.Action{{Name: "test", Command: &pluginv1.Command{Arguments: []string{"go", "test"}}, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}}}, p.Actions...)
		}, "never both"},
		"a directory above the project":         {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = ".." }, "not a plain path beneath the project"},
		"a directory that climbs out":           {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "internal/../../other" }, "not a plain path beneath the project"},
		"a directory by its full path":          {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "/tmp" }, "not a plain path beneath the project"},
		"a directory written two ways":          {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "internal/./ledger" }, "not a plain path beneath the project"},
		"a directory that ends in a slash":      {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "internal/ledger/" }, "not a plain path beneath the project"},
		"a directory with a byte no path holds": {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "a\x00b" }, "not a plain path beneath the project"},
		"a directory over two lines":            {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "internal/ledger\n" }, "not a plain path beneath the project"},
		"a directory that is a space":           {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = " " }, "not a plain path beneath the project"},
		"a directory written with backslashes":  {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "a\\..\\b" }, "not a plain path beneath the project"},
		"a directory of the action's home":      {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = ".frontseat/home" }, "not a plain path beneath the project"},
		"a directory written from the home":     {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "~/x" }, "not a plain path beneath the project"},
		"the project's directory, said":         {func(a *pluginv1.AnalyzeResponse) { piece(a).Command.Directory = "." }, "not a plain path beneath the project"},
		"a fix that says where it runs": {func(a *pluginv1.AnalyzeResponse) {
			actions(a)[1].Outputs = nil
			actions(a)[1].Kind = &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"gofmt", "-w", "."}, Directory: "internal"}, Rewrites: []string{"**/*.go"}}}
		}, "which is not served yet"},
	} {
		answer := pieced()
		change.change(answer)
		err := pluginhost.CheckAnalysis("go", roots, answer)
		if err == nil || !strings.Contains(err.Error(), change.says) {
			t.Errorf("%s: got %v, want a refusal saying %q", name, err, change.says)
		}
	}
	// The control for an action that may be idle: one that reads an
	// output of another.
	idle := pieced()
	piece(idle).GetTest().IdleWithoutInputs = true
	for _, output := range actions(idle)[2].Outputs {
		output.Optional = true
	}
	if err := pluginhost.CheckAnalysis("go", roots, idle); err != nil {
		t.Errorf("a test that has nothing to do without its program was refused: %v", err)
	}
	// The control for a shard's name: a piece cut into two.
	cut := pieced()
	piece(cut).Name = "test/internal/ledger@1of2"
	second := proto.Clone(piece(cut)).(*pluginv1.Action)
	second.Name = "test/internal/ledger@2of2"
	cut.GetAnalyses()[0].GetProjects()[0].Actions = append(actions(cut), second)
	if err := pluginhost.CheckAnalysis("go", roots, cut); err != nil {
		t.Errorf("a piece cut into two shards was refused: %v", err)
	}
	// The shards of a piece are one cut of it, whole.
	shards := func(names ...string) error {
		answer := pieced()
		project := answer.GetAnalyses()[0].GetProjects()[0]
		last := project.Actions[len(project.Actions)-1]
		project.Actions = project.Actions[:len(project.Actions)-1]
		for _, name := range names {
			shard := proto.Clone(last).(*pluginv1.Action)
			shard.Name = name
			project.Actions = append(project.Actions, shard)
		}
		return pluginhost.CheckAnalysis("go", roots, answer)
	}
	if err := shards("test/internal/ledger@1of3", "test/internal/ledger@2of3", "test/internal/ledger@3of3", "test/other@1of2", "test/other@2of2"); err != nil {
		t.Errorf("two pieces, each cut whole, were refused: %v", err)
	}
	for says, names := range map[string][]string{
		"of the 3 shards":         {"test/internal/ledger@2of3"},
		"of the 2 shards":         {"test/internal/ledger@1of2"},
		"a piece is cut one way":  {"test/internal/ledger@1of2", "test/internal/ledger@2of2", "test/internal/ledger@1of3"},
		"and shards of it":        {"test/internal/ledger", "test/internal/ledger@1of2", "test/internal/ledger@2of2"},
		"of the 999999999 shards": {"test/internal/ledger@999999999of999999999"},
	} {
		if err := shards(names...); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("the shards %v: got %v, want a refusal saying %q", names, err, says)
		}
	}
	// A program to run says nowhere to run.
	running := sound()
	project := running.GetAnalyses()[0].GetProjects()[0]
	project.Actions = append(project.Actions, &pluginv1.Action{Name: "run/hello", Command: &pluginv1.Command{Arguments: []string{"bin/hello"}}, Inputs: []*pluginv1.Input{pluginhosttest.OutputInput("compile")}, Kind: &pluginv1.Action_Program{Program: &pluginv1.Program{}}})
	if err := pluginhost.CheckAnalysis("go", roots, running); err != nil {
		t.Fatalf("a sound program to run was refused, so what follows proves nothing: %v", err)
	}
	project.Actions[len(project.Actions)-1].Command.Directory = "bin"
	if err := pluginhost.CheckAnalysis("go", roots, running); err == nil || !strings.Contains(err.Error(), "is a program to run, and says where it runs") {
		t.Errorf("a program to run that says where it runs: got %v, want it refused by name", err)
	}
	// A shared action says nowhere to run, and is no piece of tests.
	for name, answer := range map[string]*pluginv1.AnalyzeResponse{"a shared action": shared(), "a shared action named as a piece": shared()} {
		actions := answer.GetAnalyses()[0].GetProjects()[0].GetActions()
		last := actions[len(actions)-1]
		want := "it runs in a directory of its own"
		if name == "a shared action" {
			last.Command.Directory = "sub"
		} else {
			actions[1].Inputs = []*pluginv1.Input{pluginhost.SetInput("sources"), pluginhosttest.OutputInput("fetch")}
			last.Name, want = "test/x", "which only a test is"
		}
		if err := pluginhost.CheckAnalysis("go", roots, answer); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want a refusal saying %q", name, err, want)
		}
	}
}

// A requirement may say the version its project's lock resolves it to:
// one word, printed as it is, which is kept beside the line that
// requires it. One that is not a word of printable text is refused,
// without being repeated.
func TestARequirementsVersionIsOneWordOfPrintableText(t *testing.T) {
	roots := []string{"services"}
	for _, version := range []string{"", "v1.9.1", "1.0.228", "v0.0.0-20240101000000-abcdef123456", "v2.0.0+incompatible"} {
		a := sound()
		a.Analyses[0].Projects[0].Requires[0].Version = version
		if err := pluginhost.CheckAnalysis("go", roots, a); err != nil {
			t.Errorf("version %q was refused: %v", version, err)
		}
	}
	for _, version := range []string{"v1.0 v2.0", "v1\n", "v1\x1b[31m", "\tv1", strings.Repeat("9", pluginhost.VersionAtMost+1)} {
		a := sound()
		a.Analyses[0].Projects[0].Requires[0].Version = version
		err := pluginhost.CheckAnalysis("go", roots, a)
		if err == nil || !strings.Contains(err.Error(), "not one word of printable text") || strings.Contains(err.Error(), version) {
			t.Errorf("version %q: %v", version, err)
		}
	}
}

// A project's name and kind are printed and pointed at: each is there,
// one line of printable text, and no longer than a name is. A refusal
// says the rule and not the name.
func TestAProjectsNameAndKindAreHeldToWhatIsPrinted(t *testing.T) {
	roots := []string{"services"}
	for _, name := range []string{"example.com/hello", "@scope/package", "héllo wörld", strings.Repeat("n", pluginhost.ProjectNameAtMost)} {
		a := sound()
		a.Analyses[0].Projects[0].Name = name
		if err := pluginhost.CheckAnalysis("go", roots, a); err != nil {
			t.Errorf("the name %q was refused: %v", name, err)
		}
	}
	for name, rule := range map[string]string{
		"":              "it has no name",
		"hello\nworld":  "not one line of printable text",
		"hello\x1b[31m": "not one line of printable text",
		strings.Repeat("n", pluginhost.ProjectNameAtMost+1): "not one line of printable text",
	} {
		a := sound()
		a.Analyses[0].Projects[0].Name = name
		err := pluginhost.CheckAnalysis("go", roots, a)
		if err == nil || !strings.Contains(err.Error(), rule) || name != "" && strings.Contains(err.Error(), name) {
			t.Errorf("the name %q: %v", name, err)
		}
	}
	for _, kind := range []string{"", "Go Module", "go_module", "-module", "go--module", strings.Repeat("k", pluginhost.ProjectNameAtMost+1)} {
		a := sound()
		a.Analyses[0].Projects[0].Kind = kind
		err := pluginhost.CheckAnalysis("go", roots, a)
		if err == nil || !strings.Contains(err.Error(), "its kind is not lower-case words") {
			t.Errorf("the kind %q: %v", kind, err)
		}
	}
	// A project at the repository's root is said to be there.
	a := sound()
	a.Analyses[0].Root, a.Analyses[0].Projects[0].Root, a.Analyses[0].Projects[0].Name = "", "", ""
	err := pluginhost.CheckAnalysis("go", []string{""}, a)
	if err == nil || !strings.Contains(err.Error(), "project at the repository's root: it has no name") {
		t.Errorf("a nameless project at the root: %v", err)
	}
}
