package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/google/renameio/v2"

	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

// Snapshot is the repositories of a workspace as they were when a run
// began: the one source everything the run does reads, from what its
// plugins are told to what its actions are sent.
//
// The files there are, and how each looks, are taken once, when the run
// begins. What a file holds is taken the first time the run asks, and is
// what the file is for as long as the run lasts. A file that is found to
// be other than it was, by how it looks or by its content, fails
// whatever reads it, with the file named: a run is never of files from
// two moments.
//
// What is in a directory whose name begins with a dot is not listed,
// and is read where it is named. What the repository's own ignore file
// names (IgnoreFile) is not part of the repository to the run: neither
// listed nor read where it is named.
type Snapshot struct {
	Tree

	keeper *Keeper
	// hashed counts the files read with nothing known of what they held.
	hashed atomic.Int64

	mu           sync.Mutex
	repositories map[string]*taken
}

// taken is one repository of a snapshot. What is listed, what is held
// and the names change as the run reads and rewrites, under the
// snapshot's lock. A change gives the names a new slice and never writes
// over the old one, so a reader that took the names under the lock reads
// them after it without it (names).
type taken struct {
	// at is the repository's directory as the workspace names it, and
	// directory the same with any link on the way to it followed: where
	// its files are read.
	at, directory string
	// names are the files listed, sorted.
	names []string
	// listed is how each file listed looked when the run began, or when
	// the run last wrote it.
	listed map[string]looked
	// held is what the run knows each file it has asked about held.
	held map[string]kept
	// absent are the files that are not listed, that the run asked for
	// and that were not there: to the run they stay so.
	absent map[string]bool
	// ignores is what the repository's ignore file names.
	ignores ignores
}

// held is what a file held.
type held struct {
	hash       string
	size       int64
	executable bool
}

// kept is what a file held, and how it looked when it held it.
type kept struct {
	looked looked
	held   held
	// settled is whether the file had settled when what it held was
	// read: the moment its times are told in had passed, so that any
	// write since shows in how it looks. What was read sooner is held to
	// by reading again, and never by the look alone.
	settled bool
}

// Changed is a file that is not what it was when the run began, or not
// what the run read of it.
type Changed struct {
	// Repository is the workspace's name for the repository.
	Repository string
	// Path is the file, relative to the repository.
	Path string
}

func (c *Changed) Error() string {
	return fmt.Sprintf("%s changed while the run was under way: a run is of the files as they were when it began", path.Join(c.Repository, c.Path))
}

// list lists the files under a directory. A link is the file it leads
// to where that is a file under the directory, and nothing where it is
// not.
func list(ctx context.Context, at, directory string) (*taken, error) {
	t := &taken{at: at, directory: directory, listed: map[string]looked{}, held: map[string]kept{}, absent: map[string]bool{}}
	root, err := os.OpenRoot(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if t.ignores, err = readIgnores(root); err != nil {
		return nil, err
	}
	err = filepath.WalkDir(directory, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if at == directory {
			return ctx.Err()
		}
		rel, err := filepath.Rel(directory, at)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if entry.IsDir() {
			// Not entered: what is beneath is never looked at, as git
			// never looks beneath a directory it ignores.
			if strings.HasPrefix(entry.Name(), ".") || t.ignores.ignored(name, true) {
				return filepath.SkipDir
			}
			return ctx.Err()
		}
		if t.ignores.ignored(name, false) {
			return nil
		}
		var info fs.FileInfo
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			// Through the root, so that a link out of the repository leads
			// nowhere.
			if info, err = root.Stat(rel); err != nil {
				return nil
			}
		case entry.Type().IsRegular():
			if info, err = entry.Info(); errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
		default:
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		t.names = append(t.names, name)
		t.listed[name] = look(info)
		return nil
	})
	sort.Strings(t.names)
	return t, err
}

