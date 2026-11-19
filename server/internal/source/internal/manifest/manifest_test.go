package manifest_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/source/internal/manifest"
	"github.com/frontseat-dev/frontseat/server/internal/source/internal/vcs/git"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
)

const sound = `version: 1
repositories:
  - name: services
    remote: file:///nowhere
    commit: 0000000000000000000000000000000000000000
    roots: [hello]
known:
  - ref: component:default/hello
    repository: services
    root: hello
`

func TestAManifestIsReadAsItWasWritten(t *testing.T) {
	dir := t.TempDir()
	testfiles.Tree(t, dir, map[string]string{manifest.File: sound})

	got, found, err := manifest.Read(dir, git.System{}.Revision)

	if err != nil || !found {
		t.Fatalf("read: found %v, err %v", found, err)
	}
	if len(got.Repositories) != 1 || got.Repositories[0].Name != "services" || len(got.Known) != 1 {
		t.Errorf("read %+v, want the repository services and the one entity it knows", got)
	}
}

// A manifest is a file of the workspace, which whatever runs there can
// write. What it names is joined to the workspace's directory and handed
// to git, so one that leads out of the workspace, or that git would read
// as an option, is not read.
func TestAManifestThatNamesWhatAWorkspaceCannotHoldIsNotRead(t *testing.T) {
	for name, change := range map[string][2]string{
		"a repository named out of the workspace":           {"name: services", "name: ../elsewhere"},
		"a repository named by a path":                      {"name: services", "name: a/b"},
		"a repository with no name":                         {"name: services", `name: ""`},
		"a repository named as the workspace itself":        {"name: services", "name: ."},
		"a repository where the workspace keeps its record": {"name: services", "name: .frontseat"},
		"a directory out of its repository":                 {"roots: [hello]", "roots: [../other]"},
		"a directory by its full path":                      {"roots: [hello]", "roots: [/etc]"},
		"a commit that is an option":                        {"commit: 0000000000000000000000000000000000000000", "commit: --output=/tmp/written"},
		"a commit that is a branch":                         {"commit: 0000000000000000000000000000000000000000", "commit: main"},
		"an entity in a repository out of the workspace":    {"repository: services", "repository: ../elsewhere"},
		"an entity placed out of its repository":            {"root: hello", "root: ../../elsewhere"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(sound, change[0]) {
				t.Fatalf("the manifest holds no %q to change", change[0])
			}
			dir := t.TempDir()
			testfiles.Tree(t, dir, map[string]string{manifest.File: strings.Replace(sound, change[0], change[1], 1)})

			_, _, err := manifest.Read(dir, git.System{}.Revision)

			if err == nil {
				t.Error("it was read")
			}
		})
	}
}
