package config

import (
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/testservice"
	"go.yaml.in/yaml/v4"
)

// TestRuntime declares additional data for a Component's inferred tests.
// Inputs are relative to Directory, the catalog file's directory; references
// keep the catalog's existing scope. Commands and test selection belong to
// the plugin. Tools name the repository's mise pins, never versions.
type TestRuntime struct {
	// Repository is the repository it is written in; File the catalog
	// file, relative to it.
	Repository, File, Directory string
	Entity                      entity.Ref
	Inputs, Tools               []string
	Environment                 map[string]string
}

func readTestRuntime(file place, directory string, ref entity.Ref, node *yaml.Node) (TestRuntime, error) {
	wrong := func(line int, format string, args ...any) (TestRuntime, error) {
		return TestRuntime{}, file.at(line, format, args...)
	}
	if node.Kind != yaml.MappingNode {
		return wrong(node.Line, "spec.frontseat.test is a map of inputs, tools and environment")
	}
	if err := componentOnly(ref, node, "spec.frontseat.test"); err != nil {
		return TestRuntime{}, file.in(err)
	}
	out := TestRuntime{Repository: file.repository, File: file.file, Directory: directory, Entity: ref}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "inputs", "tools":
			read, err := valueOf(&pluginv1.OptionDeclaration{Name: "test." + key.Value, Kind: pluginv1.OptionDeclaration_KIND_STRING_LIST}, said)
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
				value, ok := text(content)
				if !ok || strings.ContainsRune(value, '\x00') {
					return wrong(content.Line, "test.environment.%s must be text without NUL", name.Value)
				}
				out.Environment[name.Value] = value
			}
		default:
			return TestRuntime{}, &model.Mistake{Repository: file.repository, File: file.file, Line: key.Line, Message: "spec.frontseat.test takes no key " + key.Value, Help: "it takes inputs, tools and environment"}
		}
	}
	return out, nil
}
