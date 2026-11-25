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
		Grid: buildfile.Grid{Address: "grid:8980", Instance: "a/b", Reads: []string{"a/c", "a/d"}, Assets: "grid:8983", TLS: true, Authorities: "ca.pem", Token: "token", Rewrites: []buildfile.Rewrite{{From: "^a$", To: "b"}}}}
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

// The grid's instances are named as the Remote Execution API names one,
// and as Buildbarn reads one (bb-storage's digest.NewInstanceName): its
// own instance, which may be the grid's default of no name, and each it
// reads, which is named, once, and is not its own. A name is one word of
// what is printed, its parts parted by one slash, none of them a word
// the protocol keeps for itself in a resource's name. Whoever writes the
// file and whoever reads it hold it to this one check. The control is
// each instance named as one is.
func TestTheGridsInstancesAreNamedAsAnInstanceIs(t *testing.T) {
	sound := buildfile.Grid{Address: "grid:8980", Instance: "frontseat/p", Reads: []string{"frontseat/b", "frontseat/c"}}
	if err := sound.CheckInstances(); err != nil {
		t.Fatalf("the control: %v", err)
	}
	if err := (buildfile.Grid{Address: "grid:8980", Reads: []string{"frontseat/b"}}).CheckInstances(); err != nil {
		t.Errorf("the control: the grid's default instance, reading another: %v", err)
	}
	for name, said := range map[string]struct {
		instance string
		reads    []string
		refused  string
	}{
		"an instance with a space":          {"frontseat p", nil, "grid.instance holds a space"},
		"an instance with a control":        {"frontseat\x01p", nil, "grid.instance holds a control character"},
		"an instance with a leading slash":  {"/frontseat/p", nil, "grid.instance is parted by one slash"},
		"an instance with a trailing slash": {"frontseat/p/", nil, "grid.instance is parted by one slash"},
		"an instance with two slashes":      {"frontseat//p", nil, "grid.instance is parted by one slash"},
		"an instance of a word kept":        {"frontseat/blobs", nil, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance of uploads":            {"uploads", nil, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance of actions":            {"a/actions/b", nil, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance of actionResults":      {"actionResults", nil, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance of operations":         {"operations", nil, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance of capabilities":       {"capabilities", nil, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance of compressed-blobs":   {"compressed-blobs", nil, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance read with a space":     {"frontseat/p", []string{"frontseat/b", "frontseat q"}, "grid.reads' entry 2 holds a space"},
		"an instance read of no name":       {"frontseat/p", []string{""}, "grid.reads' entry 1 names no instance"},
		"an instance read of a word kept":   {"frontseat/p", []string{"frontseat/blobs"}, "grid.reads' entry 1 has a part that is a word the Remote Execution API keeps"},
		"an instance read of two slashes":   {"frontseat/p", []string{"frontseat//b"}, "grid.reads' entry 1 is parted by one slash"},
		"its own instance read":             {"frontseat/p", []string{"frontseat/b", "frontseat/p"}, "grid.reads' entry 2 is the daemon's own instance"},
		"the default instance read":         {"", []string{""}, "grid.reads' entry 1 names no instance"},
		"an instance read twice":            {"frontseat/p", []string{"frontseat/b", "frontseat/c", "frontseat/b"}, "grid.reads' entry 3 is its entry 1 again"},
		"an instance read with a control":   {"frontseat/p", []string{"frontseat\tb"}, "grid.reads' entry 1 holds a space"},
		"an instance read of a word on two": {"frontseat/p", []string{"a\nb"}, "grid.reads' entry 1 holds a space"},
		"an instance read with a null":      {"frontseat/p", []string{"a\x00b"}, "grid.reads' entry 1 holds a control character"},
	} {
		grid := buildfile.Grid{Address: "grid:8980", Instance: said.instance, Reads: said.reads}
		err := grid.CheckInstances()
		if err == nil || !strings.Contains(err.Error(), said.refused) {
			t.Errorf("%s: %v; want it refused, saying %q", name, err, said.refused)
		}
		// A refusal never repeats what it refused: it says which entry,
		// and why, never the name.
		for _, named := range append([]string{said.instance}, said.reads...) {
			if named != "" && err != nil && strings.Contains(err.Error(), named) {
				t.Errorf("%s: %v repeats %q", name, err, named)
			}
		}
	}
	// Instances are of a grid: told them and no grid's address, the file
	// is refused, by whoever writes it and whoever reads it alike. The
	// control is no grid's address and no instance, which is no grid.
	for name, grid := range map[string]buildfile.Grid{
		"an instance and no grid":    {Instance: "frontseat/p"},
		"instances read and no grid": {Reads: []string{"frontseat/b"}},
	} {
		if err := grid.CheckInstances(); err == nil || !strings.Contains(err.Error(), "grid.address names none") {
			t.Errorf("%s: %v; want it refused, saying grid.address names none", name, err)
		}
	}
	if err := (buildfile.Grid{}).CheckInstances(); err != nil {
		t.Errorf("the control: no grid and no instance: %v", err)
	}
	// An instance whose name YAML would read as something else, as
	// nothing (~) or a truth, is a name the check lets through: it is
	// written in quotes, and read back as the text it is, so the file
	// a supervisor writes is never one its daemon refuses for it.
	odd := buildfile.File{Plugins: "/opt/plugins", Grid: buildfile.Grid{Address: "grid:8980", Instance: "~", Reads: []string{"null", "true", "1", "0x1", "yes"}}}
	if err := odd.Grid.CheckInstances(); err != nil {
		t.Fatalf("instances YAML would read as another kind: %v", err)
	}
	file := filepath.Join(t.TempDir(), "build.yaml")
	if err := buildfile.Write(file, odd); err != nil {
		t.Fatal(err)
	}
	odd.Version = buildfile.Version
	if read, err := buildfile.Read(file); err != nil || !reflect.DeepEqual(read, odd) {
		t.Errorf("instances YAML would read as another kind were read back as %+v (%v), want %+v", read.Grid, err, odd.Grid)
	}
}
