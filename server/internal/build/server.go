package build

import (
	"net/http"
	"sync"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/internal/engine/evaluate"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/download"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/source"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// configuration supplies the private services a daemon serves over.
type configuration struct {
	// Workspace is the workspace's directory.
	Workspace string
	// Mounted says the directory is one repository the daemon was given,
	// rather than a workspace it populates. Only a daemon a test runs in
	// its own process is opened so: no command or flag asks for it.
	Mounted bool
	// Plugins are the plugins the workspace is evaluated with.
	Plugins []*pluginhost.Plugin
	// Executor runs the actions.
	Executor exec.Executor
	// Downloads brings what a shared fetch downloads to the grid; nil
	// where the daemon was given no asset service.
	Downloads download.Fetcher
	// Platform is what an action asks of the workers that run it.
	Platform map[string]string
	// Telemetry correlates this daemon's work with the calling command.
	Telemetry *telemetry.Signals
	// Builds is the directory the daemon keeps its builds in: what each
	// did and what its actions printed, the newest of them, for the
	// daemon that comes after this one to find.
	Builds string
	// Now is the time, by which runs that have only just ended are kept;
	// the machine's own unless a test says otherwise.
	Now func() time.Time
}

// Daemon is the build API, served over what a daemon was given, and
// the source API beside it.
type Daemon struct {
	// Handler serves the build API (frontseat.build.v1alpha1), and
	// nothing else: what a client asks, through the control plane or on
	// the daemon's own port inside its sandbox.
	Handler http.Handler
	// Source serves the source API (frontseat.source.v1alpha1), and
	// nothing else: what the control plane tells the daemon to hold,
	// served on a port of its own that the control plane alone dials.
	Source http.Handler
	runs   *runs
	graph  *graphService
	// releases are stopped before the runs they wait on, and the
	// programs run too, which end with the daemon.
	releases *releases
	programs *programRuns
	once     sync.Once
	release  func()
	// Plugins lists the loaded plugin names in configuration order.
	Plugins []string
}

// Close ends the daemon's runs: each is cancelled, and Close returns once
// all have ended. No run is made after it. What the daemon runs actions
// with is let go only after it.
func (d *Daemon) Close() {
	d.once.Do(func() {
		// A release waits on a run, so it is stopped before the runs are.
		d.releases.stop()
		d.programs.stop()
		d.runs.stop()
		// Cancel run-owned contexts before ending their shared evaluation owner.
		d.graph.session.Close()
		if d.release != nil {
			d.release()
		}
	})
}

// newDaemon composes the APIs over the supplied services. Close it when done.
func newDaemon(config configuration) (*Daemon, error) {
	held, err := source.New(config.Workspace, config.Mounted)
	if err != nil {
		return nil, err
	}
	api := connect.NewServer()
	runs, err := newRuns(held, config.Plugins, config.Executor, config.Downloads, config.Platform, config.Telemetry, config.Builds)
	if err != nil {
		return nil, err
	}
	runs.now = config.Now
	buildv1alpha1connect.RegisterBuildServiceHandler(api, &runService{runs: runs})
	graph := &graphService{runs: runs}
	// What the daemon keeps of an evaluation, and how many graph requests
	// own source at once (retained.go).
	graph.session, err = evaluate.NewSession(config.Plugins, evaluate.RetentionLimits{Records: retainedRecords, WireBytes: retainedWireBytes}, graphRequestsAdmitted, runs.answers, graph.source)
	if err != nil {
		runs.stop()
		return nil, err
	}
	runs.evaluation = graph.session
	buildv1alpha1connect.RegisterGraphServiceHandler(api, graph)
	made := newReleases(graph)
	buildv1alpha1connect.RegisterReleaseServiceHandler(api, &releaseService{graph: graph, releases: made})
	programs := newProgramRuns(runs)
	buildv1alpha1connect.RegisterRunServiceHandler(api, &programService{programs: programs})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	return &Daemon{Handler: config.Telemetry.Handler(mux, "build API"), Source: config.Telemetry.Handler(source.Serve(held), "source API"), runs: runs, graph: graph, releases: made, programs: programs}, nil
}
