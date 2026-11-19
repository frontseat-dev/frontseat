package manifest

import (
	"errors"
	"go.yaml.in/yaml/v4"
	"io/fs"
	"os"
	"path/filepath"
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
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Manifest{}, false, err
	}
	if err := m.Within(revision); err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// Write keeps the manifest in the workspace's directory.
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
	return os.WriteFile(path, raw, 0o644)
}
