package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// A look is one to hold a later look against only where every file in
// it had settled when it began: a file written within the moment its
// times are told in before that may be written again and look the same.
// It is the keeper's own rule (settledBy), by the keeper's own moment.
// Control: the same file, looked at once that moment has passed.
func TestALookHasSettledOnceEveryFileInItHas(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	written, as := justWritten(t, file, "just written")
	moment := settles(as)
	tree := Tree{Root: root, Paths: map[string]string{"": "."}}
	for name, tc := range map[string]struct {
		at      time.Time
		settled bool
	}{
		"looked at within the moment the file was written in": {written.Add(moment - time.Nanosecond), false},
		"looked at once the moment had passed":                {written.Add(moment), true},
	} {
		for where, beside := range map[string][]string{"a file of the tree": nil, "a file beside it": {file}} {
			of := tree
			if beside != nil {
				of = Tree{Root: root}
			}
			seen, err := (&Looks{tree: of, beside: beside}).lookAt(context.Background(), tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if len(seen.files) != 1 || seen.Settled() != tc.settled {
				t.Errorf("%s, %s: the look of %d files had settled: %t; want one file, and %t", where, name, len(seen.files), seen.Settled(), tc.settled)
			}
		}
	}
}

// cargos is a repository of Cargo's configuration alone, just written:
// the repository and the file, beneath a directory whose name begins
// with a dot, which a run reads by name.
func cargos(t *testing.T) (root, file string) {
	t.Helper()
	root = t.TempDir()
	file = filepath.Join(root, ".cargo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("[build]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, file
}

// A file a run read before it had settled is doubted: a write within
// the moment its times are told in, at its size, would leave it looking
// as the run held it. Once it has settled it is read again, and named
// where it does not hold what the run read, as it would not where it
// was written so. Control: holding what the run read, it is as it was,
// and nothing is answered.
func TestAFileReadBeforeItSettledIsReadAgainOnceItHas(t *testing.T) {
	root, file := cargos(t)
	// Read the moment it was written.
	k := &Keeper{now: func() time.Time { return time.Unix(0, 0) }}
	snapshot, err := k.Take(t.Context(), Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	k.now = func() time.Time { return time.Unix(0, newest(look(info))) }
	if _, _, err := snapshot.Read(t.Context(), "", ".cargo/config.toml"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		read string
		want []string
	}{
		"holding what the run read":         {"", nil},
		"not holding what the run read now": {"what was read before it was written again", []string{".cargo/config.toml"}},
	} {
		t.Run(name, func(t *testing.T) {
			looks := snapshot.Looks()
			if looks.Settled() {
				t.Fatal("a file read the moment it was written is not doubted")
			}
			if tc.read != "" {
				as := looks.read[".cargo/config.toml"]
				as.hash = tc.read
				looks.read[".cargo/config.toml"] = as
			}
			awaits(t, looks, 300*time.Millisecond, tc.want)
		})
	}
}

// A file a run asked for by name that changed while the run read it,
// as an editor saves it, fails the run (Changed), and is waited on as it
// looked when the run opened it, which it does not now: the wait
// answers it, for the run that failed on it to be made again. Control:
// the same file read whole, with nothing written, answers nothing.
func TestAFileThatChangedAsTheRunReadItByNameIsWaitedOn(t *testing.T) {
	for name, tc := range map[string]struct {
		saved bool
		want  []string
	}{
		"saved as it was read":           {true, []string{".cargo/config.toml"}},
		"the control, read with no save": {false, nil},
	} {
		t.Run(name, func(t *testing.T) {
			root, file := cargos(t)
			time.Sleep(50 * time.Millisecond)
			run, err := new(Keeper).Take(t.Context(), Tree{Root: root, Paths: map[string]string{"": "."}})
			if err != nil {
				t.Fatal(err)
			}
			var content []byte
			_, _, err = run.ask("", ".cargo/config.toml", func(opened *os.File) (string, int64, error) {
				if tc.saved {
					if err := os.WriteFile(file, []byte("[build]\njobs = 1\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return reading(&content)(opened)
			})
			var changed *Changed
			if errors.As(err, &changed) != tc.saved {
				t.Fatalf("the read failed with %v; want it to have failed as changed: %t", err, tc.saved)
			}
			awaits(t, run.Looks(), 500*time.Millisecond, tc.want)
		})
	}
}

// Reading again a file a run read before it had settled opens it as a
// run opens it and reads it only while it is a file: a pipe put in its
// place, which nothing writes to and whose reading ends at once, is no
// longer what the run read, though what it holds, nothing, hashes as
// the run's empty file did; and reading stops once the context ends,
// however much of the file there is to read. Control: the file the run
// read, holding what it read, holds it.
func TestReadingAgainWhatARunReadStopsAtWhatIsNoFileAndWhenItIsCancelled(t *testing.T) {
	root := t.TempDir()
	empty := sha256.Sum256(nil)
	nothing := hex.EncodeToString(empty[:])
	holds := func(ctx context.Context, name string) <-chan bool {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		held := make(chan bool, 1)
		go func() { held <- (readAs{directory: root, name: name, hash: nothing}).holds(ctx, look(info)) }()
		return held
	}
	promptly := func(what string, held <-chan bool, want bool) {
		t.Helper()
		select {
		case got := <-held:
			if got != want {
				t.Errorf("%s: it holds what the run read: %t; want %t", what, got, want)
			}
		case <-time.After(time.Second):
			t.Errorf("%s: reading it again had not returned a second after", what)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "empty"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	promptly("the control, the empty file the run read", holds(t.Context(), "empty"), true)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptly("a pipe", holds(t.Context(), "pipe"), false)
	huge, err := os.Create(filepath.Join(root, "huge"))
	if err != nil {
		t.Fatal(err)
	}
	// Eight gibibytes, of which the disk holds none, and seconds to read.
	if err := huge.Truncate(8 << 30); err != nil {
		t.Fatal(err)
	}
	_ = huge.Close()
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	promptly("a huge file, cancelled while it is read", holds(ctx, "huge"), false)
}

// nothing reads a file to its end, as every reader of a file beside a
// build does (Read), and uses nothing of it.
func nothing(r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

// hashOf is the hash of content, as a run says what a file held.
func hashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// awaitedWithin is what a wait answered within a while; nothing, and
// the deadline, where nothing was.
func awaitedWithin(t *testing.T, looks *Looks, within time.Duration) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	return looks.Await(ctx, time.Millisecond)
}

// awaits holds a wait, looking every millisecond, to answer want within
// a while; where want is nil, nothing.
func awaits(t *testing.T, looks *Looks, within time.Duration, want []string) {
	t.Helper()
	changed, err := awaitedWithin(t, looks, within)
	if want == nil && !errors.Is(err, context.DeadlineExceeded) || want != nil && (err != nil || !slices.Equal(changed, want)) {
		t.Errorf("%v (%v); want %v", changed, err, want)
	}
}

// justWritten writes a file, and says when it was written, as a keeper
// goes by it (newest), and how it looks.
func justWritten(t *testing.T, file, content string) (time.Time, looked) {
	t.Helper()
	testfiles.Write(t, file, content)
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	return time.Unix(0, newest(look(info))), look(info)
}

// A file looked at beside a tree is looked at once, whenever it was
// written, and nothing is waited for: one that had not settled then,
// read by its reader, is doubted, and read again once it has (Await),
// and named only where it does not hold what the reader used (Read). So a part of a daemon, or
// a manifest, written the moment before it was looked at is neither
// waited on nor answered by every wait. Here the look is taken the
// moment the file was written (in the moment its times are told in),
// and an hour before (a disk whose clock is ahead, on which the file
// never settles by waiting). Controls: the same file looked at once it
// had settled is not doubted, nor read; and one that, read again, does
// not hold what was read is answered.
func TestAFileBesideIsLookedAtOnceAndReadAgainWhereItHadNotSettled(t *testing.T) {
	file := filepath.Join(t.TempDir(), "build.yaml")
	written, as := justWritten(t, file, "settings: these\n")
	for name, tc := range map[string]struct {
		at      time.Time
		doubted bool
	}{
		"the moment it was written":        {written, true},
		"an hour before, on a disk ahead":  {written.Add(-time.Hour), true},
		"the control, once it had settled": {written.Add(settles(as)), false},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			l, err := Beside(ctx, tc.at, file)
			if err != nil {
				t.Fatalf("looked at beside, it was waited on: %v", err)
			}
			if _, err := l.Read(file, nothing); err != nil {
				t.Fatal(err)
			}
			if doubted := !l.Settled(); doubted != tc.doubted || !l.whole[file].heldBy(hashOf("settings: these\n")) {
				t.Fatalf("doubted %t, with what was used %+v; want doubted %t, with what the file holds", doubted, l.whole[file], tc.doubted)
			}
			// The control's copy is taken first: the wait that finds the
			// file holding what was read doubts it no more in l.
			unlike := l.With()
			if changed, err := awaitedWithin(t, l.With(), 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("holding what was read, the wait answered %v (%v)", changed, err)
			}
			if !tc.doubted {
				return
			}
			unlike.whole[file] = usedOf{hashes: []string{hashOf("settings: other\n")}}
			if changed, err := awaitedWithin(t, unlike, time.Second); err != nil || !slices.Equal(changed, []string{file}) {
				t.Errorf("the control, not holding what was read: %v (%v); want %s", changed, err, file)
			}
		})
	}
}

// A file whose disk says it changed at a time still to come cannot
// settle by waiting: what the run read of it is doubted, and compared
// by what it holds whenever it is read again, as the run itself reads
// it whenever it is asked (Keeper.settled). It keeps nothing else from
// being answered: a file made elsewhere is; nor is it doubted no more
// once read again, as a file that has settled is: not holding what the
// run read after a wait found it holding it, it is answered. Here the
// wait's clock is an hour behind the disk's. Controls: with nothing
// written, nothing is answered; the file read, not holding what the run
// read, is.
func TestAFileThatCannotSettleIsReadAgainAndKeepsNothingElseFromBeingAnswered(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(t *testing.T, root string, looks *Looks)
		want   []string
	}{
		"the control, nothing written": {func(*testing.T, string, *Looks) {}, nil},
		"a file made elsewhere": {func(t *testing.T, root string, _ *Looks) {
			if err := os.WriteFile(filepath.Join(root, "app", "new.go"), []byte("package main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, []string{"app/new.go"}},
		"the control, the file read not holding what the run read": {func(_ *testing.T, _ string, looks *Looks) {
			as := looks.read[".cargo/config.toml"]
			as.hash = hashOf("[build]\njobs = 1\n")
			looks.read[".cargo/config.toml"] = as
		}, []string{".cargo/config.toml"}},
		"read again holding what the run read, and then not": {func(t *testing.T, _ string, looks *Looks) {
			if changed, err := awaitedWithin(t, looks, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("holding what the run read: %v (%v)", changed, err)
			}
			as := looks.read[".cargo/config.toml"]
			as.hash = hashOf("[build]\njobs = 1\n")
			looks.read[".cargo/config.toml"] = as
		}, []string{".cargo/config.toml"}},
	} {
		t.Run(name, func(t *testing.T) {
			root, _ := cargos(t)
			if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "app", "main.go"), []byte("package main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			behind := func() time.Time { return time.Now().Add(-time.Hour) }
			run, err := (&Keeper{now: behind}).Take(t.Context(), Tree{Root: root, Paths: map[string]string{"": "."}})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := run.Read(t.Context(), "", ".cargo/config.toml"); err != nil {
				t.Fatal(err)
			}
			looks := run.Looks()
			looks.now = behind
			if !slices.Equal(looks.doubted, []string{".cargo/config.toml"}) {
				t.Fatalf("doubted %v; want what the run read, which cannot settle", looks.doubted)
			}
			tc.change(t, root, looks)
			awaits(t, looks, time.Second, tc.want)
		})
	}
}

// A run that could not list one repository of two, failing the moment
// after a file was written, read nothing, and nothing of what it is
// held to (Unread) is doubted: with nothing written since, nothing is
// answered, where a file doubted with nothing read of it would be, as
// not holding what the run read. Here the look is taken the moment the
// file was written. Control: the file written again is answered.
func TestARunThatFailedTheMomentAfterAWriteDoubtsNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists a directory it may not read")
	}
	for name, tc := range map[string]struct {
		again bool
		want  []string
	}{
		"nothing written since":               {false, nil},
		"the control, the file written again": {true, []string{"a/main.go"}},
	} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			// The repository b may be entered and not read: it cannot be
			// listed.
			unlisted := filepath.Join(workspace, "b")
			justWritten(t, filepath.Join(unlisted, "main.go"), "package main\n")
			if err := os.Chmod(unlisted, 0o300); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(unlisted, 0o755) })
			written, _ := justWritten(t, filepath.Join(workspace, "a", "main.go"), "package main\n")
			tree := Tree{Root: workspace, Paths: map[string]string{"a": "a", "b": "b"}}
			_, failed := new(Keeper).Take(t.Context(), tree)
			if failed == nil {
				t.Fatal("a repository that cannot be listed was taken")
			}
			l := unread(t.Context(), tree, failed, written)
			if tc.again {
				time.Sleep(50 * time.Millisecond)
				if err := os.WriteFile(filepath.Join(workspace, "a", "main.go"), []byte("package main // more\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			awaits(t, l, time.Second, tc.want)
		})
	}
}

// takenAt is what a run of a repository, begun by a clock a test gives,
// listed, as it looked.
func takenAt(t *testing.T, root string, clock func() time.Time) *Looks {
	t.Helper()
	run, err := (&Keeper{now: clock}).Take(t.Context(), Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	return run.Looks()
}

// A link whose own change is said to be at a time still to come never
// settles by waiting, and holds up nothing else: its name names both the
// file it leads to, which had settled, and the link itself, each judged
// by its own look. A file made elsewhere is answered. Here the wait's
// clock is the moment the file the link leads to settled, and the link
// is made after it. Controls: with nothing written, nothing is
// answered; the link itself led elsewhere is answered.
func TestALinkAheadOfTheClockHoldsUpNothingElse(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(t *testing.T, root string)
		want   []string
	}{
		"the control, nothing written": {func(*testing.T, string) {}, nil},
		"a file made elsewhere": {func(t *testing.T, root string) {
			testfiles.Write(t, filepath.Join(root, "app", "new.go"), "package main\n")
		}, []string{"app/new.go"}},
		"the control, the link led elsewhere": {func(t *testing.T, root string) {
			link := filepath.Join(root, "app", "link.go")
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("old.go", link); err != nil {
				t.Fatal(err)
			}
		}, []string{"app/link.go"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			testfiles.Write(t, filepath.Join(root, "app", "old.go"), "package main // old\n")
			written, as := justWritten(t, filepath.Join(root, "app", "led.go"), "package main\n")
			at := written.Add(settles(as))
			clock := func() time.Time { return at }
			time.Sleep(time.Until(at) + 10*time.Millisecond)
			if err := os.Symlink("led.go", filepath.Join(root, "app", "link.go")); err != nil {
				t.Fatal(err)
			}
			looks := takenAt(t, root, clock)
			looks.now = clock
			if target, link := looks.files["app/link.go"], looks.links["app/link.go"]; !settledBy(target, at.UnixNano()) || !ahead(link, at.UnixNano()) {
				t.Fatalf("the file the link leads to settled: %t, the link ahead: %t; the test wants both", settledBy(target, at.UnixNano()), ahead(link, at.UnixNano()))
			}
			tc.change(t, root)
			awaits(t, looks, time.Second, tc.want)
		})
	}
}

// A repository's ignore file, read as the run began, the moment it was
// written, had not settled: a write within the moment its times are
// told in, at its size, leaves it looking as the run held it, so it is
// doubted, and read again once it has, and named where it does not hold
// what the run read. The test writes it again at its size with other
// content, and stands in for the moment by holding the look to how the
// file looks after (the disk says when anything of a file changed, and
// no test sets that back). Control: nothing written, nothing is
// answered.
func TestAnIgnoreFileReadAsTheRunBeganBeforeItSettledIsReadAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		again bool
		want  []string
	}{
		"written again, at its size and looking as it did": {true, []string{IgnoreFile}},
		"the control, nothing written":                     {false, nil},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			testfiles.Write(t, filepath.Join(root, "app", "main.go"), "package main\n")
			ignore := filepath.Join(root, IgnoreFile)
			written, _ := justWritten(t, ignore, "*.log\n")
			looks := takenAt(t, root, func() time.Time { return written })
			if !slices.Contains(looks.doubted, IgnoreFile) || looks.read[IgnoreFile].hash != hashOf("*.log\n") {
				t.Fatalf("doubted %v, with what was read %+v; want the ignore file doubted, with what it held", looks.doubted, looks.read[IgnoreFile])
			}
			if tc.again {
				testfiles.Write(t, ignore, "*.tmp\n")
				info, err := os.Stat(ignore)
				if err != nil {
					t.Fatal(err)
				}
				looks.files[IgnoreFile] = look(info)
			}
			awaits(t, looks, time.Second, tc.want)
		})
	}
}

// What is read beside a tree is held to the very bytes its reader used,
// and not to what was there when it was looked at: a file looked at the
// moment it was written, holding one thing, written again to hold
// another, which its reader reads and uses, and written back to hold
// the first, does not hold what was used, and is answered. The test
// stands in for writes within the moment its times are told in, which
// leave a file looking as it did, by holding the look to how the file
// looks after them. So is one read twice that held otherwise each time,
// left holding what was read last. Control: left holding what was
// used, read once, nothing is answered.
func TestAFileBesideIsHeldToTheBytesItsReaderUsed(t *testing.T) {
	for name, tc := range map[string]struct {
		back, twice bool
		want        func(file string) []string
	}{
		"written back to what it held when it was looked at": {true, false, func(file string) []string { return []string{file} }},
		"read twice, holding otherwise each time":            {false, true, func(file string) []string { return []string{file} }},
		"the control, left holding what was used":            {false, false, func(string) []string { return nil }},
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "build.yaml")
			written, _ := justWritten(t, file, "settings: AAAA\n")
			l, err := Beside(t.Context(), written, file)
			if err != nil {
				t.Fatal(err)
			}
			if tc.twice {
				if _, err := l.Read(file, nothing); err != nil {
					t.Fatal(err)
				}
			}
			testfiles.Write(t, file, "settings: BBBB\n")
			if used, err := l.Read(file, nothing); err != nil || used != hashOf("settings: BBBB\n") {
				t.Fatalf("read %s (%v); want what the file holds", used, err)
			}
			if tc.back {
				testfiles.Write(t, file, "settings: AAAA\n")
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			l.files[file] = look(info)
			awaits(t, l, time.Second, tc.want(file))
		})
	}
}

