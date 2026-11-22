package manifest

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/server/internal/records"
)

// Read returns the manifest of the workspace in a directory, and whether
// it has one. The file is the workspace's, and whatever runs there can
// write it: one that names a place outside the workspace, or a commit
// that is not one as revision holds it, is an error.
func Read(dir string, revision func(name string) error) (Manifest, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(File)))
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	// Read strictly: a key this reader does not know was written by
	// something else, and the file is not read in part.
	var m Manifest
	reader := yaml.NewDecoder(bytes.NewReader(raw))
	reader.KnownFields(true)
	if err := reader.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return Manifest{}, false, err
	}
	if err := m.Within(revision); err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// Write keeps the manifest in the workspace's directory, whole or as it
// was: whoever reads it finds all of it or the one before.
func Write(dir string, m Manifest) error {
	m.Version = 1
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