// Hashed returns how many files the run has read with nothing known of
// what they held: not by the run, and not by the daemon of the file as
// it looked. A file read for its content, where what it holds is known,
// is not counted.
func (s *Snapshot) Hashed() int { return int(s.hashed.Load()) }

// Read returns a file of a repository, and whether it is there for
// the run. The path is whatever asked for it wrote, a plugin among
// them, so it is held to the repository: one that leaves it, by its own
// spelling or through a link, is an error and not a file that is not
// there.
func (s *Snapshot) Read(_ context.Context, repository, name string) ([]byte, bool, error) {
	var content []byte
	_, found, err := s.ask(repository, name, reading(&content))
	if err != nil || !found {
		return nil, found, err
	}
	return content, true, nil
}

// reading reads an opened file whole into content, and says the hash
// and the size of what it read: how Read reads a file, for ask to
// record.
func reading(content *[]byte) func(*os.File) (string, int64, error) {
	return func(file *os.File) (hash string, size int64, err error) {
		if *content, err = io.ReadAll(file); err != nil {
			return "", 0, err
		}
		sum := sha256.Sum256(*content)
		return hex.EncodeToString(sum[:]), int64(len(*content)), nil
	}
}

// Digest returns the hash of what a file of a repository held, and
// whether the file is there for the run. The file is read only where
// nothing is known of it as it looks.
func (s *Snapshot) Digest(_ context.Context, repository, name string) (string, bool, error) {
	was, found, err := s.ask(repository, name, nil)
	return was.hash, found, err
}

// Glob returns the files of a repository that match patterns, sorted:
// of those that were there when the run began.
func (s *Snapshot) Glob(_ context.Context, repository string, patterns []string) ([]string, error) {
	names := s.names(repository)
	// The names are sorted, so those beneath a directory are together:
	// only the directories the patterns are written beneath are looked
	// in, each from its first name to its last, and a file of a
	// directory's own name with them, which "a/**" selects too. They are
	// taken in the order their names come in, which is not the order of
	// the directories' own names, and none is looked at twice.
	directories, anywhere := pattern.Under(patterns)
	spans := [][2]int{{0, len(names)}}
	if !anywhere {
		spans = spans[:0]
		for _, directory := range directories {
			if i := sort.SearchStrings(names, directory); i < len(names) && names[i] == directory {
				spans = append(spans, [2]int{i, i + 1})
			}
			spans = append(spans, beneath(names, directory))
		}
		sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	}
	var out []string
	from := 0
	for _, span := range spans {
		for i := max(span[0], from); i < span[1]; i++ {
			if pattern.Select(patterns, names[i]) {
				out = append(out, names[i])
			}
		}
		from = max(from, span[1])
	}
	return out, nil
}

// names returns the files of a repository listed now, sorted. What it
// returns is never written over: a change replaces the names.
func (s *Snapshot) names(repository string) []string {
	t := s.repositories[repository]
	if t == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return t.names
}

// beneath returns where the names beneath a directory are among sorted
// names: from the first of them to after the last.
func beneath(names []string, directory string) [2]int {
	// "/" is followed by "0": every name beneath the directory is
	// between the two.
	return [2]int{sort.SearchStrings(names, directory+"/"), sort.SearchStrings(names, directory+"0")}
}

// Within returns the files beneath a directory of a repository that
// patterns select, the patterns being written from the directory:
// what a project's actions name of the project's own files. The
// directory is a name and no pattern, whatever characters it holds.
func (s *Snapshot) Within(_ context.Context, repository, directory string, patterns []string) ([]string, error) {
	names := s.names(repository)
	directory = path.Clean(directory)
	span, cut := [2]int{0, len(names)}, 0
	if directory != "." {
		span, cut = beneath(names, directory), len(directory)+1
	}
	var out []string
	for _, name := range names[span[0]:span[1]] {
		if pattern.Select(patterns, name[cut:]) {
			out = append(out, name)
		}
	}
	return out, nil
}