// A file beside a tree that its reader could not read, one its owner may
// not read, whose change is said to be at a time still to come, is held
// to how it looks: nothing was used of it to read again, and three
// waits in a row answer nothing, where one doubted and never read would
// be answered by each, and the command build again for ever. Here the
// wait's clock is an hour behind the disk's. Control: made readable,
// with other content, it is answered.
func TestAFileBesideThatCouldNotBeReadIsAnsweredOnlyOnceItChanges(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads what it may not")
	}
	file := filepath.Join(t.TempDir(), "build.yaml")
	written, _ := justWritten(t, file, "settings: these\n")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o644) })
	behind := func() time.Time { return written.Add(-time.Hour) }
	l, err := Beside(t.Context(), behind(), file)
	if err != nil {
		t.Fatal(err)
	}
	l.now = behind
	if _, err := l.Read(file, nothing); err == nil {
		t.Fatal("a file its owner may not read was read")
	}
	for i := range 3 {
		if changed, err := awaitedWithin(t, l.With(), 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unreadable and unchanged, wait %d: %v (%v); want nothing answered", i+1, changed, err)
		}
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	testfiles.Write(t, file, "settings: other\n")
	awaits(t, l.With(), time.Second, []string{file})
}

// A link led elsewhere is answered once it has settled, as a file is:
// its own look, which had not settled when it was looked at, holds the
// wait, though the file it leads to now had settled long before, and a
// write within the moment its times are told in may yet lead it
// elsewhere again. Here the wait's clock is the moment the link was
// made again. Control: the clock past that moment, it is answered.
func TestALinkLedElsewhereIsAnsweredOnceItHasSettled(t *testing.T) {
	root := t.TempDir()
	testfiles.Write(t, filepath.Join(root, "app", "old.go"), "package main // old\n")
	written, as := justWritten(t, filepath.Join(root, "app", "led.go"), "package main\n")
	link := filepath.Join(root, "app", "link.go")
	if err := os.Symlink("led.go", link); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(written.Add(2*settles(as))) + 10*time.Millisecond)
	looks := takenAt(t, root, time.Now)
	if !looks.Settled() {
		t.Fatalf("doubted %v; the test wants a look in which everything had settled", looks.doubted)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("old.go", link); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	made := time.Unix(0, newest(look(info)))
	clock := made
	looks.now = func() time.Time { return clock }
	if old, err := os.Stat(link); err != nil || !settledBy(look(old), made.UnixNano()) {
		t.Fatalf("the file the link leads to had settled: %v; the test wants it to", err)
	}
	awaits(t, looks, 300*time.Millisecond, nil)
	clock = made.Add(settles(look(info)))
	awaits(t, looks, time.Second, []string{"app/link.go"})
}

