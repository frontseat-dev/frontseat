package shell

import (
	"github.com/frontseat-dev/ssh"
	gossh "golang.org/x/crypto/ssh"
)

type forwardingKey struct{}

// forward gives each SSH connection its own library-owned forwarding state.
// A cancellation or disconnect must never close another connection's listener,
// even when that connection has reused a port the first one released.
func forward(ctx ssh.Context, server *ssh.Server, request *gossh.Request) (bool, []byte) {
	ctx.Lock()
	handler, ok := ctx.Value(forwardingKey{}).(*ssh.ForwardedTCPHandler)
	if !ok {
		handler = &ssh.ForwardedTCPHandler{}
		ctx.SetValue(forwardingKey{}, handler)
	}
	ctx.Unlock()
	return handler.HandleSSHRequest(ctx, server, request)
}
