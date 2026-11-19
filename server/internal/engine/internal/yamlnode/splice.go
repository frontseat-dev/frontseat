package yamlnode

import (
	"bytes"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v4"
)

// splice writes a changed entry over its own lines of the file it was read
// from and keeps every other byte: document markers, comments and blank
// lines, the other documents, and the layout of the rest of the document,
// none of which the YAML library's writer keeps (it writes a stream anew,
// with no marker before its first document and its own indentation).
//
// The entry rewritten is the deepest of the key path the file spells out
// as block mappings with no anchor; where the path goes on through a flow
// mapping, an alias or a merge, the entry of the last mapping written out
// is rewritten whole, from the resolved values. The entry's lines run from
// its key to the line before what follows it in the document (its next
// sibling, an ancestor's, a document marker or the end of the file), less
// the blank and comment lines at their end, which belong to what follows.
// The library writes the entry alone, in the indentation the file uses,
// and it is indented to where its key stood.
//
// Nothing is spliced where the file does not allow it to be done soundly,
// and the caller then writes the stream whole: an anchor in what would be
// rewritten (what aliases it elsewhere would lose it), a literal or folded
// scalar at the end of what would be rewritten (its last lines may read as
// comments), a document that is not a block mapping, a key that does not
// begin its line. What is spliced
// is read back, and must say what the edited documents say, or it is not
// used: the edit is proven by the library's own reader, not by the lines
// being found right.
func splice(raw []byte, document int, keys []string, changed []*yaml.Node, documents []*yaml.Node) ([]byte, bool) {
	originals, err := parse(raw)
	if err != nil || document >= len(originals) || len(originals[document].Content) == 0 {
		return nil, false
	}
	doc := originals[document]
	path := []*yaml.Node{doc.Content[0]}
	if !blockMapping(path[0]) {
		return nil, false
	}
	// How deep the file spells the path out: changed[d] is the new mapping
	// that path[d] was.
	depth := 0
	for depth < len(keys)-1 {
		child := Child(path[depth], keys[depth])
		if !blockMapping(child) {
			break
		}
		path = append(path, child)
		depth++
	}
	// The value as its mapping now holds it, with the comment of its
	// key's line where the writer keeps it (placed).
	value := Child(changed[depth], keys[depth])
	if value == nil && len(changed[depth].Content) == 0 {
		// Taking out a mapping's only entry leaves the mapping empty,
		// which its own lines cannot say: its parent's entry is
		// rewritten instead.
		if depth == 0 {
			return nil, false
		}
		depth--
		value = Child(changed[depth], keys[depth])
	}
	mapping := path[depth]
	lines := splitLines(raw)
	limit := endOfDocument(lines, doc)
	var key, was *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == keys[depth] && mapping.Content[i].Kind == yaml.ScalarNode {
			key, was = mapping.Content[i], mapping.Content[i+1]
		}
	}
	var start, end, column int
	var first bool
	switch {
	case key != nil:
		if anchored(key) || anchored(was) || blockScalar(was) || severalLines(lines, was) {
			return nil, false
		}
		var ok bool
		if start, ok = offset(lines, key.Line, key.Column); !ok {
			return nil, false
		}
		end = entryEnd(raw, lines, doc, key, was, limit)
		column, first = key.Column, false
		if value == nil {
			// What is taken out goes with its whole lines, and the
			// comment above it that is its own.
			start = lines[headOf(lines, key)-1].at
		}
	case value == nil:
		return nil, false
	default:
		// A new entry goes after the mapping's last.
		last := len(mapping.Content) - 2
		if last < 0 || blockScalar(mapping.Content[last+1]) || severalLines(lines, mapping.Content[last+1]) {
			return nil, false
		}
		end = entryEnd(raw, lines, doc, mapping.Content[last], mapping.Content[last+1], limit)
		// It begins a line of its own, so its first line stands in to
		// the mapping's column as the rest do.
		start, column, first = end, mapping.Column, true
		if _, ok := offset(lines, mapping.Content[last].Line, column); !ok {
			return nil, false
		}
	}
	var written []byte
	if value != nil {
		var name *yaml.Node
		for i := 0; i+1 < len(changed[depth].Content); i += 2 {
			if changed[depth].Content[i].Value == keys[depth] {
				copy := *changed[depth].Content[i]
				name = &copy
			}
		}
		if name == nil {
			name = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: keys[depth]}
		}
		// The comments above the key and after the entry are the file's
		// own, kept where they are.
		name.HeadComment, name.FootComment = "", ""
		indent, compact := layout(lines, doc, key, was, depth == len(keys)-1)
		var ok bool
		if written, ok = render(name, value, indent, compact, column, first, lineEnding(lines, start)); !ok {
			return nil, false
		}
		if start == len(raw) && raw[len(raw)-1] != '\n' {
			// The file ends with no line break, and the entry goes after.
			written = append([]byte(lineEnding(lines, start)), written...)
		}
	}
	out := make([]byte, 0, len(raw)-(end-start)+len(written))
	out = append(append(append(out, raw[:start]...), written...), raw[end:]...)
	// Every byte but the entry's is the file's own.
	if !bytes.Equal(out[:start], raw[:start]) || !bytes.Equal(out[len(out)-(len(raw)-end):], raw[end:]) {
		return nil, false
	}
	if len(out) > MaxDocumentBytes || !sameMeaning(out, documents) {
		return nil, false
	}
	return out, true
}

