package source

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// IgnoreFile is the file a repository says what frontseat must not see
// in, at its root: a pattern on each line, read as git reads .gitignore.
// What it names is not part of the repository to a run: it is not
// listed, it is not read where it is named, and no action is handed it.
// Fixtures that are meant to be malformed, and examples that are not
// the repository's own code, are what it is for. It is the one such
// list: a plugin narrows what it looks for by its ecosystem's rules, in
// its own interests, and adds nothing here.
const IgnoreFile = ".frontseatignore"

// ignoreLimit is the most the ignore file may hold, in bytes: a file of
// patterns, not of data.
const ignoreLimit = 1 << 20

// ignores is what a repository's ignore file names: nothing, where the
// repository has none. Each pattern is kept with whether it names a
// file or directory by name alone, or by a path: git matches the first
// kind against each name on the way down and the second against the
// path, and a directory it excludes is never looked beneath, whatever
// later patterns would let back in.
type ignores struct {
	lines []line
}

// line is one pattern of the ignore file.
type line struct {
	matcher gitignore.Pattern
	// byName is a pattern with no slash but a trailing one, which git
	// matches against a name at any depth, never against a path.
	byName bool
}

// readIgnores reads a repository's ignore file through the repository's
// root, so that one that is a link to a file outside is refused, as
// every path that leaves the repository is.
func readIgnores(root *os.Root) (ignores, error) {
	file, err := root.Open(IgnoreFile)
	if errors.Is(err, fs.ErrNotExist) {
		return ignores{}, nil
	}
	if err != nil {
		return ignores{}, fmt.Errorf("%s: %w", IgnoreFile, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ignores{}, fmt.Errorf("%s: %w", IgnoreFile, err)
	}
	if !info.Mode().IsRegular() {
		return ignores{}, fmt.Errorf("%s is not a file", IgnoreFile)
	}
	content, err := io.ReadAll(io.LimitReader(file, ignoreLimit+1))
	if err != nil {
		return ignores{}, fmt.Errorf("%s: %w", IgnoreFile, err)
	}
	if len(content) > ignoreLimit {
		return ignores{}, fmt.Errorf("%s holds more than %d bytes", IgnoreFile, ignoreLimit)
	}
	// A blank line and a comment say nothing, as in git; every other
	// line is a pattern, relative to the repository's root.
	var lines []line
	for _, text := range strings.Split(string(content), "\n") {
		text = strings.TrimSuffix(text, "\r")
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
			continue
		}
		named := strings.TrimPrefix(text, "!")
		lines = append(lines, line{
			matcher: gitignore.ParsePattern(text, nil),
			byName:  !strings.Contains(strings.TrimSuffix(named, "/"), "/"),
		})
	}
	return ignores{lines: lines}, nil
}

// ignored reports whether the ignore file names a path of the
// repository, a directory or a file, as git decides it: each directory
// on the way down is held to the patterns, the last to match deciding,
// and one that is excluded takes everything beneath it with it; the
// file or directory itself is decided last. A pattern by name is held
// to the name alone, so that one cannot let a path back in by matching
// a directory above it.
func (i ignores) ignored(name string, isDir bool) bool {
	if len(i.lines) == 0 {
		return false
	}
	path := strings.Split(name, "/")
	for depth := 1; depth <= len(path); depth++ {
		prefix, dir := path[:depth], depth < len(path) || isDir
		decided := gitignore.NoMatch
		for _, l := range i.lines {
			asked := prefix
			if l.byName {
				asked = prefix[depth-1:]
			}
			if result := l.matcher.Match(asked, dir); result != gitignore.NoMatch {
				decided = result
			}
		}
		if decided == gitignore.Exclude {
			return true
		}
	}
	return false
}

// Seen returns, of paths of a repository at a directory, those a run
// would read: not named by the repository's ignore file, and in no
// directory whose name begins with a dot, which a run does not look
// into. A path that ends in a slash is a directory, and is seen where
// what is in it would be.
func Seen(directory string, paths []string) ([]string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	ignore, err := readIgnores(root)
	if err != nil {
		return nil, err
	}
	var seen []string
	for _, path := range paths {
		name, isDir := strings.TrimSuffix(path, "/"), strings.HasSuffix(path, "/")
		parts := strings.Split(name, "/")
		hidden := false
		for i, part := range parts {
			if strings.HasPrefix(part, ".") && (isDir || i < len(parts)-1) {
				hidden = true
			}
		}
		if name == "" || hidden || ignore.ignored(name, isDir) {
			continue
		}
		seen = append(seen, path)
	}
	return seen, nil
}
