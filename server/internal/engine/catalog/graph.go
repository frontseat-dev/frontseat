package catalog

import (
	"fmt"
	"path"
	"slices"
	"sort"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// Descriptor binds a plugin's catalog answer to the repository it read:
// which plugin answered, for which analyzed root and with which context,
// so that the same plugin can be asked what the file should say.
type Descriptor struct {
	Entity
	Relations []DeclaredRelation
	Plugin    string
	Root      string
	Context   *pluginv1.Context
}

// Entity is a catalog identity and its source holdings. Organizational entities
// may have a descriptor without governing any source directory.
type Entity struct {
	Ref                  entity.Ref
	Repository, Manifest string
	Roots                []string
}

// Node adds the resolved outgoing relations to an entity.
type Node struct {
	Entity
	Relations []Relation
}

// DeclaredRelation is a catalog fact and the name of its inverse.
type DeclaredRelation struct {
	Type, InverseType string
	Target            entity.Ref
}

// Conflict reports a catalog identity or source-ownership ambiguity. Protocol
// status and diagnostic conversion belong to the caller's wire boundary.
type Conflict struct {
	Repository, Manifest, Message string
	Duplicate                     bool
}

func (e *Conflict) Error() string { return e.Message }

// Relation is an outgoing catalog fact, including a reference outside the workspace.
type Relation struct {
	Type          string
	Target        entity.Ref
	TargetPresent bool
}

// Graph retains descriptors and the workspace's already located entities.
type Graph struct {
	nodes    map[entity.Ref]*Node
	declared []Descriptor
	ordered  []*Node
}

// Assemble checks uniqueness before ownership is used to select or order work.
func Assemble(known []Entity, declared []Descriptor) (*Graph, error) {
	g := &Graph{nodes: map[entity.Ref]*Node{}, declared: declared}
	occupied := map[string]entity.Ref{}
	add := func(ref entity.Ref, repository string, roots []string) error {
		node := g.nodes[ref]
		if node == nil {
			node = &Node{Entity: Entity{Ref: ref, Repository: repository}}
			g.nodes[ref] = node
			g.ordered = append(g.ordered, node)
		}
		if node.Repository != repository {
			return fmt.Errorf("entity %s is located in both %s and %s", ref, node.Repository, repository)
		}
		for _, root := range roots {
			at := path.Clean(path.Join(repository, root))
			if other, found := occupied[at]; found && other != ref {
				return fmt.Errorf("entities %s and %s both govern %s", other, ref, at)
			}
			occupied[at] = ref
			if !slices.Contains(node.Roots, root) {
				node.Roots = append(node.Roots, root)
			}
		}
		return nil
	}
	for _, located := range known {
		if err := add(located.Ref, located.Repository, located.Roots); err != nil {
			return nil, &Conflict{Repository: located.Repository, Message: err.Error()}
		}
	}
	for _, descriptor := range declared {
		e := descriptor.Entity
		ref := e.Ref
		if previous := g.nodes[ref]; previous != nil && previous.Manifest != "" {
			return nil, &Conflict{Repository: e.Repository, Manifest: e.Manifest, Duplicate: true, Message: fmt.Sprintf("duplicate entity %s, also described in %s", ref, path.Join(previous.Repository, previous.Manifest))}
		}
		if err := add(ref, e.Repository, e.Roots); err != nil {
			return nil, &Conflict{Repository: e.Repository, Manifest: e.Manifest, Message: err.Error()}
		}
		g.nodes[ref].Manifest = e.Manifest
	}
	return g, nil
}

// Entities returns source holdings without resolving catalog relations. Callers
// that plan builds need these roots, not the rendered catalog graph.
func (g *Graph) Entities() []Entity {
	result := make([]Entity, 0, len(g.ordered))
	for _, node := range g.ordered {
		result = append(result, node.Entity)
	}
	return result
}

// Nodes adds inferred component dependencies and inverse relations, then returns
// a deterministic catalog view. Declared component dependency pairs cannot alter
// what source inference says, in either direction. Catalog cycles are permitted.
func (g *Graph) Nodes(dependencies map[entity.Ref][]entity.Ref) []Node {
	edges := map[entity.Ref]map[Relation]bool{}
	emit := func(source entity.Ref, kind string, target entity.Ref) {
		if g.nodes[source] == nil {
			return
		}
		if edges[source] == nil {
			edges[source] = map[Relation]bool{}
		}
		edges[source][Relation{Type: kind, Target: target, TargetPresent: g.nodes[target] != nil}] = true
	}
	for _, descriptor := range g.declared {
		source := descriptor.Ref
		for _, relation := range descriptor.Relations {
			target := relation.Target
			if source.Kind == entity.KindComponent && target.Kind == entity.KindComponent &&
				(relation.Type == "dependsOn" || relation.Type == "dependencyOf" || relation.InverseType == "dependsOn" || relation.InverseType == "dependencyOf") {
				continue
			}
			emit(source, relation.Type, target)
			emit(target, relation.InverseType, source)
		}
	}
	for source, targets := range dependencies {
		for _, target := range targets {
			emit(source, "dependsOn", target)
			emit(target, "dependencyOf", source)
		}
	}
	nodes := make([]Node, 0, len(g.nodes))
	for ref, original := range g.nodes {
		node := *original
		node.Roots = slices.Clone(original.Roots)
		sort.Strings(node.Roots)
		for relation := range edges[ref] {
			node.Relations = append(node.Relations, relation)
		}
		sort.Slice(node.Relations, func(i, j int) bool {
			a, b := node.Relations[i], node.Relations[j]
			if a.Type != b.Type {
				return a.Type < b.Type
			}
			return a.Target.String() < b.Target.String()
		})
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Ref.String() < nodes[j].Ref.String() })
	return nodes
}
