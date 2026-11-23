package sandbox_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// answering is a program for a sandbox: it listens at each address it
// is given, says "listening" once it listens at them all, and answers
// one line said to a connection with "the box heard" and that line. It
// ends when what started it lets go of it, and in five minutes anyway.
const answering = `import os, socket, sys, threading
def serve(listener):
    while True:
        conn, _ = listener.accept()
        said = conn.makefile("rw")
        said.write("the box heard " + said.readline())
        said.flush()
        conn.close()
for at in sys.argv[1:]:
    host, port = at.rsplit(":", 1)
    listener = socket.socket()
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.bind((host, int(port)))
    listener.listen()
    threading.Thread(target=serve, args=(listener,), daemon=True).start()
print("listening", flush=True)
threading.Timer(300, os._exit, (0,)).start()
sys.stdin.read()
os._exit(0)`

// heardAt says a line to a port of the workspace's sandbox, reached as
// the SDK reaches it, through the control plane, and returns what is
// said back; or why nothing was.
func (w *Workspace) heardAt(port int, line string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := w.sdk().Forward(ctx, port)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, line); err != nil {
		return "", err
	}
	return bufio.NewReader(conn).ReadString('\n')
}

// Workspaces in sandboxes / A port of a workspace's sandbox is reached as bytes by its owner.
//
// What a program of the workspace serves on a port of its sandbox, on
// the loopback there and on a port the sandbox was not made to open, is
// reached by the workspace's owner through the control plane, which
// carries the bytes and knows nothing of what speaks. The port of the
// daemon's source API is the control plane's alone, and is refused by
// name. The control is a port the sandbox was made to open, the
// daemon's build API's, reached the same way.
func TestAPortOfAWorkspacesSandboxIsReachedAsBytesByItsOwner(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()
	// Two ports the sandbox was not made to open: one listened on at the
	// loopback alone, one at every address of the sandbox.
	const loopback, everywhere = 18080, 18081
	session, done := workspace.session()
	defer done()
	told, err := session.StdinPipe()
	spec.Expect(err).NotTo(HaveOccurred())
	defer told.Close()
	says, err := session.StdoutPipe()
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(session.Start(fmt.Sprintf("python3 -c '%s' 127.0.0.1:%d 0.0.0.0:%d", answering, loopback, everywhere))).To(Succeed())
	listening, err := bufio.NewReader(says).ReadString('\n')
	spec.Expect(err).NotTo(HaveOccurred(), "the program in the sandbox did not come to listen")
	spec.Expect(listening).To(Equal("listening\n"))
	// The control of what follows: the program answers inside the
	// sandbox, at the loopback, on both ports.
	for _, port := range []int{loopback, everywhere} {
		asking := fmt.Sprintf(`python3 -c 'import socket; c = socket.create_connection(("127.0.0.1", %d), 5); f = c.makefile("rw"); f.write("inside\n"); f.flush(); print(f.readline())'`, port)
		spec.Expect(workspace.prints(asking)).To(Equal("the box heard inside"), "inside the sandbox, the port %d", port)
	}

	// A port the sandbox was made to open, reached through the control
	// plane as the others are below: the daemon's build API answers.
	declared := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return workspace.sdk().Forward(ctx, ports.Daemon)
	}}}
	answered, declaredErr := declared.Get("http://sandbox/")
	if declaredErr == nil {
		answered.Body.Close()
	}

	back, loopbackErr := workspace.heardAt(loopback, "outside")
	if loopbackErr != nil || back != "the box heard outside\n" {
		elsewhere, everywhereErr := workspace.heardAt(everywhere, "outside")
		switch {
		case declaredErr != nil:
			t.Fatalf("no port of the sandbox is reached through the control plane, one the sandbox was made to open neither: the daemon's (%d): %v; an undeclared port on the loopback: %q, %v", ports.Daemon, declaredErr, back, loopbackErr)
		case everywhereErr == nil && elsewhere == "the box heard outside\n":
			t.Fatalf("the gateway does not reach a port bound to the loopback: the port %d, listened on at 127.0.0.1 alone, said %q (%v), while the port %d, listened on at every address, was reached, and neither was declared", loopback, back, loopbackErr, everywhere)
		default:
			t.Fatalf("the gateway does not reach a port the sandbox did not declare: the port %d, on the loopback, said %q (%v), and the port %d, at every address, said %q (%v), while the daemon's port (%d), which the sandbox was made to open, was reached", loopback, back, loopbackErr, everywhere, elsewhere, everywhereErr, ports.Daemon)
		}
	}
	spec.Expect(declaredErr).NotTo(HaveOccurred(), "the control: the daemon's port, which the sandbox was made to open")
	// One connection after another, each its own.
	spec.Expect(workspace.heardAt(loopback, "again")).To(Equal("the box heard again\n"))

	// The source API's port is the control plane's alone: refused by
	// name, the same way.
	_, err = workspace.heardAt(ports.Source, "outside")
	reason, domain := frontseat.Reason(err)
	spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented), "the source API's port: %v", err)
	spec.Expect(reason).To(Equal(frontseat.ReasonPortNotServed))
	spec.Expect(domain).To(Equal(frontseat.DomainWorkspace))
	// A port nothing listens on is no refusal by name, and carries
	// nothing.
	back, err = workspace.heardAt(18082, "outside")
	spec.Expect(err).To(HaveOccurred(), "a port nothing listens on said %q", back)
	if reason, _ := frontseat.Reason(err); reason != "" {
		t.Errorf("a port nothing listens on was refused by name, %s: %v", reason, err)
	}
}
