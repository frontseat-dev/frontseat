package ownertoken

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/workspace/server/controlplane/identity"
)

// The owner's token is minted where there is none, in a file of its
// own, and read again from it after: whoever presents it is the owner,
// and whoever presents another, or none, is not.
func TestTheOwnersTokenIsMintedOnceAndKnownAfter(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kept", "token")
	first, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("the token's file is %s, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(file)); info.Mode().Perm() != 0o700 {
		t.Errorf("the token's directory is %s, want 0700", info.Mode().Perm())
	}
	token := strings.TrimSpace(string(raw))
	again, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, known := range []*Token{first, again} {
		if caller, err := known.Authenticate(context.Background(), token); err != nil || caller.Subject != Subject {
			t.Errorf("the owner's token: %v, %v", caller, err)
		}
		if _, err := known.Authenticate(context.Background(), ""); !errors.Is(err, identity.ErrMissing) {
			t.Errorf("no token: %v, want it missing", err)
		}
		for _, other := range []string{token[:63], token + "0", strings.ToUpper(token), strings.Repeat("0", 64)} {
			if _, err := known.Authenticate(context.Background(), other); !errors.Is(err, identity.ErrInvalid) {
				t.Errorf("another token: %v, want it invalid", err)
			} else if strings.Contains(err.Error(), other) || strings.Contains(err.Error(), token) {
				t.Errorf("the refusal says a token: %v", err)
			}
		}
	}
}

// A token another could read, or a file that holds no token, is
// refused, and nothing is minted over it; the control is the file as it
// is minted.
func TestATokenAnotherCouldReadIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kept")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(dir, "open")
	if err := os.WriteFile(open, []byte(strings.Repeat("a", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(open); err == nil || strings.Contains(err.Error(), strings.Repeat("a", 64)) {
		t.Errorf("a token others can read: %v, want it refused without saying it", err)
	}
	garbled := filepath.Join(dir, "garbled")
	if err := os.WriteFile(garbled, []byte("not-a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(garbled); err == nil {
		t.Error("a file holding no token was taken")
	}
	if raw, _ := os.ReadFile(garbled); string(raw) != "not-a-token\n" {
		t.Errorf("a token was minted over the file: %q", raw)
	}
	if err := os.Chmod(open, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(open); err != nil {
		t.Errorf("the token made the server's alone: %v", err)
	}
}

// A token in a directory others may write, or a link to a file, is
// refused; two that mint at once go on with one token. The control is
// the token minted once, in a directory of its own.
func TestATokenIsOneOfItsOwnAndOneForAll(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(shared, "token")); err == nil {
		t.Error("a token in a directory others may write was taken")
	}
	own := filepath.Join(root, "own", "token")
	if _, err := Open(own); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "own", "link")
	if err := os.Symlink(own, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil {
		t.Error("a link to a token was taken")
	}
	raw, _ := os.ReadFile(own)
	token := strings.TrimSpace(string(raw))
	at := filepath.Join(root, "at-once", "token")
	tokens := make(chan *Token, 8)
	for range 8 {
		go func() {
			known, err := Open(at)
			if err != nil {
				t.Error(err)
			}
			tokens <- known
		}()
	}
	raced := ""
	for range 8 {
		known := <-tokens
		written, _ := os.ReadFile(at)
		if raced = strings.TrimSpace(string(written)); known != nil {
			if _, err := known.Authenticate(context.Background(), raced); err != nil {
				t.Errorf("one of eight minting at once knows another token than the one kept: %v", err)
			}
		}
	}
	if raced == token {
		t.Error("two directories hold one token")
	}
}
