package cmd

import (
	"os"
	"strings"
	"testing"
)

// frontseat version says what it was released at, as the release that
// built it wrote it in, and asks no server; built by anything else it
// says so, by the version every build that is no release is given.
func TestVersionSaysWhatItWasReleasedAt(t *testing.T) {
	d := &daemonOf{}
	if got, _, err := run(t, d, "version"); err != nil || got != "frontseat 0.0.0-dev\n" {
		t.Errorf("a program no release built said %q, %v", got, err)
	}
	released := func(v, c string) {
		before, was := version, commit
		version, commit = v, c
		t.Cleanup(func() { version, commit = before, was })
	}
	released("1.4.0", "0123abcd")
	if got, _, err := run(t, d, "version"); err != nil || got != "frontseat 1.4.0 (0123abcd)\n" {
		t.Errorf("a released program said %q, %v", got, err)
	}
	if _, _, err := run(t, d, "version", "extra"); err == nil {
		t.Error("version took an argument")
	}
	if len(d.planned)+len(d.created) != 0 {
		t.Errorf("a daemon was asked: %v %v", d.planned, d.created)
	}
}

// frontseat alone prints its help and is no mistake; a word that is no
// command is one, and says what was meant where one is near it.
func TestTheRootSaysItsHelpAndWhatWasMeant(t *testing.T) {
	d := &daemonOf{}
	printed := &strings.Builder{}
	rootCmd.SetOut(printed)
	t.Cleanup(func() { rootCmd.SetOut(os.Stdout) })
	if _, code, err := run(t, d); err != nil || code != 0 || !strings.Contains(printed.String(), "Usage:") {
		t.Errorf("frontseat alone: %d, %v, printing %q", code, err, printed)
	}
	if _, _, err := run(t, d, "biuld"); err == nil || !strings.Contains(err.Error(), "did you mean build?") {
		t.Errorf("a word near a command: %v", err)
	}
	if _, _, err := run(t, d, "zzzzzz"); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("a word near no command: %v", err)
	}
}