// blockMapping reports whether a node is a mapping written out in block
// style, with no anchor that something else may alias.
func blockMapping(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.MappingNode && node.Style&yaml.FlowStyle == 0 && node.Anchor == ""
}

// anchored reports whether a node or anything in it carries an anchor.
func anchored(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Anchor != "" {
		return true
	}
	for _, child := range node.Content {
		if anchored(child) {
			return true
		}
	}
	return false
}

// blockScalar reports whether a node or anything in it is a literal or a
// folded scalar, whose last lines may read as comments or be blank, so
// that where it ends cannot be told by them.
func blockScalar(node *yaml.Node) bool {
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return true
	}
	for _, child := range node.Content {
		if blockScalar(child) {
			return true
		}
	}
	return false
}

// line is one line of the file: where it starts, and its text with no
// line break.
type line struct {
	at   int
	text string
	crlf bool
}

func splitLines(raw []byte) []line {
	var lines []line
	for at := 0; at < len(raw); {
		next := bytes.IndexByte(raw[at:], '\n')
		text := raw[at:]
		if next >= 0 {
			text = raw[at : at+next]
		}
		l := line{at: at, text: string(text)}
		if strings.HasSuffix(l.text, "\r") {
			l.text, l.crlf = l.text[:len(l.text)-1], true
		}
		lines = append(lines, l)
		if next < 0 {
			break
		}
		at += next + 1
	}
	return lines
}

// offset is where a node at a line and column of the file starts, where
// only spaces come before it on its line.
func offset(lines []line, row, column int) (int, bool) {
	if row < 1 || row > len(lines) || column < 1 {
		return 0, false
	}
	l := lines[row-1]
	prefix := strings.TrimPrefix(l.text, "\ufeff")
	skipped := len(l.text) - len(prefix)
	if column-1 > len(prefix) || strings.TrimLeft(prefix[:column-1], " ") != "" {
		return 0, false
	}
	return l.at + skipped + column - 1, true
}

// marker reports whether a line is a document marker, `---` or `...`,
// which YAML forbids as the start of a line of any content.
func marker(text string) bool {
	if !strings.HasPrefix(text, "---") && !strings.HasPrefix(text, "...") {
		return false
	}
	return len(text) == 3 || text[3] == ' ' || text[3] == '\t'
}

// endOfDocument is the first line after the document's content that ends
// it: the next marker, or one past the last line.
func endOfDocument(lines []line, doc *yaml.Node) int {
	first := doc.Content[0].Line
	for i := first; i < len(lines); i++ {
		if marker(lines[i].text) {
			return i + 1
		}
	}
	return len(lines) + 1
}

// entryEnd is the offset just after an entry's last line: the line before
// whatever of the document comes next after it, less the blank and comment
// lines that come before that.
func entryEnd(raw []byte, lines []line, doc *yaml.Node, key, value *yaml.Node, limit int) int {
	inside := map[*yaml.Node]bool{}
	var mark func(*yaml.Node)
	mark = func(node *yaml.Node) {
		inside[node] = true
		for _, child := range node.Content {
			mark(child)
		}
	}
	mark(key)
	mark(value)
	next := limit
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if !inside[node] && node.Kind != yaml.DocumentNode &&
			(node.Line > key.Line || node.Line == key.Line && node.Column > key.Column) && node.Line < next {
			next = node.Line
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(doc)
	last := next - 1
	for last > key.Line {
		text := strings.TrimLeft(lines[last-1].text, " \t")
		if text != "" && !strings.HasPrefix(text, "#") {
			break
		}
		last--
	}
	if last >= len(lines) {
		return len(raw)
	}
	return lines[last].at
}

// layout is the indentation the file writes in: how far a mapping's
// entries stand in from their key, and whether a sequence's dashes stand
// at its key's column. What was at the entry rewritten says it first,
// then the first of the document that does.
func layout(lines []line, doc *yaml.Node, key, was *yaml.Node, list bool) (int, bool) {
	mapIndent, seqIndent, compact, seenSeq := 0, 0, false, false
	consider := func(k, v *yaml.Node) {
		if k == nil || v == nil || v.Style&yaml.FlowStyle != 0 || len(v.Content) == 0 {
			return
		}
		// Where a value begins on its key's line (after a tag), its first
		// entry says where it stands.
		line, column := v.Line, v.Column
		if line <= k.Line {
			line, column = v.Content[0].Line, v.Content[0].Column
			if v.Kind == yaml.SequenceNode {
				column = dashOf(lines, v.Content[0])
			}
		}
		if line <= k.Line || column < 1 {
			return
		}
		switch v.Kind {
		case yaml.MappingNode:
			if mapIndent == 0 {
				mapIndent = column - k.Column
			}
		case yaml.SequenceNode:
			if !seenSeq {
				seenSeq, compact, seqIndent = true, column == k.Column, column-k.Column
			}
		}
	}
	consider(key, was)
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				consider(node.Content[i], node.Content[i+1])
			}
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(doc)
	indent := mapIndent
	switch {
	case seenSeq && !compact && (list || indent == 0):
		indent = seqIndent
	case compact && list:
		// The library puts a compact list's dashes two in from the
		// indentation it is given: at its key's column, for a list, at two.
		indent = 2
	}
	if indent < 2 || indent > 9 {
		indent = 2
	}
	return indent, compact
}

