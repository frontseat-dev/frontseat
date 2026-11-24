// Package buildfile is the file a build daemon is told its settings in,
// as it is written (build.yaml, docs/build-settings.md): its shape, read
// strictly, of no more than a mebibyte, and written whole. What the settings mean, and whether a
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
	"strings"
	"unicode"

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

// Limit is how much a settings file may hold, in bytes: one mebibyte.
const Limit = 1 << 20

// Bytes returns what the file holds, a file that is not there being an
// error, and one of more than Limit refused, which is said.
func Bytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("the build's settings: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, Limit+1))
	if err != nil {
		return nil, fmt.Errorf("the build's settings: %w", err)
	}
	if len(raw) > Limit {
		return nil, oversize(path)
	}
	return raw, nil
}

func oversize(path string) error {
	return fmt.Errorf("the build's settings (%s) hold more than %d bytes (1 MiB), and are not read", path, Limit)
}

// Read reads the file, strictly (Parse).
func Read(path string) (File, error) {
	raw, err := Bytes(path)
	if err != nil {
		return File{}, err
	}
	return Parse(raw, path)
}

// Parse reads what a settings file holds, strictly; path is the file,
// for what is said of a mistake. The file is one document, written
// plainly, of the keys it has and no other, at this version: a second
// document or the mark that ends one (...), an anchor, an alias, a merge
// key and a tag are refused by name, so that every value is read where
// it is written; version is the integer written 1; and everything else
// but grid.tls, which is true or false, is a text written as one, never
// a null, a truth or a number taken for one, with no control character
// in it and no key of no name.
func Parse(raw []byte, path string) (File, error) {
	if len(raw) > Limit {
		return File{}, oversize(path)
	}
	in := func(err error) error { return fmt.Errorf("the build's settings (%s): %w", path, err) }
	for line := range bytes.Lines(raw) {
		if marked := bytes.TrimRight(line, " \t\r\n"); bytes.Equal(marked, []byte("...")) || bytes.HasPrefix(line, []byte("... ")) {
			return File{}, in(errors.New("the mark that ends a document (...) is not read: the file is one document, which ends where the file does"))
		}
	}
	var document yaml.Node
	tree := yaml.NewDecoder(bytes.NewReader(raw))
	if err := tree.Decode(&document); errors.Is(err, io.EOF) {
		return File{}, in(errors.New("the file says nothing"))
	} else if err != nil {
		return File{}, in(err)
	}
	if err := tree.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("the build's settings (%s) are one document, and hold another", path)
	}
	if err := plainly(&document, ""); err != nil {
		return File{}, in(err)
	}
	var said File
	strict := yaml.NewDecoder(bytes.NewReader(raw))
	strict.KnownFields(true)
	if err := strict.Decode(&said); err != nil {
		return File{}, in(err)
	}
	if said.Version != Version {
		return File{}, fmt.Errorf("the build's settings (%s): version is %d, and this daemon reads version %d", path, said.Version, Version)
	}
	return said, nil
}

// plainly holds a node of the file, at a key's path, and everything in
// it to what is read: nothing that has a value read elsewhere than where
// it is written, or as another kind than it is written as.
func plainly(node *yaml.Node, at string) error {
	wrong := func(of *yaml.Node, what string) error { return fmt.Errorf("line %d: %s", of.Line, what) }
	switch {
	case node.Kind == yaml.AliasNode || node.Anchor != "":
		return wrong(node, "an anchor or an alias is not read: the value is written out where it is meant")
	case node.Style&yaml.TaggedStyle != 0:
		return wrong(node, "a tag is not read: a value is written plainly, with nothing that says what kind it is")
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			switch {
			case key.Kind == yaml.ScalarNode && key.Style&yaml.TaggedStyle == 0 && key.ShortTag() == "!!merge":
				return wrong(key, "a merge key (<<) is not read: each key is written out where it is meant")
			case key.Kind != yaml.ScalarNode:
				return wrong(key, "a key is one word")
			case key.Value == "":
				return wrong(key, "a key of no name")
			}
			if err := plainly(key, at+" (a key)"); err != nil {
				return err
			}
			if err := plainly(node.Content[i+1], at+"."+key.Value); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		named, kind := strings.TrimPrefix(at, "."), node.ShortTag()
		switch at {
		case ".version":
			if kind != "!!int" || strings.Trim(node.Value, "0123456789") != "" {
				return wrong(node, "version is an integer, written as 1")
			}
		case ".grid.tls":
			if kind != "!!bool" {
				return wrong(node, "grid.tls is true or false")
			}
		default:
			if kind != "!!str" {
				return wrong(node, fmt.Sprintf("%s is written as what is read as %s, and is a text: write it in quotes, or leave its key out", named, strings.TrimPrefix(kind, "!!")))
			}
			if strings.ContainsFunc(node.Value, unicode.IsControl) {
				return wrong(node, named+" holds a control character: a value is on one line, of what is printed")
			}
		}
	default:
		for _, each := range node.Content {
			if err := plainly(each, at); err != nil {
				return err
			}
		}
	}
	return nil
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
