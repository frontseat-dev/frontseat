package place

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// member is one member of an archive a test makes.
type member struct {
	name, content, link string
	kind                byte
	mode                int64
}

// archive writes a gzipped tar of the entries into dir, as name, in the
// PAX format where pax is so (crates.io's and npm's long names are each
// one or the other).
func archive(t *testing.T, dir, name string, format tar.Format, entries ...member) {
	t.Helper()
	var packed bytes.Buffer
	zipped := gzip.NewWriter(&packed)
	written := tar.NewWriter(zipped)
	for _, each := range entries {
		kind, mode := each.kind, each.mode
		if kind == 0 {
			kind = tar.TypeReg
		}
		if mode == 0 {
			mode = 0o644
		}
		header := &tar.Header{Name: each.name, Typeflag: kind, Mode: mode, Linkname: each.link, Size: int64(len(each.content)), Format: format, Uid: 4242, Gid: 4242}
		if kind != tar.TypeReg {
			header.Size = 0
		}
		if err := written.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := written.Write([]byte(each.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := written.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), packed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run is the program run in a directory, as the worker runs it there.
func run(t *testing.T, dir string, args ...string) error {
	t.Helper()
	t.Chdir(dir)
	return Main(args, io.Discard)
}

// An archive is unpacked into its directory, made where it is not, from
// a path relative to where the program runs that may climb to it (the
// downloads are beside the home, above a project's directory): its
// files with their modes, its directories, its links within it, and
// names longer than the old tar header holds, in either way a long name
// is written. Owners are not kept.
func TestAnArchiveIsUnpackedWithItsModesLinksAndLongNames(t *testing.T) {
	long := strings.Repeat("deep/", 30) + "file.rs"
	for name, format := range map[string]tar.Format{"pax": tar.FormatPAX, "gnu": tar.FormatGNU} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			archive(t, root, "crate", format,
				member{name: "itoa-1.0.18/", kind: tar.TypeDir, mode: 0o755},
				member{name: "itoa-1.0.18/Cargo.toml", content: "[package]"},
				member{name: "itoa-1.0.18/build.sh", content: "#!/bin/sh", mode: 0o755},
				member{name: "itoa-1.0.18/" + long, content: "long"},
				member{name: "itoa-1.0.18/again.toml", kind: tar.TypeLink, link: "itoa-1.0.18/Cargo.toml"},
				member{name: "itoa-1.0.18/README", kind: tar.TypeSymlink, link: "Cargo.toml"},
			)
			project := filepath.Join(root, "project", "a")
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := run(t, project, "unpack", "../../crate", "../../home/vendor"); err != nil {
				t.Fatal(err)
			}
			at := filepath.Join(root, "home", "vendor", "itoa-1.0.18")
			for file, want := range map[string]string{"Cargo.toml": "[package]", "build.sh": "#!/bin/sh", long: "long", "again.toml": "[package]", "README": "[package]"} {
				if got, err := os.ReadFile(filepath.Join(at, file)); err != nil || string(got) != want {
					t.Errorf("%s = %q, %v; want %q", file, got, err, want)
				}
			}
			if info, err := os.Stat(filepath.Join(at, "build.sh")); err != nil || info.Mode().Perm() != 0o755 {
				t.Errorf("build.sh's mode: %v, %v; want 0755", info.Mode(), err)
			}
			if info, err := os.Stat(filepath.Join(at, "Cargo.toml")); err != nil || info.Mode().Perm() != 0o644 {
				t.Errorf("Cargo.toml's mode: %v, %v; want 0644", info.Mode(), err)
			}
			if target, err := os.Readlink(filepath.Join(at, "README")); err != nil || target != "Cargo.toml" {
				t.Errorf("README links to %q, %v; want Cargo.toml", target, err)
			}
		})
	}
}

