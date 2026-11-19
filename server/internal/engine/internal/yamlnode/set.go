package yamlnode

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v4"
)

// SetStrings returns a YAML stream with the sequence of strings at a key
// path of one of its documents set to the values given, and with every
// other key, comment and document as it was: the entry that changed is
// written in place of its own lines, and every other byte of the file is
// kept (splice). A key of the path that is
// not there is made; with no values the key is taken out, unless the
// mapping inherits it through a merge, where it is set empty so that the
// inherited one does not show through. An entry that was there and is
// one of the values is kept as it was written, its comment and style
// with it. Aliases are resolved first, each in its place, so that
// changing a list changes nothing that shared its anchor. Nothing here
// knows what the keys mean: that is the plugin's, which chose them.
func SetStrings(raw []byte, document int, keys []string, values []string) ([]byte, error) {
	if len(raw) > MaxDocumentBytes {
		return nil, fmt.Errorf("the file exceeds %d bytes", MaxDocumentBytes)
	}
	if len(keys) == 0 {
		return nil, errors.New("an edit names no key")
	}
	documents, err := parse(raw)
	if err != nil {
		return nil, err
	}
	if document < 0 || document >= len(documents) {
		return nil, fmt.Errorf("the file has no document %d: it has %d", document, len(documents))
	}
	if err := Materialize(documents...); err != nil {
		return nil, err
	}
	root := documents[document]
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("document %d is not a mapping", document)
	}
	// Each mapping on the way is copied before it is changed: a mapping
	// an alias brought here is shared with where the anchor was.
	mappings := []*yaml.Node{copied(root.Content[0])}
	for _, key := range keys[:len(keys)-1] {
		parent := mappings[len(mappings)-1]
		child, err := ResolvedChild(parent, key)
		if err != nil {
			return nil, err
		}
		switch {
		case child == nil && len(values) == 0:
			// Nothing to take out: the file stands.
			return raw, nil
		case child == nil:
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		case child.Kind != yaml.MappingNode:
			return nil, fmt.Errorf("%s is not a mapping", key)
		default:
			child = copied(child)
		}
		mappings = append(mappings, child)
	}
	leaf := mappings[len(mappings)-1]
	last := keys[len(keys)-1]
	list, err := entries(leaf, last, values)
	if err != nil {
		return nil, err
	}
	if !put(leaf, last, list) {
		return raw, nil
	}
	// The copies take their places, leaf to root.
	for i := len(mappings) - 1; i > 0; i-- {
		link(mappings[i-1], keys[i-1], mappings[i])
	}
	root.Content[0] = mappings[0]
	if out, ok := splice(raw, document, keys, mappings, documents); ok {
		return out, nil
	}
	// What cannot be spliced in is written whole, in the frame the file
	// had: what came before its first document's marker, the marker, and
	// an end marker after the last (frame).
	before, after := frame(raw, documents)
	var out limitedBuffer
	if _, err := out.Write(before); err != nil {
		return nil, err
	}
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	for _, each := range documents {
		if err := encoder.Encode(each); err != nil {
			return nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	if _, err := out.Write(after); err != nil {
		return nil, err
	}
	written := out.Bytes()
	if lines := splitLines(raw); len(lines) > 0 && lines[0].crlf {
		// The library writes a line feed alone; a file of CRLF lines is
		// written in its own.
		written = bytes.ReplaceAll(bytes.ReplaceAll(written, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	}
	// What is written whole is held to the edit as what is spliced is:
	// the library's writer does not keep every value as it read it (a
	// kept block scalar may come back with a line more), and a file that
	// means something else is never written.
	if !sameMeaning(written, documents) {
		return nil, fmt.Errorf("%s cannot be written into this file without changing what else it says: write it by hand", strings.Join(keys, "."))
	}
	return written, nil
}

// copied is a shallow copy of a mapping: its own content slice, and no
// anchor, so that what shared the anchor is left as it was.
func copied(mapping *yaml.Node) *yaml.Node {
	copy := *mapping
	copy.Anchor = ""
	copy.Content = append([]*yaml.Node(nil), mapping.Content...)
	return &copy
}

// entries are the values as nodes: an entry that was written under the
// key and is one of the values is kept as written, the first time.
func entries(mapping *yaml.Node, key string, values []string) ([]*yaml.Node, error) {
	direct := Child(mapping, key)
	used := map[int]bool{}
	list := make([]*yaml.Node, 0, len(values))
	quoting := quotingOf(direct)
	for _, value := range values {
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: quoting}
		if direct != nil && direct.Kind == yaml.SequenceNode {
			for i, original := range direct.Content {
				if !used[i] && original.Kind == yaml.ScalarNode && original.Tag == "!!str" && original.Value == value {
					node, used[i] = original, true
					break
				}
			}
		}
		list = append(list, node)
	}
	return list, nil
}

// quotingOf is the quoting every string of a list was written with, where
// they agree on one, so that an entry added is written as they were.
func quotingOf(list *yaml.Node) yaml.Style {
	if list == nil || list.Kind != yaml.SequenceNode {
		return 0
	}
	quoting := yaml.Style(0)
	for i, entry := range list.Content {
		if entry.Kind != yaml.ScalarNode {
			return 0
		}
		style := entry.Style & (yaml.SingleQuotedStyle | yaml.DoubleQuotedStyle)
		if i > 0 && style != quoting {
			return 0
		}
		quoting = style
	}
	return quoting
}

// put sets the key of a mapping to the list, or takes it out where the
// list is empty and nothing is inherited; it reports whether anything
// changed.
func put(mapping *yaml.Node, key string, list []*yaml.Node) bool {
	inherited := false
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Tag == "!!merge" {
			inherited = true
		}
	}
	replacement := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: list}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if len(list) == 0 && !inherited {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
		if same(mapping.Content[i+1], list) {
			return false
		}
		// The list's own comments and its style, flow or block, stay
		// with it, and so does its tag where it was a list.
		was := mapping.Content[i+1]
		replacement.Style, replacement.HeadComment, replacement.LineComment, replacement.FootComment = was.Style, was.HeadComment, was.LineComment, was.FootComment
		if was.Kind == yaml.SequenceNode {
			replacement.Tag = was.Tag
		} else {
			replacement.Style = 0
		}
		mapping.Content[i+1] = replacement
		placed(mapping, i)
		return true
	}
	if len(list) == 0 && !inherited {
		return false
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, replacement)
	return true
}

