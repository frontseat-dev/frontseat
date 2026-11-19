package testidentity

import (
	"context"

	"golang.org/x/crypto/ssh"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

// Shell logs in to a workspace's sandbox over SSH with Key, through the
// control plane that keeps it, as a person's SSH client does with their
// machine's key.
func Shell(ctx context.Context, w *frontseat.Workspace) (*ssh.Client, error) {
	conn, err := w.Shell(ctx)
	if err != nil {
		return nil, err
	}
	// The sandbox's host key is made when the sandbox is; what vouches
	// for the sandbox is the server the connection goes through.
	config := &ssh.ClientConfig{User: "frontseat", Auth: []ssh.AuthMethod{ssh.PublicKeys(Key())}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	c, channels, requests, err := ssh.NewClientConn(conn, w.Name(), config)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, channels, requests), nil
}
