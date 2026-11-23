package build

import (
	"context"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/manifest"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// A build is of what its directory holds, whoever filled it: a
// repository another process brings into the directory, and records in
// the directory's manifest, is built by a daemon that was started
// before it was there and is told nothing. Its control is the directory
// before: with no repository recorded, a build is refused.
func TestABuildIsOfWhatItsDirectoryHoldsWhoeverFilledIt(t *testing.T) {
	workspace := t.TempDir()
	c := serve(t, scripted{}, workspace, false)
	if _, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a build of a directory that holds nothing: %v; want it refused", err)
	}

	// What fills a directory, in a process of its own: the repository's
	// files, and then the manifest that records it whole.
	write(t, workspace, "code/hello/go.mod", "module example.com/hello\n\ngo 1.27\n")
	write(t, workspace, "code/hello/main.go", "package main\n\nfunc main() {}\n")
	testpins.Go(t, workspace+"/code")
	if err := manifest.Write(workspace, manifest.Manifest{Repositories: []record.Repository{
		{Name: "code", Remote: "https://forge.example/acme/code.git", Commit: strings.Repeat("a", 40)},
	}}); err != nil {
		t.Fatal(err)
	}

	if labels := planned(t, c); !strings.Contains(labels, "//code/hello:go.compile") {
		t.Errorf("a build plans %q; want the module of the repository the directory now holds", labels)
	}
}
