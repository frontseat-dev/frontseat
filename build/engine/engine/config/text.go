package config

import "go.yaml.in/yaml/v4"

// What a file writes as text is read as text and as nothing else: a
// number, a switch or a null written where text is asked is refused by
// whoever asked, never turned into its letters. Every key of
// frontseat.yaml and of spec.frontseat is read through these.

// text is a node read as text.
func text(node *yaml.Node) (string, bool) {
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!str" {
		return "", false
	}
	return node.Value, true
}

// whole is a node read as a whole number a field of so many bits holds.
func whole[T int32 | int64](node *yaml.Node) (T, bool) {
	var number T
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!int" || node.Decode(&number) != nil {
		return 0, false
	}
	return number, true
}

// textList is a node read as a list of text. What is wrong is the node
// that is: the list itself where it is none, or its first entry that is
// no text; nil where nothing is.
func textList(node *yaml.Node) ([]string, *yaml.Node) {
	if node.Kind != yaml.SequenceNode {
		return nil, node
	}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		value, ok := text(item)
		if !ok {
			return nil, item
		}
		out = append(out, value)
	}
	return out, nil
}

// mapOf is a node read as a map of text to what read reads each value
// as; false where it is no map, a key is no text or a value is not read.
func mapOf[V any](node *yaml.Node, read func(*yaml.Node) (V, bool)) (map[string]V, bool) {
	if node.Kind != yaml.MappingNode {
		return nil, false
	}
	out := make(map[string]V, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, ok := text(node.Content[i])
		if !ok {
			return nil, false
		}
		if out[name], ok = read(node.Content[i+1]); !ok {
			return nil, false
		}
	}
	return out, true
}
