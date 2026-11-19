package pluginhost_test

import (
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
)

// Review probe (independent reviewer, 2026-11-10): what a part's path may
// be, beyond what TestADownloadCutIntoPartsIsHeldToTheContract tries. An
// output is "written plainly, one spelling for one place" (plainOutput
// refuses a backslash, a NUL and a path that is not clean); a part's path
// reaches a worker's command line as an argument and is held to less.
func TestReviewAPartsPathIsHeldAsAnOutputsIs(t *testing.T) {
	roots := []string{"services"}
	if err := pluginhost.CheckAnalysis("cargo", roots, cutting()); err != nil {
		t.Fatalf("control refused: %v", err)
	}
	part := func(a *pluginv1.AnalyzeResponse, i int) *pluginv1.Part { return lastAction(a).Downloads[0].Parts[i] }
	for name, change := range map[string]func(*pluginv1.AnalyzeResponse){
		"a part whose path holds a NUL, which no argument of a command can":      func(a *pluginv1.AnalyzeResponse) { part(a, 0).Path = "~/.cache/apk/busybox/con\x00trol.tar.gz" },
		"a part whose path is not clean (a/../b)":                                func(a *pluginv1.AnalyzeResponse) { part(a, 0).Path = "~/.cache/apk/other/../busybox/control.tar.gz" },
		"a part whose path holds a slash twice":                                  func(a *pluginv1.AnalyzeResponse) { part(a, 0).Path = "~/.cache/apk//busybox/control.tar.gz" },
		"a part whose path holds a backslash":                                    func(a *pluginv1.AnalyzeResponse) { part(a, 0).Path = "~/.cache/apk/busybox\\control.tar.gz" },
		"a part kept beneath another part, as though that were a directory":      func(a *pluginv1.AnalyzeResponse) { part(a, 1).Path = "~/.cache/apk/busybox/control.tar.gz/data.tar.gz" },
		"a part kept as a directory that holds a file the fetch is given":        func(a *pluginv1.AnalyzeResponse) { part(a, 0).Path = "~/.cache/apk/keys" },
		"a part kept where no output of the fetch is, so that it is thrown away": func(a *pluginv1.AnalyzeResponse) { part(a, 0).Path = "~/elsewhere/control.tar.gz" },
	} {
		refused(t, name, "cargo", roots, cutting, change)
	}
}

// Review probe: the "whole" exception (a shared fetch with files and
// outputs and neither a command nor downloads). No test of the change
// tries it at the host; these are its controls and refusals.
func TestReviewASharedFetchThatRunsNothingIsHeldToItsFiles(t *testing.T) {
	roots := []string{"services"}
	whole := func() *pluginv1.AnalyzeResponse {
		a := sound()
		p := a.GetAnalyses()[0].GetProjects()[0]
		p.Actions = append(p.Actions, &pluginv1.Action{
			Name: "fetch/packages.wolfi.dev/os/wolfi-signing.rsa.pub", Shared: true, Fetch: true,
			Files:   []*pluginv1.File{{Path: "~/.cache/keys/wolfi.rsa.pub/locked.etag", Content: []byte("key")}},
			Outputs: []*pluginv1.Output{{Path: "~/.cache/keys/wolfi.rsa.pub/"}},
		})
		p.Actions[1].Inputs = append(p.Actions[1].Inputs, pluginhosttest.OutputInput("fetch/packages.wolfi.dev/os/wolfi-signing.rsa.pub"))
		return a
	}
	fetch := lastAction
	if err := pluginhost.CheckAnalysis("apko", roots, whole()); err != nil {
		t.Fatalf("control refused: %v", err)
	}
	for name, change := range map[string]func(*pluginv1.AnalyzeResponse){
		// Its files are what it leaves: an output no file of its is in or
		// beneath is left by nothing, and the action can only fail on a
		// worker for a missing output.
		"an output none of its files is beneath": func(a *pluginv1.AnalyzeResponse) { fetch(a).Outputs[0].Path = "~/.cache/other/" },
		// Its files in its own directory are no output of a shared action
		// (outputs are of the home alone), so they are left nowhere.
		"only files of its own directory, which it cannot leave": func(a *pluginv1.AnalyzeResponse) {
			fetch(a).Files = []*pluginv1.File{{Path: "key.rsa.pub", Content: []byte("key")}}
		},
		// It runs nothing, so a tool is installed for nothing.
		"tools, with nothing to run them": func(a *pluginv1.AnalyzeResponse) { fetch(a).Tools = []string{"apko"} },
	} {
		t.Run(name, func(t *testing.T) {
			a := whole()
			change(a)
			if err := pluginhost.CheckAnalysis("apko", roots, a); err == nil {
				t.Error("accepted")
			} else if !strings.Contains(err.Error(), "fetch/packages.wolfi.dev") {
				t.Errorf("refused without naming the action: %v", err)
			}
		})
	}
}
