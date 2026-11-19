package yamlnode

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

const gateway = `# The gateway.
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: gateway
spec:
  type: service
  dependsOn: [greet, component:default/log, resource:default/orders-db] # what it needs
`

// read is what the semantic reader makes of a key path of a document.
func read(t *testing.T, raw []byte, document int, keys ...string) any {
	t.Helper()
	documents, err := parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := documents[document].Decode(&value); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		mapping, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = mapping[key]
	}
	return value
}

// Setting a list keeps the file's comments, its other keys and the entries
// that stay, as they were written, and takes out what is not among the values.
func TestSettingAListKeepsWhatHolds(t *testing.T) {
	out, err := SetStrings([]byte(gateway), 0, []string{"spec", "dependsOn"}, []string{"greet", "resource:default/orders-db", "component:default/audit"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, kept := range []string{"# The gateway.", "greet", "resource:default/orders-db", "component:default/audit", "type: service", "# what it needs"} {
		if !strings.Contains(got, kept) {
			t.Errorf("the file lost %q:\n%s", kept, got)
		}
	}
	if strings.Contains(got, "component:default/log") {
		t.Errorf("the file kept what is not among the values:\n%s", got)
	}
	list, _ := read(t, out, 0, "spec", "dependsOn").([]any)
	if len(list) != 3 || list[2] != "component:default/audit" {
		t.Fatalf("read back %v", list)
	}
}

// With no values the key goes; a file without the key stands as it is,
// byte for byte, and so does one already saying what it should.
func TestNoValuesTakesTheKeyOutAndAFileInStepStands(t *testing.T) {
	out, err := SetStrings([]byte("kind: Component\nmetadata:\n  name: gateway\nspec:\n  dependsOn: [log]\n"), 0, []string{"spec", "dependsOn"}, nil)
	if err != nil || strings.Contains(string(out), "dependsOn") {
		t.Fatalf("SetStrings = %q, %v", out, err)
	}
	raw := []byte("# as is\nkind: Component\nmetadata:\n  name: gateway\n")
	out, err = SetStrings(raw, 0, []string{"spec", "dependsOn"}, nil)
	if err != nil || string(out) != string(raw) {
		t.Fatalf("a file without the key changed: %q, %v", out, err)
	}
	out, err = SetStrings([]byte(gateway), 0, []string{"spec", "dependsOn"}, []string{"greet", "component:default/log", "resource:default/orders-db"})
	if err != nil || string(out) != gateway {
		t.Fatalf("a file in step changed: %q, %v", out, err)
	}
}

// A path that is not there is made, in the document named, and the other
// documents of the stream are left alone.
func TestAMissingPathIsMadeInTheDocumentNamed(t *testing.T) {
	raw := []byte("kind: Component\nmetadata: {name: a}\n---\n# second\nkind: Component\nmetadata: {name: b}\n")
	out, err := SetStrings(raw, 1, []string{"spec", "dependsOn"}, []string{"component:default/a"})
	if err != nil {
		t.Fatal(err)
	}
	if read(t, out, 0, "spec") != nil || !strings.Contains(string(out), "# second") {
		t.Fatalf("the first document changed or the comment went:\n%s", out)
	}
	list, _ := read(t, out, 1, "spec", "dependsOn").([]any)
	if len(list) != 1 || list[0] != "component:default/a" {
		t.Fatalf("read back %v", list)
	}
	for _, refused := range []struct {
		document int
		keys     []string
	}{{2, []string{"spec"}}, {-1, []string{"spec"}}, {0, nil}, {0, []string{"metadata", "name", "x"}}} {
		if _, err := SetStrings(raw, refused.document, refused.keys, []string{"x"}); err == nil {
			t.Errorf("document %d keys %v was accepted", refused.document, refused.keys)
		}
	}
}

// Aliases and merges cannot hide the list, and setting it changes nothing
// that shared an anchor with it.
func TestAliasesAndMergesCannotHideTheListAndAreLeftAlone(t *testing.T) {
	for name, raw := range map[string]string{
		"entry alias":            "dep: &dep component:invented\nkind: Component\nmetadata: {name: gateway}\nspec: {dependsOn: [*dep, resource:database]}\n",
		"list alias":             "deps: &deps [component:invented, resource:database]\nkind: Component\nmetadata: {name: gateway}\nspec: {dependsOn: *deps}\n",
		"metadata alias":         "identity: &identity {name: gateway}\nkind: Component\nmetadata: *identity\nspec: {dependsOn: [component:invented, resource:database]}\n",
		"spec alias":             "base: &base {dependsOn: [component:invented, resource:database]}\nkind: Component\nmetadata: {name: gateway}\nspec: *base\n",
		"inherited spec":         "base: &base {spec: {dependsOn: [component:invented, resource:database]}}\n<<: *base\nkind: Component\nmetadata: {name: gateway}\n",
		"inherited dependencies": "base: &base {dependsOn: [component:invented, resource:database]}\nkind: Component\nmetadata: {name: gateway}\nspec: {<<: *base}\n",
		"shared inline anchor":   "kind: Component\nmetadata: {name: gateway}\nspec: &base {dependsOn: &deps [component:invented, resource:database]}\nother: {spec: *base, dependencies: *deps}\n",
	} {
		t.Run(name, func(t *testing.T) {
			for _, values := range [][]string{{"resource:database"}, {"resource:database", "component:default/greet"}} {
				out, err := SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, values)
				if err != nil {
					t.Fatal(err)
				}
				list, _ := read(t, out, 0, "spec", "dependsOn").([]any)
				if len(list) != len(values) || list[0] != "resource:database" {
					t.Fatalf("set = %s; read = %v", out, list)
				}
				var before, after map[string]any
				if err := yaml.Unmarshal([]byte(raw), &before); err != nil {
					t.Fatal(err)
				}
				if err := yaml.Unmarshal(out, &after); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"base", "deps", "other", "identity", "dep"} {
					a, _ := yaml.Marshal(before[key])
					b, _ := yaml.Marshal(after[key])
					if string(a) != string(b) {
						t.Errorf("unrelated %s changed: %s != %s", key, a, b)
					}
				}
			}
		})
	}
}

