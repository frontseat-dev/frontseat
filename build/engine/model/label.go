package model

import (
	"fmt"
	"strings"
)

// Label is the address of one action in a workspace:
// "//<path>:<plugin>.<name>". The path is the entity's directory, with the
// repository's name first where the workspace holds more than one.
type Label struct {
	// Path is the directory of the entity that owns the action.
	Path string
	// Plugin is the plugin that declared it.
	Plugin string
	// Name is the action's name within its project.
	Name string
}

// String returns the label as it is written.
func (l Label) String() string {
	return "//" + l.Path + ":" + l.Plugin + "." + l.Name
}

// ParseLabel reads a label as String writes it.
func ParseLabel(s string) (Label, error) {
	rest, ok := strings.CutPrefix(s, "//")
	if !ok {
		return Label{}, fmt.Errorf("model: label %q does not start with //", s)
	}
	path, action, ok := strings.Cut(rest, ":")
	if !ok {
		return Label{}, fmt.Errorf("model: label %q names no action", s)
	}
	plugin, name, ok := strings.Cut(action, ".")
	if !ok || plugin == "" || name == "" {
		return Label{}, fmt.Errorf("model: label %q wants <plugin>.<name> after the colon", s)
	}
	return Label{Path: path, Plugin: plugin, Name: name}, nil
}
