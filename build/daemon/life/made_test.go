package life

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// told lays out a daemon's settings in a directory of the test's, with
// two plugins and the authorities its grid is known by, and a file that
// stands in for this program's own (programOf), and returns the
// settings' file.
func told(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	known, err := os.ReadFile(testauthority.New(t).File)
	if err != nil {
		t.Fatal(err)
	}
	testfiles.Tree(t, dir, map[string]string{"plugins/go.wasm": "go", "plugins/cargo.wasm": "cargo", "ca.pem": string(known), "program": "the program",
		"build.yaml": "version: 1\nplugins: plugins\ngrid:\n  address: grid.example:8980\n  authorities: ca.pem\n"})
	return filepath.Join(dir, "build.yaml")
}

// again writes a file again in place, with other content of the same
// size, and gives it back the time it had: what a file system says of it
// is as it was.
func again(t *testing.T, file string, change func([]byte)) {
	t.Helper()
	was, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	change(content)
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, was.ModTime(), was.ModTime()); err != nil {
		t.Fatal(err)
	}
	if now, err := os.Stat(file); err != nil || now.Size() != was.Size() || !now.ModTime().Equal(was.ModTime()) {
		t.Fatalf("%s written again is %v (%v); want the size and the time it had", file, now, err)
	}
}

