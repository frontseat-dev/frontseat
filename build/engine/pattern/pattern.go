// Package pattern is the patterns the contracts name files by: what a
// plugin's interests, an action's inputs and a glob through the host are
// written in, in the language the plugin contract says (host.proto). One
// pattern is matched as doublestar matches it, which is what Gazelle
// globs with, once it is held to that language (Spelled); that a pattern
// can exclude, and that the last to match decides, is said here.
package pattern

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// IsReference reports whether an input of an action is no pattern of
// source but names what another action left: it starts with ":" or
// "//". What follows is read where a workspace's actions are known.
func IsReference(input string) bool {
	return strings.HasPrefix(input, ":") || strings.HasPrefix(input, "//")
}

// Local reports whether a path stays where it is relative to: it is
// there, it is no full path, and no step of it leads up and out.
func Local(at string) bool {
	return at != "" && !strings.HasPrefix(at, "/") && filepath.IsLocal(filepath.FromSlash(at))
}

// Valid reports whether a pattern is one an action's inputs take: one
// of the contract's language (Spelled), with "!" before it or without,
// of files beneath where it is written.
func Valid(p string) bool {
	plain := strings.TrimPrefix(p, "!")
	return Spelled(plain) && Local(plain)
}

// Spelled reports whether a pattern is written in the language the
// plugin contract says (host.proto): one doublestar reads, with none of
// what the contract leaves out of it, where what doublestar makes of a
// pattern is no rule a reader could write down: an empty part (a "/" at
// either end, or two together), a run of three stars or more, a "/" in
// a class, in braces or escaped, and braces with nothing in them. A
// pattern that is not one selects nothing.
func Spelled(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "//") || strings.Contains(p, "***") || !doublestar.ValidatePattern(p) {
		return false
	}
	class, braces := false, 0
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '\\':
			if i+1 < len(p) && p[i+1] == '/' {
				return false
			}
			i++
		case class:
			if c == '/' {
				return false
			}
			class = c != ']'
		case c == '[':
			class = true
		case c == '{':
			if i+1 < len(p) && p[i+1] == '}' {
				return false
			}
			braces++
		case c == '}' && braces > 0:
			braces--
		case c == '/' && braces > 0:
			return false
		}
	}
	return true
}

// Spelling keeps of patterns those that are Spelled, in their order: as
// a list, they select what they selected, since one that is not selects
// nothing and decides nothing.
func Spelling(patterns []string) []string {
	kept := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if Spelled(strings.TrimPrefix(p, "!")) {
			kept = append(kept, p)
		}
	}
	return kept
}

// Select reports whether patterns select a path. A pattern that starts
// with "!" excludes what it matches, and the last pattern to match
// decides. "*" stands for any run of characters within a name, and "**"
// for any directories, none included.
func Select(patterns []string, p string) bool {
	keep := false
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		if matched, err := doublestar.Match(strings.TrimPrefix(pattern, "!"), p); err == nil && matched {
			keep = !negated
		}
	}
	return keep
}

// Selection intersects nested input-set scopes. A generated file must match
// every scope that contains the set accepting it; a sibling cannot reopen a
// path one of those scopes excluded.
type Selection [][]string

// Group selects source within one literal repository-relative directory.
// Patterns are ordered within the group; groups are unioned, so an exclusion
// in one declaration cannot remove an input declared by another. Generated
// retains only this group's explicitly accepting input-set selections.
// Committed is whether what it selects is read as the repository holds it
// at its commit, generated source included as committed, in place of
// what the build generates: what a catalog file names from the
// repository's root, as the repository's own rules read it.
type Group struct {
	Directory string
	Patterns  []string
	Generated []Selection
	Committed bool
}

// Accepts reports whether this group selects a repository-relative file.
func (g Group) Accepts(file string) bool { return Within(g.Directory, file, g.Patterns) }

// Accepts reports whether all the scopes accept a relative path.
func (s Selection) Accepts(file string) bool {
	if len(s) == 0 {
		return false
	}
	for _, scope := range s {
		if !Select(scope, file) {
			return false
		}
	}
	return true
}

// Within matches a repository path beneath a literal directory. A directory
// name never becomes part of a glob, including names containing '[' or '*'.
func Within(directory, file string, patterns []string) bool {
	// One directory, however it is spelled: "app/.", "./app" and "app/"
	// are app, as the snapshot selects by. One that leaves the
	// repository stays as it is, and holds no file of it.
	if directory = path.Clean(directory); directory != "." {
		var inside bool
		file, inside = strings.CutPrefix(file, directory+"/")
		if !inside {
			return false
		}
	}
	return Select(patterns, file)
}

// Under returns the directories a path is, or is beneath, if patterns
// select it, sorted, or says that it may be anywhere: "a/**" selects
// what is beneath a, and a itself. Whoever holds many paths looks at
// those and no others; what it finds there is still to be asked of
// Select, which this is never narrower than. Patterns that select
// nothing are under no directory.
//
// A pattern is under the directories it names before its first "*" or
// other character that stands for more than itself, as doublestar
// splits it. What a pattern leaves out narrows nothing.
func Under(patterns []string) (directories []string, anywhere bool) {
	seen := map[string]bool{}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		// A character escaped is itself in a path and two in a pattern.
		if strings.Contains(pattern, `\`) {
			return nil, true
		}
		base, _ := doublestar.SplitPattern(pattern)
		if base == "." || base == "" || base == "/" {
			return nil, true
		}
		if !seen[base] {
			seen[base] = true
			directories = append(directories, base)
		}
	}
	sort.Strings(directories)
	return directories, false
}
