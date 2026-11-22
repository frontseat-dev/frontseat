package place

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Placer is where this form of the program is on a worker: beside the
// confiner, in the directory the runner is installed into, which every
// runner sandbox holds at /bb, under a name of its form, so that it is
// in every fetch's key.
var Placer = Placers + Form

// Placers is what every form's path begins with.
const Placers = "/bb/frontseat-place-"

// Form is what this program is: the digest of place.go, which holds all
// it does, and of the Go release that built it. The package's doc says
// why; it says so in doc.go, which is not in the digest.
var Form = func() string {
	digest := sha256.New()
	digest.Write(source)
	digest.Write([]byte("\x00" + runtime.Version()))
	return hex.EncodeToString(digest.Sum(nil))
}()

//go:embed place.go
var source []byte

// Unpack is the command that unpacks each archive into the directory
// after it, in pairs, as the worker runs it.
func Unpack(archivesAndDirectories ...string) []string {
	return append([]string{Placer, "unpack"}, archivesAndDirectories...)
}

// Cut is the command that cuts parts out of what was downloaded, in
// sevens, as the worker runs it: what was downloaded, where the part
// starts in it and how long it is, the checksum's kind ("sha1" or
// "sha256") and its hex, the file the part is kept as, and the public
// address it came from, to say so by.
func Cut(parts ...string) []string {
	return append([]string{Placer, "cut"}, parts...)
}

// Run is frontseat-place: it does what it is told, or says why it could
// not and exits 1.
func Run() {
	if err := Main(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-place:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// Main runs frontseat-place with its arguments, the program's name left
// out: "form" prints its form; "install" and a directory installs it
// there (Install) and prints where; "unpack" and "cut" place what a
// fetch was given, as Unpack and Cut say.
func Main(args []string, out io.Writer) error {
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	switch {
	case verb == "form" && len(args) == 1:
		_, err := fmt.Fprintln(out, Form)
		return err
	case verb == "install" && len(args) == 2:
		at, err := Install(args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, at)
		return err
	case verb == "unpack":
		return unpack(args[1:])
	case verb == "cut":
		return cut(args[1:])
	}
	return errors.New("frontseat-place is told form, install DIR, unpack ARCHIVE DIR..., or cut and its parts in sevens")
}

// Install puts this program in a directory under the name of its form,
// where a worker finds it (Placer), and returns where. It is written
// beside and renamed into place, whole, over whatever was there under
// that name: a placer of the same form does the same, whichever build of
// it, and a runner never runs half of one.
func Install(dir string) (string, error) {
	at := filepath.Join(dir, filepath.Base(Placer))
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	from, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer from.Close()
	beside, err := os.CreateTemp(dir, ".frontseat-place-*")
	if err != nil {
		return "", err
	}
	// Its own to delete: the file it made just above, which is gone once
	// renamed into place.
	defer os.Remove(beside.Name())
	_, err = io.Copy(beside, from)
	if err = errors.Join(err, beside.Chmod(0o755), beside.Sync(), beside.Close()); err != nil {
		return "", err
	}
	return at, os.Rename(beside.Name(), at)
}

// unpack unpacks each archive, a gzipped tar, into the directory after
// it, made where it is not, as tar -xzk does: never over what is there
// already, so that what the fetch was given beside the archive, such as
// the checksum cargo reads, is never replaced by what the archive holds.
func unpack(pairs []string) error {
	if len(pairs)%2 != 0 {
		return errors.New("unpack is told each archive and where it goes, in pairs")
	}
	for i := 0; i < len(pairs); i += 2 {
		if err := unpackInto(pairs[i], pairs[i+1]); err != nil {
			return fmt.Errorf("unpacking %s into %s: %w", pairs[i], pairs[i+1], err)
		}
	}
	return nil
}

// unpackInto unpacks one archive into its directory, which holds all of
// it: every entry is put there through os.Root, which follows no link
// out, and one that would lead out is refused (entry).
func unpackInto(archive, into string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	zipped, err := gzip.NewReader(bufio.NewReader(file))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(into, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(into)
	if err != nil {
		return err
	}
	defer root.Close()
	unpacked := unpacking{root: root, made: map[string]bool{".": true}}
	entries := tar.NewReader(zipped)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := unpacked.entry(header, entries); err != nil {
			return fmt.Errorf("%q: %w", header.Name, err)
		}
	}
}

// unpacking is an archive being unpacked into its root, and the
// directories of the root it has made, or found direct, already: no
// entry can make one of them a link after (what is there is kept).
type unpacking struct {
	root *os.Root
	made map[string]bool
}

// directory makes a directory of the root where it is not, once the way
// to it is seen to be direct.
func (u unpacking) directory(dir string) error {
	if u.made[dir] {
		return nil
	}
	if err := u.directory(filepath.Dir(dir)); err != nil {
		return err
	}
	info, err := u.root.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		err = u.root.Mkdir(dir, 0o755)
	case err == nil && info.Mode()&fs.ModeSymlink != 0:
		return errors.New("it is put through a link")
	case err == nil && !info.IsDir():
		return fmt.Errorf("%s is a file, not a directory", dir)
	}
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	u.made[dir] = true
	return nil
}

// entry puts one entry of an archive in its root: a directory, a file
// with its mode (its owner is not kept), a link to what is within the
// root, or a hard link to a file of it. Its name must be within the
// root, and no part of the way to it a link: a link the archive made, or
// one that was there, is never written through. What is there already
// under its name is kept. Any other kind of entry is refused.
func (u unpacking) entry(header *tar.Header, content io.Reader) error {
	if header.Typeflag == tar.TypeXGlobalHeader {
		return nil
	}
	name := filepath.Clean(filepath.FromSlash(header.Name))
	if !filepath.IsLocal(name) {
		return errors.New("its name leads out of where it is unpacked")
	}
	if header.Typeflag == tar.TypeDir {
		return u.directory(name)
	}
	if err := u.directory(filepath.Dir(name)); err != nil {
		return err
	}
	var err error
	switch header.Typeflag {
	case tar.TypeReg:
		var file *os.File
		if file, err = u.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
			_, err = io.Copy(file, content)
			err = errors.Join(err, file.Chmod(fs.FileMode(header.Mode)&fs.ModePerm), file.Close())
		}
	case tar.TypeSymlink:
		target := filepath.FromSlash(header.Linkname)
		if !leadsWithin(filepath.Dir(name), target) {
			return errors.New("it links out of where it is unpacked, or by a way that may")
		}
		err = u.root.Symlink(target, name)
	case tar.TypeLink:
		old := filepath.Clean(filepath.FromSlash(header.Linkname))
		if !filepath.IsLocal(old) {
			return errors.New("it links out of where it is unpacked")
		}
		if !u.made[filepath.Dir(old)] {
			return errors.New("it links to no file the archive unpacked")
		}
		if info, err := u.root.Lstat(old); err != nil || !info.Mode().IsRegular() {
			return errors.New("it links to no file the archive unpacked")
		}
		err = u.root.Link(old, name)
	default:
		return fmt.Errorf("it is an entry of kind %q, which a fetch unpacks none of", header.Typeflag)
	}
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}