// parts lays out what a daemon is made of in a directory of the test's
// (settings, a plugin in a directory of plugins, authorities, the
// program's own file), each settled, and looks at them as a daemon
// reads them: each read whole through the look and the directory of
// plugins held by its listing. It returns the look and each part's path.
func parts(t *testing.T) (*Looks, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"settings": "build.yaml", "a plugin": "plugins/go.wasm", "the authorities": "ca.pem", "the program": "program"}
	paths := map[string]string{}
	var last looked
	for part, name := range files {
		paths[part] = filepath.Join(dir, filepath.FromSlash(name))
		_, last = justWritten(t, paths[part], "what "+part+" holds\n")
	}
	time.Sleep(2*settles(last) + 10*time.Millisecond)
	made, err := Beside(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	made.Lists(filepath.Join(dir, "plugins"), ".wasm")
	for _, path := range paths {
		if _, err := made.Read(path, nothing); err != nil {
			t.Fatal(err)
		}
	}
	if !made.Settled() {
		t.Fatal("parts written long before they were looked at are doubted")
	}
	return made, paths
}

// waitsThrice waits three times, each on a copy of a look (With), and
// holds each to answer nothing and the look to what after says once it
// has ended.
func waitsThrice(t *testing.T, l *Looks, after func(wait int)) {
	t.Helper()
	for i := range 3 {
		if changed, err := awaitedWithin(t, l.With(), 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("wait %d answered %v (%v); want nothing", i+1, changed, err)
		}
		after(i + 1)
	}
}

