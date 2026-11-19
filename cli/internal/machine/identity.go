package machine

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// identityFile is where this machine's key for its sandboxes is kept:
// id_ed25519 beneath the root, beside the context.
func identityFile() (string, error) {
	return beneath("id_ed25519")
}

// Identity returns the key this machine logs in to its sandboxes with,
// making one the first time, and the file it is kept in. A sandbox is
// told the key's public half when it is made.
func Identity() (ssh.Signer, string, error) {
	path, err := identityFile()
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, "", err
		}
		block, err := ssh.MarshalPrivateKey(private, "frontseat")
		if err != nil {
			return nil, "", err
		}
		raw = pem.EncodeToMemory(block)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, "", err
		}
	} else if err != nil {
		return nil, "", err
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	return signer, path, nil
}

// AuthorizedKey is what a workspace made from this machine lets into
// its shell: this machine's key, as a line of authorized_keys.
func AuthorizedKey() (string, error) {
	identity, _, err := Identity()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(identity.PublicKey()))), nil
}
