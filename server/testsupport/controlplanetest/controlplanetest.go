// Package controlplanetest starts, for a test, the control plane there is
// where there is no platform: what frontseat-local serves, workspaces
// over the sandboxes beneath them made by the local stack's OpenShell
// gateway, with sandboxes that build on the grid of
// the local stack. The specs and the comparisons start one with
// it, and act on it through the API alone, as its owner, with the
// owner's token. The sandbox service beneath, which the control plane
// serves to no one, is served for the test alone, at an address of its
// own: what a test looks into a sandbox with.
package controlplanetest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/internal/backstage"
	"github.com/frontseat-dev/frontseat/server/internal/controlplane"
	"github.com/frontseat-dev/frontseat/server/internal/forge"
	"github.com/frontseat-dev/frontseat/server/internal/source"
	"github.com/frontseat-dev/frontseat/server/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgateway"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
)

// Served is a control plane a test serves.
type Served struct {
	// URL is where it is asked, as its owner asks it.
	URL string
	// Image is the box image its workspaces run.
	Image string
	// Owner presents its owner's token, for a client of the SDK.
	Owner frontseat.Option
	// TokenFile is the file its owner's token is in, which a test lays
	// out for the command line where it finds a control plane's token
	// (testowner.LayOut).
	TokenFile string
	// Sandboxes is where the sandbox service beneath it is asked, for the
	// test alone: its calls, and the tunnels to its sandboxes' ports.
	Sandboxes string
}

// Option is what an operator gives a control plane beside what every
// one of the tests' is given.
type Option func(*options)

type options struct {
	entities controlplane.Entities
}

// WithBackstage has the control plane resolve entities through a fake
// Backstage, as frontseat-local's --backstage, --backstage-token and
// --backstage-loopback do; without it, it resolves none.
func WithBackstage(t testing.TB, fake backstagetest.Served) Option {
	t.Helper()
	token, err := backstage.ReadToken(fake.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := backstage.New(backstage.Config{Base: fake.URL, Token: token, Loopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return func(o *options) { o.entities = catalog }
}

// Start serves a control plane, which it stops when the test ends.
// It skips the test where there is no container runtime or no grid, and
// fails it where no gateway answers.
func Start(t testing.TB, given ...Option) Served {
	t.Helper()
	served, close := StartAt(t, filepath.Join(t.TempDir(), "state"), given...)
	t.Cleanup(close)
	return served
}

// StartAt serves a control plane whose state directory and lifetime belong to
// the caller. Close must run before state is removed; no test handle is kept.
func StartAt(t testing.TB, state string, given ...Option) (Served, func()) {
	t.Helper()
	srv, plane, served, beneath := serving(t, state, nil, given)
	return served, func() {
		srv.Close()
		plane.Close()
		beneath.Close()
		// Its test may have ended: what is left is said where anyone
		// running it sees.
		if err := EndLeft(identityIn(state), state); err != nil {
			fmt.Fprintln(os.Stderr, "controlplanetest:", err)
		}
	}
}

// StartWithEnvironment adds operator settings to a test-owned control plane.
func StartWithEnvironment(t testing.TB, extra map[string]string, given ...Option) Served {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	srv, plane, served, beneath := serving(t, state, extra, given)
	t.Cleanup(func() {
		srv.Close()
		plane.Close()
		beneath.Close()
		if err := EndLeft(identityIn(state), state); err != nil {
			t.Log(err)
		}
	})
	return served
}

// Plane is a control plane a test serves: what answers the workspace API,
// and Close, which ends what the plane runs. A test names it so, and
// links nothing of the control plane's own.
type Plane interface {
	http.Handler
	Close()
}

// Serving serves a control plane, with its state in a directory of the
// caller's and operator settings added, and returns the server, the plane
// and what it is. The caller closes both, the server first; a caller that
// ends its workspaces through the plane does that before closing it. The
// sandbox service beneath is served until the test ends. It skips the
// test where there is no container runtime or no grid, and fails it
// where no gateway answers.
func Serving(t testing.TB, state string, extra map[string]string, given ...Option) (*httptest.Server, Plane, Served) {
	t.Helper()
	srv, plane, served, beneath := serving(t, state, extra, given)
	// After the caller has closed the server and the plane.
	t.Cleanup(func() {
		beneath.Close()
		if err := EndLeft(identityIn(state), state); err != nil {
			t.Log(err)
		}
	})
	return srv, plane, served
}

func serving(t testing.TB, state string, extra map[string]string, given []Option) (*httptest.Server, Plane, Served, *httptest.Server) {
	t.Helper()
	var with options
	for _, option := range given {
		option(&with)
	}
	grid := testgrid.InSandbox(t)
	image := testbox.Image(t)
	identity := testgateway.RequiredAt(t, identityIn(state))
	tokens := filepath.Join(state, "owner", "token")
	callers, err := controlplane.OwnerToken(tokens)
	if err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(tokens)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	plane, err := controlplane.OverGateway(controlplane.Config{
		PublicURL: srv.URL, State: state, Box: image, Callers: callers, Environment: extra,
		Grid:     controlplane.Grid{Hosts: grid.Hosts(), Arguments: grid.Arguments()},
		Fetching: source.Fetching(), Releasing: forge.GitHubAlone(), Entities: with.entities,
	}, controlplane.Gateway{Address: testgateway.Address, Identity: identity, Runtime: "docker"})
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	srv.Config.Handler = plane
	beneath := httptest.NewServer(sandboxes(plane))
	return srv, plane, Served{
		URL: srv.URL, Image: image, Owner: frontseat.WithToken(strings.TrimSpace(string(token))), TokenFile: tokens, Sandboxes: beneath.URL,
	}, beneath
}

// sandboxes serves the sandbox service a plane makes sandboxes with: its
// calls, and the tunnels to its sandboxes' ports, as the sandbox API
// lays them out.
func sandboxes(plane *controlplane.Plane) http.Handler {
	mux := http.NewServeMux()
	api := connect.NewServer()
	sandboxv1alpha1connect.RegisterSandboxServiceHandler(api, plane.Sandboxes)
	connecthttp.Mount(mux, api)
	mux.HandleFunc("/sandboxes/{id}/tunnels/{port}", func(w http.ResponseWriter, r *http.Request) {
		port, err := strconv.Atoi(r.PathValue("port"))
		if err != nil || port <= 0 || port > 65535 {
			http.Error(w, "the port is not a number", http.StatusBadRequest)
			return
		}
		plane.Sandboxes.Tunnel(w, r, r.PathValue("id"), port)
	})
	return mux
}

// identityIn is where, in a control plane's state, what the gateway is
// known by and knows it by is kept.
func identityIn(state string) string { return filepath.Join(state, "gateway-identity") }
