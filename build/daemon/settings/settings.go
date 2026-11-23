// Package settings is what a build daemon is told, read from one file
// when it starts: its plugins, the grid its actions run on and how that
// is asked, the platform of the grid's workers, and what a publish may
// be given. The daemon knows a directory and these, and nothing of who
// wrote them: a box's supervisor, from what the control plane told it,
// or whoever set a machine or a job up.
//
// The file is read strictly, and what is wrong with it is said when the
// daemon starts. It holds no secret: a token is a file's path.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/build/daemon/build"
)

// version is the version of the file this daemon reads.
const version = 1

// Settings are what a build daemon is told.
type Settings struct {
	// Plugins are the plugins' wasm modules, by path.
	Plugins []string
	// Grid is the grid its actions run on, and how it is asked.
	Grid build.Grid
	// Platform is what the grid's workers are: the properties every
	// action asks for.
	Platform map[string]string
	// Given are the variables of where the daemon runs that what
	// publishes a release may be given, by name.
	Given []string
}

// file is the settings as they are written.
type file struct {
	Version int    `yaml:"version"`
	Plugins string `yaml:"plugins"`
	Grid    struct {
		Address     string `yaml:"address"`
		Instance    string `yaml:"instance"`
		Assets      string `yaml:"assets"`
		TLS         bool   `yaml:"tls"`
		Authorities string `yaml:"authorities"`
		Token       string `yaml:"token"`
		Rewrites    []struct {
			From string `yaml:"from"`
			To   string `yaml:"to"`
		} `yaml:"rewrites"`
	} `yaml:"grid"`
	Platform map[string]string `yaml:"platform"`
	Publish  struct {
		Environment []string `yaml:"environment"`
	} `yaml:"publish"`
}

// Read reads a daemon's settings from a file. A path the file names is
// from the file's own directory. It is refused where it says a key that
// is none, another version or a second document, where its plugins are
// not there, and where its grid could not be asked as it says.
func Read(path string) (Settings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("the build's settings: %w", err)
	}
	var said file
	strict := yaml.NewDecoder(bytes.NewReader(raw))
	strict.KnownFields(true)
	if err := strict.Decode(&said); err != nil {
		return Settings{}, fmt.Errorf("the build's settings (%s): %w", path, err)
	}
	if err := strict.Decode(new(file)); !errors.Is(err, io.EOF) {
		return Settings{}, fmt.Errorf("the build's settings (%s) are one document, and hold another", path)
	}
	told, err := said.told(filepath.Dir(path))
	if err != nil {
		return Settings{}, fmt.Errorf("the build's settings (%s): %w", path, err)
	}
	return told, nil
}

// told is what the file says, held to what a daemon can be told, with
// every path from a directory.
func (f file) told(dir string) (Settings, error) {
	from := func(path string) string {
		if path == "" || filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(dir, path)
	}
	if f.Version != version {
		return Settings{}, fmt.Errorf("version is %d, and this daemon reads version %d", f.Version, version)
	}
	if f.Plugins == "" {
		return Settings{}, errors.New("plugins names no directory: a daemon with no plugin builds nothing")
	}
	if f.Grid.Address == "" {
		return Settings{}, errors.New("grid.address is not said: a build's actions run on a grid")
	}
	plugins, err := Modules(nil, from(f.Plugins))
	if err != nil {
		return Settings{}, err
	}
	var rewrites []build.Rewrite
	for _, each := range f.Grid.Rewrites {
		rewrites = append(rewrites, build.Rewrite{From: each.From, To: each.To})
	}
	if err := Rewrites(rewrites); err != nil {
		return Settings{}, err
	}
	for i, name := range f.Publish.Environment {
		if !variable.MatchString(name) {
			return Settings{}, fmt.Errorf("publish.environment names %q, which is no variable's name", name)
		}
		if slices.Contains(f.Publish.Environment[:i], name) {
			return Settings{}, fmt.Errorf("publish.environment names %s twice", name)
		}
	}
	grid := build.Grid{Address: f.Grid.Address, Instance: f.Grid.Instance, Assets: f.Grid.Assets, Rewrites: rewrites,
		TLS: f.Grid.TLS || f.Grid.Authorities != "", Authorities: from(f.Grid.Authorities), Token: from(f.Grid.Token)}
	if err := grid.Check(); err != nil {
		return Settings{}, err
	}
	return Settings{Plugins: plugins, Grid: grid, Platform: Platform(f.Platform), Given: f.Publish.Environment}, nil
}

// variable is a variable's name.
var variable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Modules is the plugins given one by one, and after them every
// <name>.wasm of a directory of plugins, by name. A directory named that
// holds none is refused: a daemon with none of the plugins it was
// pointed at would refuse every repository later, and say less.
func Modules(plugins []string, directory string) ([]string, error) {
	if directory == "" {
		return plugins, nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("the directory of plugins: %w", err)
	}
	var found []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".wasm") {
			found = append(found, filepath.Join(directory, entry.Name()))
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("the directory of plugins, %s, holds no <name>.wasm", directory)
	}
	return append(plugins, found...), nil
}

// Rewrites holds which proxy stands in for each public site a download
// names to what a daemon can take: each a regular expression and the
// proxy's address for what it matches, no expression given twice. It is
// found when the daemon starts, and not at the first download.
func Rewrites(rewrites []build.Rewrite) error {
	for i, each := range rewrites {
		if each.From == "" || each.To == "" {
			return errors.New("a rewrite is a regular expression and the proxy's address for what it matches, and one of them is not said")
		}
		if _, err := regexp.Compile(each.From); err != nil {
			return fmt.Errorf("a rewrite: %w", err)
		}
		if slices.ContainsFunc(rewrites[:i], func(earlier build.Rewrite) bool { return earlier.From == each.From }) {
			return fmt.Errorf("a rewrite is given twice: %s", each.From)
		}
	}
	return nil
}

// Platform is the platform of the grid's workers as a daemon was told
// it, or, told none, Linux on this machine's architecture.
func Platform(told map[string]string) map[string]string {
	if len(told) > 0 {
		return told
	}
	isa := map[string]string{"arm64": "aarch64", "amd64": "x86-64"}[runtime.GOARCH]
	return map[string]string{"OSFamily": "linux", "ISA": isa}
}
