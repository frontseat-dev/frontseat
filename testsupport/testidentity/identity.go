// Package testidentity gives a test a key to log in to the sandboxes of the
// workspaces it makes, and logs in with it (Shell): the key a person's
// machine keeps (cli/internal/machine), made for the test and kept
// nowhere.
package testidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

var made = sync.OnceValues(func() (ssh.Signer, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(private)
})

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
