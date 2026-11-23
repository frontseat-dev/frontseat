package pattern

import "testing"

func TestAPatternSelectsPaths(t *testing.T) {
	cases := []struct {
		patterns []string
		path     string
		want     bool
	}{
		{[]string{"go.mod"}, "go.mod", true},
		{[]string{"go.mod"}, "a/go.mod", false},
		{[]string{"**/go.mod"}, "go.mod", true},
		{[]string{"**/go.mod"}, "a/b/go.mod", true},
		{[]string{"**/*.go"}, "a/b/main.go", true},
		{[]string{"**/*.go"}, "a/b/main.rs", false},
		{[]string{"src/**/*.go"}, "src/a/b/main.go", true},
		{[]string{"src/**/*.go"}, "lib/a/main.go", false},
		{[]string{"**/*.go", "!**/testdata/**"}, "a/testdata/x/main.go", false},
		{[]string{"**/*.go", "!**/testdata/**"}, "a/main.go", true},
	}
	for _, c := range cases {
		if got := Select(c.patterns, c.path); got != c.want {
			t.Errorf("Select(%v, %q) = %v, want %v", c.patterns, c.path, got, c.want)
		}
	}
}

// An input that starts with ":" or "//" names what another action left;
// anything else is a pattern, an input set or an exclusion.
func TestAReferenceIsToldFromAPattern(t *testing.T) {
	for input, is := range map[string]bool{":x": true, ":": true, "//a:x": true, "//": true, "x": false, "=x": false, "!x": false, "a:b": false, "/x": false, "": false} {
		if IsReference(input) != is {
			t.Errorf("IsReference(%q) = %v", input, !is)
		}
	}
}