// writes holds what SetStrings writes of spec.dependsOn, byte for byte.
func writes(t *testing.T, raw string, document int, values []string, want string) {
	t.Helper()
	out, err := SetStrings([]byte(raw), document, []string{"spec", "dependsOn"}, values)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != want {
		t.Errorf("SetStrings wrote\n%s\nwant\n%s", out, want)
	}
}

// A file kept in step keeps everything it had but the value changed: its
// document markers, the comments before and between them, its other
// documents byte for byte, and its own layout; the file with no marker is
// the control, and is given none.
func TestAFileKeepsEverythingButTheValueChanged(t *testing.T) {
	values := []string{"resource:database", "component:default/greet"}
	block := "kind: Component\nmetadata:\n  name: gateway\nspec:\n  type: service\n  dependsOn:\n  - resource:database\n"
	blockSet := "kind: Component\nmetadata:\n  name: gateway\nspec:\n  type: service\n  dependsOn:\n  - resource:database\n  - component:default/greet\n"
	first := "---\n# first, as a person wrote it\nkind:    Component\nmetadata:\n    name: a   # four\n\nspec: {dependsOn: [\"x\"]}\n"
	for name, c := range map[string]struct {
		raw, want string
		document  int
	}{
		"no marker":            {raw: block, want: blockSet},
		"a leading marker":     {raw: "---\n" + block, want: "---\n" + blockSet},
		"a comment, a marker":  {raw: "# The gateway.\n---\n" + block, want: "# The gateway.\n---\n" + blockSet},
		"an end marker":        {raw: "---\n" + block + "...\n", want: "---\n" + blockSet + "...\n"},
		"the second document":  {raw: first + "---\n" + block + "...\n", want: first + "---\n" + blockSet + "...\n", document: 1},
		"the first of several": {raw: "---\n" + block + "---\n" + first[4:], want: "---\n" + blockSet + "---\n" + first[4:]},
		"a blank line and a foot comment kept": {
			raw:  "kind: Component\n\nspec:\n    type: service\n\n    dependsOn: [\"resource:database\"] # needs\n    # what it needs\n\n    owner: me\n",
			want: "kind: Component\n\nspec:\n    type: service\n\n    dependsOn: [\"resource:database\", \"component:default/greet\"] # needs\n    # what it needs\n\n    owner: me\n",
		},
		"a missing path made": {
			raw:  "---\nkind: Component\nmetadata:\n    name: gateway # named\n# the end\n",
			want: "---\nkind: Component\nmetadata:\n    name: gateway # named\nspec:\n    dependsOn:\n        - resource:database\n        - component:default/greet\n# the end\n",
		},
	} {
		t.Run(name, func(t *testing.T) { writes(t, c.raw, c.document, values, c.want) })
	}
}

