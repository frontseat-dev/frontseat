package life

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// seen is a look at what a build of a directory is made from, once
// everything in it has settled: one a later look is held against.
func seen(t *testing.T, directory, settings string) *Seen {
	t.Helper()
	for {
		now, err := Look(t.Context(), directory, settings)
		if err != nil {
			t.Fatal(err)
		}
		if now.Settled() {
			return now
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Every file a daemon is made of is looked at, by the path Read reads
// it at: each written again in place, at its size and with the time it
// had, makes another daemon (Read) and looks otherwise (Look), so that
// a command that builds again when something changed builds again with
// the daemon there is then. A plugin added to those the settings load
// is seen too, and so is a file of the directory built. Control: with
// nothing written, nothing looks otherwise.
func TestWhatADaemonIsMadeOfLooksOtherwiseWhenItIsWrittenAgain(t *testing.T) {
	file := told(t)
	directory := t.TempDir()
	testfiles.Tree(t, directory, map[string]string{"frontseat.yaml": "plugins: {}\n", "app/main.go": "package main\n"})
	parts, err := Parts(file)
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 5 || parts[0] != file || parts[len(parts)-1] != program {
		t.Fatalf("a daemon is made of %v; want the settings, two plugins, the authorities and this program, %s", parts, program)
	}
	before := seen(t, directory, file)
	if again := seen(t, directory, file).Since(before); len(again) != 0 {
		t.Fatalf("the control: with nothing written, %v look otherwise", again)
	}
	for _, part := range parts[:len(parts)-1] {
		made, err := Read(file)
		if err != nil {
			t.Fatal(err)
		}
		// Its last byte, which leaves the settings and the authorities
		// what they say.
		var last byte
		again(t, part, func(content []byte) { last, content[len(content)-1] = content[len(content)-1], ' ' })
		if now, err := Read(file); err == nil && now.Made == made.Made {
			t.Errorf("%s written again makes the same daemon: it is no part of one", part)
		}
		now := seen(t, directory, file)
		if changed := now.Since(before); !slices.Equal(changed, []string{part}) {
			t.Errorf("%s written again: %v look otherwise; want it alone", part, changed)
		}
		again(t, part, func(content []byte) { content[len(content)-1] = last })
		before = seen(t, directory, file)
	}

	added := filepath.Join(filepath.Dir(file), "plugins", "npm.wasm")
	testfiles.Tree(t, directory, map[string]string{"app/main.go": "package main // more\n"})
	testfiles.Tree(t, filepath.Dir(added), map[string]string{"npm.wasm": "npm"})
	if changed := seen(t, directory, file).Since(before); !slices.Equal(changed, []string{added, "app/main.go"}) {
		t.Errorf("a plugin added and a file of the directory written: %v look otherwise; want %s and app/main.go", changed, added)
	}
}

// What cannot be read is looked at as the file it is, and is no failure
// of the look: settings a person is writing, and a manifest that is not
// one a build reads, whose directory then lists nothing. Put right,
// each looks otherwise, and with the manifest the files it now says are
// there. The build asked for meanwhile says what is wrong.
func TestWhatCannotBeReadIsLookedAtAsTheFileItIs(t *testing.T) {
	file := told(t)
	directory := t.TempDir()
	written := filepath.Join(directory, filepath.FromSlash(manifest.File))
	testfiles.Tree(t, directory, map[string]string{manifest.File: "no manifest", "repo/main.go": "package main\n"})
	right, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	testfiles.Tree(t, filepath.Dir(file), map[string]string{filepath.Base(file): "version: [\n"})
	if _, err := Parts(file); err == nil {
		t.Fatal("settings that are not settings were read")
	}
	before := seen(t, directory, file)

	testfiles.Tree(t, filepath.Dir(file), map[string]string{filepath.Base(file): string(right)})
	changed := seen(t, directory, file).Since(before)
	if !slices.Contains(changed, file) || slices.Contains(changed, written) || slices.Contains(changed, "repo/main.go") {
		t.Errorf("the settings put right: %v look otherwise; want the settings and what they load, and nothing of the directory", changed)
	}
	before = seen(t, directory, file)
	if err := os.Remove(written); err != nil {
		t.Fatal(err)
	}
	if changed := seen(t, directory, file).Since(before); !slices.Equal(changed, []string{written, "repo/main.go"}) {
		t.Errorf("the manifest gone, and the directory a checkout: %v look otherwise; want %s and repo/main.go", changed, written)
	}
}

// lookAt is a directory to look at in BenchmarkAnIdleLook, in place of
// one of the benchmark's own: a checkout, to measure what one look of an
// idle watch costs there.
var lookAt = flag.String("look", "", "a directory BenchmarkAnIdleLook looks at, in place of one of its own")

// What one look of a watch with nothing changed costs: the look of the
// directory as a build lists it, of its manifest and of what the daemon
// is made of, and the look held against the one before. It has no
// budget: a watch waits ten times as long as a look took before the
// next (watchEvery, in the command line). Of a checkout:
//
//	go test -run '^$' -bench BenchmarkAnIdleLook -benchmem ./build/daemon/life/ -args -look "$PWD"
func BenchmarkAnIdleLook(b *testing.B) {
	file, directory := told(b), *lookAt
	if directory == "" {
		directory = b.TempDir()
		files := map[string]string{"frontseat.yaml": "plugins: {}\n"}
		for i := range 2000 {
			files[fmt.Sprintf("p%02d/f%03d.go", i/100, i%100)] = "package p\n"
		}
		testfiles.Tree(b, directory, files)
	}
	before, err := Look(b.Context(), directory, file)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		now, err := Look(b.Context(), directory, file)
		if err != nil {
			b.Fatal(err)
		}
		if changed := now.Since(before); len(changed) != 0 {
			b.Fatalf("%v changed while the look was measured", changed)
		}
	}
}
