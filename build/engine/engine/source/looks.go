package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Looks is how files looked, without any of them being read: what
// whoever builds again when something changed (`frontseat build
// --watch`, through the build daemon's WaitSourceChanged) holds one
// moment against another by. It is either what a run listed and read,
// each file as the run held it (Snapshot.Looks), or a look taken now at
// the same files (Look, Await). Which files a tree has is a run's own
// listing (list), with what it leaves out, and how a file looks is the
// keeper's own look (look): there is one of each, so a file a run would
// read otherwise looks otherwise here.
//
// Nothing is read for what it holds. A file written again within the
// moment its times are told in, at its size, looks as it did, so a look
// at a file that had not settled when it was taken is doubted (Settled),
// as what a keeper keeps of a file is gone by only where the file had
// (Keeper): from then on a write shows. Of what a run read, only a file
// whose content the run read before it had settled is doubted, and is
// read again once it has, to know whether it holds what the run read
// (Await): a file the run only listed matters to the run by how it
// looks, and a write that leaves it looking as it did leaves the run's
// listing as it was. What is looked at beside a tree is looked at once,
// and held by one rule: a file beside a build is answered exactly when
// what it holds now differs from bytes that were used (Beside, Read,
// unlike). It is read again where it looks otherwise, had not settled
// when it was read, was read holding otherwise by two readings, or is
// said to have changed at a time still to come; one written again with
// what it held (touched, copied over, moved over by a file of the same
// bytes) is taken as it looks now, by the look it was read through too,
// and is answered by no wait (Await). A file said to have changed at a time still to come
// never settles by waiting: it keeps nothing else from being answered,
// and what of it a run read is read again whenever the rest has settled
// (Await).
//
// A directory is not looked at itself: a file made in it, or removed,
// is named by the listing, and a file the repository ignores, made in
// it, is not. A run reads no file it did not list but by name, and a
// file made and removed again since it listed is none it read. Each
// link the listing followed is looked at itself, by the same look: a
// link led elsewhere and back leaves its file looking as it did, and
// the link otherwise, since when anything of it last changed is the
// disk's own to say.
//
// A file a run reads by name that no listing takes, one beneath a
// directory whose name begins with a dot (.cargo/config.toml), is looked
// at by its name, there or not, read or not, and so is each file named
// beside the tree: the workspace's manifest, and what the daemon is
// made of; and a directory listed beside it, the daemon's plugins, by
// the names of its listing (Lists).
type Looks struct {
	// at is when the look began, in nanoseconds: every file listed was
	// looked at after it.
	at int64
	// files is how each file looked, by its name: a file of a repository
	// by the repository's name and its path, with slashes, and a file
	// beside by the path it was named with. A file that is not there is
	// not in it.
	files map[string]looked
	// links is how each link followed looked itself, by its name as a
	// file's is.
	links map[string]looked
	// doubted names, sorted, what had not settled when it was looked at:
	// a write within the moment its times are told in may have left it
	// looking as it did. read is, of those, each a run read the content
	// of, where it is and what the run read it to hold; of what a run
	// read, nothing else is doubted.
	doubted []string
	read    map[string]readAs
	// tree, named and beside are what a look again looks at: the tree's
	// listing, the files of each repository read by name that no listing
	// takes, and the files beside.
	tree   Tree
	named  map[string][]string
	beside []string
	// lists is each directory beside whose listing is held, by how the
	// names held of it end (Lists).
	lists map[string]string
	// now is the time a look again is taken at; time.Now unless a test
	// says otherwise, as the keeper's (Keeper.now).
	now func() time.Time
	// waits is whether a file or a link of a look had not settled when
	// it was taken and is not ahead: waiting settles it (steady).
	waits bool
	// whole is, of each file beside read through the look (Read), the
	// bytes its readers used, by every look this one was made of (add):
	// the file is answered exactly when what it holds now differs from
	// bytes that were used (Await).
	whole map[string]usedOf
	// records are the looks this one was made of (With, add), each
	// perhaps shared by others who wait (what the daemon is made of, a
	// build's manifest): what a wait learns of a file beside, that it
	// still holds what was read, it tells each, so that no other wait
	// reads it again for it (keep).
	records []*Looks
	// mu guards what a look shared by those who wait holds, its files,
	// what of them is doubted and read: each wait copies it (add) and
	// tells it what it learned (keep).
	mu sync.Mutex
}

