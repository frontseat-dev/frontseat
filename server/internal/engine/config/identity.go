package config

import (
	"fmt"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"

	"github.com/frontseat-dev/frontseat/server/internal/engine/internal/yamlnode"
)

// spelled is each kind Backstage defines, by its reference's kind, as
// Backstage spells it: its schema of a Component takes "Component" and
// nothing else, and so does the Backstage plugin.
var spelled = map[string]string{
	"component": "Component",
	"api":       "API",
	"resource":  "Resource",
	"system":    "System",
	"domain":    "Domain",
	"group":     "Group",
	"user":      "User",
	"location":  "Location",
}

// entityOf is the entity a catalog document that says spec.frontseat
// is: the one reading of its kind, name and namespace, for every key of
// spec.frontseat. Each is text; a kind Backstage defines is spelled as
// Backstage spells it; and the reference is read by entity.New, which
// lowers ASCII alone, so that no name is folded into another by
// Unicode's rules. A refusal says the line and nothing of what was
// written.
func entityOf(document, frontseat *yaml.Node) (entity.Ref, error) {
	metadata := yamlnode.Child(document, "metadata")
	kind, name, namespace := yamlnode.Child(document, "kind"), yamlnode.Child(metadata, "name"), yamlnode.Child(metadata, "namespace")
	written := func(node *yaml.Node) (string, bool) {
		if node == nil {
			return "", false
		}
		return text(node)
	}
	k, hasKind := written(kind)
	n, hasName := written(name)
	space, hasSpace := written(namespace)
	if !hasKind || !hasName || namespace != nil && !hasSpace {
		return entity.Ref{}, fmt.Errorf("line %d: spec.frontseat is said by an entity, which has a kind and a name, each text, and a namespace, where it says one, of text", frontseat.Line)
	}
	ref := entity.New(k, space, n)
	if want, defined := spelled[ref.Kind]; defined && k != want {
		return entity.Ref{}, fmt.Errorf("line %d: kind is written as Backstage writes it: %s", kind.Line, want)
	}
	if ref.Validate() != nil {
		return entity.Ref{}, fmt.Errorf("line %d: spec.frontseat is said by an entity whose reference is none: a kind, namespace and name are each of letters and digits, with dashes, dots or underscores between them", frontseat.Line)
	}
	return ref, nil
}

// componentOnly refuses a key of spec.frontseat that only a Component
// says, where the entity is another.
func componentOnly(ref entity.Ref, node *yaml.Node, key string) error {
	if ref.Kind != entity.KindComponent {
		return fmt.Errorf("line %d: %s belongs to a Component", node.Line, key)
	}
	return nil
}
