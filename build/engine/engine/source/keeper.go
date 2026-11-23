package source

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Keeper is what a daemon keeps of its workspace's files from one run
// to the next: what each file held, for as long as the file looks as it
// did then. A run that begins is told, and reads only the files that
// look different or that no run has read.
//
// How a file looks is its size, when it was written, when anything of
// it last changed, which file of the disk it is, and what may be done
// with it. Reading every file for every command is what a build of any
// size cannot afford, and Bazel and Buck2 go by the same.
//
// A file written again within the moment its times are told in looks
// as it did, where its size is the same: Linux before 6.13 tells them
// in the kernel's ticks, a few milliseconds. So what a file held is
// gone by its look only where it was read once the file had settled:
// twenty milliseconds after it was last written or changed, or two
// seconds where its times are told in whole seconds. What was read
// sooner is not kept for the run after, which reads the file again,
// and the run that read it reads it again whenever it is asked, until
// it has settled: by then a change to it shows. Git holds its index to
// the same ("racily clean" entries), and
// Bazel waits the moment out at the end of a build
// (TimestampGranularityMonitor).
type Keeper struct {
	// now is the time; time.Now unless a test says otherwise.
	now func() time.Time

	mu    sync.Mutex
	files map[string]kept
}

// settles is how long after a file was last written or changed what it
// held is not kept: longer than the moment its times are told in.
func settles(as looked) time.Duration {
	if newest(as)%int64(time.Second) == 0 {
		return 2 * time.Second
	}
	return 20 * time.Millisecond
}

// newest is when a file was last written or changed, in nanoseconds.
func newest(as looked) int64 { return max(as.written, as.changed) }

// Take lists the files of each repository as they are now: the source
// of a run that begins now.
func (k *Keeper) Take(ctx context.Context, tree Tree) (*Snapshot, error) {
	// The workspace is named in full: a file is known by where it is,
	// whatever directory the daemon is in.
	root, err := filepath.Abs(tree.Root)
	if err != nil {
		return nil, err
	}
	tree.Root = root
	s := &Snapshot{Tree: tree, keeper: k, repositories: map[string]*taken{}}
	for _, repository := range tree.Repositories() {
		// A repository may be reached through a link, the workspace's own
		// directory among them: its files are where the link leads.
		at := tree.directory(repository)
		directory, err := filepath.EvalSymlinks(at)
		if err != nil {
			directory = at
		}
		t, err := list(ctx, at, directory)
		if err != nil {
			return nil, fmt.Errorf("listing the repository %s: %w", repository, err)
		}
		s.repositories[repository] = t
		k.only(t)
	}
	return s, nil
}

// kept returns what a file held when it looked a certain way, if that
// is known.
func (k *Keeper) kept(at string, as looked) (kept, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	known, there := k.files[at]
	if !there || known.looked != as {
		return kept{}, false
	}
	return known, true
}

// settled reports whether a file that looks so has settled by now: the
// moment its times are told in has passed since it was last written or
// changed, so that a write from now on shows in how it looks.
//
// A file said to be written at a time still to come is gone by when it
// last changed: that time is set only by changing the file, which the
// disk dates itself, so it would otherwise be read for every ask until
// the day it names. Not held: the daemon's clock is held against the
// disk's, and a disk whose clock runs behind the daemon's by more than
// the moment (a remote one) has a file settle too soon.
func (k *Keeper) settled(as looked) bool {
	now := time.Now
	if k.now != nil {
		now = k.now
	}
	at, last := now().UnixNano(), newest(as)
	if as.written > at && as.changed <= at {
		last = as.changed
	}
	return at-last >= int64(settles(as))
}

// keep records what a file held, and how it looked; or lets go of what
// was known of it, where it was read before it had settled, which
// whoever read it says: it is so of the moment before the read.
func (k *Keeper) keep(at string, found kept) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !found.settled {
		delete(k.files, at)
		return
	}
	if k.files == nil {
		k.files = map[string]kept{}
	}
	k.files[at] = found
}

// forget lets go of what was known of a file.
func (k *Keeper) forget(at string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.files, at)
}

// only lets go of what was known of the files of a repository that are
// not listed in it now, so that what is kept is no more than what is
// there.
func (k *Keeper) only(t *taken) {
	k.mu.Lock()
	defer k.mu.Unlock()
	beneath := t.directory + string(filepath.Separator)
	for at := range k.files {
		rel, within := strings.CutPrefix(at, beneath)
		if !within {
			continue
		}
		if _, listed := t.listed[filepath.ToSlash(rel)]; !listed {
			delete(k.files, at)
		}
	}
}

// looked is how a file looked without its content being read.
type looked struct {
	size int64
	// written and changed are when its content was last written and when
	// anything of it last changed, in nanoseconds.
	written, changed int64
	// file is which file of the disk it is.
	file uint64
	mode fs.FileMode
}

// look returns how a file looks.
func look(info fs.FileInfo) looked {
	file, changed := identity(info)
	return looked{size: info.Size(), written: info.ModTime().UnixNano(), changed: changed, file: file, mode: info.Mode()}
}