// What is taken out goes with its own lines, and nothing else does.
func TestTakingAListOutKeepsTheRest(t *testing.T) {
	raw := "# head\n---\nkind: Component\nspec:\n  type: service\n  dependsOn:\n  - log # the log\n  owner: me\n---\nkind: Other\n"
	out, err := SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# head\n---\nkind: Component\nspec:\n  type: service\n  owner: me\n---\nkind: Other\n"; string(out) != want {
		t.Errorf("SetStrings wrote\n%s\nwant\n%s", out, want)
	}
}

// A list is written in the style it was written in, its new entries quoted
// as the ones there were. A plain entry holding a colon cannot be written
// plain in a flow list by the YAML library (libyaml's emitter takes any
// colon for a flow indicator), so there it is quoted: the last case holds
// that, and fails when the library comes to keep it.
func TestAListKeepsItsStyle(t *testing.T) {
	values := []string{"resource:database", "component:default/greet"}
	for name, c := range map[string]struct{ raw, want string }{
		"single-quoted flow":  {"spec:\n  dependsOn: ['resource:database']\n", "spec:\n  dependsOn: ['resource:database', 'component:default/greet']\n"},
		"double-quoted flow":  {"spec:\n  dependsOn: [\"resource:database\"]\n", "spec:\n  dependsOn: [\"resource:database\", \"component:default/greet\"]\n"},
		"plain block":         {"spec:\n  dependsOn:\n    - resource:database\n", "spec:\n  dependsOn:\n    - resource:database\n    - component:default/greet\n"},
		"double-quoted block": {"spec:\n  dependsOn:\n  - \"resource:database\"\n", "spec:\n  dependsOn:\n  - \"resource:database\"\n  - \"component:default/greet\"\n"},
		"plain flow":          {"spec:\n  dependsOn: [resource:database]\n", "spec:\n  dependsOn: ['resource:database', 'component:default/greet']\n"},
	} {
		t.Run(name, func(t *testing.T) { writes(t, c.raw, 0, values, c.want) })
	}
}

// A block scalar's lines may read as comments; what ends an entry is not
// taken for what follows it, and nothing of it is written twice.
func TestABlockScalarsLastLinesAreNotTakenForComments(t *testing.T) {
	raw := "kind: A\nspec:\n  dependsOn:\n  - |\n    x\n    # y\n"
	values := []string{"x\n# y\n", "b"}
	out, err := SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, values)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), "# y"); n != 1 {
		t.Errorf("the block scalar's last line is written %d times:\n%s", n, out)
	}
	list, _ := read(t, out, 0, "spec", "dependsOn").([]any)
	if len(list) != 2 || list[0] != values[0] || list[1] != "b" {
		t.Fatalf("read back %q", list)
	}
}

// Where the change cannot be spliced in (here, the list has an anchor
// that another key aliases) the stream is written whole, and still keeps
// what comes before its first document's marker, the marker, and an end
// marker; the file without them is the control, given none.
func TestAFileWrittenWholeKeepsItsMarkers(t *testing.T) {
	body := "kind: Component\nspec:\n  dependsOn: &deps [component:default/a]\nother: *deps\n"
	for name, c := range map[string]struct{ before, after string }{
		"no marker":           {},
		"a leading marker":    {before: "---\n"},
		"a comment, a marker": {before: "# The gateway.\n\n# owned by us\n---\n"},
		"an end marker":       {before: "---\n", after: "...\n"},
	} {
		t.Run(name, func(t *testing.T) {
			raw := c.before + body + c.after
			out, err := SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, []string{"component:default/a", "component:default/b"})
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if !strings.HasPrefix(got, c.before) || !strings.HasSuffix(got, c.after) {
				t.Errorf("SetStrings wrote\n%s\nwant it to begin %q and end %q", got, c.before, c.after)
			}
			if c.before == "" && strings.HasPrefix(got, "---") || c.after == "" && strings.Contains(got, "...") {
				t.Errorf("a marker was added:\n%s", got)
			}
			if strings.Count(got, "# The gateway.") > 1 || strings.Count(got, "# owned by us") > 1 {
				t.Errorf("a comment is written twice:\n%s", got)
			}
			if list, _ := read(t, out, 0, "spec", "dependsOn").([]any); len(list) != 2 || list[1] != "component:default/b" {
				t.Errorf("read back %v", list)
			}
			if other, _ := read(t, out, 0, "other").([]any); len(other) != 1 || other[0] != "component:default/a" {
				t.Errorf("what aliased the list changed: %v", other)
			}
		})
	}
}