// clock is the time a look again is taken at.
func (l *Looks) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Look looks at the files a run of a tree would list now, and at the
// files named beside, each where it is a file.
func Look(ctx context.Context, tree Tree, beside []string) (*Looks, error) {
	return lookTree(ctx, tree, beside, time.Now())
}

// lookTree is Look, at a time a test says.
func lookTree(ctx context.Context, tree Tree, beside []string, at time.Time) (*Looks, error) {
	root, err := filepath.Abs(tree.Root)
	if err != nil {
		return nil, err
	}
	tree.Root = root
	return (&Looks{tree: tree, beside: beside}).lookAt(ctx, at)
}

// Beside looks at files beside a tree, each by the path it is named
// with, at a time, the clock's now: what a run, or a daemon, reads
// beside a tree, looked at before it is read, so that a write after
// shows. Nothing is waited for, and nothing is read: whoever reads a
// file beside reads it through the look (Read), which holds the look to
// what was read; only that is doubted where the file had not settled by
// that time, and read again once it has (Await), to be named where it
// does not hold the very bytes that were used. A file no one read is
// held to how it looked: one that could not be read, read by no one,
// waits on no read of its own, and whatever makes it readable makes it
// look otherwise. It returns when the context ends, with its error.
func Beside(ctx context.Context, at time.Time, files ...string) (*Looks, error) {
	// Nothing is listed, so nothing fails but the context.
	l, err := (&Looks{beside: files}).lookAt(ctx, at)
	if err != nil {
		return nil, err
	}
	l.doubted = nil
	return l, nil
}

// Read reads a file beside a tree, by the path it is named with,
// through read, and holds the look to the bytes read used: the one way
// what is read beside a tree is told it, so that nothing read is left
// unheld. One rule holds what is read so: a file beside a build is
// answered exactly when what it holds now differs from bytes that were
// used (Await).
//
// The file is opened as a run opens one and looked at as it was
// opened, before any of it is read, so that a write after shows, and
// read is given what it holds. read reads it to its end: one that
// returns before it is refused, by the file's name, since what it left
// unread nothing would hold. Read says the SHA-256, in hex, of exactly
// the bytes read used, whatever was done to the file meanwhile, and the
// look holds the file to them: written as it was read, it no longer
// holds them, and is answered; touched, or written again with them, it
// still does, and is not. Where the file had not settled by the time
// the look was taken at (Beside), it is doubted: a write within the
// moment its times are told in, at its size, leaves it looking as it
// did, so it is read again once it has settled (Await).
//
// One that could not be opened is held to how it looks. One that read
// refused used no bytes that are known: the look holds it to none, so
// it is answered whenever a wait reads it again, where it looks
// otherwise or had not settled (unlike). Of a nil look, the file is
// read and nothing is held. Read is said before the look is waited on
// or added to another (With): it is no one else's yet.
func (l *Looks) Read(file string, read func(io.Reader) error) (string, error) {
	opened, info, err := openedAt(file)
	if err != nil {
		l.holds(file)(named(os.Stat(file)))
		return "", err
	}
	defer opened.Close()
	as, _ := named(info, nil)
	l.holds(file)(as, true)
	sum := sha256.New()
	reading := &toItsEnd{from: io.TeeReader(opened, sum)}
	err = read(reading)
	if err == nil && !reading.ended {
		err = fmt.Errorf("%s was not read to its end: whatever reads a file beside a build reads all of it", file)
	}
	used := usedOf{failed: true}
	if err == nil {
		used = usedOf{hashes: []string{hex.EncodeToString(sum.Sum(nil))}}
	}
	if l != nil {
		l.use(file, used, !settledBy(as, l.at))
	}
	if err != nil {
		return "", err
	}
	return used.hashes[0], nil
}

// toItsEnd is what a reader of a file beside reads (Read), and whether
// it read to the file's end.
type toItsEnd struct {
	from  io.Reader
	ended bool
}

func (r *toItsEnd) Read(p []byte) (int, error) {
	n, err := r.from.Read(p)
	r.ended = r.ended || errors.Is(err, io.EOF)
	return n, err
}

// usedOf is the bytes the readers of a file beside used (Read): the
// SHA-256 of each reading, every one, sorted and each once, and whether
// a reader failed, which used none that are known.
type usedOf struct {
	hashes []string
	failed bool
}