// writtenAgain writes a file again, by a way, and holds it to look
// otherwise than before.
func writtenAgain(t *testing.T, file string, how func(t testing.TB, file string)) {
	t.Helper()
	was, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	how(t, file)
	now, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if look(now) == look(was) {
		t.Fatalf("%s written again looks as it did: the test shows nothing", file)
	}
}

// A part of what a daemon is made of written again with what it held
// (touched, copied over, moved over by the same bytes, installed again)
// looks otherwise, and is read again: holding what the daemon read, it
// is no change, and no wait answers it. The look every wait is given a
// copy of takes it as it looks now, by the first wait, so that no wait
// after finds it looking otherwise and reads it again, where each would
// read it again and, before, answer it, and the command build again for
// ever. Control: written again in place with other content of its
// size, it is answered.
func TestAPartOfADaemonWrittenAgainWithWhatItHeldIsAnsweredByNoWait(t *testing.T) {
	for how, write := range testfiles.Again {
		for _, part := range []string{"settings", "a plugin", "the authorities", "the program"} {
			t.Run(part+", "+how, func(t *testing.T) {
				made, paths := parts(t)
				file := paths[part]
				writtenAgain(t, file, write)
				now, err := os.Lstat(file)
				if err != nil {
					t.Fatal(err)
				}
				waitsThrice(t, made, func(wait int) {
					// Taken as it looks now by the first wait: no wait after
					// finds it looking otherwise, and none reads it again.
					if made.files[file] != look(now) || !made.Settled() {
						t.Fatalf("after wait %d the look waits are given holds %s as %+v, doubted %v; want it as it looks now, %+v", wait, file, made.files[file], made.doubted, look(now))
					}
				})
				other := func(t testing.TB, file string) {
					testfiles.Write(t, file, "What "+part+" holds\n")
				}
				writtenAgain(t, file, other)
				awaits(t, made.With(), time.Second, []string{file})
			})
		}
	}
}

