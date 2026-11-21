// Package git is git, as a workspace asks a version control system: it
// clones and widens a repository, says where it stands, reads its
// history and tags it. Git is run, and a remote is read, as frontseat's
// one package for git does it (server/git), and so runs none of the
// programs that package turns off. Only a clone and its widening reach a
// remote, and each only the address the repository was asked for from,
// given none of the git variables of the environment but the settings
// the sandbox was told, which say how a credential is presented; what
// asks where a repository stands, its history and its tags, and what
// tags it, reaches none and fetches nothing it lacks. A repository
// within the repository, which is not served, is asked nothing: it is
// said to be uncommitted.
package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	rungit "github.com/frontseat-dev/frontseat/server/git"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

// System is git, as the program of that name on the daemon's path.
type System struct{}

var _ vcs.System = System{}
var _ vcs.History = System{}

// NameOf returns the last element of a remote's path, as git names the
// directory it clones one into.
func (System) NameOf(remote string) string { return rungit.Name(remote) }

// Revision holds a name to a commit's full hash. Git is asked for a
// commit by what it is named here, and reads anything else as a branch,
// a path or an option of its own.
func (System) Revision(name string) error {
	if len(name) != 40 && len(name) != 64 {
		return errors.New("is not a commit's full hash")
	}
	for _, c := range name {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return errors.New("is not a commit's full hash")
		}
	}
	return nil
}

// Clone clones a repository without its files' content, which git
// fetches as each is checked out, and returns the commit it stands at.
// Git runs where the clone is made, so that a remote written as a path
// is found from there.
func (System) Clone(ctx context.Context, directory string, from vcs.Source) (string, error) {
	args := []string{"clone", "--quiet", "--filter=blob:none"}
	if from.Ref != "" {
		args = append(args, "--branch", from.Ref)
	}
	if len(from.Roots) > 0 {
		args = append(args, "--sparse")
	}
	if _, err := fetching(ctx, filepath.Dir(directory), from.Remote).Run(ctx, append(args, "--", from.Remote, directory)...); err != nil {
		return "", err
	}
	cloned := fetching(ctx, directory, from.Remote)
	if len(from.Roots) > 0 {
		if _, err := cloned.Run(ctx, append([]string{"sparse-checkout", "set", "--cone", "--"}, from.Roots...)...); err != nil {
			return "", err
		}
	}
	return trimmed(ctx, cloned, "rev-parse", "HEAD")
}

// Widen adds directories to a sparse checkout, or ends its being one,
// fetching what they hold from the remote it was cloned from.
func (System) Widen(ctx context.Context, directory, remote string, roots []string) error {
	args := []string{"sparse-checkout", "disable"}
	if len(roots) > 0 {
		args = append([]string{"sparse-checkout", "add", "--"}, roots...)
	}
	_, err := fetching(ctx, directory, remote).Run(ctx, args...)
	return err
}

// Standing says the branch a repository is on, the commit it stands at,
// and the default branch of the remote it was cloned from, which git
// records as origin's HEAD.
func (System) Standing(ctx context.Context, directory string) (vcs.Standing, error) {
	g := in(ctx, directory)
	revision, err := trimmed(ctx, g, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return vcs.Standing{}, err
	}
	branch, err := trimmed(ctx, g, "branch", "--show-current")
	if err != nil {
		return vcs.Standing{}, err
	}
	origin, err := trimmed(ctx, g, "for-each-ref", "--format=%(symref)", "refs/remotes/origin/HEAD")
	if err != nil {
		return vcs.Standing{}, err
	}
	written, err := trimmed(ctx, g, "show", "--no-patch", "--format=%ct", revision)
	if err != nil {
		return vcs.Standing{}, err
	}
	committed, err := strconv.ParseInt(written, 10, 64)
	if err != nil {
		return vcs.Standing{}, fmt.Errorf("git: %s was committed at %q, which is no time", revision, written)
	}
	return vcs.Standing{Branch: branch, Revision: revision, DefaultBranch: strings.TrimPrefix(origin, "refs/remotes/origin/"), Time: committed}, nil
}

