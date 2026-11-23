// Package buildtest starts a daemon for a test, as frontseatd starts
// one: over a workspace directory, with the Go and Backstage plugins, running
// actions on the grid of the local stack. A test then speaks to
// it as any client does, through the build API at the address returned,
// and tells it what to hold as the control plane does (Bring), through
// its source API, which no client reaches, its entities resolved as the
// control plane resolves them, through a fake Backstage.
package buildtest

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/build"
	"github.com/frontseat-dev/frontseat/server/internal/source"
	"github.com/frontseat-dev/frontseat/server/internal/source/told"
	"github.com/frontseat-dev/frontseat/server/internal/source/told/toldtest"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"github.com/frontseat-dev/frontseat/server/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
)

var (
	once     sync.Once
	modules  [][]byte
	buildErr error

	// sources are where each daemon started serves its source API, by
	// the address of its build API.
	sources sync.Map
	// generations counts what the daemons are told, as the control plane
	// counts what it tells.
	generations atomic.Int64
)

// Start serves the build API over an empty workspace directory and
// returns its address. It skips the test where no grid answers.
func Start(t testing.TB, workspace string) string { return start(t, workspace, false, nil) }

// Mounted serves the API over a directory of the test's, one repository,
// which the daemon is given and does not bring. Additional
// Wasm modules let contract specs exercise SDK fixtures beside the real plugins.
func Mounted(t testing.TB, checkout string, additionalPlugins ...[]byte) string {
	return MountedWith(t, checkout, Options{Plugins: additionalPlugins})
}

// Options are what a daemon a test starts is given beside the grid.
type Options struct {
	// Plugins are Wasm modules it loads beside the real plugins.
	Plugins [][]byte
	// Rewrites send a download's address elsewhere (frontseatd
	// --rewrite): a regular expression over it to what replaces it.
	Rewrites map[string]string
}

// MountedWith is Mounted, the daemon given what options say.
func MountedWith(t testing.TB, checkout string, options Options) string {
	return start(t, checkout, true, options.Rewrites, options.Plugins...)
}

func start(t testing.TB, workspace string, mounted bool, rewrites map[string]string, additionalPlugins ...[]byte) string {
	t.Helper()
	ctx := context.Background()
	signals, stop, err := telemetry.Setup(ctx, "frontseatd")
	t.Cleanup(func() {
		if err := telemetry.Ended(stop); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx = signals.Context(ctx)
	once.Do(func() {
		for _, name := range []string{"go", "backstage", "release", "container", "postgres", "npm", "jest"} {
			var module []byte
			module, buildErr = testmodule.Plugin(name)
			if buildErr != nil {
				return
			}
			modules = append(modules, module)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	// A test's rewrites are tried in the order of their expressions: no
	// test gives two that match one download.
	ordered := make([]build.Rewrite, 0, len(rewrites))
	for _, from := range slices.Sorted(maps.Keys(rewrites)) {
		ordered = append(ordered, build.Rewrite{From: from, To: rewrites[from]})
	}
	// What fills the directory, beside the build, as frontseatd has it:
	// stopped after the build is.
	held, err := source.New(workspace, mounted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(held.Close)
	daemon, err := build.New(ctx, build.Config{
		Workspace: workspace, Mounted: mounted, System: source.System(),
		Plugins:  append(append([][]byte(nil), modules...), additionalPlugins...),
		Grid:     build.Grid{Address: testgrid.Address(t), Assets: testgrid.Assets(t), Rewrites: ordered},
		Platform: testgrid.Platform(), Builds: t.TempDir(), Telemetry: signals,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(daemon.Close)
	srv := httptest.NewServer(daemon.Handler)
	t.Cleanup(srv.Close)
	told := httptest.NewServer(source.Serve(held))
	t.Cleanup(told.Close)
	sources.Store(srv.URL, told.URL)
	t.Cleanup(func() { sources.Delete(srv.URL) })
	return srv.URL
}

// sourceOf is the source API of the daemon whose build API is at an
// address.
func sourceOf(at string) (sourcev1alpha1connect.SourceServiceClient, error) {
	found, ok := sources.Load(at)
	if !ok {
		return nil, fmt.Errorf("no daemon this test started serves its build API at %s", at)
	}
	return sourcev1alpha1connect.NewSourceServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, found.(string)))), nil
}

// Bring tells the daemon whose build API is at an address to hold what
// a spec names, as the control plane tells it, and returns once it holds
// it, or why it does not: its refusal, a refusal of the catalog, or why
// bringing it failed. Its entities are resolved as the control plane
// resolves them, through a fake Backstage; with none, a spec of
// entities is refused, as a control plane given none refuses it.
func Bring(ctx context.Context, at string, spec *workspacev1.WorkspaceSpec, catalog backstagetest.Served) error {
	api, err := sourceOf(at)
	if err != nil {
		return err
	}
	generation := generations.Add(1)
	telling, err := toldtest.Spec(ctx, spec, generation, catalog, source.Fetching().NameOf)
	if err != nil {
		return err
	}
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: telling}); err != nil {
		return err
	}
	waited, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: generation})
	if err != nil {
		return err
	}
	if status := told.Status(waited.GetSource()); status.GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		return fmt.Errorf("the workspace failed: %s", status.GetFailure())
	}
	return nil
}

// Held is what the daemon whose build API is at an address holds, as a
// workspace's status says it.
func Held(ctx context.Context, at string) (*workspacev1.WorkspaceStatus, error) {
	api, err := sourceOf(at)
	if err != nil {
		return nil, err
	}
	// Waited after no generation, it answers at once.
	waited, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{})
	if err != nil {
		return nil, err
	}
	return told.Status(waited.GetSource()), nil
}