// A part of a daemon read before it had settled is doubted and read
// again once it has (reread): holding what the daemon read, it is
// doubted no more by the look every wait is given a copy of, from the
// first wait on, and so is read again by the first wait alone: no wait
// after is given it doubted. Before, each wait read it again. Control:
// one said to have changed at a time still to come, which never
// settles, stays doubted, and is read again by every wait.
func TestAPartReadBeforeItSettledIsReadAgainByTheFirstWaitAlone(t *testing.T) {
	for name, behind := range map[string]time.Duration{
		"settled once read again":                       0,
		"the control, said to change at a time to come": time.Hour,
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "build.yaml")
			written, _ := justWritten(t, file, "settings: these\n")
			clock := func() time.Time { return time.Now().Add(-behind) }
			made, err := Beside(t.Context(), written.Add(-behind))
			if err != nil {
				t.Fatal(err)
			}
			made.now = clock
			if _, err := made.Read(file, nothing); err != nil {
				t.Fatal(err)
			}
			if made.Settled() {
				t.Fatal("a part read the moment it was written is not doubted")
			}
			waitsThrice(t, made, func(wait int) {
				// What a wait is given doubted it reads again: doubted no
				// more, it is read again by no wait after the first.
				if doubted := !made.Settled(); doubted != (behind != 0) || doubted && (!slices.Equal(made.doubted, []string{file}) || !made.whole[file].heldBy(hashOf("settings: these\n"))) {
					t.Fatalf("after wait %d the look waits are given doubts %v, with what was used %+v; want it doubted: %t", wait, made.doubted, made.whole[file], behind != 0)
				}
			})
		})
	}
}

// beside is a file beside a tree, of the test's, holding settings.
func beside(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "build.yaml")
	testfiles.Write(t, file, "settings: AAAA\n")
	return file
}

// answering is what a wait is to answer: the file, where it is
// answered, and nothing otherwise.
func answering(answered bool, file string) []string {
	if answered {
		return []string{file}
	}
	return nil
}

// settledBeside looks at a file beside a tree once it had settled, at
// the moment it did, and reads it through the look.
func settledBeside(t *testing.T, file string) *Looks {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	l, err := Beside(t.Context(), time.Unix(0, newest(look(info))).Add(settles(look(info))), file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Read(file, nothing); err != nil {
		t.Fatal(err)
	}
	return l
}

// A file beside a tree written as its reader read it, appended to once
// its reader had read it to its end and before Read returned (a save
// still writing), holds what its reader never used: Read says the hash
// of what the reader used, and the look holds no hash of the file and
// doubts it, so a wait answers it, where reading it whole again would
// find it holding what was hashed after the reader and take it as it
// looks now. Here the file had settled long before it was looked at.
// Control: nothing appended, Read says what the file holds and nothing
// is answered.
func TestAFileBesideWrittenAsItWasReadIsAnswered(t *testing.T) {
	for name, appended := range map[string]bool{
		"appended to once its reader had read it": true,
		"the control, nothing appended":           false,
	} {
		t.Run(name, func(t *testing.T) {
			file, l := readSettled(t, func(file string) {
				if !appended {
					return
				}
				appending, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer appending.Close()
				if _, err := appending.WriteString("jobs: 1\n"); err != nil {
					t.Fatal(err)
				}
			})
			awaits(t, l.With(), time.Second, answering(appended, file))
		})
	}
}

// Waits on what a daemon is made of and a build's manifest run at once,
// each on its own copy (With), while every part and the manifest are
// written again in place with what they held: what each wait learns it
// tells the looks they share (keep) under each one's lock, and each
// copies them under it, so that, run with the race detector, nothing is
// read as it is written. No wait answers anything, and each shared look
// is left holding each file as it looks now.
func TestWaitsAtOnceTellTheLooksTheyShareUnderTheirLocks(t *testing.T) {
	made, paths := parts(t)
	file := filepath.Join(t.TempDir(), "frontseat.yaml")
	testfiles.Write(t, file, "repositories: {}\n")
	time.Sleep(50 * time.Millisecond)
	manifest := settledBeside(t, file)
	shared := map[string]*Looks{file: manifest}
	for _, part := range paths {
		shared[part] = made
	}
	for range 4 {
		for name := range shared {
			writtenAgain(t, name, testfiles.Again["in place"])
		}
		var waits sync.WaitGroup
		// Each waits again and again, every wait as long as the one
		// before and a millisecond more, from one to forty: some end
		// before they have looked twice, some as another takes what it
		// learned, some after. Each of a context of its own, which
		// shares no lock with the others' as the test's would: what
		// orders the waits is the looks' locks.
		round := time.Now().Add(250 * time.Millisecond)
		for range 8 {
			waits.Go(func() {
				for i := 0; time.Now().Before(round); i++ {
					ctx, cancel := context.WithTimeout(context.Background(), time.Duration(1+i%40)*time.Millisecond)
					changed, err := made.With(manifest).Await(ctx, time.Millisecond)
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("a wait answered %v (%v); want nothing", changed, err)
					}
				}
			})
		}
		waits.Wait()
	}
	for name, l := range shared {
		now, err := os.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		if l.files[name] != look(now) || !l.Settled() {
			t.Errorf("the look shared holds %s as %+v, doubted %v; want it as it looks now, %+v", name, l.files[name], l.doubted, look(now))
		}
	}
}