// Tags returns the repository's tags, each with the commit it names: a
// tag with a message names the commit it was made of.
func (System) Tags(ctx context.Context, directory string) ([]vcs.Tag, error) {
	g := in(ctx, directory)
	listed, err := trimmed(ctx, g, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(*objectname)", "refs/tags")
	if err != nil || listed == "" {
		return nil, err
	}
	merged, err := trimmed(ctx, g, "for-each-ref", "--merged", "HEAD", "--format=%(refname)", "refs/tags")
	if err != nil {
		return nil, err
	}
	reachable := map[string]bool{}
	for _, ref := range strings.Split(merged, "\n") {
		reachable[ref] = true
	}
	var tags []vcs.Tag
	for _, line := range strings.Split(listed, "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 {
			return nil, fmt.Errorf("git for-each-ref: a tag was listed as %q", line)
		}
		revision := fields[1]
		if fields[2] != "" {
			revision = fields[2]
		}
		tags = append(tags, vcs.Tag{Name: strings.TrimPrefix(fields[0], "refs/tags/"), Revision: revision, Reachable: reachable[fields[0]]})
	}
	return tags, nil
}

// Changes returns the commits of the history that changed a file in one
// of the directories. Each directory is given to git as a name, after
// what ends its options and with its patterns turned off.
func (s System) Changes(ctx context.Context, directory, since string, directories []string) ([]vcs.Change, error) {
	if len(directories) == 0 {
		return nil, errors.New("git log: no directory is named")
	}
	for _, each := range directories {
		if each == "" || path.IsAbs(each) || path.Clean(each) != each || each == ".." || strings.HasPrefix(each, "../") {
			return nil, fmt.Errorf("git log: %q is no directory of the repository, written plainly", each)
		}
	}
	history := "HEAD"
	if since != "" {
		if err := s.Revision(since); err != nil {
			return nil, fmt.Errorf("git log: %q %w", since, err)
		}
		history = since + "..HEAD"
	}
	args := append([]string{"--literal-pathspecs", "log", "-z", "--reverse", "--format=%H%n%ct%n%B", history, "--"}, directories...)
	logged, err := in(ctx, directory).Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var changes []vcs.Change
	for _, record := range strings.Split(logged, "\x00") {
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\n", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git log: a commit was logged as %q", record)
		}
		at, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("git log: a commit's time was logged as %q", fields[1])
		}
		changes = append(changes, vcs.Change{Revision: fields[0], Message: strings.TrimRight(fields[2], "\n"), Time: at})
	}
	return changes, nil
}

// TagName asks git whether it takes a name for a tag, by the rule it
// holds every reference to (git check-ref-format). The name is given as
// the reference it would be, so that it is never read as an option.
func (System) TagName(ctx context.Context, name string) error {
	if name == "" || strings.ContainsRune(name, 0) {
		return errors.New("is no name git takes for a tag")
	}
	if _, err := in(ctx, "").Run(ctx, "check-ref-format", "refs/tags/"+name); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("is no name git takes for a tag")
	}
	return nil
}

