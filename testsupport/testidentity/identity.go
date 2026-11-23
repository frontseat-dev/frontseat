// Package testidentity gives a test a key to log in to the sandboxes of the
// workspaces it makes, and logs in with it (Shell): the key a person's
// machine keeps (cli/internal/machine), made for the test and kept
// nowhere.
package testidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// private is the test process's key itself, the same each time.
var private = sync.OnceValues(func() (ed25519.PrivateKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	return key, err
})

var made = sync.OnceValues(func() (ssh.Signer, error) {
	key, err := private()
	if err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(key)
})

// Kept keeps the test process's key where a command line given a
// configuration directory (XDG_CONFIG_HOME) keeps this machine's key
// for its sandboxes, frontseat/id_ed25519: a command line run with it
// logs in to the workspaces the test made with Authorized, as Shell
// does. The directory is the test's own.
func Kept(config string) error {
	key, err := private()
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(key, "frontseat")
	if err != nil {
		return err
	}
	dir := filepath.Join(config, "frontseat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "id_ed25519"), pem.EncodeToMemory(block), 0o600)
}

// Key is the test process's key, the same each time it is asked.
func Key() ssh.Signer {
	signer, err := made()
	if err != nil {
		panic(err)
	}
	return signer
}

// Authorized is what a workspace is made with so that Key opens a shell
// in it (frontseat.WorkspaceSpec's AuthorizedKey).
func Authorized() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(Key().PublicKey())))
}
