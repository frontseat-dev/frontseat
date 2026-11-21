package cmd

import (
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
