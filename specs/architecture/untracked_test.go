package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// checkout reports whether a repository's root is a checkout, which git
// is asked about; a test action is given the repository's source alone.
func checkout(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// gitView is what a repository's own .gitignore files say git would not
// track, read as git reads them: never this machine's git settings.
type gitView struct{ gitignore.Matcher }

func untracked(t *testing.T, root string) gitView {
	t.Helper()
	var patterns []gitignore.Pattern
	err := filepath.WalkDir(root, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && at != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != ".gitignore" {
			return nil
		}
		content, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(at))
		if err != nil {
			return err
		}
		var domain []string
		if rel != "." {
			domain = strings.Split(filepath.ToSlash(rel), "/")
		}
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
				patterns = append(patterns, gitignore.ParsePattern(line, domain))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return gitView{gitignore.NewMatcher(patterns)}
}

// files are the regular files beneath the root that git would track,
// as Frontseat's source is: no directory whose name begins with a dot is
// looked in.
func (g gitView) files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(at string, entry fs.DirEntry, err error) error {
		if err != nil || at == root {
			return err
		}
		rel, err := filepath.Rel(root, at)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || g.Match(parts, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() && !g.Match(parts, false) {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