// A key added to a mapping that stands in from its parent is written at
// the mapping's own column, and the rest of the file is left as it was.
func TestAKeyAddedToAnIndentedMappingStandsWithItsSiblings(t *testing.T) {
	raw := "kind: Component\nspec:\n    type:   service\n    owner: \"me\"\nlist:\n- x\n"
	writes(t, raw, 0, []string{"a"}, "kind: Component\nspec:\n    type:   service\n    owner: \"me\"\n    dependsOn:\n    - a\nlist:\n- x\n")
	// The control: a file whose lists stand in from their keys.
	writes(t, "spec:\n    type: service\nlist:\n    - x\n", 0, []string{"a"}, "spec:\n    type: service\n    dependsOn:\n        - a\nlist:\n    - x\n")
}

// What is written always means the edit asked for and nothing else: an
// emptied mapping keeps the comment on its key's line in place, and
// where the file cannot be written so (a kept block scalar the library
// would change), it is refused, never written otherwise.
func TestWhatIsWrittenMeansTheEditOrIsRefused(t *testing.T) {
	writes(t, "spec: # c\n  dependsOn:\n    - a\nz: 1\n", 0, nil, "spec: {} # c\nz: 1\n")
	raw := "spec:\n  o: 1\neps: |+\n  keep\n\n# c\n"
	out, err := SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, []string{"a"})
	if err == nil {
		if eps := read(t, out, 0, "eps"); eps != "keep\n\n" {
			t.Errorf("another key changed: eps reads %q in\n%s", eps, out)
		}
	}
}

// A quoted scalar of several lines whose later lines read as comments is
// not taken to end where they begin: nothing of it is left behind.
func TestAQuotedScalarOfSeveralLinesIsNotCutShort(t *testing.T) {
	for _, raw := range []string{
		"spec:\n  \"dependsOn\":\n    - \"multi\n      # h1\"\n  o: 1\n",
		"spec:\n  o: 'one\n    # h1'\n",
	} {
		out, err := SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, []string{"a", "b"})
		if err != nil {
			continue
		}
		if strings.Contains(string(out), "# h1") && read(t, out, 0, "spec", "o") != "one # h1" {
			t.Errorf("a quoted scalar's line was left behind:\n%s", out)
		}
		if list, _ := read(t, out, 0, "spec", "dependsOn").([]any); len(list) != 2 {
			t.Errorf("read back %v from\n%s", list, out)
		}
	}
}

// A list keeps its tag, and the comment on its line, and a tagged list
// keeps its indentation; a key taken out takes the comment above it.
func TestAListKeepsItsTagItsCommentAndTakesItsHeadCommentWithIt(t *testing.T) {
	writes(t, "spec:\n  dependsOn: !mine\n    - a\n  o: 1\n", 0, []string{"a", "b"}, "spec:\n  dependsOn: !mine\n    - a\n    - b\n  o: 1\n")
	writes(t, "spec:\n  dependsOn: !!seq\n  - a\n  o: 1\n", 0, []string{"a", "b"}, "spec:\n  dependsOn: !!seq\n  - a\n  - b\n  o: 1\n")
	writes(t, "spec:\n  dependsOn: ~ # c\n  o: 1\n", 0, []string{"a"}, "spec:\n  dependsOn: # c\n    - a\n  o: 1\n")
	writes(t, "spec:\n  o: 1\n  # about deps\n  dependsOn: [a]\n  p: 1\n", 0, nil, "spec:\n  o: 1\n  p: 1\n")
	writes(t, "spec:\n  o: 1\n\n  # about p\n\n  dependsOn: [a]\n  p: 1\n", 0, nil, "spec:\n  o: 1\n\n  # about p\n\n  p: 1\n")
	// The line ending is the entry's own, in a file that mixes them.
	writes(t, "spec:\r\n  o: 1\n  dependsOn: [a]\n", 0, []string{"a", "b"}, "spec:\r\n  o: 1\n  dependsOn: [a, b]\n")
}

// What is read back is held to the edited documents node by node: a tag,
// a key's order or a value's kind that differs is a difference, though a
// reader into Go values would not see it; the control is the same text.
func TestReadingBackHoldsTagsOrderAndKinds(t *testing.T) {
	documents := func(raw string) []*yaml.Node {
		t.Helper()
		read, err := parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return read
	}
	want := documents("a: [x]\nb: 1\n")
	for raw, same := range map[string]bool{
		"a: [x]\nb: 1\n":       true,
		"a:\n- x\nb: 1 # c\n":  true,
		"a: !mine [x]\nb: 1\n": false,
		"b: 1\na: [x]\n":       false,
		"a: [x]\nb: '1'\n":     false,
	} {
		if got := sameMeaning([]byte(raw), want); got != same {
			t.Errorf("sameMeaning(%q) = %v, want %v", raw, got, same)
		}
	}
}
