package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/common/record/records"
)

// version is the manifest's version this program writes, and the one it
// reads.
const version = 1

// Read returns the manifest of the workspace in a directory, and whether
// it has one. The file is the workspace's, and whatever runs there can
// write it: one that names a place outside the workspace, or a commit
// that is not one as revision holds it, is an error. So is one of
// another version, or with a key this program does not write: another
// reader meant something by it that this one would pass over.
func Read(dir string, revision func(name string) error) (Manifest, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(File)))
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	m, err := Parse(raw, revision)
	if err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// Parse reads a manifest from its file's bytes, as Read does: for
// whoever must know the very bytes it was read from.
func Parse(raw []byte, revision func(name string) error) (Manifest, error) {
	// Read strictly: a key this reader does not know was written by
	// something else, and the file is not read in part.
	var m Manifest
	strict := yaml.NewDecoder(bytes.NewReader(raw))
	strict.KnownFields(true)
	if err := strict.Decode(&m); errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("it is empty")
	} else if err != nil {
		return Manifest{}, err
	}
	// One document is written: a second is what another writer meant
	// something by, which this reader would pass over.
	var more yaml.Node
	if err := strict.Decode(&more); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("it holds more than one document, and one is written")
	}
	if m.Version != version {
		return Manifest{}, fmt.Errorf("it says version %d, and version %d is the one read", m.Version, version)
	}
	if err := m.Within(revision); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Settle removes what a write that was never finished left beside the
// manifest, in a supervisor that ended between making it and putting it in
// place. It is called where nothing is being written: when the
// workspace is opened.
func Settle(dir string) error {
	return records.Settle(filepath.Join(dir, filepath.FromSlash(File)))
}

// Write keeps the manifest in the workspace's directory, whole or as it
// was: it is written beside the one it replaces and put in its place,
// so that whoever reads it finds all of it or the one before, and a
// supervisor stopped while it writes leaves the one that was there.
func Write(dir string, m Manifest) error {
	m.Version = version
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.FromSlash(File))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return records.Write(path, raw, 0o644)
}
