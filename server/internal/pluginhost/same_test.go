package pluginhost_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

func (f files) Digest(_ context.Context, path string) (string, bool, error) {
	content, ok := f[path]
	if !ok {
		return "", false, nil
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:]), true, nil
}

// What a plugin answered holds for as long as what it read is the same:
// the files it read, those it looked for and did not find, and the files
// its patterns matched.
func TestWhatACallReadIsTheSameOrIsNot(t *testing.T) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	plugin, err := host.Load(ctx, module)
	if err != nil {
		t.Fatal(err)
	}
	read := files{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"}
	reads, err := plugin.Call(ctx, read, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, &pluginv1.AnalyzeResponse{})
	if err != nil || len(reads) == 0 {
		t.Fatalf("the plugin read %d things, err %v; want it to read the module", len(reads), err)
	}

	for name, c := range map[string]struct {
		now  files
		same bool
	}{
		"as it was":                 {files{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"}, true},
		"a file it read changed":    {files{"go.mod": goMod + "\n// a word more\n", "go.sum": goSum, "main.go": "package main\n"}, false},
		"a file it read is gone":    {files{"go.mod": goMod, "main.go": "package main\n"}, false},
		"a file its patterns match": {files{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n", "more.go": "package main\n"}, false},
	} {
		same, err := pluginhost.Same(ctx, c.now, reads)
		if err != nil || same != c.same {
			t.Errorf("%s: same %v, err %v; want %v", name, same, err, c.same)
		}
	}
}

func TestScopedGlobDependenciesFollowOnlyTheirLiteralDirectory(t *testing.T) {
	plugin := load(t)
	read := files{"pkg[1]/go.mod": goMod, "pkg[1]/main.go": "package main\n", "pkg1/other.go": "package other\n"}
	reads, err := plugin.Call(t.Context(), read, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext(), Roots: []string{"pkg[1]"}}, new(pluginv1.AnalyzeResponse))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		path string
		same bool
	}{
		{"pkg1/neighbor.go", true},
		{"pkg[1]/notes.txt", true},
		{"pkg[1]/new.go", false},
	} {
		read[change.path] = "new content"
		same, err := pluginhost.Same(t.Context(), read, reads)
		if err != nil || same != change.same {
			t.Errorf("adding %s: same=%v, err=%v; want %v", change.path, same, err, change.same)
		}
		delete(read, change.path)
	}
}

func TestGlobRefusesDirectoriesOutsideTheRepository(t *testing.T) {
	plugin := load(t)
	for _, directory := range []string{"", "safe", "../outside", "/outside"} {
		manifest := "go.mod"
		if directory != "" {
			manifest = directory + "/go.mod"
		}
		// The intentionally permissive fixture supplies the manifest even for
		// an invalid root. The host must refuse its glob before calling Source.
		_, err := plugin.Call(t.Context(), files{manifest: goMod}, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext(), Roots: []string{directory}}, new(pluginv1.AnalyzeResponse))
		allowed := directory == "" || directory == "safe"
		if (err == nil) != allowed {
			t.Errorf("directory %q: %v, allowed=%v", directory, err, allowed)
		}
	}
}