// Two looks a wait is made of (With) that each read one file beside,
// holding otherwise each time, as a file written between the daemon's
// reading and a build's would be, hold it to both: written
// again with what the second reader read, it is answered, since the
// first reader used what it no longer holds. Control: both read what it
// holds, it is not answered.
func TestTwoLooksThatReadOneFileOtherwiseHoldNoHashOfItTogether(t *testing.T) {
	for name, between := range map[string]bool{
		"written between the two readings": true,
		"the control, nothing written":     false,
	} {
		t.Run(name, func(t *testing.T) {
			file, first := readAndWritten(t, between)
			second := settledBeside(t, file)
			writtenAgain(t, file, testfiles.Again["in place"])
			awaits(t, first.With(second), time.Second, answering(between, file))
		})
	}
}

// What a wait learns of a file beside, that it holds what was read of
// it, it tells only those looks it was made of that read the same of
// it: one that read it again since the wait's copy was made, holding
// otherwise, keeps it as it looked and a wait on it answers it, though
// the wait's copy took it. Control: one that read nothing since takes
// it as it looks now, and a wait on it answers nothing.
func TestAWaitTellsWhatItLearnsOnlyToTheLooksThatReadTheSame(t *testing.T) {
	for name, since := range map[string]bool{
		"read again since, holding otherwise": true,
		"the control, read nothing since":     false,
	} {
		t.Run(name, func(t *testing.T) {
			file := beside(t)
			made := settledBeside(t, file)
			copied := made.With()
			if since {
				writtenAgain(t, file, func(t testing.TB, file string) { testfiles.Write(t, file, "settings: BBBB\n") })
				if _, err := made.Read(file, nothing); err != nil {
					t.Fatal(err)
				}
			}
			writtenAgain(t, file, func(t testing.TB, file string) { testfiles.Write(t, file, "settings: AAAA\n") })
			awaits(t, copied, 300*time.Millisecond, nil)
			awaits(t, made.With(), time.Second, answering(since, file))
		})
	}
}

// A part written again with what it held whose disk says it changed at
// a time still to come is taken as it looks now and stays doubted: a
// write within the moment its times are told in would leave it looking
// so, and no wait settles it, so it is read again whenever the rest has
// settled. Here the wait's clock is an hour behind the disk's. Control:
// the clock not behind, it is doubted no more.
func TestAPartWrittenAgainWithWhatItHeldAheadOfTheClockStaysDoubted(t *testing.T) {
	for name, behind := range map[string]time.Duration{
		"said to change at a time to come":  time.Hour,
		"the control, the clock not behind": 0,
	} {
		t.Run(name, func(t *testing.T) {
			made, paths := parts(t)
			made.now = func() time.Time { return time.Now().Add(-behind) }
			file := paths["settings"]
			writtenAgain(t, file, testfiles.Again["in place"])
			awaits(t, made.With(), 300*time.Millisecond, nil)
			now, err := os.Lstat(file)
			if err != nil {
				t.Fatal(err)
			}
			doubted := !made.Settled()
			if made.files[file] != look(now) || doubted != (behind != 0) || doubted && (!slices.Equal(made.doubted, []string{file}) || !made.whole[file].heldBy(hashOf("what settings holds\n"))) {
				t.Errorf("the look waits are given holds %s as %+v, doubted %v with what was used %+v; want it as it looks now, %+v, doubted: %t", file, made.files[file], made.doubted, made.whole[file], look(now), behind != 0)
			}
		})
	}
}

// A file beside a build written again with what it held while its
// reader read it, once the reader had read it to its end (touched, its
// mode changed, written again in place, moved over by a file of the
// same bytes), still holds the bytes that were used: three waits on the
// look answer nothing, and the look they share takes it as it looks
// now. Before, Read looked at the open file again and, finding it
// looking otherwise, held no hash of it, and every wait answered it.
// Control: written in place with other content of its size, it is
// answered.
func TestAFileBesideWrittenAgainWithWhatItHeldAsItWasReadIsAnsweredByNoWait(t *testing.T) {
	ways := map[string]func(t testing.TB, file string){
		"touched": func(t testing.TB, file string) {
			now := time.Now()
			if err := os.Chtimes(file, now, now); err != nil {
				t.Fatal(err)
			}
		},
		"its mode changed": func(t testing.TB, file string) {
			if err := os.Chmod(file, 0o640); err != nil {
				t.Fatal(err)
			}
		},
		"written again in place": testfiles.Again["in place"],
		"moved over it":          testfiles.Again["moved over it"],
		"the control, other content of its size": func(t testing.TB, file string) {
			testfiles.Write(t, file, "settings: those\n")
		},
	}
	for how, write := range ways {
		t.Run(how, func(t *testing.T) {
			file, l := readSettled(t, func(file string) { writtenAgain(t, file, write) })
			if strings.HasPrefix(how, "the control") {
				awaits(t, l.With(), time.Second, []string{file})
				return
			}
			now, err := os.Lstat(file)
			if err != nil {
				t.Fatal(err)
			}
			waitsThrice(t, l, func(wait int) {
				if l.files[file] != look(now) || !l.Settled() {
					t.Fatalf("after wait %d the look holds %s as %+v, doubted %v; want it as it looks now, %+v", wait, file, l.files[file], l.doubted, look(now))
				}
			})
		})
	}
}

