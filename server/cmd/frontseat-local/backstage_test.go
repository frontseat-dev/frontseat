package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tokenFile writes a token file as told, in a directory of the test's
// own, and returns it.
func tokenFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "backstage")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "token")
	if err := os.WriteFile(file, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, mode); err != nil {
		t.Fatal(err)
	}
	return file
}

// A Backstage token in a file another may read, a link, a file of two
// lines, or none at all where one is named, stops the server from
// starting, and the refusal never says the token. The control is a
// file of one line, the server's alone.
func TestATokenFileThatIsNotTheServersAloneStopsItStarting(t *testing.T) {
	const token = "a-backstage-token"
	open := tokenFile(t, token+"\n", 0o644)
	lines := tokenFile(t, token+"\nanother\n", 0o600)
	spaced := tokenFile(t, "a backstage token\n", 0o600)
	long := tokenFile(t, strings.Repeat("a", 4097)+"\n", 0o600)
	empty := tokenFile(t, "\n", 0o600)
	good := tokenFile(t, token+"\n", 0o600)
	link := filepath.Join(filepath.Dir(good), "linked")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	for what, file := range map[string]string{
		"a file others may read": open, "a link": link, "two lines": lines, "a token with a space": spaced,
		"a token of more than 4 KiB": long, "no token": empty, "a file that is not there": filepath.Join(t.TempDir(), "none"),
	} {
		if _, err := catalogOf("https://backstage.example.com", file, false); err == nil {
			t.Errorf("%s was taken", what)
		} else if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "another") {
			t.Errorf("the refusal of %s says the token: %v", what, err)
		}
	}
	if catalog, err := catalogOf("https://backstage.example.com", good, false); err != nil || catalog == nil {
		t.Errorf("the control, a file of one line, the server's alone: %v", err)
	}
}

// Without --backstage the server resolves no entities, and a token or
// loopback given without it is refused; an address it is not asked at
// is refused before anything starts.
func TestBackstageIsGivenWholeOrNotAtAll(t *testing.T) {
	if catalog, err := catalogOf("", "", false); err != nil || catalog != nil {
		t.Errorf("no Backstage: %v, %v, want none and no error", catalog, err)
	}
	good := tokenFile(t, "a-backstage-token\n", 0o600)
	for what, refused := range map[string]func() error{
		"a token without an address":    func() error { _, err := catalogOf("", good, false); return err },
		"loopback without an address":   func() error { _, err := catalogOf("", "", true); return err },
		"http to another machine":       func() error { _, err := catalogOf("http://backstage.example.com", good, true); return err },
		"an address with a password in": func() error { _, err := catalogOf("https://a:b@backstage.example.com", good, false); return err },
	} {
		if err := refused(); err == nil {
			t.Errorf("%s was taken", what)
		}
	}
	if catalog, err := catalogOf("http://127.0.0.1:7007", "", true); err != nil || catalog == nil {
		t.Errorf("the control, this machine with loopback and no token: %v", err)
	}
}