// What a daemon is made of is said by content: the settings' bytes,
// every plugin they load and the authorities they name. A plugin or the
// authorities written again in place, at the same size and with the time
// they had, make another daemon, and so does a plugin under another
// name; what a daemon loads is the bytes that were digested. A file of
// these that cannot be read is an error that names it, never a file left
// out. The control is the same files read again, which make the same.
func TestWhatADaemonIsMadeOfIsSaidByContent(t *testing.T) {
	file := told(t)
	dir := filepath.Dir(file)
	first, err := Read(file)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := Read(file); err != nil || same.Made != first.Made {
		t.Fatalf("the control: read again with nothing changed: %+v, %v; want %+v", same.Made, err, first.Made)
	}
	if len(first.Modules) != 2 || string(first.Modules[0]) != "cargo" || string(first.Modules[1]) != "go" || len(first.Made.Program) != 64 {
		t.Fatalf("it loads %q as the program %q; want the two plugins' own bytes, by name, and the program's digest", first.Modules, first.Made.Program)
	}
	made := first.Made
	differs := func(what string) {
		t.Helper()
		now, err := Read(file)
		if err != nil {
			t.Fatal(err)
		}
		if now.Made.Settings == made.Settings {
			t.Errorf("%s: a daemon is made of the same as before", what)
		}
		if now.Made.Program != made.Program {
			t.Errorf("%s: the program is another", what)
		}
		made = now.Made
	}
	again(t, filepath.Join(dir, "plugins", "go.wasm"), func(content []byte) { copy(content, "og") })
	differs("a plugin written again in place")
	if now, _ := Read(file); string(now.Modules[1]) != "og" {
		t.Errorf("after a plugin was written again a daemon loads %q", now.Modules[1])
	}
	again(t, filepath.Join(dir, "ca.pem"), func(content []byte) {
		// The same certificate, written with another line's end.
		content[len(content)-1] = ' '
	})
	differs("the authorities written again in place")
	if now, _ := Read(file); len(now.Settings.Grid.Known) == 0 || now.Settings.Grid.Known[len(now.Settings.Grid.Known)-1] != ' ' {
		t.Errorf("the grid is known by %d bytes; want the authorities as they were digested, and their file not read again", len(now.Settings.Grid.Known))
	}
	if err := os.Rename(filepath.Join(dir, "plugins", "go.wasm"), filepath.Join(dir, "plugins", "to.wasm")); err != nil {
		t.Fatal(err)
	}
	differs("a plugin under another name")
	again(t, file, func(content []byte) { copy(content[len(content)-7:], "ac.pem\n") })
	if _, err := Read(file); err == nil {
		t.Error("settings that name authorities that are not there were read")
	}
	again(t, file, func(content []byte) { copy(content[len(content)-7:], "ca.pem\n") })
	if os.Getuid() == 0 {
		return
	}
	for _, unread := range []string{filepath.Join(dir, "plugins", "cargo.wasm"), filepath.Join(dir, "ca.pem")} {
		if err := os.Chmod(unread, 0o000); err != nil {
			t.Fatal(err)
		}
		if got, err := Read(file); err == nil || !strings.Contains(err.Error(), unread) {
			t.Errorf("with %s unreadable: %+v, %v; want an error that names it", unread, got.Made, err)
		}
		if err := os.Chmod(unread, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// What saying what a daemon is made of costs each command: this
// program's own file, the settings, two plugins and the authorities,
// each read whole. A command pays it before it asks its daemon anything.
func BenchmarkWhatADaemonIsMadeOf(b *testing.B) {
	file := told(b)
	for b.Loop() {
		if _, err := Read(file); err != nil {
			b.Fatal(err)
		}
	}
}

// programOf is the file that stands in for this program's own beside
// a daemon's settings (told): a test does not write the program it runs.
func programOf(file string) string { return filepath.Join(filepath.Dir(file), "program") }

// madeAt is what a daemon of a file of settings is made of, looked at a
// time and read as the daemon reads it when it starts (madeAndRead),
// with meanwhile run once the settings are read and before what they
// name is; and what it read.
func madeAt(t *testing.T, at time.Time, file string, meanwhile func()) (*source.Looks, Loaded) {
	t.Helper()
	made, loaded, err := madeAndRead(t.Context(), at, file, programOf(file), meanwhile)
	if err != nil {
		t.Fatal(err)
	}
	return made, loaded
}

// answersNothing holds three waits running, each on a copy of what a
// daemon is made of as a wait is given one, to answer nothing.
func answersNothing(t *testing.T, made *source.Looks) {
	t.Helper()
	for i := range 3 {
		waiting, stop := context.WithTimeout(t.Context(), 200*time.Millisecond)
		changed, err := made.With().Await(waiting, time.Millisecond)
		stop()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("with nothing written, wait %d answered %v (%v)", i+1, changed, err)
		}
	}
}

// answers holds a wait on a copy of what a daemon is made of to answer
// want.
func answers(t *testing.T, made *source.Looks, want ...string) {
	t.Helper()
	waiting, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	if changed, err := made.With().Await(waiting, time.Millisecond); err != nil || !slices.Equal(changed, want) {
		t.Errorf("the wait answered %v (%v); want %v", changed, err, want)
	}
}

// What a daemon is made of is looked at once, before the daemon reads
// it, and nothing is waited for: a part written the moment before the
// daemon starts, which had not settled, is held to what the daemon read
// of it, and read again once it has; a wait for what a build read to change
// answers it only where it holds otherwise, and not every wait for as
// long as the daemon lives. Here the look is taken the moment the part
// was written. Control: the part written again is answered.
func TestWhatADaemonIsMadeOfWrittenAsItStartsIsReadAgainAndNotAnswered(t *testing.T) {
	for name, written := range map[string]bool{"nothing written since": false, "the control, the settings written again": true} {
		t.Run(name, func(t *testing.T) {
			file := told(t)
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			made, _ := madeAt(t, info.ModTime(), file, nil)
			if made.Settled() {
				t.Fatal("a part looked at the moment it was written is not doubted")
			}
			if !written {
				answersNothing(t, made)
				return
			}
			time.Sleep(50 * time.Millisecond)
			testfiles.Tree(t, filepath.Dir(file), map[string]string{filepath.Base(file): "version: 1\n"})
			answers(t, made, file)
		})
	}
}

// Each part of a daemon is held as the daemon read it when it started:
// the settings, each plugin they load, the authorities they name and
// this program's own file. Each, written the moment before the daemon
// read it, is doubted, held to the very bytes the daemon read and read
// again: three waits running with nothing written answer nothing. Each
// written again in place, at its size and with the time it had, makes
// another daemon (Read) and is answered, alone. Here the look is taken
// the moment the part was written, the others written earlier.
func TestEachPartOfADaemonIsHeldAsTheDaemonReadIt(t *testing.T) {
	for _, part := range []string{"build.yaml", "plugins/go.wasm", "plugins/cargo.wasm", "ca.pem", "program"} {
		t.Run(part, func(t *testing.T) {
			file := told(t)
			written := filepath.Join(filepath.Dir(file), filepath.FromSlash(part))
			content, err := os.ReadFile(written)
			if err != nil {
				t.Fatal(err)
			}
			// The others settle, and the part is written again as it was.
			time.Sleep(50 * time.Millisecond)
			testfiles.Write(t, written, string(content))
			info, err := os.Stat(written)
			if err != nil {
				t.Fatal(err)
			}
			made, loaded := madeAt(t, info.ModTime(), file, nil)
			if made.Settled() {
				t.Fatalf("%s, looked at the moment it was written, is not doubted: it was not read as a part of the daemon", part)
			}
			answersNothing(t, made)
			// Its last byte, which leaves the settings and the authorities
			// what they say.
			again(t, written, func(content []byte) { content[len(content)-1] = ' ' })
			if now, err := readOf(file, programOf(file), nil, nil); err != nil || now.Made == loaded.Made {
				t.Errorf("%s written again makes the same daemon (%v): it is no part of one", part, err)
			}
			answers(t, made, written)
		})
	}
}

// The directory of plugins is held as the daemon listed it: a plugin
// added after the daemon started, which the next daemon would load, is
// answered, and so are one removed, one made a link and a directory
// that cannot be listed, which the next daemon refuses. Controls: with nothing written, and with a file that
// is no plugin added, nothing is answered.
func TestTheDirectoryOfPluginsIsHeldAsTheDaemonListedIt(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(t *testing.T, plugins string)
		want   string
	}{
		"the control, nothing written": {func(*testing.T, string) {}, ""},
		"the control, a file that is no plugin added": {func(t *testing.T, plugins string) {
			testfiles.Write(t, filepath.Join(plugins, "README"), "plugins\n")
		}, ""},
		"a plugin added": {func(t *testing.T, plugins string) {
			testfiles.Write(t, filepath.Join(plugins, "npm.wasm"), "npm")
		}, "npm.wasm"},
		"a plugin removed": {func(t *testing.T, plugins string) {
			if err := os.Remove(filepath.Join(plugins, "cargo.wasm")); err != nil {
				t.Fatal(err)
			}
		}, "cargo.wasm"},
		"a plugin made a link to what it held": {func(t *testing.T, plugins string) {
			if err := os.Rename(filepath.Join(plugins, "cargo.wasm"), filepath.Join(plugins, "cargo.module")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("cargo.module", filepath.Join(plugins, "cargo.wasm")); err != nil {
				t.Fatal(err)
			}
		}, "cargo.wasm"},
		"the directory made one that cannot be listed": {func(t *testing.T, plugins string) {
			if os.Geteuid() == 0 {
				t.Skip("root lists a directory it may not read")
			}
			if err := os.Chmod(plugins, 0o300); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(plugins, 0o755) })
		}, "."},
	} {
		t.Run(name, func(t *testing.T) {
			file := told(t)
			plugins := filepath.Join(filepath.Dir(file), "plugins")
			made, _ := madeAt(t, time.Now(), file, nil)
			tc.change(t, plugins)
			if tc.want == "" {
				answersNothing(t, made)
				return
			}
			answers(t, made, filepath.Join(plugins, tc.want))
		})
	}
}