// Select returns the union of scoped source groups, once per file, in order.
// Keeping each literal directory apart from its patterns prevents directory
// names containing glob characters from selecting a neighbour's files.
func (s *Snapshot) Select(ctx context.Context, repository string, groups []pattern.Group) ([]string, error) {
	if len(groups) == 1 {
		return s.Within(ctx, repository, groups[0].Directory, groups[0].Patterns)
	}
	var out []string
	for _, group := range groups {
		files, err := s.Within(ctx, repository, group.Directory, group.Patterns)
		if err != nil {
			return nil, err
		}
		out = append(out, files...)
	}
	if len(groups) > 1 {
		slices.Sort(out)
		out = slices.Compact(out)
	}
	return out, nil
}

// File returns what the file at a path held: its content's hash and
// size, and whether it may be run. The path is one where the daemon
// runs, and is a file of one of the repositories; the content is not
// kept, so what is sent of the file later is held to the hash by
// whoever takes it.
func (s *Snapshot) File(at string) (hash string, size int64, executable bool, err error) {
	repository, name, err := s.within(at)
	if err != nil {
		return "", 0, false, err
	}
	was, found, err := s.ask(repository, name, nil)
	if err != nil {
		return "", 0, false, err
	}
	if !found {
		return "", 0, false, fmt.Errorf("%s: %w", path.Join(repository, name), fs.ErrNotExist)
	}
	return was.hash, was.size, was.executable, nil
}

// Content returns what the file at a path held, the path being one
// where the daemon runs, as File's is.
func (s *Snapshot) Content(at string) ([]byte, error) {
	repository, name, err := s.within(at)
	if err != nil {
		return nil, err
	}
	content, found, err := s.Read(context.Background(), repository, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s: %w", path.Join(repository, name), fs.ErrNotExist)
	}
	return content, nil
}

// Change is a change to one file of a repository, worked out from the
// file as the run read it, to be applied over that and nothing else.
type Change struct {
	// Path is the file, relative to the repository.
	Path string
	// Kind is what the change is.
	Kind Kind
	// Content is the file's bytes after the change; nothing for a delete.
	Content []byte
	// Executable is whether the file may be run after the change.
	Executable bool
	// From is the hash of the file as the run read it, which the change
	// was worked out from; empty for a create.
	From string
}

// Kind is what a change is.
type Kind int

const (
	// Replace: the file holds the content, and is executable or not, as
	// the change says.
	Replace Kind = iota + 1
	// Create: a file that is not there is written; where one is, the
	// change is refused.
	Create
	// Delete: the file is removed.
	Delete
)

