// Package buildfile is the file a build daemon is told its settings in,
// as it is written (build.yaml, docs/build-settings.md): its shape, read
// strictly and written whole. What the settings mean, and whether a
// daemon could be told them, is the build daemon's to say
// (build/daemon/settings); whoever writes the file, a box's supervisor
// among them, writes this shape.
package buildfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/common/record/records"
)

// Version is the version of the file that is written, and the one read.
const Version = 1

// File is the settings as they are written.
type File struct {
	Version int    `yaml:"version"`
	Plugins string `yaml:"plugins"`
	Grid    Grid   `yaml:"grid"`
	// Platform is the properties of the grid's workers.
	Platform map[string]string `yaml:"platform,omitempty"`
	Publish  Publish           `yaml:"publish,omitempty"`
}

// Grid is the grid a build's actions run on, and how it is asked.
type Grid struct {
	Address     string    `yaml:"address"`
	Instance    string    `yaml:"instance,omitempty"`
	Assets      string    `yaml:"assets,omitempty"`
	TLS         bool      `yaml:"tls,omitempty"`
	Authorities string    `yaml:"authorities,omitempty"`
	Token       string    `yaml:"token,omitempty"`
	Rewrites    []Rewrite `yaml:"rewrites,omitempty"`
}

// Rewrite is which proxy of the grid's stands in for a public site a
// download names: a regular expression, and the proxy's address for
// what it matches.
type Rewrite struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// Publish is what a publish may be given.
type Publish struct {
	// Environment are variables of where the daemon runs, by name.
	Environment []string `yaml:"environment,omitempty"`
}

// Read reads the file, strictly: a key that is none, a second document
// or another version is refused, and a file that is not there is an
// error.
func Read(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("the build's settings: %w", err)
	}
	var said File
	strict := yaml.NewDecoder(bytes.NewReader(raw))
	strict.KnownFields(true)
	if err := strict.Decode(&said); err != nil {
		return File{}, fmt.Errorf("the build's settings (%s): %w", path, err)
	}
	if err := strict.Decode(new(File)); !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("the build's settings (%s) are one document, and hold another", path)
	}
	if said.Version != Version {
		return File{}, fmt.Errorf("the build's settings (%s): version is %d, and this daemon reads version %d", path, said.Version, Version)
	}
	return said, nil
}

// Write writes the file, whole or as it was: beside the one it replaces
// and put in its place, so that a daemon that reads it finds all of it
// or the one before.
func Write(path string, said File) error {
	said.Version = Version
	raw, err := yaml.Marshal(said)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return records.Write(path, raw, 0o644)
}
