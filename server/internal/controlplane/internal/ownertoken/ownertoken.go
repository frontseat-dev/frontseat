// Package ownertoken knows the one person a local control plane answers: its
// owner, by a token of 256 random bits, which it mints the first time it
// is asked and keeps in a file of its own.
package ownertoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/frontseat-dev/frontseat/server/internal/controlplane/identity"
	"github.com/frontseat-dev/frontseat/server/internal/secretfile"
)

// Subject is who the owner is said to be.
const Subject = "local"

// written is a token as its file holds it: 64 lower-case hexadecimal
// digits, and a line's end.
var written = regexp.MustCompile(`^[0-9a-f]{64}\n?$`)

// Token is the owner's token, as a digest: the token itself is not held.
type Token struct {
	digest [sha256.Size]byte
}

var _ identity.Authenticator = (*Token)(nil)

// Open reads the owner's token from a file, and mints one there where
// there is none. The file and its directory are this process's alone:
// one another could read or write, or a link, is refused, and the token
// is never said. Two that mint at once go on with the one that was
// there first: a token is put in place by a link, which fails where one
// is there.
func Open(file string) (*Token, error) {
	directory := filepath.Dir(file)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("the owner's token: %w", err)
	}
	if err := secretfile.Directory(directory, "the owner's token"); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(file); errors.Is(err, fs.ErrNotExist) {
		if err := mint(file); err != nil {
			return nil, err
		}
	}
	raw, err := secretfile.Read(file, "the owner's token", 128)
	if err != nil {
		return nil, err
	}
	if !written.Match(raw) {
		return nil, fmt.Errorf("%s holds no owner's token: one is 64 hexadecimal digits on a line", file)
	}
	return &Token{digest: sha256.Sum256(raw[:64])}, nil
}

// mint puts a new token in a file where there is none: written whole
// beside it, and linked into place, which fails where another put one
// there first, whose token is then the one.
func mint(file string) error {
	var minted [32]byte
	if _, err := rand.Read(minted[:]); err != nil {
		return fmt.Errorf("minting the owner's token: %w", err)
	}
	beside, err := os.CreateTemp(filepath.Dir(file), ".token-*")
	if err != nil {
		return fmt.Errorf("minting the owner's token: %w", err)
	}
	defer os.Remove(beside.Name())
	if _, err := beside.WriteString(hex.EncodeToString(minted[:]) + "\n"); err != nil {
		beside.Close()
		return fmt.Errorf("minting the owner's token: %w", err)
	}
	if err := beside.Close(); err != nil {
		return fmt.Errorf("minting the owner's token: %w", err)
	}
	if err := os.Link(beside.Name(), file); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("keeping the owner's token in %s: %w", file, err)
	}
	return nil
}

// Authenticate implements identity.Authenticator: the owner is the one
// who presents the token. Digests of equal length are compared in time
// that does not depend on where they differ.
func (t *Token) Authenticate(_ context.Context, token string) (identity.Caller, error) {
	if token == "" {
		return identity.Caller{}, identity.ErrMissing
	}
	presented := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(presented[:], t.digest[:]) != 1 {
		return identity.Caller{}, identity.ErrInvalid
	}
	return identity.Caller{Subject: Subject}, nil
}
