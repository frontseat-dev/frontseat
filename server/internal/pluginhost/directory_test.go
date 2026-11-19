package pluginhost_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

type snapshotSource struct{ snapshot *source.Snapshot }

func (s snapshotSource) Read(ctx context.Context, path string) ([]byte, bool, error) {
	return s.snapshot.Read(ctx, "", path)
}
func (s snapshotSource) Glob(ctx context.Context, directory string, patterns []string) ([]string, error) {
	return s.snapshot.Within(ctx, "", directory, patterns)
}
func TestProjectRootIsLiteralDuringEmbedDiscovery(t *testing.T) {
	for _, c := range []struct {
		name, root string
		neighbor   bool
	}{
		{"ordinary-control", "ordinary", false},
		{"literal-brackets", "pkg[1]", false},
		{"literal-brackets-with-neighbor", "pkg[1]", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkout := t.TempDir()
			files := map[string]string{
				c.root + "/go.mod":    "module example.com/hello\n\ngo 1.27\n",
				c.root + "/main.go":   "package main\nimport _ \"embed\"\n//go:embed asset.txt\nvar asset string\nfunc main() {}\n",
				c.root + "/asset.txt": "correct-project-asset",
			}
			if c.neighbor {
				files["pkg1/main.go"] = "package main\nimport _ \"embed\"\n//go:embed wrong.txt\nvar wrong string\n"
				files["pkg1/wrong.txt"] = "wrong-project-asset"
			}
			for name, content := range files {
				path := filepath.Join(checkout, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: checkout, Paths: map[string]string{"": "."}})
			if err != nil {
				t.Fatal(err)
			}
			within, err := snapshot.Within(t.Context(), "", c.root, []string{"**/*.go"})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(within, c.root+"/main.go") {
				t.Fatalf("Snapshot.Within control missed main.go: %v", within)
			}
			plugin := load(t)
			reply := new(pluginv1.AnalyzeResponse)
			reads, err := plugin.Call(t.Context(), snapshotSource{snapshot}, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext(), Roots: []string{c.root}}, reply)
			if err != nil {
				t.Fatal(err)
			}
			if err := pluginhost.CheckAnalysis("go", []string{c.root}, reply); err != nil {
				t.Fatal(err)
			}
			project := reply.GetAnalyses()[0].GetProjects()[0]
			for _, set := range project.GetInputSets() {
				patterns, _ := pluginhost.SplitInputs(set.GetInputs())
				if !pattern.Select(patterns, "asset.txt") {
					t.Errorf("%s did not include its embedded asset: patterns %v; observed reads %+v", set.GetName(), set.GetInputs(), reads)
				}
			}
			ownRead := false
			for _, read := range reads {
				if read.Import == "read" && read.Argument == c.root+"/main.go" {
					ownRead = true
				}
				if read.Import == "read" && read.Argument == "pkg1/main.go" {
					t.Errorf("read neighboring project: %+v", read)
				}
			}
			if !ownRead {
				t.Error("never read this project's embedding directive")
			}
		})
	}
}
