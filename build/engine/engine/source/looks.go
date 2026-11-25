package source

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

// Looks is how the files a run of a tree would list looked at one
// moment, with files named beside them: what whoever builds again when
// something changed (`frontseat build --watch`) holds one moment against
// another by. Which files there are is a run's own listing (list), with
// what it leaves out, and how a file looks is the keeper's own look
// (look): there is one of each, so a file a run would read otherwise
// looks otherwise here.
//
// Nothing is read for what it holds. A file written again within the
// moment its times are told in, at its size, looks as it did, so a look
// is one to hold a later one against only where every file in it had
// settled (Settled), as what a keeper keeps of a file is gone by only
// where the file had (Keeper): from then on a write shows.
//
// Each directory the listing entered, and each link it followed, is
// looked at too, by the same look: a file made and removed again, or a
// link led elsewhere and back, between two looks leaves the directory
// or the link looking otherwise, since when anything of it last changed
// is the disk's own to say. So two looks that are the same say that
// nothing a run lists changed at any moment between them, and a run
// that began between them read what the later one shows.
//
// Not held: what a run reads that it does not list, a file in a
// directory whose name begins with a dot that a plugin names, is not
// looked at.
type Looks struct {
	// at is when the look began, in nanoseconds: every file was looked
	// at after it.
	at int64
	// files is how each file looked, by its name: a file of a repository
	// by the repository's name and its path, with slashes, and a file
	// beside by the path it was named with. A file that is not there is
	// not in it.
	files map[string]looked
	// entered is how each directory listed and each link followed looked
	// itself, by its name as a file's is, a repository's own directory by
	// the repository's name.
	entered map[string]looked
}

// Look looks at the files a run of a tree would list now, and at the
// files named beside, each where it is a file.
func Look(ctx context.Context, tree Tree, beside []string) (*Looks, error) {
	return lookAt(ctx, tree, beside, time.Now())
}

// lookAt is Look, at a time a test says.
func lookAt(ctx context.Context, tree Tree, beside []string, now time.Time) (*Looks, error) {
	root, err := filepath.Abs(tree.Root)
	if err != nil {
		return nil, err
	}
	tree.Root = root
	l := &Looks{at: now.UnixNano(), files: map[string]looked{}, entered: map[string]looked{}}
	for _, repository := range tree.Repositories() {
		entered := map[string]looked{}
		t, err := tree.list(ctx, repository, entered)
		if err != nil {
			return nil, err
		}
		for name, as := range t.listed {
			l.files[path.Join(repository, name)] = as
		}
		for name, as := range entered {
			l.entered[path.Join(repository, name)] = as
		}
	}
	for _, file := range beside {
		info, err := os.Stat(file)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, err
		case info.Mode().IsRegular():
			l.files[file] = look(info)
		}
	}
	return l, nil
}

// list lists one repository of a tree, where its files are: it may be
// reached through a link, the workspace's own directory among them.
// What it entered is said where it is given somewhere to say it (list).
func (t Tree) list(ctx context.Context, repository string, entered map[string]looked) (*taken, error) {
	at := t.Directory(repository)
	directory, err := filepath.EvalSymlinks(at)
	if err != nil {
		directory = at
	}
	listed, err := list(ctx, at, directory, entered)
	if err != nil {
		return nil, fmt.Errorf("listing the repository %s: %w", repository, err)
	}
	return listed, nil
}

// Since names the files that do not look as they did in a look before:
// those that look otherwise, those that are there now and were not, and
// those that were and are not, sorted. A file moved is two: the name it
// had and the one it has. Where no file does, it names the directories
// and links that do not look as they did, a directory with a slash
// after it: what a file made and removed again between the two looks
// left. Nothing named, nothing a run lists changed between them.
func (l *Looks) Since(before *Looks) []string {
	if changed := since(l.files, before.files, ""); len(changed) > 0 {
		return changed
	}
	return since(l.entered, before.entered, "/")
}

// since names what of now does not look as it did before, sorted, a
// directory's name followed by what says it is one.
func since(now, before map[string]looked, directory string) []string {
	var changed []string
	named := func(name string, as looked) string {
		if as.mode.IsDir() {
			return name + directory
		}
		return name
	}
	for name, as := range now {
		if was, there := before[name]; !there || was != as {
			changed = append(changed, named(name, as))
		}
	}
	for name, was := range before {
		if _, there := now[name]; !there {
			changed = append(changed, named(name, was))
		}
	}
	sort.Strings(changed)
	return changed
}

// Settled reports whether every file, directory and link had settled
// when the look began, as a keeper holds a file to have (settledBy): a
// write to any of them since then shows in a later look.
func (l *Looks) Settled() bool {
	for _, of := range []map[string]looked{l.files, l.entered} {
		for _, as := range of {
			if !settledBy(as, l.at) {
				return false
			}
		}
	}
	return true
}