// What is there already is never replaced by what an archive holds: the
// checksum file a fetch is given beside the crate is the one cargo reads.
func TestUnpackingNeverReplacesAFileThatIsThere(t *testing.T) {
	root := t.TempDir()
	archive(t, root, "crate", tar.FormatPAX,
		member{name: "itoa/.cargo-checksum.json", content: "from the archive"},
		member{name: "itoa/lib.rs", content: "fn main() {}"},
	)
	if err := os.MkdirAll(filepath.Join(root, "vendor", "itoa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "itoa", ".cargo-checksum.json"), []byte("given"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(t, root, "unpack", "crate", "vendor"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "vendor", "itoa", ".cargo-checksum.json")); string(got) != "given" {
		t.Errorf("the file given was replaced: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "vendor", "itoa", "lib.rs")); string(got) != "fn main() {}" {
		t.Errorf("the rest of the archive was not unpacked: %q", got)
	}
}

// An entry that would be put, or lead, outside the directory it is
// unpacked into is refused, and nothing outside is written: a name that
// climbs or is absolute, a link that leads out, a hard link to what is
// not the archive's, and an entry written through a link the archive
// made. The control, the same archive with the entry kept within,
// unpacks.
func TestAnEntryThatLeadsOutOfItsDirectoryIsRefused(t *testing.T) {
	for name, entries := range map[string][]member{
		"a name that climbs":                   {{name: "../outside", content: "x"}},
		"a name that climbs within":            {{name: "a/../../outside", content: "x"}},
		"an absolute name":                     {{name: "/outside", content: "x"}},
		"a link that leads out":                {{name: "l", kind: tar.TypeSymlink, link: "../outside"}},
		"an absolute link":                     {{name: "l", kind: tar.TypeSymlink, link: "/etc/passwd"}},
		"a link that climbs from a directory":  {{name: "d/l", kind: tar.TypeSymlink, link: "../../outside"}},
		"a hard link out":                      {{name: "h", kind: tar.TypeLink, link: "../outside"}},
		"a file through a link it made":        {{name: "d", kind: tar.TypeSymlink, link: "."}, {name: "d/outside", content: "x"}},
		"a link through a link it made":        {{name: "d", kind: tar.TypeSymlink, link: "."}, {name: "d/l", kind: tar.TypeSymlink, link: "../outside"}},
		"a device":                             {{name: "null", kind: tar.TypeChar}},
		"a link out through a chain of links":  {{name: "a/b/", kind: tar.TypeDir}, {name: "a/b/c", kind: tar.TypeSymlink, link: "../.."}, {name: "x", kind: tar.TypeSymlink, link: "a/b/c/../../outside"}},
		"a link that climbs after it descends": {{name: "a/", kind: tar.TypeDir}, {name: "x", kind: tar.TypeSymlink, link: "a/../outside"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			archive(t, root, "archive", tar.FormatPAX, entries...)
			if err := os.Mkdir(filepath.Join(root, "into"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := run(t, root, "unpack", "archive", "into"); err == nil {
				t.Error("it was unpacked")
			}
			if _, err := os.Lstat(filepath.Join(root, "outside")); err == nil {
				t.Error("something was written outside the directory")
			}
		})
	}
	root := t.TempDir()
	archive(t, root, "archive", tar.FormatPAX,
		member{name: "./a/inside", content: "x"},
		member{name: "d/l", kind: tar.TypeSymlink, link: "../a/inside"},
		member{name: "h", kind: tar.TypeLink, link: "a/inside"},
	)
	if err := run(t, root, "unpack", "archive", "into"); err != nil {
		t.Fatalf("the control was refused: %v", err)
	}
}

// What the program is told is checked before anything is done: pairs
// to unpack, sevens to cut, and nothing else. Told nothing to unpack, it
// does nothing, as a shared fetch with no command does.
func TestWhatTheProgramIsToldIsChecked(t *testing.T) {
	root := t.TempDir()
	if err := run(t, root, "unpack"); err != nil {
		t.Errorf("unpacking nothing: %v", err)
	}
	for name, args := range map[string][]string{
		"nothing":                  nil,
		"another verb":             {"run", "x"},
		"an archive without where": {"unpack", "a"},
		"six of a part":            {"cut", "whole", "0", "1", "sha1", strings.Repeat("1a", 20), "kept"},
		"a kind of checksum":       {"cut", "whole", "0", "1", "md5", strings.Repeat("1a", 16), "kept", "https://example.com/x"},
		"an offset that is none":   {"cut", "whole", "-1", "1", "sha1", strings.Repeat("1a", 20), "kept", "https://example.com/x"},
	} {
		if err := run(t, root, args...); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Errorf("something was made: %v %v", entries, err)
	}
}

// Each part is cut out by its offset and length and kept where every
// part is what its checksum says; at one that is not, the program fails,
// says which address and bytes, and keeps nothing, not even a sound part
// before it, nor the directories made for either.
func TestCuttingKeepsEachPartThatIsWhatItsChecksumSaysAndFailsAtOneThatIsNot(t *testing.T) {
	directory := t.TempDir()
	whole := []byte("0123456789control-part--data-part-and-the-rest")
	if err := os.WriteFile(filepath.Join(directory, "whole"), whole, 0o444); err != nil {
		t.Fatal(err)
	}
	control, data := whole[10:25], whole[26:]
	sha1Of := func(b []byte) string { sum := sha1.Sum(b); return hex.EncodeToString(sum[:]) }
	sha256Of := func(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
	cut := func(parts ...string) (string, error) {
		t.Chdir(directory)
		var said bytes.Buffer
		err := Main(append([]string{"cut"}, parts...), &said)
		if err != nil {
			said.WriteString(err.Error())
		}
		return said.String(), err
	}
	out, err := cut(
		"whole", "10", "15", "sha1", sha1Of(control), "kept/a b/control", "https://example.com/x.apk",
		"whole", "26", strconv.Itoa(len(data)), "sha256", sha256Of(data), "kept/data", "https://example.com/x.apk",
	)
	if err != nil {
		t.Fatalf("sound parts were refused, so this test proves nothing: %v\n%s", err, out)
	}
	for name, want := range map[string][]byte{"kept/a b/control": control, "kept/data": data} {
		if got, err := os.ReadFile(filepath.Join(directory, name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	for name, parts := range map[string][]string{
		"a part that is not what its sha1 says":   {"whole", "11", "15", "sha1", sha1Of(control), "bad/control", "https://example.com/x.apk"},
		"a part that is not what its sha256 says": {"whole", "26", strconv.Itoa(len(data) - 1), "sha256", sha256Of(data), "bad/data", "https://example.com/x.apk"},
		"a part past the end of the download":     {"whole", "40", "100", "sha256", sha256Of(data), "bad/past", "https://example.com/x.apk"},
		"a sound part before one that is not": {
			"whole", "10", "15", "sha1", sha1Of(control), "bad/first", "https://example.com/x.apk",
			"whole", "0", "3", "sha1", sha1Of(control), "bad/second", "https://example.com/y.apk",
		},
	} {
		out, err := cut(parts...)
		if err == nil {
			t.Errorf("%s: passed", name)
			continue
		}
		address := parts[len(parts)-1]
		if !strings.Contains(out, address+": the ") || !strings.Contains(out, "the lock holds") {
			t.Errorf("%s: said %q; want the address %s and what the lock holds", name, out, address)
		}
		if _, err := os.Stat(filepath.Join(directory, "bad")); err == nil {
			t.Errorf("%s: something was kept", name)
		}
	}
}

// The program is at a path of its form, which is the digest of what it
// does, and says it when asked.
func TestThePlacerIsAtAPathOfItsForm(t *testing.T) {
	if Placer != "/bb/frontseat-place-"+Form || len(Form) != 64 {
		t.Fatalf("Placer = %q", Placer)
	}
	var said bytes.Buffer
	if err := Main([]string{"form"}, &said); err != nil || strings.TrimSpace(said.String()) != Form {
		t.Errorf("form said %q, %v", said.String(), err)
	}
}

// Installed, it is put under the name of its form, whole, over whatever
// was there under that name; what is beside it is left.
func TestInstallingPutsItUnderTheNameOfItsForm(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, filepath.Base(Placer))
	if err := os.WriteFile(at, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "frontseat-place-another")
	if err := os.WriteFile(other, []byte("another form"), 0o755); err != nil {
		t.Fatal(err)
	}
	var said bytes.Buffer
	if err := Main([]string{"install", dir}, &said); err != nil || strings.TrimSpace(said.String()) != at {
		t.Fatalf("install said %q, %v", said.String(), err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(self)
	if got, err := os.ReadFile(at); err != nil || !bytes.Equal(got, want) {
		t.Errorf("what is installed is not this program: %d bytes, %v", len(got), err)
	}
	if info, err := os.Stat(at); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("its mode: %v, %v", info.Mode(), err)
	}
	if got, _ := os.ReadFile(other); string(got) != "another form" {
		t.Errorf("another form was touched: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("left beside it: %v", entries)
	}
}
