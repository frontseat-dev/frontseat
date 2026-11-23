// Package testbuild reaches, for a test, the build daemon of a
// workspace's sandbox from outside the sandbox, as the workspace's owner
// can: a workspace is built from inside it, and no control plane serves
// or forwards the build API. The daemon is the one the command line
// starts in the box for itself; a test has it started by running a
// command there, reads where it serves and its token from what it says
// of itself in the box, and asks it over a port carried into the sandbox
// (the SDK's Workspace.Forward). What a test asserts of a build's
// actions, its graph or its runs it asks of that daemon.
package testbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
	"github.com/frontseat-dev/frontseat/testsupport/testidentity"
)

// Box is how a test reaches into a workspace's sandbox.
type Box struct {
	// Run runs a line by a shell in the box, standing in the workspace's
	// directory, and returns what it printed, to either stream.
	Run func(ctx context.Context, line string) (string, error)
	// Dial connects to a port of the box, on its loopback.
	Dial func(ctx context.Context, port int) (net.Conn, error)
}

// Workspace is the box of a workspace a control plane keeps, as its
// owner reaches into it: a line is run over its shell, with the test's
// key (testidentity), and a port is carried by the control plane
// (Forward). The workspace was made with that key.
func Workspace(w *frontseat.Workspace) Box {
	return Box{
		Run: func(ctx context.Context, line string) (string, error) {
			shell, err := testidentity.Shell(ctx, w)
			if err != nil {
				return "", err
			}
			defer shell.Close()
			session, err := shell.NewSession()
			if err != nil {
				return "", err
			}
			defer session.Close()
			printed, err := session.CombinedOutput(line)
			return string(printed), err
		},
		Dial: w.Forward,
	}
}

// Owned is the box of the workspace of a name that the control plane at
// an address keeps, as its owner, known by a token, reaches into it
// (Workspace): for a test that holds no client of the SDK's.
func Owned(server, token, name string) (Box, error) {
	fs, err := frontseat.New(server, frontseat.WithToken(token))
	if err != nil {
		return Box{}, err
	}
	return Workspace(fs.Workspace(name)), nil
}

// starts is a command that asks the build daemon of the workspace's
// directory and changes nothing: the command line starts the daemon
// where none serves, and waits until it does.
const starts = "/opt/frontseat/bin/frontseat build list"

// said is what a build daemon says of itself that a test reads: where
// it serves, on the box's loopback, and the token it is asked with.
type said struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

// Daemon is the build daemon of a box's workspace, as a test outside the
// box asks it.
type Daemon struct {
	// Address is where it serves, as a URL: on the box's loopback, which
	// HTTP dials from outside.
	Address string
	// HTTP asks it: each request is carried to its port in the box, on a
	// connection of its own, and presents its token.
	HTTP *http.Client
}

// Daemon returns the build daemon of the box's workspace, started where
// none serves: a command that builds is run in the box, which starts
// one and waits for it, and what the daemon then says of itself is read
// there (ports.Builds). A box whose workspace holds nothing yet, or
// that was told no grid, has none, and the error says what the command
// printed.
func (b Box) Daemon(ctx context.Context) (Daemon, error) {
	if printed, err := b.Run(ctx, starts); err != nil {
		return Daemon{}, fmt.Errorf("the build daemon of the box was not started by %q: %w: %s", starts, err, strings.TrimSpace(printed))
	}
	printed, err := b.Run(ctx, "cat "+ports.Builds+"/*/daemon.json")
	if err != nil {
		return Daemon{}, fmt.Errorf("what the build daemon of the box says of itself, beneath %s: %w: %s", ports.Builds, err, strings.TrimSpace(printed))
	}
	// One workspace, one directory, one daemon: more than one that says
	// where it serves is not what a box holds.
	var is said
	read := json.NewDecoder(strings.NewReader(printed))
	if err := read.Decode(&is); err != nil || read.More() || is.Token == "" {
		return Daemon{}, fmt.Errorf("beneath %s there is not one build daemon that says where it serves (%v)", ports.Builds, err)
	}
	_, numbered, err := net.SplitHostPort(is.Address)
	if err != nil {
		return Daemon{}, fmt.Errorf("where the build daemon of the box says it serves: %w", err)
	}
	port, err := strconv.Atoi(numbered)
	if err != nil {
		return Daemon{}, fmt.Errorf("where the build daemon of the box says it serves: %w", err)
	}
	origin := &url.URL{Scheme: "http", Host: is.Address}
	// HTTP/1.1, a connection to a request: each is a tunnel of its own,
	// which lasts until its answer is read to the end, a stream's too,
	// whatever becomes of the context it was asked for in, and is kept
	// for no other request.
	carried := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return b.Dial(context.WithoutCancel(ctx), port)
		},
	}}
	return Daemon{Address: origin.String(), HTTP: bearer.NewClient(carried, is.Token, origin)}, nil
}

// Here is what the daemon builds, as the SDK reaches it: the workspace
// of a Client made for the daemon.
func (d Daemon) Here() (*frontseat.Workspace, error) {
	fs, err := frontseat.New(d.Address, frontseat.WithHTTPClient(d.HTTP))
	if err != nil {
		return nil, err
	}
	return fs.Here(), nil
}