// Apply applies a change to a file of a repository, where the file is
// still what the change was worked out from: a replacement or a
// deletion is of a file the run has read, holding what the change's From
// names, and not what its author wrote since or what the run itself
// wrote since, from which the change was not worked out; a creation is
// of a file that is not there. A file is written beside itself and moved
// into place, so it is the old one or the new one and never part of
// each; a link is followed to the file it leads to, within the
// repository, and stays a link. To the run, the file is then as the
// change left it. A path that leaves the repository, or that the
// repository's ignore file names, is refused.
func (s *Snapshot) Apply(repository string, change Change) error {
	t := s.repositories[repository]
	if t == nil {
		return fmt.Errorf("there is no repository %q", repository)
	}
	key := path.Clean(filepath.ToSlash(change.Path))
	shown := path.Join(repository, key)
	if change.Path == "" || key == "." || path.IsAbs(key) || !filepath.IsLocal(filepath.FromSlash(key)) {
		return fmt.Errorf("%s is not a file of the repository", shown)
	}
	if t.ignores.ignored(key, false) {
		return fmt.Errorf("%s is a file the repository ignores", shown)
	}
	// Whatever asks about the file from here on waits, and finds it as
	// the change left it.
	s.mu.Lock()
	defer s.mu.Unlock()
	switch change.Kind {
	case Replace, Delete:
		known, read := t.held[key]
		if !read {
			return fmt.Errorf("%s is changed before the run has read it", shown)
		}
		if known.held.hash != change.From {
			return fmt.Errorf("%s is no longer what this was worked out from: the run has changed it since", shown)
		}
		file, err := t.led(key)
		if err != nil {
			return fmt.Errorf("%s: %w", shown, err)
		}
		on, err := os.ReadFile(file)
		if err != nil {
			return &Changed{Repository: repository, Path: key}
		}
		if sum := sha256.Sum256(on); hex.EncodeToString(sum[:]) != change.From {
			return &Changed{Repository: repository, Path: key}
		}
		if change.Kind == Delete {
			// A link is not deleted for what it leads to: the file would
			// be gone and the link not, and the run would know neither.
			if file != filepath.Join(t.directory, filepath.FromSlash(key)) {
				return fmt.Errorf("%s is a link, and is not deleted for what it leads to", shown)
			}
			return s.remove(t, repository, key, file)
		}
		was, err := os.Stat(file)
		if err != nil {
			return err
		}
		return s.write(t, repository, key, file, change.Content, permissions(was.Mode().Perm(), change.Executable), true)
	case Create:
		if change.From != "" {
			return fmt.Errorf("%s is created from something: a creation is of a file that is not there", shown)
		}
		if _, listed := t.listed[key]; listed {
			return fmt.Errorf("%s is there already: a creation is of a file that is not", shown)
		}
		root, err := os.OpenRoot(t.directory)
		if err != nil {
			return err
		}
		defer root.Close()
		if _, err := root.Lstat(filepath.FromSlash(key)); err == nil {
			return fmt.Errorf("%s is there already: a creation is of a file that is not", shown)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s: %w", shown, err)
		}
		if dir := path.Dir(key); dir != "." {
			// Where it is made is where it is named: not through a link,
			// which would have the run know it by a name it is not at.
			// The nearest directory there is on the way is held to that,
			// before anything is made beneath it.
			nearest := dir
			for nearest != "." {
				if _, err := root.Lstat(filepath.FromSlash(nearest)); err == nil {
					break
				}
				nearest = path.Dir(nearest)
			}
			if nearest != "." {
				led, err := filepath.EvalSymlinks(filepath.Join(t.directory, filepath.FromSlash(nearest)))
				if err != nil {
					return fmt.Errorf("%s: %w", shown, err)
				}
				if here, err := filepath.EvalSymlinks(t.directory); err != nil || led != filepath.Join(here, filepath.FromSlash(nearest)) {
					return fmt.Errorf("%s is created through a link", shown)
				}
			}
			if err := root.MkdirAll(filepath.FromSlash(dir), 0o755); err != nil {
				return fmt.Errorf("%s: %w", shown, err)
			}
		}
		return s.write(t, repository, key, filepath.Join(t.directory, filepath.FromSlash(key)), change.Content, permissions(0o644, change.Executable), false)
	default:
		return fmt.Errorf("%s: the change says not what it is", shown)
	}
}

// permissions are a file's, with whether it may be run set as asked.
func permissions(perm fs.FileMode, executable bool) fs.FileMode {
	if executable {
		return perm | 0o111
	}
	return perm &^ 0o111
}

