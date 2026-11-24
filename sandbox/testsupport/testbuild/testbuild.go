// Package testbuild reaches, for a test, the build daemon of a
// workspace's sandbox from outside the sandbox, as the workspace's owner
// can: a workspace is built from inside it, and no control plane serves
// or forwards the build API. The daemon is the one the command line
// starts in the box for itself; a test has it started by running a
// command there, reads where it serves and its token from what it says
// of itself in the box, and asks it over a port carried into the sandbox
// (the SDK's Workspace.Forward). What a test asserts of a build's
// actions, its graph or its runs it asks of that daemon. The daemon is
// held to be the workspace's, the one the box has, and to answer to its
// token, before a test is given it; what carries a test to it ends with
// the test.
package testbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
	"github.com/frontseat-dev/frontseat/testsupport/testidentity"
)

// Box is how a test reaches into a workspace's sandbox.
type Box struct {
	// Run runs a line by a shell in the box, standing in the workspace's
	// directory, and returns what it printed and, apart, what it
	// complained of: what is read of the box is read of what a line
	// printed alone.
	Run func(ctx context.Context, line string) (printed, complained string, err error)
	// Dial connects to a port of the box, on its loopback. The
	// connection lasts as long as the context.
	Dial func(ctx context.Context, port int) (net.Conn, error)
}

// Workspace is the box of a workspace a control plane keeps, as its
// owner reaches into it: a line is run over its shell, with the test's
// key (testidentity), and a port is carried by the control plane
// (Forward). The workspace was made with that key.
func Workspace(w *frontseat.Workspace) Box {
	return Box{
		Run: func(ctx context.Context, line string) (string, string, error) {
			shell, err := testidentity.Shell(ctx, w)
			if err != nil {
				return "", "", err
			}
			defer shell.Close()
			session, err := shell.NewSession()
			if err != nil {
				return "", "", err
			}
			defer session.Close()
			var complained bytes.Buffer
			session.Stderr = &complained
			printed, err := session.Output(line)
			return string(printed), complained.String(), err
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

// workspace is the directory of a box's workspace (stack/box.Dockerfile):
// the one directory built there, and so the one its build daemon says
// it builds.
const workspace = "/work"

// Told is what a build daemon says of itself, in its state directory
// (daemon.json), that a test reads: where it serves, on the box's
// loopback, the token it is asked with, and the directory it builds.
// The shape is life.Info's (build/daemon/life), which this package does
// not import: the sandbox is beneath the build daemon. A spec that may
// import both holds this to what a daemon says
// (TestWhatATestReadsOfABuildDaemonIsWhatOneSaysOfItself).
type Told struct {
	Address   string `json:"address"`
	Token     string `json:"token"`
	Directory string `json:"directory"`
}

// Read reads what one build daemon says of itself, as it is written:
// one JSON value and nothing after it, that says where it serves, as a
// host and a port, and a token.
func Read(written string) (Told, int, error) {
	var told Told
	read := json.NewDecoder(strings.NewReader(written))
	if err := read.Decode(&told); err != nil {
		return Told{}, 0, fmt.Errorf("what the build daemon says of itself is not read: %w", err)
	}
	if _, err := read.Token(); !errors.Is(err, io.EOF) {
		return Told{}, 0, errors.New("what the build daemon says of itself is followed by more, and one daemon says one thing")
	}
	if told.Token == "" {
		return Told{}, 0, errors.New("the build daemon says no token it is asked with")
	}
	_, numbered, err := net.SplitHostPort(told.Address)
	if err != nil {
		return Told{}, 0, fmt.Errorf("where the build daemon says it serves: %w", err)
	}
	port, err := strconv.Atoi(numbered)
	if err != nil || port < 1 || port > 65535 {
		return Told{}, 0, fmt.Errorf("where the build daemon says it serves, %q, is at no port", told.Address)
	}
	return told, port, nil
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

	mu sync.Mutex
	// carried are the connections to it that are not closed yet, and
	// closed whether it was let go of, after which none is carried.
	carried map[net.Conn]struct{}
	closed  bool
}

// Daemon returns the build daemon of the box's workspace, started where
// none serves, for a test: a box whose workspace holds nothing yet, or
// that was told no grid, has none, and the test fails there, saying
// what the box said. What carries the test to the daemon ends when the
// test does (Close).
func (b Box) Daemon(ctx context.Context, t testing.TB) *Daemon {
	t.Helper()
	daemon, err := b.reach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(daemon.Close)
	return daemon
}

// reach is the build daemon of the box's workspace, started where none
// serves: a command that builds is run in the box, which starts one and
// waits for it, and what the daemon then says of itself is read there
// (ports.Builds). It is the one daemon the box has, of the workspace's
// directory, and it answers to its token: a box that holds the state of
// several, or of one that builds another directory, or whose daemon
// does not answer, is said here, with what was found, and not by what a
// test asks of it later.
func (b Box) reach(ctx context.Context) (*Daemon, error) {
	if printed, complained, err := b.Run(ctx, starts); err != nil {
		return nil, fmt.Errorf("the build daemon of the box was not started by %q: %w: %s", starts, err, strings.TrimSpace(printed+complained))
	}
	// One workspace, one directory, one daemon: each keeps its state in
	// a directory of its own.
	listed, complained, err := b.Run(ctx, "ls -d "+ports.Builds+"/*/")
	if err != nil {
		return nil, fmt.Errorf("no build daemon keeps its state beneath %s: %w: %s", ports.Builds, err, strings.TrimSpace(complained))
	}
	states := strings.Fields(listed)
	if len(states) != 1 {
		return nil, fmt.Errorf("beneath %s there are the state directories of %d build daemons, and a box has one: %s", ports.Builds, len(states), strings.Join(states, ", "))
	}
	state := path.Clean(states[0])
	written, complained, err := b.Run(ctx, "cat "+state+"/daemon.json")
	if err != nil {
		return nil, fmt.Errorf("what the build daemon of the box says of itself, in %s: %w: %s", state, err, strings.TrimSpace(complained))
	}
	told, port, err := Read(written)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", state, err)
	}
	if told.Directory != workspace {
		return nil, fmt.Errorf("the build daemon whose state is in %s builds %s, and the workspace of a box is %s", state, told.Directory, workspace)
	}
	origin := &url.URL{Scheme: "http", Host: told.Address}
	daemon := &Daemon{Address: origin.String(), carried: map[net.Conn]struct{}{}}
	// HTTP/1.1, a connection to a request: each is a tunnel of its own,
	// which lasts until its answer is read to the end, a stream's too,
	// whatever becomes of the context it was asked for in, and is kept
	// for no other request. It ends when the daemon is let go of, which
	// closes it.
	daemon.HTTP = bearer.NewClient(&http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := b.Dial(context.WithoutCancel(ctx), port)
			if err != nil {
				return nil, err
			}
			return daemon.carry(conn)
		},
	}}, told.Token, origin)
	// It is asked once, with its token, before a test is given it.
	here, err := daemon.Here()
	if err == nil {
		for _, err = range here.Builds(ctx) {
			break
		}
	}
	if err != nil {
		daemon.Close()
		return nil, fmt.Errorf("the build daemon whose state is in %s says it serves at %s, and did not answer there to its token: %w", state, told.Address, err)
	}
	return daemon, nil
}

