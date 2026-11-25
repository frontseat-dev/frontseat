package build

import (
	"io"
	"net/http"
	"sync"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/frontseat-dev/frontseat/build/daemon/directory"
	"github.com/frontseat-dev/frontseat/build/engine/engine/evaluate"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/download"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/core/telemetry"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// configuration supplies the private services a daemon serves over.
type configuration struct {
	// Workspace is the workspace's directory.
	Workspace string
	// Mounted says the directory is one repository the daemon was given,
	// rather than a workspace something fills: a job's checkout, which
	// has no workspace's manifest (life.Alone), or a test's.
	Mounted bool
	// System is the version control system the directory's repositories
	// are read with.
	System directory.System
	// Plugins are the plugins the workspace is evaluated with.
	Plugins []*pluginhost.Plugin
	// Executor runs the actions.
	Executor exec.Executor
	// Downloads brings what a shared fetch downloads to the grid; nil
	// where the daemon was given no asset service.
	Downloads download.Fetcher
	// Platform is what an action asks of the workers that run it.
	Platform map[string]string
	// Given are the variables of where the daemon runs that what
	// publishes a release may be given.
	Given []string
	// Telemetry correlates this daemon's work with the calling command.
	Telemetry *telemetry.Signals
	// Builds is the directory the daemon keeps its builds in: what each
	// did and what its actions printed, the newest of them, for the
	// daemon that comes after this one to find.
	Builds string
	// Files is the file the daemon saves what it knows of the
	// directory's files in when it ends, for the daemon that comes after
	// this one to read, and Program the program that saves it, which
	// alone reads it (source.OpenKeeper); no file, nothing is saved.
	Files, Program string
	// Log is where the daemon says what a person who reads its log
	// should know of it; none, it is said to telemetry alone.
	Log io.Writer
	// Now is the time, by which runs that have only just ended are kept;
	// the machine's own unless a test says otherwise.
	Now func() time.Time
}

// Daemon is the build API, served over a directory and what a daemon was
// given. It reads the directory as it is, and knows nothing of what
// fills it.
type Daemon struct {
	// Handler serves the build API (frontseat.build.v1alpha1), and
	// nothing else: what a client asks, through the control plane or on
	// the daemon's own port inside its sandbox.
	Handler http.Handler
	runs    *builds
	graph   *graphService
	// releases are stopped before the runs they wait on, and the
	// programs run too, which end with the daemon.
	releases *releases
	programs *runs
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

// Busy says whether the daemon has anything in hand: a build, a run of a
// program or a release that has not ended. Each was asked for and is
// answered later, with no call waiting on it meanwhile, so a daemon that
// ends itself when it has nothing to do asks this first.
func (d *Daemon) Busy() bool {
	return d.runs.busy() || d.programs.busy() || d.releases.busy()
}

// newDaemon composes the APIs over the supplied services. Close it when done.
func newDaemon(config configuration) (*Daemon, error) {
	held, err := directory.Open(config.Workspace, config.Mounted, config.System)
	if err != nil {
		return nil, err
	}
	api := connect.NewServer()
	runs, err := newBuilds(held, config.Plugins, config.Executor, config.Downloads, config.Platform, config.Telemetry, config.Builds, config.Now)
	if err != nil {
		return nil, err
	}
	runs.given, runs.said = config.Given, config.Log
	if config.Files != "" {
		// What the daemon before this one knew of the directory's files,
		// where it ended as a daemon ends. No run has begun yet.
		runs.files = source.OpenKeeper(config.Files, config.Program)
	}
	buildv1alpha1connect.RegisterBuildServiceHandler(api, &buildService{runs: runs})
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
	programs := newRuns(runs)
	buildv1alpha1connect.RegisterRunServiceHandler(api, &runService{programs: programs})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	return &Daemon{Handler: config.Telemetry.Handler(mux, "build API"), runs: runs, graph: graph, releases: made, programs: programs}, nil
}
