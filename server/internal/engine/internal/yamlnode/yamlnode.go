// Package yamlnode finds its way in a YAML document read as nodes, which
// is how a file is read where what it says is tied to the line that
// says it, and written back as it was.
package yamlnode

import "go.yaml.in/yaml/v4"

// Child returns the value of a key of a mapping, or nil where the node
// is no mapping or holds no such key.
func Child(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