// write writes a file beside itself and moves it into place, and records
// what the run then knows of it. Held by the snapshot's lock.
func (s *Snapshot) write(t *taken, repository, key, file string, content []byte, perm fs.FileMode, existed bool) error {
	pending, err := renameio.NewPendingFile(file, renameio.WithPermissions(perm))
	if err != nil {
		return err
	}
	defer pending.Cleanup()
	if _, err := pending.Write(content); err != nil {
		return err
	}
	wrote, err := pending.Stat()
	if err != nil {
		return err
	}
	if err := pending.CloseAtomicallyReplace(); err != nil {
		return err
	}
	// The file there now is the one that was written, or somebody put
	// another in its place at once: what is known is of the first only.
	now, err := os.Stat(file)
	if err != nil || !os.SameFile(wrote, now) {
		delete(t.held, key)
		s.keeper.forget(filepath.Join(t.directory, filepath.FromSlash(key)))
		return &Changed{Repository: repository, Path: key}
	}
	sum := sha256.Sum256(content)
	written := kept{looked: look(now), held: held{hash: hex.EncodeToString(sum[:]), size: int64(len(content)), executable: now.Mode()&0o100 != 0}}
	if _, listed := t.listed[key]; listed || !existed {
		t.listed[key] = written.looked
	}
	if !existed {
		if i, there := slices.BinarySearch(t.names, key); !there {
			// Clipped, the names are copied, never written over.
			t.names = slices.Insert(slices.Clip(t.names), i, key)
		}
		delete(t.absent, key)
	}
	t.held[key] = written
	// Just written, it has not settled: nothing of it is kept for the
	// run after, which reads it.
	s.keeper.forget(filepath.Join(t.directory, filepath.FromSlash(key)))
	// Written through a link, the file it leads to is what was written
	// too, and is known as that by its own name.
	if direct := filepath.Join(t.directory, filepath.FromSlash(key)); file != direct {
		if rel, err := filepath.Rel(t.directory, file); err == nil && filepath.IsLocal(rel) {
			target := filepath.ToSlash(rel)
			if _, listed := t.listed[target]; listed {
				t.listed[target] = written.looked
			}
			if _, held := t.held[target]; held {
				t.held[target] = written
			}
			s.keeper.forget(file)
		}
	}
	return nil
}

// remove removes a file and records that, to the run, it is gone. Held
// by the snapshot's lock.
func (s *Snapshot) remove(t *taken, repository, key, file string) error {
	if err := os.Remove(file); err != nil {
		return fmt.Errorf("%s: %w", path.Join(repository, key), err)
	}
	delete(t.held, key)
	delete(t.listed, key)
	if i, there := slices.BinarySearch(t.names, key); there {
		t.names = slices.Concat(t.names[:i], t.names[i+1:])
	}
	t.absent[key] = true
	s.keeper.forget(filepath.Join(t.directory, filepath.FromSlash(key)))
	return nil
}

// led returns where a file of the repository is, any link it is
// followed: the file a link within the repository leads to. A link that
// leads out of the repository leads nowhere.
func (t *taken) led(key string) (string, error) {
	root, err := os.OpenRoot(t.directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for range 16 {
		info, err := root.Lstat(filepath.FromSlash(key))
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return filepath.Join(t.directory, filepath.FromSlash(key)), nil
		}
		to, err := root.Readlink(filepath.FromSlash(key))
		if err != nil {
			return "", err
		}
		next := path.Join(path.Dir(key), filepath.ToSlash(to))
		if filepath.IsAbs(to) || !filepath.IsLocal(filepath.FromSlash(next)) {
			return "", errors.New("it is a link that leaves the repository")
		}
		key = next
	}
	return "", errors.New("it is a link that leads to a link, and so on without end")
}

// within returns the repository a path of the daemon's is in, and the
// path within it. The path is one made from the snapshot's own root, as
// every action's is, or the same place by another name.
func (s *Snapshot) within(at string) (repository, name string, err error) {
	// As it is written: nothing of the disk is asked, for what is asked
	// for every input of every action.
	if repository, name, found := s.beneath(at); found {
		return repository, name, nil
	}
	if abs, err := filepath.Abs(at); err == nil {
		at = abs
	}
	if repository, name, found := s.beneath(at); found {
		return repository, name, nil
	}
	if led, err := filepath.EvalSymlinks(filepath.Dir(at)); err == nil {
		if repository, name, found := s.beneath(filepath.Join(led, filepath.Base(at))); found {
			return repository, name, nil
		}
	}
	return "", "", fmt.Errorf("%s is in no repository of the workspace", at)
}