// A plugin that appears once the daemon has read its settings and
// before it reads its plugins is loaded, and held as it was read: what
// the daemon is made of is said by one reading of it, and nothing the
// daemon used goes unheld. Written again in place, at its size and with
// the time it had, it is answered. Control: with nothing written, three
// waits running answer nothing.
func TestAPluginThatAppearsAsTheDaemonReadsItsSettingsIsHeldAsItWasRead(t *testing.T) {
	for name, rewritten := range map[string]bool{"written again": true, "the control, nothing written": false} {
		t.Run(name, func(t *testing.T) {
			file := told(t)
			added := filepath.Join(filepath.Dir(file), "plugins", "npm.wasm")
			made, loaded := madeAt(t, time.Now(), file, func() { testfiles.Write(t, added, "npm") })
			if len(loaded.Modules) != 3 || string(loaded.Modules[2]) != "npm" {
				t.Fatalf("the daemon loads %q; want the plugin that appeared among them", loaded.Modules)
			}
			if !rewritten {
				answersNothing(t, made)
				return
			}
			again(t, added, func(content []byte) { copy(content, "mpn") })
			answers(t, made, added)
		})
	}
}

// A daemon told its settings by a path from where it was started holds
// them, and what they name, wherever it then runs: it is started in the
// directory it builds. With nothing written, three waits running answer
// nothing; the settings written again are answered by where they are.
func TestADaemonToldItsSettingsFromWhereItWasStartedHoldsThemWhereverItRuns(t *testing.T) {
	file := told(t)
	t.Chdir(filepath.Dir(file))
	o, err := absolute(Options{Directory: ".", State: "state", Settings: filepath.Base(file)})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(file), "state"); o.State != want || o.Settings != file {
		t.Fatalf("the state %s and the settings %s; want %s and %s", o.State, o.Settings, want, file)
	}
	made, _ := madeAt(t, time.Now(), o.Settings, nil)
	t.Chdir(t.TempDir())
	answersNothing(t, made)
	again(t, file, func(content []byte) { content[len(content)-1] = ' ' })
	answers(t, made, file)
}

