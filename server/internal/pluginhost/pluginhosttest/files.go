// Package pluginhosttest gives a test a repository held in memory, which
// a plugin's host reads as it reads a workspace, and the inputs a
// plugin's action names, written as a plugin writes them.
package pluginhosttest

import (
	"context"
	"sort"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

// Files is a repository held in memory: what each file holds, by its
// path with slashes.
type Files map[string]string

// Read returns what a file holds, and whether it is there.
func (f Files) Read(_ context.Context, path string) ([]byte, bool, error) {
	content, ok := f[path]
	return []byte(content), ok, nil
}

// Glob returns the files Within says, as a host's repository does.
func (f Files) Glob(_ context.Context, directory string, patterns []string) ([]string, error) {
	return f.Within(directory, patterns), nil
}

// Within returns, sorted, the path of each file beneath a directory that
// a pattern matches, matched as it is named beneath that directory. The
// directory is a name and never a pattern; empty, or ".", it is the
// repository's root.
func (f Files) Within(directory string, patterns []string) []string {
	var out []string
	for path := range f {
		within := path
		if directory != "" && directory != "." {
			var ok bool
			within, ok = strings.CutPrefix(path, directory+"/")
			if !ok {
				continue
			}
		}
		if pattern.Select(patterns, within) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