// Two looks a wait is made of that read one file beside, holding
// otherwise each time, used two contents of it: whatever it holds now
// differs from one of them, so it is answered, with nothing written
// after, whichever of the two the wait is made from and whether or not
// the second reading had settled. Before, a mark that no file hashes to
// was kept in one order alone, and the wait made the other way, finding
// the file looking as the last reading left it, answered nothing.
// Control: both read the same bytes, it is not answered.
func TestTwoLooksThatReadOneFileOtherwiseAreAnsweredInEitherOrder(t *testing.T) {
	for _, between := range []bool{true, false} {
		for _, settled := range []bool{true, false} {
			for order, with := range map[string]func(first, second *Looks) *Looks{
				"the first with the second": func(first, second *Looks) *Looks { return first.With(second) },
				"the second with the first": func(first, second *Looks) *Looks { return second.With(first) },
			} {
				name := fmt.Sprintf("%s, written between: %t, the second reading settled: %t", order, between, settled)
				t.Run(name, func(t *testing.T) {
					file, first := readAndWritten(t, between)
					second := settledBeside(t, file)
					if !settled {
						info, err := os.Stat(file)
						if err != nil {
							t.Fatal(err)
						}
						if second, err = Beside(t.Context(), time.Unix(0, newest(look(info))), file); err != nil {
							t.Fatal(err)
						}
						if _, err := second.Read(file, nothing); err != nil {
							t.Fatal(err)
						}
						if second.Settled() {
							t.Fatal("read the moment it was written, the second reading is not doubted")
						}
					}
					awaits(t, with(first, second), time.Second, answering(between, file))
				})
			}
		}
	}
}

// Whatever reads a file beside a build through the look reads it to its
// end: what it left unread no hash says, and a reader that returns
// before the end is refused, by the file's name. Control: a reader that
// reads it to its end is not.
func TestAReaderOfAFileBesideThatStopsBeforeItsEndIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		read    func(io.Reader) error
		refused bool
	}{
		"stopped after a byte": {func(r io.Reader) error {
			_, err := r.Read(make([]byte, 1))
			return err
		}, true},
		"the control, read to its end": {nothing, false},
	} {
		t.Run(name, func(t *testing.T) {
			file := beside(t)
			l, err := Beside(t.Context(), time.Now(), file)
			if err != nil {
				t.Fatal(err)
			}
			_, err = l.Read(file, tc.read)
			if refused := err != nil; refused != tc.refused || refused && !strings.Contains(err.Error(), file) {
				t.Fatalf("refused: %v; want refused %t, by the file's name", err, tc.refused)
			}
		})
	}
}

// A file beside whose reader failed used no bytes that are known: it is
// answered whenever a wait would read it again, where it looks otherwise
// or was read before it had settled, and a round after reads it afresh:
// read then once it has settled, and failing again, it is answered by no
// wait while it is left as it is. So a failed read is answered once, and
// no wait loops on it: what holds that is the round after, whose look is
// of a reading that had settled. (What the daemon is made of is never
// such a look: a part that fails to be read starts no daemon.) Here,
// too, one read before it settled on a disk whose clock is ahead, which
// no wait settles, is held to how it looks, and answered by no wait.
func TestAFileBesideWhoseReaderFailedIsAnsweredOnceAndNotAgain(t *testing.T) {
	failing := func(r io.Reader) error {
		if err := nothing(r); err != nil {
			return err
		}
		return errors.New("settings that say nothing")
	}
	for name, tc := range map[string]struct {
		unsettled, touched bool
		behind             time.Duration
		answered           bool
	}{
		"the control, settled, nothing written":       {false, false, 0, false},
		"settled, touched":                            {false, true, 0, true},
		"read before it had settled":                  {true, false, 0, true},
		"read before it had settled, on a disk ahead": {true, false, time.Hour, false},
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "build.yaml")
			written, as := justWritten(t, file, "settings: these\n")
			clock := func() time.Time { return time.Now().Add(-tc.behind) }
			round := func(at time.Time) *Looks {
				l, err := Beside(t.Context(), at, file)
				if err != nil {
					t.Fatal(err)
				}
				l.now = clock
				if _, err := l.Read(file, failing); err == nil {
					t.Fatal("a reader that failed was not said to")
				}
				return l
			}
			at := written.Add(settles(as))
			if tc.unsettled {
				at = written.Add(-tc.behind)
			}
			l := round(at)
			if tc.touched {
				writtenAgain(t, file, func(t testing.TB, file string) {
					now := time.Now()
					if err := os.Chtimes(file, now, now); err != nil {
						t.Fatal(err)
					}
				})
			}
			if !tc.answered {
				waitsThrice(t, l, func(int) {})
				return
			}
			awaits(t, l.With(), time.Second, []string{file})
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			waitsThrice(t, round(time.Unix(0, newest(look(info))).Add(settles(look(info)))), func(int) {})
		})
	}
}

// One look that read one file beside twice, holding otherwise each
// time, used two contents of it, and is answered with nothing written
// after, though each reading had settled by the time the look is said
// to be taken at: a look that reads it twice is held to both, by the
// same rule as two looks are. The test says the look was taken once the
// second content had settled, which a look taken before the first
// reading never is. Control: both readings used the same bytes.
func TestOneLookThatReadOneFileOtherwiseIsAnswered(t *testing.T) {
	for name, between := range map[string]bool{
		"written between the two readings": true,
		"the control, nothing written":     false,
	} {
		t.Run(name, func(t *testing.T) {
			file, l := readAndWritten(t, between)
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			l.at = time.Unix(0, newest(look(info))).Add(settles(look(info))).UnixNano()
			if _, err := l.Read(file, nothing); err != nil {
				t.Fatal(err)
			}
			awaits(t, l.With(), time.Second, answering(between, file))
		})
	}
}

