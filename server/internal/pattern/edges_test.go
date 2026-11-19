package pattern_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

// Under says it is never narrower than Select. The check is the
// author's own (TestLookingOnlyWhereItSaysFindsTheSame), over more
// patterns and paths.
func TestLookingOnlyWhereItSaysFindsTheSame(t *testing.T) {
	paths := []string{"a", "a/b", "a/b/c", "ab", "a.go", "b/a", "a/b.go", "[a]/b.go", "a b/c", "a/b/", "é/x", "a//b"}
	for _, patterns := range [][]string{
		{"a/**"}, {"a/b/**"}, {"a/**/b"}, {"a/{b,c}"}, {"{a,b}/a"}, {"a/"}, {"a/b"}, {"a"}, {"a/b/c/../**"}, {"./a/**"}, {"a//**"}, {"[a]/*.go"}, {"a/**", "!a"}, {"é/*"},
	} {
		directories, anywhere := pattern.Under(patterns)
		for _, path := range paths {
			looked := anywhere
			for _, directory := range directories {
				if path == directory || strings.HasPrefix(path, directory+"/") {
					looked = true
				}
			}
			if pattern.Select(patterns, path) && !looked {
				t.Errorf("%q selects %q, which is none of, and under none of %q", patterns, path, directories)
			}
		}
	}
}
