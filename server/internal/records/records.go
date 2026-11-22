// Package records keeps what a server must find again when it starts: a
// directory of the server's own, one JSON file for each record, named by
// the record's name. A record is written whole or it is the one before,
// and is read strictly: one that holds a field its reader does not know
// was written by another server, and is not read in part. The workspaces
// a control plane keeps, the sandboxes a sandbox service made and the
// credentials a control plane knows of are each kept so; what is kept
// elsewhere in another form (a workspace's manifest) is written whole by
// the same Write.
package records

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/renameio/v2"
)

// suffix is what a record's file is named with, after its name.
const suffix = ".json"

// Dir is a directory of records.
type Dir struct{ dir string }

// Open returns the records kept in a directory, making it, the server's
// alone, where it is not there.
func Open(dir string) (Dir, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Dir{}, err
	}
	return Dir{dir: dir}, nil
}

// File is where the record of a name is kept.
func (d Dir) File(name string) string { return filepath.Join(d.dir, name+suffix) }

// Keep writes a record under a name, in place of the one there was.
func (d Dir) Keep(name string, record any) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return Write(d.File(name), raw, 0o600)
}

// Drop removes the record of a name. Dropping one that is gone succeeds.
func (d Dir) Drop(name string) error {
	if err := os.Remove(d.File(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Read reads the record of a name into a value, and says whether there
// is one. A record that holds a field the value has not, or anything
// after itself, is an error.
func (d Dir) Read(name string, into any) (bool, error) {
	raw, err := os.ReadFile(d.File(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	reader := json.NewDecoder(bytes.NewReader(raw))
	reader.DisallowUnknownFields()
	if err := reader.Decode(into); err != nil {
		return true, err
	}
	if reader.More() {
		return true, fmt.Errorf("the record holds more than one value")
	}
	return true, nil
}

// Names are the names of the records kept, in the directory's order.
// What is not a record's file is left out: a file being written when the
// server stopped is named with a dot before it.
func (d Dir) Names() ([]string, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		name, is := strings.CutSuffix(entry.Name(), suffix)
		if is && name != "" && !strings.HasPrefix(name, ".") && !entry.IsDir() {
			out = append(out, name)
		}
	}
	return out, nil
}

// Write writes a file whole: it is written beside and moved into place,
// so that whoever reads it finds all of it or the one before.
func Write(file string, content []byte, mode os.FileMode) error {
	return renameio.WriteFile(file, content, mode)
}
