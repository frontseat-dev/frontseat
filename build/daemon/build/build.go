package build

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/directory"
	"github.com/frontseat-dev/frontseat/build/engine/exec/access"
	"github.com/frontseat-dev/frontseat/build/engine/exec/download"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/core/telemetry"
)

// Rewrite is one rule of which proxy stands in for a public site a
// download names (download.Rewrite).
type Rewrite = download.Rewrite

// Grid names the standard Remote Execution API used for this daemon's
// actions, and the grid's asset service, which fetches what a shared
// fetch downloads: its address, and which proxy of the grid's stands in
// for each public site a download names (download.Config). A daemon
// given no asset service downloads nothing, and says so where a fetch
// asks.
type Grid struct {
	Address  string
	Instance string
	Assets   string
	// Rewrites are taken in the order given: the first that matches.
	Rewrites []Rewrite
	// TLS asks the grid and its asset service over TLS, known by the
	// system's authorities; Authorities names a PEM file of the
	// authorities they are known by instead. Token names a file holding
	// the bearer token the grid admits this daemon by, read again for
	// every call and never sent in the clear. A daemon given none of
	// them asks in the clear, as the local stack's grid is asked.
	TLS         bool
	Authorities string
	Token       string
	// Known is what the authorities' file held when the daemon's
	// settings were read: the grid is known by those bytes, which the
	// daemon is identified by, and the file is not read again.
	Known []byte
}

// Check says what is wrong with how the grid is to be asked, before
// anything is dialled: a token with no TLS, a file that is not there or
// holds no authority or no token. It never repeats what a token file held.
func (g Grid) Check() error { return g.access().Check() }

// access is how the grid and its asset service are asked.
func (g Grid) access() access.Config {
	return access.Config{TLS: g.TLS, Authorities: g.Authorities, Known: g.Known, Token: g.Token}
}

// Config is the source, plugins and services a build daemon owns.
type Config struct {
	Workspace string
	Mounted   bool
	// System is the version control system the directory's repositories
	// are read with.
	System    directory.System
	Builds    string
	Plugins   [][]byte
	Grid      Grid
	Platform  map[string]string
	Telemetry *telemetry.Signals
	// Files is the file what the daemon knows of the directory's files
	// is saved in when it ends and read from when it starts, in its
	// state and never beneath the directory; Program is the program the
	// daemon is, by content: one saved by another is not read.
	Files, Program string
	// Given are the variables of where the daemon runs that what
	// publishes a release may be given, each by its name.
	Given []string
	// Log is where the daemon says what a person who reads its log
	// should know of it, a line each; none, it is said to telemetry
	// alone.
	Log io.Writer
}

// New opens the grid and plugin runtime, then serves the build API. Close
// joins runs before releasing those owned services.
func New(ctx context.Context, config Config) (*Daemon, error) {
	ctx = config.Telemetry.Context(ctx)
	grid, err := dial(ctx, config.Grid)
	if err != nil {
		return nil, err
	}
	var downloads download.Fetcher
	closeDownloads := func() {}
	if config.Grid.Assets != "" {
		client, err := download.Dial(ctx, download.Config{Address: config.Grid.Assets, Instance: config.Grid.Instance, Rewrites: config.Grid.Rewrites, Access: config.Grid.access()})
		if err != nil {
			grid.Close()
			return nil, err
		}
		downloads, closeDownloads = client, func() { _ = client.Close() }
	}
	host, err := pluginhost.New(ctx)
	if err != nil {
		closeDownloads()
		grid.Close()
		return nil, err
	}
	release := func() { _ = host.Close(context.Background()); closeDownloads(); _ = grid.Close() }
	var loaded []*pluginhost.Plugin
	var names []string
	for _, module := range config.Plugins {
		plugin, err := host.Load(ctx, module)
		if err != nil {
			release()
			return nil, err
		}
		loaded = append(loaded, plugin)
		names = append(names, plugin.Description.GetName())
	}
	daemon, err := newDaemon(configuration{
		Workspace: config.Workspace, Mounted: config.Mounted, System: config.System, Builds: config.Builds, Files: config.Files, Program: config.Program,
		Plugins: loaded, Executor: grid, Downloads: downloads, Platform: config.Platform, Telemetry: config.Telemetry, Given: config.Given, Log: config.Log,
	})
	if err != nil {
		release()
		return nil, err
	}
	daemon.release, daemon.Plugins = release, names
	return daemon, nil
}

// A grid started beside the daemon may take a moment to answer. One that
// answers that it does not admit the daemon has answered; so has one that
// was reached and cannot be asked over TLS as the daemon was told to ask
// it, and a daemon that cannot read its token (reapi.Settled).
func dial(ctx context.Context, config Grid) (*reapi.Grid, error) {
	if config.Address == "" {
		return nil, fmt.Errorf("no grid: the settings say none (grid.address)")
	}
	deadline := time.Now().Add(30 * time.Second)
	var grid *reapi.Grid
	var dialed error
	err := poll.Until(ctx, time.Second, func() (bool, error) {
		// Before each dial: a file that became something else since the
		// daemon started is said so as it is at the start.
		if err := config.Check(); err != nil {
			return false, err
		}
		grid, dialed = reapi.Dial(ctx, reapi.Config{Address: config.Address, Instance: config.Instance, Access: config.access()})
		return dialed == nil || reapi.Settled(dialed) || time.Now().After(deadline), nil
	})
	if err != nil {
		return nil, err
	}
	return grid, dialed
}
