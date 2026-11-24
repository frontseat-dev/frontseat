package source

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// HashAttribute is the extended attribute in which a file of a mounted
// repository says the SHA-256 of what it holds, as 64 hexadecimal
// digits in lower case. What mounts a repository keeps to the layout
// said where the layers are (Mounted, in core/ports); this is what a
// run does with it.
//
// A run reads a mounted repository as it reads a clone: it lists it,
// holds each file to how it looked when the run began, and reads and
// hashes what it sends. One thing differs. A file whose change time is
// the mount's stamp is taken to hold what its attribute says, and is
// not read, so that a file the mount gave is not fetched to be hashed.
//
// Why that is the file's hash. The stamp is read from the repository's
// layer, and only where the layer is on a file system mounted
// read-only (Layer): a sandbox can mount nothing, so such a stamp was
// put there by whoever made the sandbox, and is a moment before
// anything in it ran. A change time is the file system's to say, and
// never the writer's: on the mount, the layout has every file the
// workspace made or changed shown with another change time than the
// stamp, and has the mount alone say the attribute. So a file shown at
// the stamp is one the mount gave and nothing changed, and its
// attribute is the mount's word of what it gave.
//
// What a run checks, and what it does not go by:
//
//   - The attribute is taken only where it is 64 hexadecimal digits in
//     lower case; a file with none, or with anything else, is read.
//   - The hash, the size and the mode of a file are three answers. The
//     run asks the mount itself how the file looks, and not what the
//     kernel kept of an earlier answer, before it asks the attribute
//     and again after: where either look is not the one the run holds
//     the file to, at the stamp, the file is read. The layout has a
//     mount that comes to give anything else say a stamp it never said,
//     and answer nothing of what it gave before once it has answered
//     anything of what it gives now: so two looks at one stamp are of
//     one state, and the attribute asked between them is too. This is
//     what Buck2 gets by asking EdenFS for a file's hash and size in
//     one call (903bfd7, app/buck2_eden/src/io_provider.rs,
//     getAttributesFromFilesV2); Bazel takes the attribute a file
//     system gives with no condition (94776af184, UnixFileSystem.java,
//     getFastDigest, --unix_digest_hash_attribute_name), which leaves
//     the pairing to the file system.
//   - Nothing taken from an attribute is kept from one run to the next,
//     and nothing read of a file shown at the stamp is either: every
//     file the mount gave has the same times, so how such a file looks
//     says nothing of whether it is the file a run before saw. Each
//     run asks the mount again, which is one question and no read.
//   - Nothing of a mounted repository is saved for the daemon after
//     this one, and nothing saved is used of one (OpenKeeper): how its
//     files look is the mount's to say, and no file system's own.
//
// Not held. A layout made up inside a box is believed as far as a
// file system mounted read-only there holds a file named stamp, which
// nothing in a box can arrange; outside a sandbox no layer is read at
// all, since the command line names where the layers are only there.
// That the mount keeps to the layout is its own to hold: a mount that
// shows a file the workspace changed at its stamp, or says an
// attribute that is not the hash of what it gives, is believed. And a
// clock set back to the stamp itself, on a file system that dates a
// change by it, would show a changed file at the stamp: the layout
// has the mount show none so, whatever the clock said.
const HashAttribute = "user.frontseat.sha256"

// Mount is what the layer of a mounted repository said when a run's
// tree was read.
type Mount struct {
	// Stamp is the mount's stamp, in nanoseconds, where Stamped: the
	// change time of every file the mount gave that nothing changed.
	// Not stamped, the layer said none a run goes by, and every file of
	// the repository is read.
	Stamp   int64
	Stamped bool
}

// stampFile is the file of a layer that holds the mount's stamp.
const stampFile = "stamp"

// Layer reads the layer of a mounted repository, a directory. It says
// a stamp only where the layer is on a file system mounted read-only,
// which nothing in a sandbox can make, and holds a file named stamp
// that is one moment as the layout writes it. Anything else is a layer
// that says no stamp: the repository is mounted all the same, and
// read.
func Layer(directory string) Mount {
	return layer(directory, readOnly)
}

// layer is Layer, told what says a directory is on a file system
// mounted read-only.
func layer(directory string, readOnly func(string) bool) Mount {
	if !readOnly(directory) {
		return Mount{}
	}
	file := filepath.Join(directory, stampFile)
	// A file and nothing else, and no longer than a moment is written:
	// a pipe there would be waited on.
	if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return Mount{}
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return Mount{}
	}
	line, rest, ended := strings.Cut(string(raw), "\n")
	if !ended || rest != "" {
		return Mount{}
	}
	stamp, err := time.Parse(time.RFC3339Nano, line)
	if err != nil || !strings.HasSuffix(line, "Z") {
		return Mount{}
	}
	return Mount{Stamp: stamp.UnixNano(), Stamped: true}
}

// attribute is what an extended attribute of an open file says, where
// it has it and it is shorter than a length.
func attribute(file *os.File, name string, shorter int) (string, bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return "", false
	}
	said, read := make([]byte, shorter), 0
	var failed error
	if conn.Control(func(fd uintptr) { read, failed = unix.Fgetxattr(int(fd), name, said) }) != nil || failed != nil || read >= shorter {
		return "", false
	}
	return string(said[:read]), true
}

// attributed is the SHA-256 a file's hash attribute says it holds,
// where it says one as the layout writes it.
func attributed(file *os.File) (string, bool) {
	said, ok := attribute(file, HashAttribute, 65)
	if !ok || len(said) != 64 || strings.Trim(said, "0123456789abcdef") != "" {
		return "", false
	}
	return said, true
}

// said is what a file of a mounted repository that is shown at the
// mount's stamp says it holds, by its attribute, where the mount says
// the file looks as the run holds it to before the attribute is asked
// and after.
func (k *Keeper) said(file *os.File, as fs.FileInfo) (string, bool) {
	unchanged, asks := k.unchanged, k.attribute
	if unchanged == nil {
		unchanged = looksStill
	}
	if asks == nil {
		asks = attributed
	}
	if !unchanged(file, as) {
		return "", false
	}
	hash, ok := asks(file)
	if !ok || !unchanged(file, as) {
		return "", false
	}
	return hash, true
}

// stamped is whether a file that looks so is of a mounted repository
// and shown at its mount's stamp.
func (s *Snapshot) stamped(repository string, as looked) bool {
	mount, mounted := s.Mounted[repository]
	return mounted && mount.Stamped && as.changed == mount.Stamp
}