// leadsWithin says whether a link in a directory of the root leads,
// by its target, to what is within the root, however other links of it
// lead: the target climbs first, if at all, no higher than the root, and
// then only descends. Its directory is direct (directory), so its climb
// is where it reads; what it then descends through, a link among it,
// leads within the root as this one does, and stays beneath it. A
// target that climbs after it descends ("a/../b") could climb out of
// where a link it descended through led, and is refused.
func leadsWithin(dir, target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return false
	}
	depth := 0
	if dir != "." {
		depth = len(strings.Split(dir, string(filepath.Separator)))
	}
	descended := false
	for _, step := range strings.Split(target, string(filepath.Separator)) {
		switch {
		case step == "" || step == ".":
		case step == "..":
			if descended || depth == 0 {
				return false
			}
			depth--
		default:
			descended = true
		}
	}
	return true
}

// part is one part of a download a fetch keeps.
type part struct {
	archive        string
	offset, length int64
	kind, sum      string
	file, address  string
}

// cut cuts each part out of what was downloaded and keeps it, where
// every part is what its checksum says: each is checked first, and at
// the first that is not, it fails, says which, and keeps nothing.
func cut(args []string) error {
	if len(args)%7 != 0 {
		return errors.New("cut is told each part in sevens: what was downloaded, offset, length, kind, checksum, file, address")
	}
	parts := make([]part, 0, len(args)/7)
	for i := 0; i < len(args); i += 7 {
		p := part{archive: args[i], kind: args[i+3], sum: args[i+4], file: args[i+5], address: args[i+6]}
		var err error
		if p.offset, err = strconv.ParseInt(args[i+1], 10, 64); err != nil || p.offset < 0 {
			return fmt.Errorf("%s: the offset %q is no number of bytes", p.address, args[i+1])
		}
		if p.length, err = strconv.ParseInt(args[i+2], 10, 64); err != nil || p.length < 0 {
			return fmt.Errorf("%s: the length %q is no number of bytes", p.address, args[i+2])
		}
		if p.kind != "sha1" && p.kind != "sha256" {
			return fmt.Errorf("%s: %q is no kind of checksum a lock holds", p.address, p.kind)
		}
		parts = append(parts, p)
	}
	for _, p := range parts {
		if err := p.check(); err != nil {
			return err
		}
	}
	for _, p := range parts {
		if err := p.keep(); err != nil {
			return fmt.Errorf("keeping %s: %w", p.file, err)
		}
	}
	return nil
}

// check says how a part is not what its checksum says.
func (p part) check() error {
	var digest hash.Hash = sha256.New()
	if p.kind == "sha1" {
		digest = sha1.New()
	}
	if err := p.copyTo(digest); err != nil {
		return fmt.Errorf("%s: %w", p.address, err)
	}
	if said := hex.EncodeToString(digest.Sum(nil)); said != p.sum {
		return fmt.Errorf("%s: the %d bytes at %d have the %s %s, and the lock holds %s", p.address, p.length, p.offset, p.kind, said, p.sum)
	}
	return nil
}

// keep writes a part as its file, making the directories it is in.
func (p part) keep() error {
	if err := os.MkdirAll(filepath.Dir(p.file), 0o755); err != nil {
		return err
	}
	file, err := os.Create(p.file)
	if err != nil {
		return err
	}
	return errors.Join(p.copyTo(file), file.Close())
}

// copyTo writes the part's bytes, as many of them as what was
// downloaded holds.
func (p part) copyTo(out io.Writer) error {
	file, err := os.Open(p.archive)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(out, io.NewSectionReader(file, p.offset, p.length))
	return err
}