// Each part of a daemon written again with what it held, in place or by
// a file of the same bytes moved over it, as a touch, a copy over it or
// an install again does, makes the same daemon (Read), and no wait of
// three running answers it, where each answered it before and the
// command built again on the same daemon for ever. Control: written
// again in place with other content of its size, it is answered.
func TestAPartOfADaemonWrittenAgainWithWhatItHeldMakesTheSameDaemonAndIsNotAnswered(t *testing.T) {
	for how, write := range testfiles.Again {
		for _, part := range []string{"build.yaml", "plugins/go.wasm", "ca.pem", "program"} {
			t.Run(part+", "+how, func(t *testing.T) {
				file := told(t)
				written := filepath.Join(filepath.Dir(file), filepath.FromSlash(part))
				time.Sleep(50 * time.Millisecond)
				made, loaded := madeAt(t, time.Now(), file, nil)
				time.Sleep(5 * time.Millisecond)
				write(t, written)
				if now, err := readOf(file, programOf(file), nil, nil); err != nil || now.Made != loaded.Made {
					t.Fatalf("%s written again with what it held makes another daemon (%v)", part, err)
				}
				answersNothing(t, made)
				again(t, written, func(content []byte) { content[len(content)-1] = ' ' })
				answers(t, made, written)
			})
		}
	}
}

// Each part of a daemon is a file of its own: a file that is two parts
// (the grid's authorities named as the build's settings, the settings
// one of the plugins, this program's own file one of them) is refused,
// by which parts it is, before what the second is read: read twice, it
// would be held to two readings, and a file written between them, which
// then holds what one of them used and not the other, would be answered
// by every wait for as long as the daemon lived. Control: each part a
// file of its own, the daemon is made.
func TestAFileThatIsTwoPartsOfADaemonIsRefusedByWhichPartsItIs(t *testing.T) {
	for name, tc := range map[string]struct {
		// lay changes what told laid out and says the settings and the
		// program's own file.
		lay  func(t *testing.T, dir string) (settings, program string)
		want string
	}{
		"the control, each part a file of its own": {func(t *testing.T, dir string) (string, string) {
			return filepath.Join(dir, "build.yaml"), filepath.Join(dir, "program")
		}, ""},
		"the grid's authorities named as the build's settings": {func(t *testing.T, dir string) (string, string) {
			testfiles.Write(t, filepath.Join(dir, "build.yaml"), "version: 1\nplugins: plugins\ngrid:\n  address: grid.example:8980\n  authorities: build.yaml\n")
			return filepath.Join(dir, "build.yaml"), filepath.Join(dir, "program")
		}, "the build's settings and the grid's authorities are one file"},
		"the build's settings among the plugins": {func(t *testing.T, dir string) (string, string) {
			settings := filepath.Join(dir, "plugins", "build.wasm")
			testfiles.Write(t, settings, "version: 1\nplugins: .\ngrid:\n  address: grid.example:8980\n")
			return settings, filepath.Join(dir, "program")
		}, "the build's settings and a plugin are one file"},
		"the grid's authorities named as a plugin": {func(t *testing.T, dir string) (string, string) {
			testfiles.Write(t, filepath.Join(dir, "plugins", "go.wasm"), readFile(t, filepath.Join(dir, "ca.pem")))
			testfiles.Write(t, filepath.Join(dir, "build.yaml"), "version: 1\nplugins: plugins\ngrid:\n  address: grid.example:8980\n  authorities: plugins/go.wasm\n")
			return filepath.Join(dir, "build.yaml"), filepath.Join(dir, "program")
		}, "the grid's authorities and a plugin are one file"},
		"this program's own file the build's settings": {func(t *testing.T, dir string) (string, string) {
			return filepath.Join(dir, "build.yaml"), filepath.Join(dir, "build.yaml")
		}, "the build's settings and this program's own file are one file"},
		"this program's own file a plugin": {func(t *testing.T, dir string) (string, string) {
			return filepath.Join(dir, "build.yaml"), filepath.Join(dir, "plugins", "go.wasm")
		}, "a plugin and this program's own file are one file"},
		"this program's own file the grid's authorities": {func(t *testing.T, dir string) (string, string) {
			return filepath.Join(dir, "build.yaml"), filepath.Join(dir, "ca.pem")
		}, "the grid's authorities and this program's own file are one file"},
	} {
		t.Run(name, func(t *testing.T) {
			settings, program := tc.lay(t, filepath.Dir(told(t)))
			_, _, err := madeAndRead(t.Context(), time.Now(), settings, program, nil)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("each part a file of its own: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("the daemon was made (%v); want it refused: %s", err, tc.want)
			}
		})
	}
}

// readFile is what a file of the test's holds.
func readFile(t *testing.T, file string) string {
	t.Helper()
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
