// Package generated reads where this repository's generated code is
// written: the directory of each output of each Buf template of the
// contracts (api/buf.gen*.yaml). The gate holds what is there to what
// Buf writes now, and the architecture test holds generated files to
// being there alone.
package generated

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Contracts is the directory of the buf workspace, from the root.
const Contracts = "api"

// Templates are the Buf templates of the contracts, by their full path.
func Templates(root string) ([]string, error) {
	return filepath.Glob(filepath.Join(root, Contracts, "buf.gen*.yaml"))
}

// Outputs reads where a template writes, from the root: each plugin's
// out, which is written from the buf workspace's directory. One that
// leads out of the repository is refused.
func Outputs(template string) ([]string, error) {
	raw, err := os.ReadFile(template)
	if err != nil {
		return nil, err
	}
	var read struct {
		Plugins []struct {
			Out string `yaml:"out"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &read); err != nil {
		return nil, fmt.Errorf("%s: %w", template, err)
	}
	var outs []string
	for _, plugin := range read.Plugins {
		out := filepath.Clean(filepath.Join(Contracts, filepath.FromSlash(plugin.Out)))
		if plugin.Out == "" || filepath.IsAbs(plugin.Out) || out == "." || out == ".." || strings.HasPrefix(out, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s writes to %q, which is no directory of the repository", template, plugin.Out)
		}
		if !slices.Contains(outs, out) {
			outs = append(outs, out)
		}
	}
	return outs, nil
}

// All are the directories every template of the repository at a root
// writes, from the root.
func All(root string) ([]string, error) {
	templates, err := Templates(root)
	if err != nil {
		return nil, err
	}
	var all []string
	for _, template := range templates {
		outs, err := Outputs(template)
		if err != nil {
			return nil, err
		}
		all = append(all, outs...)
	}
	return all, nil
}
