package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
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

	got, found, err := manifest.Read(dir, revision)

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
		"a repository recorded twice":                       {"known:", "  - name: services\n    remote: file:///elsewhere\n    commit: 0000000000000000000000000000000000000000\nknown:"},
		"a state that is none":                              {"version: 1", "version: 1\nstate: ready"},
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

			_, _, err := manifest.Read(dir, revision)

			if err == nil {
				t.Error("it was read")
			}
		})
	}
}

// A manifest is read as what this daemon writes and nothing more: a key
// it does not know, a version it does not write, or a second document,
// is one another reader meant something by, and is refused by name, as
// is a manifest that says nothing. The control is
// the manifest as written, which TestAManifestIsReadAsItWasWritten reads.
func TestAManifestOfAnotherVersionOrWithAKeyUnknownIsNotRead(t *testing.T) {
	for name, c := range map[string]struct{ change, to, says string }{
		"a later version":       {"version: 1", "version: 2", "version 2"},
		"no version":            {"version: 1\n", "", "version 0"},
		"a key of the manifest": {"known:", "mounts: []\nknown:", "mounts"},
		"a key of a repository": {"    roots: [hello]", "    roots: [hello]\n    depth: 1", "depth"},
		"a second document":     {"version: 1\n", "version: 1\n---\nanything: at all\n---\n", "more than one document"},
		"nothing":               {sound, "# nothing\n", "it is empty"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(sound, c.change) {
				t.Fatalf("the manifest holds no %q to change", c.change)
			}
			dir := t.TempDir()
			testfiles.Tree(t, dir, map[string]string{manifest.File: strings.Replace(sound, c.change, c.to, 1)})

			_, _, err := manifest.Read(dir, revision)

			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Errorf("read: %v, want it refused, naming %s", err, c.says)
			}
		})
	}
}

// A manifest is written whole or not at all: beside the one it
// replaces, then put in its place, so that what is written is read
// back as it was, a reader that reads while it is written again and
// again, at two sizes, finds all of one or of the other and never part
// of one, and nothing but the manifest is left where it is kept.
func TestAManifestIsWrittenWholeInThePlaceOfTheOneItReplaces(t *testing.T) {
	dir := t.TempDir()
	testfiles.Tree(t, dir, map[string]string{manifest.File: sound})
	whole, _, err := manifest.Read(dir, revision)
	if err != nil {
		t.Fatal(err)
	}
	shorter := whole
	shorter.Known = nil

	if err := manifest.Write(dir, shorter); err != nil {
		t.Fatal(err)
	}

	again, found, err := manifest.Read(dir, revision)
	if err != nil || !found || len(again.Known) != 0 || len(again.Repositories) != 1 {
		t.Errorf("read back %+v (found %v, %v), want the repository and no entity", again, found, err)
	}
	written := make(chan error, 1)
	go func() {
		for i := range 200 {
			if err := manifest.Write(dir, []manifest.Manifest{whole, shorter}[i%2]); err != nil {
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
		read, found, err := manifest.Read(dir, revision)
		if err != nil || !found || len(read.Repositories) != 1 {
			t.Fatalf("a manifest read while it is written is %+v (found %v, %v), want all of one", read, found, err)
		}
	}
	left, err := os.ReadDir(filepath.Dir(filepath.Join(dir, filepath.FromSlash(manifest.File))))
	if err != nil || len(left) != 1 {
		t.Errorf("beside the manifest there is %v (%v), want it alone", left, err)
	}
}

// What a write left beside the manifest, by a daemon that ended before
// putting it in place, is cleared; the manifest, and what is beside it
// of another name, are kept.
func TestWhatAWriteLeftUnfinishedIsCleared(t *testing.T) {
	dir := t.TempDir()
	testfiles.Tree(t, dir, map[string]string{manifest.File: sound, ".frontseat/.workspace.yaml1234": "version: 1\n", ".frontseat/other.yaml": "kept\n"})

	if err := manifest.Settle(dir); err != nil {
		t.Fatal(err)
	}

	kept, err := os.ReadDir(filepath.Dir(filepath.Join(dir, filepath.FromSlash(manifest.File))))
	var names []string
	for _, entry := range kept {
		names = append(names, entry.Name())
	}
	if err != nil || strings.Join(names, ",") != "other.yaml,workspace.yaml" {
		t.Errorf("beside the manifest are %v (%v), want the manifest and other.yaml", names, err)
	}
}

// revision holds a name to being a commit's hash written in full, as the
// version control system a workspace is brought with does: the manifest
// is read with whatever its reader holds a revision to.
func revision(name string) error {
	if len(name) != 40 || strings.Trim(name, "0123456789abcdef") != "" {
		return errors.New("is not a commit's full hash")
	}
	return nil
}
