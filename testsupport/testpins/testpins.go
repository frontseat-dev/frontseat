// Package testpins gives test fixtures the tool pins this repository
// builds with, so that a fixture's tools are real, locked ones and no
// version is written down twice.
package testpins

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testmodule"

	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// Go makes a directory a repository that Go builds: it writes mise.toml
// and mise.lock, pinning Go as this repository pins it, and a
// frontseat.yaml that lists the Go plugin and the Backstage adapter, so
// that a catalog file the fixture holds is read, and kept in step, by
// the plugin that owns its schema.
func Go(t testing.TB, dir string) {
	t.Helper()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.ReadFile(filepath.Join(root, "mise.lock"))
	if err != nil {
		t.Fatal(err)
	}
	// The lock is written as mise writes one, with the lines that say
	// which format it is in: a mise that does not know the format reads
	// no lock at all, and a fixture without them would never show it.
	header, _, _ := strings.Cut(string(lock), "[[tools.")
	_, section, found := strings.Cut(string(lock), "[[tools.go]]\n")
	if !found {
		t.Fatal("mise.lock pins no go")
	}
	if end := strings.Index(section, "\n[[tools."); end >= 0 {
		section = section[:end+1]
	}
	// A worker's mise installs Go with its own installer and no plugin of
	// anyone's. A lock that names another one is of no use to a fixture,
	// and says so here rather than in every build that needs Go.
	if !strings.Contains(section, `backend = "core:go"`) {
		t.Fatal("mise.lock does not pin go through mise's own installer; mend it with: mise backends switch go && mise lock --platform linux-arm64,linux-x64,macos-arm64,macos-x64 go")
	}
	version := ""
	for _, line := range strings.Split(section, "\n") {
		if value, is := strings.CutPrefix(line, "version = "); is {
			version = strings.Trim(value, `"`)
			break
		}
	}
	if version == "" {
		t.Fatal("mise.lock pins go at no version")
	}
	testfiles.Write(t, filepath.Join(dir, "mise.toml"), "[tools]\ngo = \""+version+"\"\n")
	testfiles.Write(t, filepath.Join(dir, "mise.lock"), header+"[[tools.go]]\n"+section)
	testfiles.Write(t, filepath.Join(dir, "frontseat.yaml"), "plugins:\n  go: {}\n  backstage: {}\n")
}

// Own makes a directory a repository pinned as this one is: it holds
// this repository's mise.toml and mise.lock, whole, so that a fixture
// runs with every tool this repository builds with, as it is locked.
func Own(t testing.TB, dir string) {
	t.Helper()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"mise.toml", "mise.lock"} {
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		testfiles.Write(t, filepath.Join(dir, file), string(content))
	}
}

// Caddy adds the official native release the tests of a service run. It
// can stand alone or accompany Go's pins; the sandbox service's tests and
// the acceptance specs share the same archive checksums, never a host
// installation.
func Caddy(t testing.TB, dir string) {
	add(t, dir, "caddy", `"http:caddy"`)
}

// Node adds the pinned Node distribution (including its npm), alone or beside
// another ecosystem's pins, for the real JavaScript framework proofs.
func Node(t testing.TB, dir string) {
	add(t, dir, "node", "[tools.node")
}

// Yarn adds the pinned Yarn: its own npm package, held to its checksum
// and made runnable, beside Node, which runs it.
func Yarn(t testing.TB, dir string) {
	add(t, dir, "yarn", `[tools."http:yarn"`)
}

// otherPins are the pins of the other ecosystems' tools, held by this
// package: they are built into it, and are not looked for.
//
//go:embed testdata
var otherPins embed.FS

func add(t testing.TB, dir, tool, marker string) {
	t.Helper()
	for _, file := range []string{"mise.toml", "mise.lock"} {
		pinned, err := fs.ReadFile(otherPins, path.Join("testdata", tool, file))
		if err != nil {
			t.Fatal(err)
		}
		existing, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if len(existing) > 0 {
			if strings.Contains(string(existing), marker) {
				t.Fatalf("fixture already pins %s", tool)
			}
			if file == "mise.lock" {
				_, section, ok := strings.Cut(string(pinned), "[[tools.")
				if !ok {
					t.Fatalf("%s fixture has no tool lock", tool)
				}
				pinned = []byte("[[tools." + section)
			}
		}
		testfiles.Write(t, filepath.Join(dir, file), string(existing)+"\n"+string(pinned))
	}
}
