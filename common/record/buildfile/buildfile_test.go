package buildfile_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/common/record/buildfile"
)

// What is written as the file's shape is read back as it was, at the
// file's version: whoever writes the settings and whoever reads them
// agree, by this one shape.
func TestTheFileIsReadAsItWasWritten(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kept", "build.yaml")
	written := buildfile.File{Plugins: "/opt/plugins", Platform: map[string]string{"OSFamily": "linux"}, Publish: buildfile.Publish{Environment: []string{"GH_TOKEN"}},
		Grid: buildfile.Grid{Address: "grid:8980", Instance: "a/b", Assets: "grid:8983", TLS: true, Authorities: "ca.pem", Token: "token", Rewrites: []buildfile.Rewrite{{From: "^a$", To: "b"}}}}
	if err := buildfile.Write(file, written); err != nil {
		t.Fatal(err)
	}
	written.Version = buildfile.Version
	if read, err := buildfile.Read(file); err != nil || !reflect.DeepEqual(read, written) {
		t.Errorf("read %+v (%v); want what was written, %+v", read, err, written)
	}
}

// The file is one document, written plainly, and what is not is refused
// for what it is, with the line it is on: nothing in it has a value read
// elsewhere than where it is written, or as another kind than it is
// written as. The control is the same document written plainly.
func TestAFileThatIsNotWrittenPlainlyIsRefusedForWhatItIs(t *testing.T) {
	const plain = "version: 1\nplugins: plugins\ngrid:\n  address: grid:8980\n"
	if _, err := buildfile.Parse([]byte(plain), "build.yaml"); err != nil {
		t.Fatalf("the control: %v", err)
	}
	for name, said := range map[string][2]string{
		"an anchor on a map":            {"version: 1\nplugins: plugins\ngrid: &grid\n  address: grid:8980\n", "line 3: an anchor or an alias"},
		"a tag on a map":                {"version: 1\nplugins: plugins\ngrid: !!map\n  address: grid:8980\n", "line 3: a tag is not read"},
		"a merge key in the grid":       {plain + "  <<: {instance: a}\n", "line 5: a merge key"},
		"a key that is a list":          {plain + "? [a]\n: b\n", "a key is one word"},
		"a key that is a truth":         {plain + "true: b\n", "(a key) is written as what is read as bool"},
		"plugins that are nothing":      {"version: 1\nplugins:\ngrid:\n  address: grid:8980\n", "plugins is written as what is read as null"},
		"plugins that are a truth":      {"version: 1\nplugins: yes\ngrid:\n  address: grid:8980\n", ""},
		"a version in quotes":           {"version: '1'\nplugins: plugins\ngrid:\n  address: grid:8980\n", "version is an integer"},
		"tls in quotes":                 {plain + "  tls: 'true'\n", "grid.tls is true or false"},
		"a value on two lines":          {plain + "  instance: |\n    a\n    b\n", "grid.instance holds a control character"},
		"a file that says nothing":      {"# nothing\n", "the file says nothing"},
		"the mark that ends a document": {plain + "...\n", "ends a document"},
		"a second document":             {plain + "---\nversion: 1\n", "hold another"},
	} {
		got, err := buildfile.Parse([]byte(said[0]), "build.yaml")
		if said[1] == "" {
			// Written plainly: a word YAML 1.1 read as a truth is a text.
			if err != nil || got.Plugins != "yes" {
				t.Errorf("%s: %+v, %v; want the word as it is written", name, got, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), said[1]) {
			t.Errorf("%s: %+v, %v; want it refused, saying %q", name, got, err, said[1])
		}
	}
	if _, err := buildfile.Parse([]byte(plain+strings.Repeat("#", buildfile.Limit)), "build.yaml"); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Errorf("more than a mebibyte: %v; want it refused, saying the bound", err)
	}
	if _, err := buildfile.Read(filepath.Join(t.TempDir(), "absent.yaml")); err == nil || !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("a file that is not there: %v", err)
	}
}