// beneath returns the repository a path is written beneath, the deepest
// where one repository is within another, and the path within it.
func (s *Snapshot) beneath(at string) (repository, name string, found bool) {
	longest := -1
	for each, t := range s.repositories {
		for _, directory := range [2]string{t.at, t.directory} {
			if len(directory) <= longest || len(at) <= len(directory) || at[len(directory)] != filepath.Separator || !strings.HasPrefix(at, directory) {
				continue
			}
			rest := at[len(directory)+1:]
			if !filepath.IsLocal(rest) {
				continue
			}
			longest, repository, name = len(directory), each, filepath.ToSlash(rest)
		}
	}
	return repository, name, longest >= 0
}

// knew is what the run knew of a file when something began to ask about
// it. What is found is kept only if the run still knows the same: the
// run may have rewritten the file meanwhile, and then what was found is
// of a file that is no longer the run's.
type knew struct {
	was    looked
	listed bool
	known  kept
	asked  bool
	absent bool
}

// knows returns what the run knows of a file now.
func (s *Snapshot) knows(t *taken, key string) knew {
	s.mu.Lock()
	defer s.mu.Unlock()
	var k knew
	k.was, k.listed = t.listed[key]
	k.known, k.asked = t.held[key]
	k.absent = t.absent[key]
	return k
}

// keep records what was found of a file, if the run still knows of it
// what it knew when the asking began, and reports whether it did.
func (s *Snapshot) keep(t *taken, key string, when knew, found *kept) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var now knew
	now.was, now.listed = t.listed[key]
	now.known, now.asked = t.held[key]
	now.absent = t.absent[key]
	if now != when {
		return false
	}
	if found == nil {
		t.absent[key] = true
	} else {
		t.held[key] = *found
	}
	return true
}

// ask returns what a file of a repository held, and whether the file is
// there for the run. The file is held to what it was: to how it looked
// when the run began, and to what the run knew of it before.
//
// Where read is given, the file is read through it, which returns the
// hash and the size of what it read. Where it is not, the file is read
// only if nothing is known of it as it looks: not by this run, and not
// by the daemon from a run before.
//
// The run may rewrite the file while it is asked about. What was found
// is then of a file that is no longer the run's, and it is asked about
// again.
func (s *Snapshot) ask(repository, name string, read func(*os.File) (hash string, size int64, err error)) (held, bool, error) {
	t := s.repositories[repository]
	if t == nil {
		return held{}, false, nil
	}
	key := path.Clean(filepath.ToSlash(name))
	if t.ignores.ignored(key, false) {
		// Named or not, what the repository ignores is not there to the run.
		return held{}, false, nil
	}
	for range 100 {
		when := s.knows(t, key)
		found, there, err := s.once(t, repository, name, key, when, read)
		if errors.Is(err, errAgain) {
			continue
		}
		var changed *Changed
		if errors.As(err, &changed) && s.knows(t, key) != when {
			continue
		}
		return found, there, err
	}
	return held{}, false, fmt.Errorf("reading %s of the repository: it was rewritten each time it was read", name)
}

// errAgain is what was found of a file the run has rewritten since the
// asking began.
var errAgain = errors.New("the run rewrote the file while it was asked about")

