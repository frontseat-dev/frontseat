package life

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// told lays out a daemon's settings in a directory of the test's, with
// two plugins and the authorities its grid is known by, and returns the
// settings' file.
func told(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	known, err := os.ReadFile(testauthority.New(t).File)
	if err != nil {
		t.Fatal(err)
	}
	testfiles.Tree(t, dir, map[string]string{"plugins/go.wasm": "go", "plugins/cargo.wasm": "cargo", "ca.pem": string(known),
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
