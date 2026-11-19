package stage

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// What an action is handed is refused where a file it reads of the
// repository is at that place or within it; a neighbour whose name only
// begins the same, and a file elsewhere, are the controls.
func TestWhatIsHandedIsRefusedWhereAFileReadIs(t *testing.T) {
	inputs := []string{"lab/a/go.mod", "lab/a/made2/word.txt", "lab/a/read/main.go", "lab/a/spare"}
	held := exec.Held{}
	copies := map[string]exec.Copy{"lab/a/.frontseat/go/example.com/dep": {}}
	for _, c := range []struct {
		left   []string
		at     string
		refuse bool
	}{
		{[]string{"lab/a/made"}, "", false},
		{[]string{".frontseat/home/go/bin"}, "", false},
		{[]string{"lab/a/made2"}, "lab/a/made2", true},
		{[]string{"lab/a/spare"}, "lab/a/spare", true},
		{[]string{"lab/a/read/main.go"}, "lab/a/read/main.go", true},
		{[]string{"lab/a/read"}, "lab/a/read", true},
		{[]string{"lab/a"}, "lab/a", true},
		{[]string{"lab/a/made", "lab/a/read"}, "lab/a/read", true},
		// Generated source goes among what is handed of another project;
		// nothing sits at the very place of it.
		{[]string{"lab/a/.frontseat/go/example.com/dep/gen/x.pb.go"}, "", false},
		{[]string{"lab/a/.frontseat/go/example.com/dep"}, "lab/a/.frontseat/go/example.com/dep", true},
		{nil, "", false},
	} {
		left := map[string]exec.Held{}
		for _, at := range c.left {
			left[at] = held
		}
		at, refused := shadowed(inputs, copies, left)
		if refused != c.refuse || at != c.at {
			t.Errorf("shadowed(%v) = %q, %v; want %q, %v", c.left, at, refused, c.at, c.refuse)
		}
	}
}

// A few handed places are looked for among the inputs as they are, and
// many among them in order: both ways say the same of the same places,
// whatever sorts between a place and what is within it.
func TestFewAndManyHandedPlacesAreHeldAlike(t *testing.T) {
	names := []string{"made", "made-x", "made.go", "made/word.txt", "made2/word.txt", "made+", "read/main.go", "spare", "go.mod"}
	padding := map[string]exec.Held{}
	for _, at := range []string{"zz/a", "zz/b", "zz/c", "zz/d", "zz/e"} {
		padding[at] = exec.Held{}
	}
	if len(padding) <= lookedFor {
		t.Fatal("the padding does not reach the path that puts the inputs in order")
	}
	// Every subset of the names as the inputs, against each place handed.
	for subset := 0; subset < 1<<len(names); subset++ {
		var inputs []string
		for i, name := range names {
			if subset&(1<<i) != 0 {
				inputs = append(inputs, "lab/a/"+name)
			}
		}
		for _, place := range []string{"lab/a/made", "lab/a/read", "lab/a/spare", "lab/a/go.mod", "lab/a/absent", "lab/a"} {
			few := map[string]exec.Held{place: {}}
			many := map[string]exec.Held{place: {}}
			for at, held := range padding {
				many[at] = held
			}
			at, refused := shadowed(inputs, nil, few)
			atMany, refusedMany := shadowed(inputs, nil, many)
			if at != atMany || refused != refusedMany {
				t.Fatalf("inputs %v, handed %s: looked for says %q %v, in order says %q %v", inputs, place, at, refused, atMany, refusedMany)
			}
			// What both say is what is so: a file at the place or within it.
			want := false
			for _, input := range inputs {
				want = want || input == place || len(input) > len(place) && input[:len(place)+1] == place+"/"
			}
			if refused != want {
				t.Fatalf("inputs %v, handed %s: refused %v, want %v", inputs, place, refused, want)
			}
		}
	}
}