// heldBy reports whether a file that holds what hashes so holds the
// bytes every reading used: one content, read by every reader.
func (u usedOf) heldBy(hash string) bool {
	return !u.failed && len(u.hashes) == 1 && u.hashes[0] == hash
}

// with is what both readings used: every hash of either, in any order.
func (u usedOf) with(other usedOf) usedOf {
	hashes := slices.Concat(u.hashes, other.hashes)
	slices.Sort(hashes)
	return usedOf{hashes: slices.Compact(hashes), failed: u.failed || other.failed}
}

// doubts reports whether what was used of a file must be read again
// whatever the file looks like: two contents were used, and whatever it
// holds differs from one of them.
func (u usedOf) doubts() bool { return len(u.hashes) > 1 }

// holds is how a file beside, by the path it is named with, is held as
// it looks, where it is there: looked at again by every look after.
func (l *Looks) holds(file string) func(as looked, there bool) {
	return func(as looked, there bool) {
		if l == nil {
			return
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		if !slices.Contains(l.beside, file) {
			l.beside = append(l.beside, file)
		}
		if there {
			l.files[file] = as
		} else {
			delete(l.files, file)
		}
	}
}

// use holds a file beside to the bytes a reading used of it (whole),
// with those of every reading before, and, where it had not settled
// (doubt) or two contents were used, doubts it, to be read again.
func (l *Looks) use(file string, used usedOf, doubt bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.whole == nil {
		l.whole = map[string]usedOf{}
	}
	used = l.whole[file].with(used)
	l.whole[file] = used
	if !doubt && !used.doubts() {
		return
	}
	if i, found := slices.BinarySearch(l.doubted, file); !found {
		l.doubted = slices.Insert(l.doubted, i, file)
	}
}

// Lists holds the look to the listing of a directory beside a tree, as
// whoever listed it took it: each name in it that ends so, by its path,
// looked at as it is and not where a link leads, so that a name made,
// removed or made other than a file is answered; a name that ends
// otherwise is no part of it. A directory that cannot be listed looks
// otherwise than one that can. What the listing held is what whoever
// listed it read of it (Read): a name listed and not read is answered.
// Of a nil look, nothing is held.
func (l *Looks) Lists(directory, ending string) {
	if l == nil {
		return
	}
	if l.lists == nil {
		l.lists = map[string]string{}
	}
	l.lists[directory] = ending
}

// ahead reports whether a file that looks so is said to have changed at
// a time still to come, at a time, in nanoseconds: waiting does not
// settle it, until the clock catches the disk's up.
func ahead(as looked, at int64) bool { return lastBy(as, at) > at }

// steady reports whether a look is as settled as waiting makes it:
// every file and link of it had settled when it was taken, but those
// said to have changed at a time still to come (ahead), which no wait
// settles. Each is judged by its own look: a link's name names both the
// file it leads to and the link itself, and either ahead holds up
// neither the other nor anything else. What of those a run read is
// doubted, and read again (Await).
func (l *Looks) steady() bool { return !l.waits }

// lookAt looks, at a time a test says, at what l looks at: the listing
// of its tree, the files named in each repository and the files beside.
// What cannot be looked at, a repository that cannot be listed among
// it, is not there: it looks otherwise than where it could be. It fails
// only where the context ends.
func (l *Looks) lookAt(ctx context.Context, now time.Time) (*Looks, error) {
	seen := &Looks{at: now.UnixNano(), files: map[string]looked{}, links: map[string]looked{}, tree: l.tree, named: l.named, beside: l.beside, lists: l.lists, now: l.now}
	for _, repository := range l.tree.Repositories() {
		t, err := l.tree.list(ctx, repository, true)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err != nil {
			continue
		}
		for name, as := range t.listed {
			seen.files[path.Join(repository, name)] = as
		}
		for name, as := range t.links {
			seen.links[path.Join(repository, name)] = as
		}
	}
	for repository, names := range l.named {
		_, directory := l.tree.where(repository)
		root, err := os.OpenRoot(directory)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		for _, name := range names {
			key := path.Join(repository, name)
			if err != nil {
				// As a run that asks for it finds it.
				seen.files[key] = unlooked
				continue
			}
			// Through the root, as a run reads it: a link out of the
			// repository leads nowhere.
			if as, there := named(root.Stat(filepath.FromSlash(name))); there {
				seen.files[key] = as
			}
		}
		if err == nil {
			_ = root.Close()
		}
	}
	for _, file := range l.beside {
		if as, there := named(os.Stat(file)); there {
			seen.files[file] = as
		}
	}
	for directory, ending := range l.lists {
		listed, err := os.ReadDir(directory)
		if err != nil {
			seen.files[directory] = unlooked
			continue
		}
		for _, entry := range listed {
			if !strings.HasSuffix(entry.Name(), ending) {
				continue
			}
			// Not where a link leads: a name that is a link is no file
			// of the listing's own.
			name := filepath.Join(directory, entry.Name())
			if as, there := named(os.Lstat(name)); there {
				seen.files[name] = as
			} else {
				delete(seen.files, name)
			}
		}
	}
	for _, of := range []map[string]looked{seen.files, seen.links} {
		for name, as := range of {
			if !settledBy(as, seen.at) {
				seen.doubted = append(seen.doubted, name)
				// Each by its own look: the file a link leads to and the
				// link itself are under one name.
				seen.waits = seen.waits || !ahead(as, seen.at)
			}
		}
	}
	sort.Strings(seen.doubted)
	seen.doubted = slices.Compact(seen.doubted)
	return seen, nil
}

// Unlisted is a repository that could not be listed.
type Unlisted struct {
	// Repository is the workspace's name for it.
	Repository string
	Err        error
}

func (u *Unlisted) Error() string {
	return fmt.Sprintf("listing the repository %s: %v", u.Repository, u.Err)
}

func (u *Unlisted) Unwrap() error { return u.Err }

// list lists one repository of a tree, where its files are: it may be
// reached through a link, the workspace's own directory among them.
// Watched, it says how the links it followed looked (list).
func (t Tree) list(ctx context.Context, repository string, watched bool) (*taken, error) {
	at, directory := t.where(repository)
	listed, err := list(ctx, at, directory, watched)
	if err != nil {
		return nil, &Unlisted{Repository: repository, Err: err}
	}
	return listed, nil
}

// Looks is what the run listed and read, each file as the run held it,
// with what was looked at beside it before it was read (Beside): every
// file listed as it looked when the run began, or when the run last
// wrote it; every link the listing followed; every file the run asked
// for by name that no listing takes, as it looked when it was read, or
// when the run could not read it, or named where it was not there (as
// named says it: what it was where it was no file, unlooked where it
// could not be looked at); every file the run wrote where no listing
// takes it, as it looked once written; and each repository's ignore
// file, which the run read as it began, listed or not. Asked of a run
// that has ended, it is all the run read. Each call is a Looks of its
// own.
//
// A file the run read the content of before it had settled is doubted,
// and is read again once it has (Await).
func (s *Snapshot) Looks(beside ...*Looks) *Looks {
	l := &Looks{at: s.at, files: map[string]looked{}, links: map[string]looked{}, tree: s.Tree, named: map[string][]string{}, read: map[string]readAs{}}
	s.mu.Lock()
	for repository, t := range s.repositories {
		for name, as := range t.listed {
			l.files[path.Join(repository, name)] = as
		}
		for name, as := range t.links {
			l.links[path.Join(repository, name)] = as
		}
		for name, known := range t.held {
			key := path.Join(repository, name)
			if _, listed := t.listed[name]; !listed {
				l.named[repository] = append(l.named[repository], name)
				l.files[key] = known.looked
			}
			if !known.settled {
				l.doubted = append(l.doubted, key)
				l.read[key] = readAs{directory: t.directory, name: name, hash: known.held.hash}
			}
		}
		for name := range t.absent {
			l.named[repository] = append(l.named[repository], name)
		}
		for name, as := range t.unread {
			if _, read := t.held[name]; read || t.absent[name] {
				continue
			}
			l.named[repository] = append(l.named[repository], name)
			l.files[path.Join(repository, name)] = as
		}
	}
	s.mu.Unlock()
	sort.Strings(l.doubted)
	return l.add(beside)
}

// Unread is what a run that could not take its snapshot of a tree is
// held to, failing as it did (Keeper.Take): how what can be listed of
// the tree looks now, the moment after, less the repository the run
// could not list (Unlisted), which looks otherwise once it can be. A
// repository that cannot be listed then is not there in either look,
// and one that can is: a wait is answered when a file of either
// changes, and not while what failed the run still would. The run read
// nothing, so nothing of it is doubted. Where the context ends before
// the tree is looked at, it is nothing of the tree, and every file of
// it looks otherwise.
func Unread(ctx context.Context, tree Tree, failed error) *Looks {
	return unread(ctx, tree, failed, time.Now())
}

// unread is Unread, looking at a time a test says.
func unread(ctx context.Context, tree Tree, failed error, at time.Time) *Looks {
	l, err := lookTree(ctx, tree, nil, at)
	if err != nil {
		return &Looks{at: time.Now().UnixNano(), files: map[string]looked{}, links: map[string]looked{}, tree: tree}
	}
	var unlisted *Unlisted
	if errors.As(failed, &unlisted) {
		for _, of := range []map[string]looked{l.files, l.links} {
			for name := range of {
				if repositoryOf(tree, name) == unlisted.Repository {
					delete(of, name)
				}
			}
		}
	}
	l.doubted = nil
	return l
}

// repositoryOf is the repository of a tree a name of a look is of: the
// one whose name it begins with, the longest where a repository's name
// begins another's.
func repositoryOf(tree Tree, name string) string {
	of := ""
	for repository := range tree.Paths {
		if len(repository) > len(of) && strings.HasPrefix(name, repository+"/") {
			of = repository
		}
	}
	return of
}

// With is a Looks of its own, of what l looked at and what was looked
// at beside it; l nil is nothing of a tree.
func (l *Looks) With(beside ...*Looks) *Looks {
	of := &Looks{files: map[string]looked{}, links: map[string]looked{}}
	if l != nil {
		l.mu.Lock()
		of.at, of.tree, of.named, of.doubted, of.now = l.at, l.tree, l.named, slices.Clone(l.doubted), l.now
		of.beside = slices.Clone(l.beside)
		of.lists = maps.Clone(l.lists)
		maps.Copy(of.files, l.files)
		maps.Copy(of.links, l.links)
		of.read = maps.Clone(l.read)
		of.whole = maps.Clone(l.whole)
		of.records = append(slices.Clone(l.records), l)
		l.mu.Unlock()
	} else {
		of.at = time.Now().UnixNano()
	}
	return of.add(beside)
}

// add adds to l what was looked at beside it; nil is nothing: with
// what of it had not settled, and what that held when it was read
// (Beside); each is one of l's records, told what a wait on l learns
// (keep).
func (l *Looks) add(beside []*Looks) *Looks {
	for _, b := range beside {
		if b == nil {
			continue
		}
		l.records = append(l.records, b)
		b.mu.Lock()
		maps.Copy(l.files, b.files)
		l.beside = append(l.beside, b.beside...)
		if len(b.lists) > 0 && l.lists == nil {
			l.lists = map[string]string{}
		}
		maps.Copy(l.lists, b.lists)
		l.doubted = append(l.doubted, b.doubted...)
		if len(b.read) > 0 && l.read == nil {
			l.read = map[string]readAs{}
		}
		maps.Copy(l.read, b.read)
		if len(b.whole) > 0 && l.whole == nil {
			l.whole = map[string]usedOf{}
		}
		for name, used := range b.whole {
			used = l.whole[name].with(used)
			l.whole[name] = used
			if used.doubts() {
				l.doubted = append(l.doubted, name)
			}
		}
		b.mu.Unlock()
	}
	slices.Sort(l.doubted)
	l.doubted = slices.Compact(l.doubted)
	return l
}

// Since names the files that do not look as they did in a look before,
// and the links: those that look otherwise, those that are there now
// and were not, and those that were and are not, sorted, each once. A
// file moved is two: the name it had and the one it has. Nothing named,
// nothing a run lists changed between them.
func (l *Looks) Since(before *Looks) []string {
	changed := append(since(l.files, before.files), since(l.links, before.links)...)
	slices.Sort(changed)
	return slices.Compact(changed)
}

// since names what of now does not look as it did before.
func since(now, before map[string]looked) []string {
	var changed []string
	for name, as := range now {
		if was, there := before[name]; !there || was != as {
			changed = append(changed, name)
		}
	}
	for name := range before {
		if _, there := now[name]; !there {
			changed = append(changed, name)
		}
	}
	return changed
}

// Settled reports whether every file and link had settled when it was
// looked at, as a keeper holds a file to have (settledBy): a write to
// any of them since then shows in a later look.
func (l *Looks) Settled() bool { return len(l.doubted) == 0 }

// Await returns once what was looked at looks otherwise and has
// settled, naming what looks otherwise (Since), but a file beside that
// holds the bytes its readers used (unchanged); or, where nothing does,
// what had not settled when the run read it and, read again once it
// has, does not hold what the run read of it, and a file beside read
// holding otherwise by two readings (reread). What is read again and
// holds what the run read is as it was: from then on a write to it
// shows, and it is doubted no more. It looks again at once, and then
// every while (every), and never for more than a tenth of the time:
// after a look that took long it waits longer. It returns when the
// context ends, with its error, a read again among what it stops.
//
// Settled is two looks, one after the other, that are the same, every
// file in the second written longer ago than the moment its times are
// told in: a save, or a checkout, that is still writing is waited out,
// and what is named is how the files were left.
func (l *Looks) Await(ctx context.Context, every time.Duration) ([]string, error) {
	var (
		last *Looks
		wait time.Duration
	)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		began := time.Now()
		now, err := l.lookAt(ctx, l.clock())
		if err != nil {
			return nil, err
		}
		wait = max(every, 9*time.Since(began))
		changed := now.Since(l)
		switch {
		case len(changed) == 0 && l.Settled():
			// As it was, or as it was again: nothing to build.
			last = nil
		case now.steady() && last != nil && len(now.Since(last)) == 0:
			// A file beside that still holds what was read of it is
			// taken as it looks now, and is no change.
			changed, err = l.unchanged(ctx, now, changed)
			if err != nil {
				return nil, err
			}
			if len(changed) > 0 {
				return changed, nil
			}
			// Nothing looks otherwise, and what had not settled when it
			// was read has since: what does not hold what the run read
			// of it changed, and the rest is as it was.
			unlike, err := l.reread(ctx, now)
			if err != nil {
				return nil, err
			}
			if len(unlike) > 0 {
				return unlike, nil
			}
			last = nil
		default:
			last = now
		}
	}
}

