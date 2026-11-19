package yamlnode

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// The cases below are made by a generator a review wrote to attack
// SetStrings (catalog files of every shape it read of: markers, comments,
// block and quoted scalars of several lines, flow and block lists, CRLF,
// no final line break), seeded so that each run makes the same ones.

func pick(r *rand.Rand, s ...string) string { return s[r.Intn(len(s))] }

func ind(n int, s string) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for _, l := range strings.SplitAfter(s, "\n") {
		if l == "" {
			continue
		}
		if strings.TrimSpace(l) == "" {
			b.WriteString(l)
		} else {
			b.WriteString(pad + l)
		}
	}
	return b.String()
}

// cn numbers the comments of a run, so that each is told apart.
var cn int

func cm() string { cn++; return fmt.Sprintf("# c%d", cn) }

func other(r *rand.Rand, name string) string {
	switch r.Intn(14) {
	case 0:
		return name + ": 1\n"
	case 1:
		return name + ": v " + cm() + "\n"
	case 2:
		return name + ": [x, y]\n"
	case 3:
		return name + ":\n- x\n- y\n"
	case 4:
		return name + ":\n  k: 1\n  l:\n    - m " + cm() + "\n"
	case 5:
		return name + ": |\n  text\n  # notcomment" + fmt.Sprint(r.Intn(1000)) + "\n"
	case 6:
		return name + ": \"a\n  # q" + fmt.Sprint(r.Intn(1000)) + "\"\n"
	case 7:
		return name + ": >-\n  folded\n\n  # nc" + fmt.Sprint(r.Intn(1000)) + "\n\n"
	case 8:
		return cm() + "\n" + name + ": {a: 1, b: [2]}\n"
	case 9:
		return name + ": 'it''s'\n\n"
	case 10:
		return name + ":\n" + cm() + "\n  k: 1\n"
	case 11:
		return name + ": a\n  b\n"
	case 12:
		return name + ": |+\n  keep\n\n"
	default:
		return name + ":\n  - k: 1\n    l: 2\n  - |\n    z\n"
	}
}

func deps(r *rand.Rand, items []string) string {
	q := pick(r, "", "", "'", "\"")
	it := func(s string) string { return q + s + q }
	switch r.Intn(12) {
	case 0:
		s := "dependsOn:\n"
		for _, i := range items {
			s += "- " + it(i) + "\n"
		}
		return s
	case 1:
		s := "dependsOn:\n"
		for _, i := range items {
			s += "  - " + it(i) + "\n"
		}
		return s
	case 2:
		s := "dependsOn:" + pick(r, "", "  ", " "+cm()) + "\n"
		for _, i := range items {
			s += pick(r, "", "    "+cm()+"\n") + "    - " + it(i) + pick(r, "", " "+cm()) + "\n"
		}
		return s
	case 3:
		var q2 []string
		for _, i := range items {
			q2 = append(q2, "'"+i+"'")
		}
		return "dependsOn: [" + strings.Join(q2, ", ") + "]" + pick(r, "", " "+cm()) + "\n"
	case 4:
		var q2 []string
		for _, i := range items {
			q2 = append(q2, "\""+i+"\"")
		}
		return "dependsOn: [\n    " + strings.Join(q2, ",\n    ") + "\n  ]\n"
	case 5:
		return "dependsOn: []" + pick(r, "", " "+cm()) + "\n"
	case 6:
		return "dependsOn:" + pick(r, "", " ~", " null", " "+cm(), " ~ "+cm()) + "\n"
	case 7:
		return "dependsOn: solo" + pick(r, "", " "+cm()) + "\n"
	case 8:
		return "\"dependsOn\":\n  - \"multi\n    # h" + fmt.Sprint(r.Intn(1000)) + "\"\n"
	case 9:
		return "dependsOn :\n  -   a\n  -\n    b\n"
	case 10:
		return "dependsOn:\n  - a\n\n  - b\n\n"
	default:
		return "" // absent
	}
}

func gen(r *rand.Rand) (pre, entry, post string, crlf bool) {
	names := []string{"alpha", "beta", "gamma", "delta", "eps", "zeta", "eta"}
	n := 0
	nm := func() string { n++; return names[n-1] }
	indent := pick(r, "  ", "    ", " ")
	var b strings.Builder
	if r.Intn(4) == 0 {
		b.WriteString(pick(r, "---\n", cm()+"\n---\n", "--- "+cm()+"\n", "%YAML 1.2\n---\n", "\ufeff", cm()+"\n\n"))
	}
	for i := r.Intn(3); i > 0; i-- {
		b.WriteString(other(r, nm()))
	}
	b.WriteString("spec:" + pick(r, "", "", " "+cm()) + "\n")
	nb := r.Intn(3)
	for i := nb; i > 0; i-- {
		b.WriteString(ind(len(indent), other(r, nm())))
	}
	if r.Intn(4) == 0 && nb > 0 {
		b.WriteString(pick(r, "\n", indent+cm()+"\n", cm()+"\n"))
	}
	items := []string{"a", "c", "d"}[:1+r.Intn(3)]
	e := deps(r, items)
	na := r.Intn(3)
	if e == "" && nb == 0 {
		nb = 1
		b.WriteString(ind(len(indent), other(r, nm())))
	}
	pre = b.String()
	entry = ind(len(indent), e)
	var p strings.Builder
	if r.Intn(3) == 0 {
		p.WriteString(pick(r, "\n", indent+cm()+"\n", cm()+"\n", "\n\n"+indent+cm()+"\n\n"))
	}
	for i := na; i > 0; i-- {
		p.WriteString(ind(len(indent), other(r, nm())))
	}
	for i := r.Intn(2); i > 0; i-- {
		p.WriteString(other(r, nm()))
	}
	if r.Intn(4) == 0 {
		p.WriteString(pick(r, "...\n", "---\nkind: Other\nspec:\n  dependsOn: [zz]\n", cm()+"\n", "\n\n", "...\n---\nx: 1\n...\n"))
	}
	post = p.String()
	crlf = r.Intn(6) == 0
	return
}

