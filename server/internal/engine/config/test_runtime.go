package config

import (
	"fmt"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/internal/yamlnode"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/testservice"
	"go.yaml.in/yaml/v4"
)

// TestRuntime declares additional data for a Component's inferred tests.
// Inputs are relative to Directory, the catalog file's directory; references
// keep the catalog's existing scope. Commands and test selection belong to
// the adapter. Tools name the repository's mise pins, never versions.
type TestRuntime struct {
	File, Directory string
	Entity          entity.Ref
	Inputs, Tools   []string
	Environment     map[string]string
}

func readTestRuntime(file, directory string, document, node *yaml.Node) (TestRuntime, error) {
	wrong := func(line int, format string, args ...any) (TestRuntime, error) {
		return TestRuntime{}, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: ", line) + fmt.Sprintf(format, args...)}
	}
	if node.Kind != yaml.MappingNode {
		return wrong(node.Line, "spec.frontseat.test is a map of inputs, tools and environment")
	}
	ref, err := componentOf(document, node, "spec.frontseat.test")
	if err != nil {
		return TestRuntime{}, &model.Mistake{File: file, Message: err.Error()}
	}
	out := TestRuntime{File: file, Directory: directory, Entity: ref}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "inputs", "tools":
			read, err := valueOf(&pluginv1.Option{Name: "test." + key.Value, Kind: pluginv1.Option_KIND_STRING_LIST}, said)
			if err != nil {
				return wrong(said.Line, "%v", err)
			}
			items := read.GetStringListValue().GetValues()
			for _, item := range items {
				if item == "" || strings.ContainsRune(item, '\x00') {
					return wrong(said.Line, "test.%s entries must be nonempty text without NUL", key.Value)
				}
			}
			if key.Value == "inputs" {
				out.Inputs = items
			} else {
				out.Tools = items
			}
		case "environment":
			if said.Kind != yaml.MappingNode {
				return wrong(said.Line, "test.environment is a map of names to text values")
			}
			out.Environment = make(map[string]string, len(said.Content)/2)
			for j := 0; j+1 < len(said.Content); j += 2 {
				name, content := said.Content[j], said.Content[j+1]
				if name.ShortTag() != "!!str" || !testservice.Exportable(name.Value) {
					return wrong(name.Line, "test.environment contains an invalid variable name")
				}
				if content.Kind != yaml.ScalarNode || content.ShortTag() != "!!str" || strings.ContainsRune(content.Value, '\x00') {
					return wrong(content.Line, "test.environment.%s must be text without NUL", name.Value)
				}
				out.Environment[name.Value] = content.Value
			}
		default:
			return wrong(key.Line, "spec.frontseat.test takes no key %s; it takes inputs, tools and environment", key.Value)
		}
	}
	return out, nil
}

// componentOf is the Component a catalog document is, for what is said of
// it under a key that only a Component says: refused where the document
// is no Component or names none.
func componentOf(document, node *yaml.Node, key string) (entity.Ref, error) {
	kind := yamlnode.Child(document, "kind")
	metadata := yamlnode.Child(document, "metadata")
	name, namespace := yamlnode.Child(metadata, "name"), yamlnode.Child(metadata, "namespace")
	if kind == nil || kind.ShortTag() != "!!str" || kind.Value != "Component" {
		return entity.Ref{}, fmt.Errorf("line %d: %s belongs to a Component", node.Line, key)
	}
	if name == nil || name.ShortTag() != "!!str" || namespace != nil && namespace.ShortTag() != "!!str" {
		return entity.Ref{}, fmt.Errorf("line %d: %s requires a Component with a text name and namespace", node.Line, key)
	}
	space := ""
	if namespace != nil {
		space = namespace.Value
	}
	ref := entity.New(kind.Value, space, name.Value)
	if err := ref.Validate(); err != nil {
		return entity.Ref{}, fmt.Errorf("line %d: %s: %v", node.Line, key, err)
	}
	return ref, nil
}
