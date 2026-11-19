package pattern_test

import (
	"reflect"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

// Where patterns can select a path is said by the directories they are
// written beneath, so that whoever holds many paths looks only at those.
// It is never narrower than what the patterns select.
func TestWherePatternsSelect(t *testing.T) {
	for name, c := range map[string]struct {
		patterns    []string
		directories []string
		anywhere    bool
	}{
		"beneath one directory":            {[]string{"m/**/*.go", "m/go.mod", "!m/**/*_test.go"}, []string{"m"}, false},
		"beneath several":                  {[]string{"a/b/*.go", "c/*.go"}, []string{"a/b", "c"}, false},
		"one beneath another":              {[]string{"a/**", "a/b/*.go"}, []string{"a", "a/b"}, false},
		"a file at the top":                {[]string{"go.mod"}, nil, true},
		"any directory":                    {[]string{"**/*.go"}, nil, true},
		"a directory that is a pattern":    {[]string{"m*/go.mod"}, nil, true},
		"only what is left out":            {[]string{"!m/**"}, nil, false},
		"what is left out does not narrow": {[]string{"m/**", "!**/*_test.go"}, []string{"m"}, false},
		"a character escaped":              {[]string{`m\*/go.mod`}, nil, true},
		"none":                             {nil, nil, false},
	} {
		directories, anywhere := pattern.Under(c.patterns)
		if !reflect.DeepEqual(directories, c.directories) || anywhere != c.anywhere {
			t.Errorf("%s: under %v, anywhere %v; want %v, %v", name, directories, anywhere, c.directories, c.anywhere)
		}
	}
}
