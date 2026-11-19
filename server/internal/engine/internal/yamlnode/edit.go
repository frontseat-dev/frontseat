package yamlnode

import (
	"fmt"
	"go.yaml.in/yaml/v4"
)

// MaxDocumentBytes bounds host configuration, catalog documents and rewrites,
// matching the SDK's manifest limit.
const MaxDocumentBytes = 4 * 1024 * 1024

// ResolvedChild asks the YAML decoder to resolve a mapping's aliases and merge
// keys. It returns a node copy, so replacing it cannot change a merged parent.
func ResolvedChild(node *yaml.Node, key string) (*yaml.Node, error) {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return nil, err
	}
	value, found := fields[key]
	if !found {
		return nil, nil
	}
	return &value, nil
}

// Materialize copies the parser's resolved syntax tree before a writer edits
// anchor definitions. Alias targets come from the library's Node.Alias links;
// scalar text, tags and style are never decoded through another YAML schema.
// The bounded copy refuses cycles and removes anchors after copying every use.
// The caller must also bound serialized output.
func Materialize(roots ...*yaml.Node) error {
	remaining := 100_000
	remainingBytes := MaxDocumentBytes
	active := map[*yaml.Node]bool{}
	var copyNode func(*yaml.Node, int) (*yaml.Node, error)
	copyNode = func(node *yaml.Node, depth int) (*yaml.Node, error) {
		remaining--
		if remaining < 0 || depth > 64 {
			return nil, fmt.Errorf("YAML rewrite exceeds node or nesting limit")
		}
		if node == nil || active[node] {
			return nil, fmt.Errorf("YAML rewrite contains a cyclic or unresolved alias")
		}
		remainingBytes -= len(node.Value)
		if remainingBytes < 0 {
			return nil, fmt.Errorf("YAML rewrite exceeds expanded scalar byte limit")
		}
		active[node] = true
		defer delete(active, node)
		if node.Kind == yaml.AliasNode {
			copied, err := copyNode(node.Alias, depth)
			if err != nil {
				return nil, err
			}
			if node.HeadComment != "" {
				copied.HeadComment = node.HeadComment
			}
			if node.LineComment != "" {
				copied.LineComment = node.LineComment
			}
			if node.FootComment != "" {
				copied.FootComment = node.FootComment
			}
			return copied, nil
		}
		if len(node.Content) > remaining {
			return nil, fmt.Errorf("YAML rewrite exceeds node limit")
		}
		copied := *node
		copied.Anchor, copied.Alias = "", nil
		copied.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			var err error
			copied.Content[i], err = copyNode(child, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return &copied, nil
	}
	copied := make([]*yaml.Node, 0, len(roots))
	for _, root := range roots {
		copy, err := copyNode(root, 0)
		if err != nil {
			return err
		}
		copied = append(copied, copy)
	}
	for i, root := range roots {
		*root = *copied[i]
	}
	return nil
}