// once asks about a file, given what the run knew of it when the asking
// began.
func (s *Snapshot) once(t *taken, repository, name, key string, when knew, read func(*os.File) (hash string, size int64, err error)) (held, bool, error) {
	at := filepath.Join(t.directory, filepath.FromSlash(key))
	if when.absent {
		// The run asked before and it was not there: to the run it is not.
		return held{}, false, nil
	}
	known, asked := when.known, when.asked
	if read == nil && when.listed {
		// Asked only what it held, a file that was listed is looked at
		// and not opened: it is the file that was listed, or it does not
		// look as that one did. Nothing is read of it here.
		info, err := os.Stat(at)
		if err != nil || !info.Mode().IsRegular() || look(info) != when.was {
			return held{}, false, &Changed{Repository: repository, Path: key}
		}
		if !asked {
			known, asked = s.keeper.kept(at, when.was)
		}
		// What was read before the file had settled is not gone by as
		// the file looks: written again within the moment its times are
		// told in, it looks the same. It is read again, below.
		if asked && known.settled {
			if !s.keep(t, key, when, &known) {
				return held{}, false, errAgain
			}
			return known.held, true, nil
		}
	}

	root, err := os.OpenRoot(t.directory)
	if errors.Is(err, fs.ErrNotExist) {
		return s.gone(t, repository, key, when)
	}
	if err != nil {
		return held{}, false, err
	}
	defer root.Close()
	// Not waited on: what is opened may be a pipe by now, which nothing
	// writes to.
	file, err := root.OpenFile(filepath.FromSlash(name), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return s.gone(t, repository, key, when)
	}
	if err != nil {
		return held{}, false, fmt.Errorf("reading %s of the repository: %w", name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return held{}, false, err
	}
	now := look(info)
	// Whether the file has settled is asked before any of it is read: a
	// write from here on then shows in how it looks, which is looked at
	// again once it has been read.
	settled := s.keeper.settled(now)
	switch {
	case when.listed:
		if !info.Mode().IsRegular() || now != when.was {
			return held{}, false, &Changed{Repository: repository, Path: key}
		}
	case !info.Mode().IsRegular():
		return held{}, false, fmt.Errorf("reading %s of the repository: it is not a file", name)
	case !hidden(key):
		// It would have been listed had it been there: it came after the
		// run began, and is of the next one.
		return held{}, false, nil
	case when.asked && when.known.looked != now:
		return held{}, false, &Changed{Repository: repository, Path: key}
	}
	// mine is whether the run itself has gone by what the file held.
	mine := when.asked
	if !asked {
		// What the daemon knew the file held when it looked as it does.
		known, asked = s.keeper.kept(at, now)
	}
	if read == nil && asked && known.settled {
		if !s.keep(t, key, when, &known) {
			return held{}, false, errAgain
		}
		return known.held, true, nil
	}
	if read == nil {
		read = func(file *os.File) (string, int64, error) {
			sum := sha256.New()
			size, err := io.Copy(sum, file)
			return hex.EncodeToString(sum.Sum(nil)), size, err
		}
	}
	if !asked {
		s.hashed.Add(1)
	}
	sum, size, err := read(file)
	if err != nil {
		return held{}, false, fmt.Errorf("reading %s of the repository: %w", name, err)
	}
	// What was read is of the file as it looked when it was opened only
	// if it looks the same now that it has been read.
	if after, err := file.Stat(); err != nil || look(after) != now {
		return held{}, false, &Changed{Repository: repository, Path: key}
	}
	found := kept{looked: now, settled: settled, held: held{hash: sum, size: size, executable: info.Mode()&0o100 != 0}}
	if mine && when.known.held != found.held {
		// Whatever was known of it is not so, for the next run either.
		s.keeper.forget(at)
		return held{}, false, &Changed{Repository: repository, Path: key}
	}
	if !s.keep(t, key, when, &found) {
		return held{}, false, errAgain
	}
	s.keeper.keep(at, found)
	return found.held, true, nil
}

// gone is what is said of a file that is not there: that it has changed,
// where it was there for the run; and that it is not there, which is
// what the run is then held to.
func (s *Snapshot) gone(t *taken, repository, key string, when knew) (held, bool, error) {
	switch {
	case when.listed, when.asked:
		return held{}, false, &Changed{Repository: repository, Path: key}
	case hidden(key) && !s.keep(t, key, when, nil):
		return held{}, false, errAgain
	}
	return held{}, false, nil
}

// hidden says whether a file is under a directory that is not listed.
func hidden(name string) bool {
	directory := path.Dir(name)
	for _, segment := range strings.Split(directory, "/") {
		if strings.HasPrefix(segment, ".") && segment != "." {
			return true
		}
	}
	return false
}