// readAs is a file a run read the content of before it had settled:
// the directory of its repository, its name there and the hash of what
// the run read. A file beside a tree, read again (unlike), has no
// directory, and is named by its path; what was used of it is the
// look's (whole).
type readAs struct {
	directory, name, hash string
}

// reread reads again each file that had not settled when the run read
// it, now that every file has that can (now, a look in which it looks
// as the run held it), and returns, sorted, those that do not hold what
// the run read; a file beside, by what its readers used (unlike). What
// is said to have changed at a time still to come (ahead) stays
// doubted, and is read again whenever it is, as the run reads it
// whenever it asks; nothing else is doubted after it. It fails only
// where the context ends, and then nothing is said of what was read.
func (l *Looks) reread(ctx context.Context, now *Looks) ([]string, error) {
	var unlike, still []string
	var holding []heldAs
	for _, name := range l.doubted {
		if _, beside := l.whole[name]; beside {
			switch hash, verdict := l.unlike(ctx, now, name); verdict {
			case differs:
				unlike = append(unlike, name)
			case unknown:
				still = append(still, name)
			case holds:
				holding = append(holding, heldAs{name, hash})
			}
			continue
		}
		as := l.read[name]
		switch {
		case !as.holds(ctx, now.files[name]):
			unlike = append(unlike, name)
		case !settledBy(now.files[name], now.at):
			still = append(still, name)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.doubted = still
	// What is beside, and holds what was used, is taken as it looks now,
	// by every wait after.
	for _, h := range holding {
		l.keep(h.name, h.hash, now.files[h.name], settledBy(now.files[h.name], now.at))
	}
	return unlike, nil
}

// heldAs is a file beside found to hold what was used of it: its name and
// the hash of what it holds.
type heldAs struct{ name, hash string }

// unchanged is, of what looks otherwise now (changed), what has changed:
// a file beside that was read through the look (Read) is held to the
// bytes its readers used (unlike), and is taken as it looks now where
// it holds them, here and by each record of l that used the same, so
// that it is read again by no wait after; where it is said to have
// changed at a time still to come it stays doubted, and is read again
// whenever the rest has settled, as one read before it had settled is
// (reread). One that changed as it was read again is neither: it is
// read again by the next wait. A file of a tree is held to how it looks.
// Reading stops where the context ends, which is an error.
func (l *Looks) unchanged(ctx context.Context, now *Looks, changed []string) ([]string, error) {
	var still []string
	for _, name := range changed {
		if _, beside := l.whole[name]; !beside {
			still = append(still, name)
			continue
		}
		switch hash, verdict := l.unlike(ctx, now, name); verdict {
		case differs:
			still = append(still, name)
		case holds:
			as := now.files[name]
			l.keep(name, hash, as, settledBy(as, now.at))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return still, nil
}

// verdict is what a wait finds of a file beside, read again (unlike).
type verdict int

const (
	// holds is a file that holds the bytes every reading used.
	holds verdict = iota
	// differs is one that does not: it is answered.
	differs
	// unknown is one that changed as it was read again, or that cannot
	// be known any better by reading it again: it is read again later.
	unknown
)

// unlike is the one rule a file beside a build is held to: it is
// answered exactly when what it holds now differs from bytes that were
// used of it (whole). It is read again as it looks now (now), and looked
// at before and after: one that looked otherwise either time is read
// again later, and what was read of it is not used. A file removed, that
// cannot be read or that is no file differs; so does one whose reader
// failed, which used no bytes that are known, but where it looks as it
// was read and is said to have changed at a time still to come: no wait
// settles it, nor would the round after, which reads it again, so it is
// held to how it looks. Holding the bytes used, it says their hash.
func (l *Looks) unlike(ctx context.Context, now *Looks, name string) (string, verdict) {
	used := l.whole[name]
	as, there := now.files[name]
	switch {
	case !there, as.size < 0:
		// Not there, no file (a name of a listing made a link), or not
		// to be looked at.
		return "", differs
	case used.failed && as == l.files[name] && ahead(as, now.at):
		return "", unknown
	case used.failed:
		return "", differs
	}
	hash, err := (readAs{name: name}).hashed(ctx, as)
	switch {
	case errors.Is(err, errLooksOtherwise):
		return "", unknown
	case err != nil || !used.heldBy(hash):
		return "", differs
	}
	return hash, holds
}

// keep takes a file beside, found to hold the bytes used of it (hash)
// while it looked so (as), as it looks: in l, and in each record of l
// that used those bytes alone, under the record's lock, since others
// who wait copy it. Settled, it is doubted no more; otherwise, said to
// have changed at a time still to come, it is doubted, to be read
// again.
func (l *Looks) keep(name, hash string, as looked, settled bool) {
	l.take(name, as, settled)
	for _, r := range l.records {
		r.mu.Lock()
		if used, read := r.whole[name]; read && used.heldBy(hash) {
			r.take(name, as, settled)
		}
		r.mu.Unlock()
	}
}

// take is keep, of l alone.
func (l *Looks) take(name string, as looked, settled bool) {
	l.files[name] = as
	i, doubted := slices.BinarySearch(l.doubted, name)
	switch {
	case settled && doubted:
		l.doubted = slices.Delete(l.doubted, i, i+1)
	case !settled && !doubted:
		l.doubted = slices.Insert(l.doubted, i, name)
	}
}

// holds is whether a file holds what the run read of it, read while it
// looks as it did (was, hashed). Reading stops where the context ends.
func (as readAs) holds(ctx context.Context, was looked) bool {
	hash, err := as.hashed(ctx, was)
	return err == nil && hash == as.hash
}

// hashed reads a file whole, where it is still a file that looks as it
// did (was), and says the hash of what it held: it is opened as a run
// opens it (opened), and read only where it is a file that looks so,
// and looked at again after. A file that is no longer one, or that
// looks otherwise before or after, has changed. Reading stops where the
// context ends.
func (as readAs) hashed(ctx context.Context, was looked) (string, error) {
	file, info, err := as.open()
	if err != nil {
		return "", err
	}
	defer file.Close()
	if !info.Mode().IsRegular() || look(info) != was {
		return "", errLooksOtherwise
	}
	hash, _, err := digest(ctx, file)
	if err != nil {
		return "", err
	}
	if after, err := file.Stat(); err != nil || look(after) != was {
		return "", errLooksOtherwise
	}
	return hash, nil
}

// errLooksOtherwise is a file read again that does not look as it did.
var errLooksOtherwise = errors.New("the file does not look as it did")

// open opens the file as a run opens it: through the root of its
// repository, or, beside a tree, by its path.
func (as readAs) open() (*os.File, fs.FileInfo, error) {
	if as.directory == "" {
		return openedAt(as.name)
	}
	root, err := os.OpenRoot(as.directory)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	return opened(root, as.name)
}
