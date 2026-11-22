package records_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/records"
)

type kept struct {
	Owner string `json:"owner"`
	Count int    `json:"count,omitempty"`
}

// A record kept is read back as it was kept, listed by its name, and
// gone once it is dropped; dropping it again is no error.
func TestARecordKeptIsReadBackListedAndDropped(t *testing.T) {
	dir, err := records.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Keep("one", kept{Owner: "someone", Count: 2}); err != nil {
		t.Fatal(err)
	}
	var read kept
	if found, err := dir.Read("one", &read); err != nil || !found || read != (kept{Owner: "someone", Count: 2}) {
		t.Fatalf("read %v, %v, %v, want the record as it was kept", read, found, err)
	}
	if names, err := dir.Names(); err != nil || !slices.Equal(names, []string{"one"}) {
		t.Fatalf("the records are %v, %v, want the one kept", names, err)
	}
	if info, err := os.Stat(dir.File("one")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the record's file is %v, %v, want the server's alone to read", info, err)
	}
	for range 2 {
		if err := dir.Drop("one"); err != nil {
			t.Fatal(err)
		}
	}
	if found, err := dir.Read("one", &read); found || err != nil {
		t.Errorf("a record dropped is read: %v, %v", found, err)
	}
}

// A record that holds a field its reader does not know is not read in
// part, and says which; nor is one that is no JSON object, or that has
// anything after itself. The control, the object alone, is read.
func TestARecordOfAFieldItsReaderDoesNotKnowIsRefused(t *testing.T) {
	dir, err := records.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir.File("one"), []byte(`{"owner":"someone","since":"removed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var read kept
	if _, err := dir.Read("one", &read); err == nil || !strings.Contains(err.Error(), "since") {
		t.Fatalf("err = %v, want the field it does not know named", err)
	}
	for what, written := range map[string]string{
		"null":                       `null`,
		"a list":                     `[{"owner":"someone"}]`,
		"a closing bracket after it": `{"owner":"someone"}]`,
		"a closing brace after it":   `{"owner":"someone"}}`,
		"another record after it":    `{"owner":"someone"} {"owner":"another"}`,
		"nothing":                    ``,
	} {
		if err := os.WriteFile(dir.File("one"), []byte(written), 0o600); err != nil {
			t.Fatal(err)
		}
		if found, err := dir.Read("one", &read); err == nil {
			t.Errorf("a record of %s is read: %v, %v", what, read, found)
		}
	}
	if err := os.WriteFile(dir.File("one"), []byte(`{"owner":"someone"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := dir.Read("one", &read); err != nil || !found || read.Owner != "someone" {
		t.Errorf("the record without the field is read as %v, %v, %v", read, found, err)
	}
}

// A record kept has the mode a record has, whatever the file before it
// had, and the directory it is kept in is the server's alone, whatever
// it was when it was opened.
func TestARecordIsKeptAsTheServersAlone(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	modeOf := func(path string) os.FileMode {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	dir, err := records.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if mode := modeOf(state); mode != 0o700 {
		t.Errorf("the directory is %v, want the server's alone", mode)
	}
	if err := os.WriteFile(dir.File("one"), []byte(`{"owner":"someone"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir.File("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dir.Keep("one", kept{Owner: "another"}); err != nil {
		t.Fatal(err)
	}
	if mode := modeOf(dir.File("one")); mode != 0o600 {
		t.Errorf("the record kept over one others could read is %v, want the server's alone", mode)
	}
	if entries, err := os.ReadDir(state); err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %v, %v, want the record alone", entries, err)
	}
}

// What was being written when a server stopped, and what is no record's
// file, is no record.
func TestWhatIsNoRecordsFileIsNotListed(t *testing.T) {
	dir, err := records.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Keep("one", kept{}); err != nil {
		t.Fatal(err)
	}
	for _, other := range []string{".one.json123", ".two.json", "notes.txt", ".json"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(dir.File("one")), other), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(dir.File("directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if names, err := dir.Names(); err != nil || !slices.Equal(names, []string{"one"}) {
		t.Errorf("the records are %v, %v, want the one kept alone", names, err)
	}
}
