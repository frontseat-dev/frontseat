package manifest_test

import (
	"os"
	"path/filepath"
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

// A manifest is read strictly: one that holds a key this reader does
// not know was written by something else, and is not read in part. The
// control is the same manifest without the key, which is read
// (TestAManifestIsReadAsItWasWritten).
func TestAManifestOfAKeyUnknownIsNotReadInPart(t *testing.T) {
	dir := t.TempDir()
	testfiles.Tree(t, dir, map[string]string{manifest.File: sound + "another: true\n"})
	if got, found, err := manifest.Read(dir, git.System{}.Revision); err == nil {
		t.Errorf("a manifest of a key unknown was read as %+v (found %v)", got, found)
	}
}

// A manifest is written whole: what is written is read back as it was,
// a reader that reads while it is written again and again, at two
// sizes, finds all of one or of the other and never part of one, and
// nothing of the writing is left beside it.
func TestAManifestIsWrittenWholeAndReadBack(t *testing.T) {
	dir := t.TempDir()
	testfiles.Tree(t, dir, map[string]string{manifest.File: sound})
	first, _, err := manifest.Read(dir, git.System{}.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(dir, first); err != nil {
		t.Fatal(err)
	}
	again, found, err := manifest.Read(dir, git.System{}.Revision)
	if err != nil || !found || len(again.Repositories) != 1 || again.Repositories[0].Name != "services" {
		t.Errorf("what was written is read back as %+v (found %v, %v)", again, found, err)
	}
	shorter := manifest.Manifest{Repositories: first.Repositories}
	written := make(chan error, 1)
	go func() {
		for i := range 200 {
			if err := manifest.Write(dir, []manifest.Manifest{first, shorter}[i%2]); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()
	for done := false; !done; {
		select {
		case err := <-written:
			if err != nil {
				t.Fatal(err)
			}
			done = true
		default:
		}
		read, found, err := manifest.Read(dir, git.System{}.Revision)
		if err != nil || !found || len(read.Repositories) != 1 {
			t.Fatalf("a manifest read while it is written is %+v (found %v, %v), want all of one", read, found, err)
		}
	}
	left, err := os.ReadDir(filepath.Dir(filepath.Join(dir, filepath.FromSlash(manifest.File))))
	if err != nil || len(left) != 1 {
		t.Errorf("beside the manifest there is %v (%v), want it alone", left, err)
	}
}