// status asks git status of everything, each entry as "XY path": files
// it does not keep, a submodule that stands at another commit whatever
// the repository says of looking at them, and a file's mode, which a
// repository can tell git to overlook. What a submodule's own files are
// is not asked: git would ask it as a repository of its own, which runs
// what its configuration names.
func status(ctx context.Context, g rungit.Git, more ...string) ([][2]string, error) {
	args := append([]string{"-c", "core.fileMode=true", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames", "--ignore-submodules=dirty"}, more...)
	listed, err := g.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var entries [][2]string
	for _, entry := range strings.Split(listed, "\x00") {
		if entry == "" {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, fmt.Errorf("git status: a file was listed as %q", entry)
		}
		entries = append(entries, [2]string{entry[:2], entry[3:]})
	}
	return entries, nil
}

// Uncommitted asks git status for what is not as HEAD has it, and the
// index for what git was told not to look at: a file marked as assumed
// unchanged, and one marked to be skipped that is there all the same,
// which a sparse checkout does not leave. Git says nothing of either,
// so neither can be said to be as its commit has it. Nor can anything
// where a submodule would be: one that is there, a repository of its
// own, which is not asked, or files written where one is not.
func (System) Uncommitted(ctx context.Context, directory string) ([]string, error) {
	g := in(ctx, directory)
	entries, err := status(ctx, g)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry[1]] = true
	}
	// Each entry is "T MODE OBJECT STAGE\tpath".
	indexed, err := g.Run(ctx, "ls-files", "-v", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	for _, entry := range strings.Split(indexed, "\x00") {
		described, file, found := strings.Cut(entry, "\t")
		fields := strings.Fields(described)
		if !found || len(fields) != 4 || len(fields[0]) != 1 {
			if entry == "" {
				continue
			}
			return nil, fmt.Errorf("git ls-files: a file was listed as %q", entry)
		}
		tag, mode := fields[0][0], fields[1]
		at := filepath.Join(directory, filepath.FromSlash(file))
		assumed := tag >= 'a' && tag <= 'z'
		skipped := tag == 'S' || tag == 's'
		if skipped {
			if _, err := os.Lstat(at); err != nil {
				skipped = false
			}
		}
		within := false
		if mode == "160000" {
			within, err = something(at)
			if err != nil {
				return nil, err
			}
		}
		if assumed || skipped || within {
			seen[file] = true
		}
	}
	paths := make([]string, 0, len(seen))
	for file := range seen {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	return paths, nil
}

// something says whether there is anything where a submodule would be:
// a submodule that is there, or files written where one is not, which
// git keeps nowhere and says nothing of. An empty directory, or none, as
// a clone leaves one, is nothing.
func something(at string) (bool, error) {
	info, err := os.Lstat(at)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return true, nil
	}
	dir, err := os.Open(at)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	if _, err := dir.Readdirnames(1); err == io.EOF {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// Ignored asks git status for what it ignores that is there.
func (System) Ignored(ctx context.Context, directory string) ([]string, error) {
	entries, err := status(ctx, in(ctx, directory), "--ignored=matching")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry[0] == "!!" {
			paths = append(paths, entry[1])
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// Tag writes a tag as the reference it is, only where there is none: so
// that a name is never read as an option, and two that tag at once do
// not write over each other. It is a tag with no message: a daemon has
// no one to sign one as.
func (s System) Tag(ctx context.Context, directory, name, revision string) error {
	if err := s.TagName(ctx, name); err != nil {
		return fmt.Errorf("git tag: %q %w", name, err)
	}
	if err := s.Revision(revision); err != nil {
		return fmt.Errorf("git tag: %q %w", revision, err)
	}
	g := in(ctx, directory)
	if _, err := g.Run(ctx, "cat-file", "-e", revision+"^{commit}"); err != nil {
		return fmt.Errorf("git tag: the repository has no commit %s", revision)
	}
	ref := "refs/tags/" + name
	_, written := g.Run(ctx, "update-ref", ref, revision, strings.Repeat("0", len(revision)))
	if written == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// It is there already: it is this tag where it names this commit.
	at, err := trimmed(ctx, g, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return written
	}
	if at != revision {
		return fmt.Errorf("git tag: %s names %s already, another commit", name, at)
	}
	return nil
}

// in is git as the daemon runs it in a directory to ask what is there:
// it reaches no remote and fetches nothing a partial clone lacks.
func in(ctx context.Context, directory string) rungit.Git {
	return rungit.In(ctx, directory, rungit.Options{Offline: true})
}

// fetching is git as the daemon runs it in a directory to fetch from a
// remote: that remote alone, with what its sandbox was told, which says
// how a credential is presented.
func fetching(ctx context.Context, directory, remote string) rungit.Git {
	return rungit.In(ctx, directory, rungit.Options{Told: true, Remote: remote})
}

// trimmed runs git and returns what it printed, its surrounding space
// trimmed.
func trimmed(ctx context.Context, g rungit.Git, args ...string) (string, error) {
	out, err := g.Run(ctx, args...)
	return strings.TrimSpace(out), err
}