func decodeAll(raw []byte) ([]any, error) {
	docs, err := parse(raw)
	if err != nil {
		return nil, err
	}
	var out []any
	for _, d := range docs {
		var v any
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// expect is what the documents read as once the edit is made.
func expect(orig []any, doc int, keys []string, values []string) []any {
	m, _ := orig[doc].(map[string]any)
	cur := m
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			if len(values) == 0 {
				return orig
			}
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	if len(values) == 0 {
		delete(cur, keys[len(keys)-1])
	} else {
		l := []any{}
		for _, v := range values {
			l = append(l, v)
		}
		cur[keys[len(keys)-1]] = l
	}
	return orig
}

// Whatever the file, what SetStrings writes reads as the edit asked for,
// or it is refused; writing it again changes nothing; and nothing of a
// quoted scalar of several lines is left behind. Where nothing in the
// file asks for it to be written whole (a block or quoted scalar of
// several lines, an anchor, a tag, a directive), everything around the
// entry is kept byte for byte.
func TestEveryFileGeneratedIsWrittenAsTheEditOrRefused(t *testing.T) {
	seeds := int64(20000)
	if testing.Short() {
		seeds = 2000
	}
	failures := map[string]int{}
	fail := func(kind string, seed int64, values []string, raw string, out []byte, err error) {
		failures[kind]++
		if failures[kind] <= 3 {
			t.Errorf("%s, seed %d, values %q:\nin:  %q\nout: %q\nerr: %v", kind, seed, values, raw, out, err)
		}
	}
	for seed := range seeds {
		r := rand.New(rand.NewSource(seed))
		pre, entry, post, crlf := gen(r)
		raw := pre + entry + post
		if r.Intn(8) == 0 {
			raw = strings.TrimRight(raw, "\n")
			post = strings.TrimRight(post, "\n")
			if post == "" {
				entry = strings.TrimRight(entry, "\n")
			}
		}
		if crlf {
			raw = strings.ReplaceAll(raw, "\n", "\r\n")
			pre = strings.ReplaceAll(pre, "\n", "\r\n")
			post = strings.ReplaceAll(post, "\n", "\r\n")
		}
		var values []string
		switch r.Intn(6) {
		case 0:
		case 1:
			values = []string{"a"}
		case 2:
			values = []string{"a", "b"}
		case 3:
			values = []string{"z: y", "c", "a"}
		case 4:
			values = []string{"d", "c", "a", "#x"}
		default:
			values = []string{"multi\n# h1", "q"}
		}
		before, err := decodeAll([]byte(raw))
		if err != nil {
			continue
		}
		out, err := SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, values)
		if err != nil {
			continue
		}
		after, err := decodeAll(out)
		if err != nil {
			fail("unreadable", seed, values, raw, out, err)
			continue
		}
		if want := expect(before, 0, []string{"spec", "dependsOn"}, values); !reflect.DeepEqual(after, want) {
			fail("another meaning", seed, values, raw, out, nil)
			continue
		}
		if again, err := SetStrings(out, 0, []string{"spec", "dependsOn"}, values); err != nil || string(again) != string(out) {
			fail("not in step after", seed, values, raw, again, err)
		}
		written := string(out)
		if strings.Contains(entry, "# h") && strings.Count(written, "# h") > strings.Count(strings.Join(values, ""), "# h") {
			fail("left behind", seed, values, raw, out, nil)
		}
		plain := strings.ReplaceAll(raw, "\r\n", "\n")
		whole := strings.ContainsAny(plain, "|>&!%\ufeff") || strings.Contains(plain, "\"a\n") || strings.Contains(plain, "\"multi")
		emptied := len(values) == 0
		// A new entry is an insertion into the file as it was; one that
		// was there is rewritten between what came before and after it.
		kept := strings.HasPrefix(written, pre) && strings.HasSuffix(written, post)
		if entry == "" {
			kept = inserted(raw, written)
		}
		if written != raw && !whole && !emptied && !kept {
			fail("rewrote more than the entry", seed, values, raw, out, nil)
		}
	}
	if len(failures) > 0 {
		t.Logf("failures by kind: %v", fmt.Sprint(failures))
	}
}

// inserted reports whether a text is another with one stretch put in it.
func inserted(was, is string) bool {
	if len(is) < len(was) {
		return false
	}
	head := 0
	for head < len(was) && was[head] == is[head] {
		head++
	}
	return strings.HasSuffix(is, was[head:])
}
