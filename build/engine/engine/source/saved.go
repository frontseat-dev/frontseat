package source

import (
	"bytes"
	"encoding/gob"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/renameio/v2"
)

// keeperFormat is the format a keeper is saved in; one saved in another
// is not read.
const keeperFormat = 1

// saved is a keeper as it is saved: what wrote it and when, the
// directory it is of, and what it knew of the files of each repository
// there.
type saved struct {
	Format int
	// Program is the program that saved it.
	Program string
	// Saved is when, in nanoseconds, by the clock of the daemon that
	// saved it.
	Saved int64
	// Root is the directory the daemon built.
	Root string
	// Repositories are the repositories it held, by its name for each.
	Repositories map[string]savedRepository
}

// savedRepository is what a keeper knew of a repository, as it is
// saved: where it was, and its files, each by its path beneath that.
type savedRepository struct {
	Place place
	Files map[string]savedFile
}

// savedFile is what a keeper knew of a file, as it is saved: how it
// looked, and what it held then.
type savedFile struct {
	Size, Written, Changed int64
	File                   uint64
	Mode                   fs.FileMode
	Hash                   string
	Held                   int64
	Executable             bool
}

// place is where a repository is: its directory, any link followed, and
// which directory of which disk that is.
type place struct {
	Directory    string
	Device, File uint64
}

// placed returns where the repository in a directory is, and whether
// the disk says.
func placed(directory string) (place, bool) {
	info, err := os.Stat(directory)
	if err != nil {
		return place{}, false
	}
	device, file, known := disk(info)
	return place{Directory: directory, Device: device, File: file}, known
}

// OpenKeeper returns a keeper that knows what the daemon before it
// saved at a file, and that Save saves there again, as a program: the
// content of the daemon's own, however it names it.
//
// What was saved is the daemon's own state, and is used only as far as
// it could be this daemon's own knowledge. It is read where this
// program saved it, in this format: what a file's look and its hash
// mean is the program's, so a keeper of another program is not read,
// whatever format it says, and the daemon of a program built again
// reads its files once. It is used for the directory it was saved of,
// and for each repository that has the name it had and is where it was,
// the same directory of the same disk: one saved of another directory,
// or of another repository at the same place, knows nothing of this
// one. It is used only where this daemon's clock is not behind the
// clock it was saved by, and of each file only where the file had
// settled by then, as its saved times tell. And then what it knew of a
// file is gone by only while the file looks as it did, as for a keeper
// that never stopped.
//
// One never saved, or that cannot be read, cut short, or holding
// anything else, knows nothing: the daemon reads its files again, and
// starts whatever is there. Nothing is said of what such a file holds.
// A path saved is only ever compared with the path of a file a run
// lists; nothing is opened by it.
//
// What is trusted: that the disk dates every change to a file itself
// (when anything of it last changed, which nothing that writes the file
// can set), by a clock that tells no moment twice. Then a file that
// looks as it did, which file of the disk it is and when it changed
// among that, holds what it held: rewritten in place with its times set
// back, or replaced by another moved into its place, it changed later
// than it had. A disk that keeps no such time tells when the file was
// written, which the writer sets, so what was saved is used, and
// anything is saved, only of a repository on a file system known to
// keep one (filesystem.go, which names them and why): of one on any
// other, or on one the system does not name, nothing saved is used and
// nothing is saved, the daemon started again reads its files, and the
// daemon is told so once (Unkept). Nor is anything saved, or used, of a
// repository something else mounted, whatever file system it is said
// to be on: how its files look is the mount's to say, and every file
// the mount gave has the same times (HashAttribute). A daemon none of whose repositories
// is on such a file system saves nothing, and leaves what was saved
// before it as it was: that is used again only by a daemon that finds
// the repository where it was, on a file system it trusts, and of a
// file that looks as it did then, as if no daemon had run between. A
// keeper that never stopped is held to none of this: while its daemon
// runs it goes by how a file looks on any file system, as it did before
// anything was saved.
//
// Not held, and for longer now than a daemon lives: a clock set back to
// the very moment a file last changed, to what the disk can tell apart,
// with the daemon started again once it is right again. A change that
// does not show lasts until the file looks otherwise; what cures it is
// deleting the saved file while no daemon runs, which restarting the
// daemon used to do. Not held either: an overlay is trusted whatever
// file system is beneath it, which cannot be asked from inside a
// container; a repository is asked of where its own directory is, and
// a directory within it that is another file system is taken to be as
// the repository is; and a disk written through something that sets
// its change times, as its device itself may be by whoever holds it.
// And the file is the daemon's user's to write, as is everything of its
// state, so whatever runs as that user may plant in it what a file
// held, which the daemon started after it goes by.
func OpenKeeper(file, program string) *Keeper {
	k := &Keeper{file: file, program: program}
	// A file and nothing else: a pipe there would be waited on.
	if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() {
		return k
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return k
	}
	var before saved
	if gob.NewDecoder(bytes.NewReader(raw)).Decode(&before) != nil || before.Format != keeperFormat || before.Program != program {
		return k
	}
	k.before = &before
	return k
}

