package config

import (
	"fmt"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/testservice"
	"go.yaml.in/yaml/v4"
)

// TestRuntime declares additional data for a Component's inferred tests.
// Inputs are patterns relative to Directory, the catalog file's
// directory, or "/…" from the repository's root; Needs are what other
// actions left, ":what" or "//path:what", in the catalog's scope. Commands and test selection belong to
// the plugin. Tools name the repository's mise pins, never versions.
type TestRuntime struct {
	File, Directory string
	Entity          entity.Ref
	Inputs, Needs   []string
	Tools           []string
	Environment     map[string]string
}

func readTestRuntime(file, directory string, ref entity.Ref, node *yaml.Node) (TestRuntime, error) {
	wrong := func(line int, format string, args ...any) (TestRuntime, error) {
		return TestRuntime{}, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: ", line) + fmt.Sprintf(format, args...)}
	}
	if node.Kind != yaml.MappingNode {
		return wrong(node.Line, "spec.frontseat.test is a map of inputs, needs, tools and environment")
	}
	if err := componentOnly(ref, node, "spec.frontseat.test"); err != nil {
		return TestRuntime{}, &model.Mistake{File: file, Message: err.Error()}
	}
	out := TestRuntime{File: file, Directory: directory, Entity: ref}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		var err error
		switch key.Value {
		case "inputs":
			out.Inputs, err = testList("test.inputs", said)
		case "needs":
			out.Needs, err = testList("test.needs", said)
		case "tools":
			out.Tools, err = testList("test.tools", said)
		case "environment":
			out.Environment, err = testEnvironment("test.environment", said)
		default:
			return wrong(key.Line, "spec.frontseat.test takes no key %s; it takes inputs, needs, tools and environment", key.Value)
		}
		if err != nil {
			return TestRuntime{}, &model.Mistake{File: file, Message: err.Error()}
		}
	}
	return out, nil
}

// testList reads a list a test is given, written under at: text, each
// entry something and no NUL.
func testList(at string, node *yaml.Node) ([]string, error) {
	read, err := valueOf(&pluginv1.Option{Name: at, Kind: pluginv1.Option_KIND_STRING_LIST}, node)
	if err != nil {
		return nil, fmt.Errorf("line %d: %v", node.Line, err)
	}
	items := read.GetStringListValue().GetValues()
	for _, item := range items {
		if item == "" || strings.ContainsRune(item, '\x00') {
			return nil, fmt.Errorf("line %d: %s entries must be nonempty text without NUL", node.Line, at)
		}
	}
	return items, nil
}

// testEnvironment reads the variables a test is started with, written
// under at: names a shell can export, each with its value as text.
func testEnvironment(at string, node *yaml.Node) (map[string]string, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: %s is a map of names to text values", node.Line, at)
	}
	out := make(map[string]string, len(node.Content)/2)
	for j := 0; j+1 < len(node.Content); j += 2 {
		name, content := node.Content[j], node.Content[j+1]
		if name.ShortTag() != "!!str" || !testservice.Exportable(name.Value) {
			return nil, fmt.Errorf("line %d: %s contains an invalid variable name", name.Line, at)
		}
		value, ok := text(content)
		if !ok || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("line %d: %s.%s must be text without NUL", content.Line, at, name.Value)
		}
		out[name.Value] = value
	}
	return out, nil
}