// A file beside read again by a wait that looks otherwise when it is
// opened than the wait's look said, as one written between the look and
// the reading does, is neither answered nor taken: what was read of it
// is not used, and it is read again by a wait after. Here the wait's
// look is said to have found it at another size. Control: the look as
// the file is, it holds what was used.
func TestAFileBesideThatChangedAsAWaitReadItAgainIsReadAgainLater(t *testing.T) {
	for name, tc := range map[string]struct {
		otherwise bool
		want      verdict
	}{
		"looking otherwise when it is read again": {true, unknown},
		"the control, looking as the wait found":  {false, holds},
	} {
		t.Run(name, func(t *testing.T) {
			file := beside(t)
			l := settledBeside(t, file)
			now := l.With()
			if tc.otherwise {
				as := now.files[file]
				as.size++
				now.files[file] = as
			}
			if hash, got := l.unlike(t.Context(), now, file); got != tc.want || got == holds && hash != hashOf("settings: AAAA\n") {
				t.Fatalf("read again: %v, %s; want %v", got, hash, tc.want)
			}
		})
	}
}

// A file beside read again by a wait, and written while it was read,
// is neither answered nor taken: what was read of it is part of what it
// held and part of what it was written with, and is not used; it is
// read again by a wait after. Here the file is written, in place, with
// other content of its size once its first part was read, and written
// back with what it held before the rest is, so that it holds what was
// used once the reading ends, and only its look, after, says it was
// written meanwhile. Control: nothing written as it is read, it holds
// what was used.
func TestAFileBesideWrittenAsAWaitReadItAgainIsReadAgainLater(t *testing.T) {
	// Two parts of a reading (digest reads a part at a time).
	held := strings.Repeat("A", part+part/4)
	for name, written := range map[string]bool{
		"written as it is read again":            true,
		"the control, nothing written meanwhile": false,
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "build.yaml")
			testfiles.Write(t, file, held)
			// Settled as the disk tells it, not only as the look is told:
			// written in the tick of the disk's clock it was written in,
			// on a system whose files are dated by a coarse clock, it
			// would look as it did.
			passed(t, file)
			l := settledBeside(t, file)
			now := l.With()
			ctx := &asked{Context: t.Context(), each: func(times int) {
				if !written {
					return
				}
				// A reading asks before each part: the second time is
				// once the first part was read, the third once the rest.
				switch times {
				case 2:
					rewrite(t, file, strings.Repeat("B", len(held)))
					if info, err := os.Stat(file); err != nil || look(info) == now.files[file] {
						t.Fatalf("%s written as it is read looks as it did (%v): the test shows nothing", file, err)
					}
				case 3:
					rewrite(t, file, held)
				}
			}}
			hash, got := l.unlike(ctx, now, file)
			if written && (ctx.times < 3 || readBack(t, file) != held) {
				t.Fatalf("the reading asked %d times, and the file holds what was used: %t; the test shows nothing", ctx.times, readBack(t, file) == held)
			}
			want := holds
			if written {
				want = unknown
			}
			if got != want || got == holds && hash != hashOf(held) {
				t.Fatalf("read again: %v, %s; want %v", got, hash, want)
			}
		})
	}
}

// asked is a context that does something each time it is asked whether
// it has ended (Err), and counts the times: a reading asks it before
// each part of a file (until), so each is a moment within the reading.
type asked struct {
	context.Context
	times int
	each  func(times int)
}

func (a *asked) Err() error {
	a.times++
	a.each(a.times)
	return a.Context.Err()
}

// passed waits until the disk's clock has passed when a file was last
// written or changed: until a file written beside it now is dated
// later, so that whatever writes the file next dates it otherwise.
func passed(t *testing.T, file string) {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	probe := file + ".clock"
	defer os.Remove(probe)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		testfiles.Write(t, probe, "")
		now, err := os.Stat(probe)
		if err != nil {
			t.Fatal(err)
		}
		if newest(look(now)) > newest(look(info)) {
			return
		}
	}
	t.Fatalf("the disk's clock did not pass when %s was written", file)
}

// rewrite writes a file again in place, so that whoever has it open
// reads what it is written with.
func rewrite(t *testing.T, file, content string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readBack is what a file holds.
func readBack(t *testing.T, file string) string {
	t.Helper()
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// readAndWritten is a file beside, holding settings, read through a look
// once it had settled, and, where between, written after with other
// content of its size.
func readAndWritten(t *testing.T, between bool) (string, *Looks) {
	t.Helper()
	file := beside(t)
	first := settledBeside(t, file)
	if between {
		writtenAgain(t, file, func(t testing.TB, file string) { testfiles.Write(t, file, "settings: BBBB\n") })
	}
	return file, first
}

// readSettled writes a file beside, holding settings, and reads it
// through a look taken once it had settled, by a reader that does
// meanwhile to it once it has read it to its end; Read is held to say
// the hash of what the reader used. It says the file and the look.
func readSettled(t *testing.T, meanwhile func(file string)) (string, *Looks) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "build.yaml")
	written, as := justWritten(t, file, "settings: these\n")
	l, err := Beside(t.Context(), written.Add(settles(as)), file)
	if err != nil {
		t.Fatal(err)
	}
	used, err := l.Read(file, func(r io.Reader) error {
		if err := nothing(r); err != nil {
			return err
		}
		meanwhile(file)
		return nil
	})
	if err != nil || used != hashOf("settings: these\n") {
		t.Fatalf("Read said %s (%v); want the hash of what its reader used", used, err)
	}
	return file, l
}