// begin is told the directory a run that begins is of, and where each
// of its repositories is. The first run takes up what the daemon before
// this one saved, as far as it is of this directory and of a repository
// on a file system that dates a file's changes itself (OpenKeeper), and
// of none that something else mounted (HashAttribute).
func (k *Keeper) begin(root string, places map[string]place, mounted map[string]bool) {
	now := k.clock().UnixNano()
	k.mu.Lock()
	defer k.mu.Unlock()
	before := k.before
	k.before, k.root, k.places, k.mounted = nil, root, places, mounted
	if k.file != "" {
		k.dated(places)
	}
	if before == nil || before.Root != root || before.Saved > now {
		return
	}
	for name, was := range before.Repositories {
		if at, there := places[name]; !there || at != was.Place || !k.dates[at] {
			continue
		}
		for rel, file := range was.Files {
			as := looked{size: file.Size, written: file.Written, changed: file.Changed, file: file.File, mode: file.Mode}
			if !settledBy(as, before.Saved) {
				continue
			}
			if k.files == nil {
				k.files = map[string]kept{}
			}
			// A keeper keeps only what it read once the file had settled
			// (keep), so all it saved had.
			k.files[was.Place.Directory+string(filepath.Separator)+rel] = kept{looked: as, settled: true, held: held{hash: file.Hash, size: file.Held, executable: file.Executable}}
		}
	}
}

// errBeneath is a keeper told to save itself among the files it keeps.
var errBeneath = errors.New("what a daemon knows of its files is kept in its state, and never beneath a directory it builds")

// Save saves what the keeper knows, whole or not at all and for its
// user alone, where it was opened from; a keeper not opened from a file
// is not saved. What it knows of a repository the directory no longer
// holds is not saved: it would outlive the repository. Nor is what it
// knows of a repository on a file system not known to date a file's
// changes itself, and where every repository is on one nothing is
// written, and what was saved before stays (OpenKeeper). A keeper no run
// of which has begun knows nothing new, and of no directory: what was
// saved before it stays. It is never saved beneath a repository it
// keeps the files of, where it would be a file of the build.
func (k *Keeper) Save() error {
	if k.file == "" {
		return nil
	}
	// Where the file is, any link on the way followed, as a repository's
	// directory is known.
	file := k.file
	if in, err := filepath.EvalSymlinks(filepath.Dir(file)); err == nil {
		file = filepath.Join(in, filepath.Base(file))
	}
	now := k.clock().UnixNano()
	k.mu.Lock()
	if k.places == nil {
		k.mu.Unlock()
		return nil
	}
	s := saved{Format: keeperFormat, Program: k.program, Saved: now, Root: k.root, Repositories: make(map[string]savedRepository, len(k.places))}
	for name, at := range k.places {
		if _, within := strings.CutPrefix(file, at.Directory+string(filepath.Separator)); within {
			k.mu.Unlock()
			return errBeneath
		}
		// As the file system is now, asked again: what is saved outlasts
		// the daemon.
		if _, dates := k.on(at.Directory); dates && !k.mounted[name] {
			s.Repositories[name] = savedRepository{Place: at, Files: map[string]savedFile{}}
		}
	}
	if len(k.places) > 0 && len(s.Repositories) == 0 {
		k.mu.Unlock()
		return nil
	}
	for at, file := range k.files {
		name, rel, there := k.within(at)
		if _, trusted := s.Repositories[name]; !there || !trusted {
			continue
		}
		s.Repositories[name].Files[rel] = savedFile{
			Size: file.looked.size, Written: file.looked.written, Changed: file.looked.changed, File: file.looked.file, Mode: file.looked.mode,
			Hash: file.held.hash, Held: file.held.size, Executable: file.held.executable,
		}
	}
	k.mu.Unlock()
	var raw bytes.Buffer
	if err := gob.NewEncoder(&raw).Encode(s); err != nil {
		return err
	}
	return renameio.WriteFile(k.file, raw.Bytes(), 0o600)
}

// within returns the repository a file is in, of those the run that
// began last held, and the file's path beneath it: the nearest above
// the file, where one repository is within another.
func (k *Keeper) within(at string) (repository, rel string, held bool) {
	nearest := 0
	for name, in := range k.places {
		beneath, within := strings.CutPrefix(at, in.Directory+string(filepath.Separator))
		if within && len(in.Directory) >= nearest {
			repository, rel, held, nearest = name, beneath, true, len(in.Directory)
		}
	}
	return repository, rel, held
}