// placed puts the comment on the line of a mapping's i-th key where the
// writer keeps it: on the value where the value is written on the key's
// line (a scalar, a flow or an empty collection), on the key where the
// value is written beneath it. The library writes a key's line comment
// before a value of the first kind, and drops a value's before one of the
// second, so that an emptied mapping came out as `spec: # c` and `{}` on
// a line of its own, which reads as nothing.
func placed(mapping *yaml.Node, i int) {
	key, value := mapping.Content[i], mapping.Content[i+1]
	inline := value.Kind == yaml.ScalarNode || value.Kind == yaml.AliasNode || value.Style&yaml.FlowStyle != 0 || len(value.Content) == 0
	switch {
	case inline && key.LineComment != "" && value.LineComment == "":
		k, v := *key, *value
		v.LineComment, k.LineComment = k.LineComment, ""
		mapping.Content[i], mapping.Content[i+1] = &k, &v
	case !inline && value.LineComment != "" && key.LineComment == "":
		k, v := *key, *value
		k.LineComment, v.LineComment = v.LineComment, ""
		mapping.Content[i], mapping.Content[i+1] = &k, &v
	}
}

// same reports whether a node is a sequence of exactly these nodes.
func same(node *yaml.Node, list []*yaml.Node) bool {
	if node.Kind != yaml.SequenceNode || len(node.Content) != len(list) {
		return false
	}
	for i := range list {
		if node.Content[i] != list[i] {
			return false
		}
	}
	return true
}

// link sets the key of a mapping to the child, where the key is.
func link(mapping *yaml.Node, key string, child *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = child
			placed(mapping, i)
			return
		}
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
}

// parse reads the documents of a stream.
func parse(raw []byte) ([]*yaml.Node, error) {
	var documents []*yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		document := &yaml.Node{}
		err := decoder.Decode(document)
		if errors.Is(err, io.EOF) {
			return documents, nil
		}
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
}

// limitedBuffer holds what is written back to the bound a document is
// read to: an alias must not make a small file an unbounded one.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxDocumentBytes-b.Len() {
		return 0, fmt.Errorf("the rewritten file exceeds %d bytes", MaxDocumentBytes)
	}
	return b.Buffer.Write(p)
}
