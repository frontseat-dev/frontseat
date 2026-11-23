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
	"io"
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
// alone, where it is not there. One that is there open to others is
// narrowed to the server's alone: it is the server's own directory, and
// what it holds is said to no one else.
func Open(dir string) (Dir, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Dir{}, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return Dir{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, info.Mode().Perm()&0o700); err != nil {
			return Dir{}, err
		}
	}
	return Dir{dir: dir}, nil
}

// File is where the record of a name is kept. A name is one its caller
// has checked to be a plain name: it is joined as it is written, and one
// that climbs names a file outside the directory.
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
// is one. A record is one JSON object: one that is anything else (null
// among it), that holds a field the value has not, or that has anything
// after itself, is an error. A field is matched as encoding/json
// matches one, its name in any case, and a field written twice is read
// as the last.
func (d Dir) Read(name string, into any) (bool, error) {
	raw, err := os.ReadFile(d.File(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return true, errors.New("the record is no JSON object")
	}
	reader := json.NewDecoder(bytes.NewReader(raw))
	reader.DisallowUnknownFields()
	if err := reader.Decode(into); err != nil {
		return true, err
	}
	if _, err := reader.Token(); !errors.Is(err, io.EOF) {
		return true, errors.New("the record holds more than one value")
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

// Write writes a file whole: it is written beside, in the same
// directory and named with a dot before it, and moved into place, so
// that whoever reads it finds all of it or the one before. The file has
// the mode asked, whatever the one before had and whatever the umask,
// and once Write returns it is on the disk: the file and the directory
// that names it are each synced.
func Write(file string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(file)
	pending, err := renameio.NewPendingFile(file, renameio.WithTempDir(dir), renameio.WithStaticPermissions(mode))
	if err != nil {
		return err
	}
	// Once it has replaced the file, there is nothing left to clean.
	defer func() { _ = pending.Cleanup() }()
	if _, err := pending.Write(content); err != nil {
		return err
	}
	if err := pending.CloseAtomicallyReplace(); err != nil {
		return err
	}
	return synced(dir)
}

// Settle removes what a Write of the file left beside it and never moved
// into place, in a process that ended between the two: a regular file
// of the name Write gives one, a dot, the file's name and digits. It is
// called where nothing is writing the file; the file itself, and
// whatever else is beside it, are kept.
func Settle(file string) error {
	dir, prefix := filepath.Dir(file), "."+filepath.Base(file)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rest, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok || rest == "" || strings.Trim(rest, "0123456789") != "" || !entry.Type().IsRegular() {
			continue
		}
		// Its own to delete: a file of the name Write makes one beside the
		// file by, which only an unfinished Write leaves.
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// synced syncs a directory, so that what it names is on the disk.
func synced(dir string) error {
	opened, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := opened.Sync(); err != nil {
		_ = opened.Close()
		return err
	}
	return opened.Close()
}
