package pluginhost_test

import (
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestValidGoEmbedSyntax(t *testing.T) {
	for name, directive := range map[string]string{"ordinary": "//go:embed asset.txt", "indented": "\t//go:embed asset.txt", "escaped": `//go:embed "\x61sset.txt"`, "octal": `//go:embed "\141sset.txt"`, "unicode": `//go:embed "\u0061sset.txt"`} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			for name, content := range map[string]string{"go.mod": "module example.com/embed\n\ngo 1.27\n", "main.go": "package main\nimport _ \"embed\"\n" + directive + "\nvar asset string\nfunc main() { println(asset) }\n", "asset.txt": "real-asset"} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: directory, Paths: map[string]string{"": "."}})
			if err != nil {
				t.Fatal(err)
			}
			plugin := load(t)
			reply := new(pluginv1.AnalyzeResponse)
			_, err = plugin.Call(t.Context(), snapshotSource{snapshot}, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext(), Roots: []string{""}}, reply)
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range reply.GetAnalyses()[0].GetProjects()[0].GetInputSets() {
				patterns, _ := pluginhost.SplitInputs(set.GetInputs())
				files, err := snapshot.Within(t.Context(), "", "", patterns)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(files, "asset.txt") {
					t.Errorf("valid native Go embed omitted from %s: patterns %v; files %v", set.GetName(), set.GetInputs(), files)
				}
			}
		})
	}
}
