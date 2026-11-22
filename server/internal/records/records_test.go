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
// part, and says which; the control, without the field, is read.
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
	if err := os.WriteFile(dir.File("one"), []byte(`{"owner":"someone"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := dir.Read("one", &read); err != nil || !found || read.Owner != "someone" {
		t.Errorf("the record without the field is read as %v, %v, %v", read, found, err)
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