// carry keeps a connection to the daemon until it is closed, or the
// daemon is let go of, which closes it.
func (d *Daemon) carry(conn net.Conn) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		_ = conn.Close()
		return nil, errors.New("the build daemon of the box was let go of")
	}
	kept := &carried{Conn: conn, of: d}
	d.carried[kept] = struct{}{}
	return kept, nil
}

// carried is a connection to the daemon, which the daemon forgets when
// it is closed.
type carried struct {
	net.Conn
	of *Daemon
}

func (c *carried) Close() error {
	c.of.mu.Lock()
	delete(c.of.carried, c)
	c.of.mu.Unlock()
	return c.Conn.Close()
}

// Close lets go of the daemon: every connection carried to it ends, one
// a stream is still read on among them, and no other is made. The
// daemon itself is the box's, and goes on.
func (d *Daemon) Close() {
	d.mu.Lock()
	d.closed = true
	open := make([]net.Conn, 0, len(d.carried))
	for conn := range d.carried {
		open = append(open, conn)
	}
	d.mu.Unlock()
	for _, conn := range open {
		_ = conn.Close()
	}
}

// Here is what the daemon builds, as the SDK reaches it: the workspace
// of a Client made for the daemon.
func (d *Daemon) Here() (*frontseat.Workspace, error) {
	fs, err := frontseat.New(d.Address, frontseat.WithHTTPClient(d.HTTP))
	if err != nil {
		return nil, err
	}
	return fs.Here(), nil
}