// render writes one entry with the library, every line after the first
// indented to the key's column, and the first too where it begins a line
// of its own.
func render(key, value *yaml.Node, indent int, compact bool, column int, first bool, newline string) ([]byte, bool) {
	var out bytes.Buffer
	dumper, err := yaml.NewDumper(&out, yaml.WithV3Defaults(), yaml.WithIndent(indent), yaml.WithCompactSeqIndent(compact), yaml.WithLineWidth(-1))
	if err != nil {
		return nil, false
	}
	// The comments after the entry are the file's, kept where they are:
	// the writer is given none to write after it.
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{key, untailed(value)}}
	if err := dumper.Dump(entry); err != nil {
		return nil, false
	}
	if err := dumper.Close(); err != nil {
		return nil, false
	}
	written := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	pad := strings.Repeat(" ", column-1)
	var text strings.Builder
	for i, each := range written {
		if (i > 0 || first) && each != "" {
			text.WriteString(pad)
		}
		text.WriteString(each)
		text.WriteString(newline)
	}
	return []byte(text.String()), true
}

// sameMeaning reports whether a stream reads as the documents do: node
// by node, each of the same kind, tag and value, and a mapping's keys in
// the same order, aliases resolved on both sides; and, as a reader into Go
// values takes them, equal. Comments and styles are not compared: they
// are how a thing is written, not what it is.
func sameMeaning(raw []byte, documents []*yaml.Node) bool {
	read, err := parse(raw)
	if err != nil || len(read) != len(documents) || Materialize(read...) != nil {
		return false
	}
	for i := range read {
		var got, want any
		if !sameNode(read[i], documents[i]) || read[i].Decode(&got) != nil || documents[i].Decode(&want) != nil || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}

// tagOf is a node's tag, the merge key's taken as the reader gives it
// when the key is read back: it reads `<<` before an alias as a string
// and before a mapping as a merge, though the two mean one thing.
func tagOf(node *yaml.Node) string {
	if node.Kind == yaml.ScalarNode && node.Value == "<<" && node.Style == 0 {
		return "!!merge"
	}
	return node.ShortTag()
}

// sameTag reports whether two nodes are of one tag, where each says one:
// the reader leaves some a node it was not asked to decode without one
// (scalars of a flow mapping an alias names), whose value the reader
// into Go values is held to instead.
func sameTag(a, b *yaml.Node) bool {
	return a.Tag == "" || b.Tag == "" || tagOf(a) == tagOf(b)
}

// sameNode reports whether two nodes are of one kind, tag and value, with
// the same nodes in them in the same order.
func sameNode(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || !sameTag(a, b) || len(a.Content) != len(b.Content) {
		return false
	}
	if a.Kind == yaml.ScalarNode && a.Value != b.Value {
		return false
	}
	for i := range a.Content {
		if !sameNode(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// frame is what a stream written whole by the library is to be put
// between, so that it keeps the frame the file had: the library writes no
// marker before its first document and no end marker after its last.
// Before is the file up to and with the line that starts its first
// document, where a bare `---` (with no more than a comment after it)
// starts it after nothing but comments, blank lines and directives; the
// comments there, which the reader gave the document's first key, are
// taken from it, so that they are written once. After is an end marker,
// where the file's last line that is not blank is one. Where the file has
// neither, both are empty, and nothing is added.
func frame(raw []byte, documents []*yaml.Node) (before, after []byte) {
	lines := splitLines(raw)
	lineBreak := "\n"
	if len(lines) > 0 && lines[0].crlf {
		lineBreak = "\r\n"
	}
	var comments []string
	for i, l := range lines {
		text := strings.TrimLeft(strings.TrimPrefix(l.text, "\ufeff"), " \t")
		if text == "" || strings.HasPrefix(text, "#") {
			comments = append(comments, text)
			continue
		}
		if strings.HasPrefix(text, "%") {
			continue
		}
		rest, ok := strings.CutPrefix(text, "---")
		if !ok || !marker(text) || (strings.TrimSpace(rest) != "" && !strings.HasPrefix(strings.TrimSpace(rest), "#")) {
			break
		}
		end := len(raw)
		if i+1 < len(lines) {
			end = lines[i+1].at
		}
		before = raw[:end]
		if end == len(raw) && !bytes.HasSuffix(before, []byte("\n")) {
			before = append(append([]byte(nil), before...), lineBreak...)
		}
		unhead(documents, strings.TrimRight(strings.Join(comments, "\n"), "\n"))
		break
	}
	for i := len(lines) - 1; i >= 0; i-- {
		text := strings.TrimSpace(lines[i].text)
		if text == "" {
			continue
		}
		if text == "..." {
			after = []byte("..." + lineBreak)
		}
		break
	}
	return before, after
}

// unhead takes the comments that came before the first document's marker
// from where the reader put them, the head of its first node that has
// one, so that the frame writes them and the library does not.
func unhead(documents []*yaml.Node, comments string) {
	if comments == "" || len(documents) == 0 {
		return
	}
	for node := documents[0]; node != nil; {
		if node.HeadComment != "" {
			if rest, ok := strings.CutPrefix(node.HeadComment, comments); ok {
				node.HeadComment = strings.TrimLeft(rest, "\n")
			}
			return
		}
		if len(node.Content) == 0 {
			return
		}
		node = node.Content[0]
	}
}

// dashOf is the column of the dash an entry of a block sequence follows
// on its line, or 0 where nothing but spaces and the dash come before it.
func dashOf(lines []line, entry *yaml.Node) int {
	if entry.Line < 1 || entry.Line > len(lines) || entry.Column < 1 {
		return 0
	}
	text := lines[entry.Line-1].text
	if entry.Column-1 > len(text) {
		return 0
	}
	before := strings.TrimRight(text[:entry.Column-1], " ")
	if !strings.HasSuffix(before, "-") || strings.TrimLeft(before[:len(before)-1], " ") != "" {
		return 0
	}
	return len(before)
}

// severalLines reports whether a node or anything in it is a quoted
// scalar that goes on past the line it begins on: its later lines may
// read as comments, so where it ends cannot be told by them.
func severalLines(lines []line, node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.ScalarNode && node.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 && !closesOnItsLine(lines, node) {
		return true
	}
	for _, child := range node.Content {
		if severalLines(lines, child) {
			return true
		}
	}
	return false
}

// closesOnItsLine reports whether a quoted scalar's closing quote is on
// the line its opening one is.
func closesOnItsLine(lines []line, node *yaml.Node) bool {
	if node.Line < 1 || node.Line > len(lines) {
		return false
	}
	text := lines[node.Line-1].text
	at := strings.IndexAny(text[min(max(node.Column-1, 0), len(text)):], "'\"")
	if at < 0 {
		return false
	}
	at += min(max(node.Column-1, 0), len(text))
	quote := text[at]
	for i := at + 1; i < len(text); i++ {
		switch {
		case quote == '"' && text[i] == '\\':
			i++
		case text[i] == quote && quote == '\'' && i+1 < len(text) && text[i+1] == '\'':
			i++
		case text[i] == quote:
			return true
		}
	}
	return false
}

// headOf is the first line of a key's own head comment: the comment lines
// right above it, with no blank line between, as many as the reader gave
// it; the key's line where it has none.
func headOf(lines []line, key *yaml.Node) int {
	if key.HeadComment == "" {
		return key.Line
	}
	count := strings.Count(key.HeadComment, "\n") + 1
	first := key.Line
	for first > 1 && count > 0 {
		text := strings.TrimLeft(lines[first-2].text, " \t")
		if !strings.HasPrefix(text, "#") {
			break
		}
		first--
		count--
	}
	return first
}

// lineEnding is the line break of the line an offset is on, or of the
// line before where it is at the end of the file.
func lineEnding(lines []line, at int) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].at <= at {
			if lines[i].crlf {
				return "\r\n"
			}
			return "\n"
		}
	}
	return "\n"
}

// untailed is a node with no comment after it: of it and of its last
// entry, and of that one's, as far down as they go, each a copy.
func untailed(node *yaml.Node) *yaml.Node {
	copy := *node
	copy.FootComment = ""
	if n := len(copy.Content); n > 0 {
		copy.Content = append([]*yaml.Node(nil), copy.Content...)
		copy.Content[n-1] = untailed(copy.Content[n-1])
	}
	return &copy
}
