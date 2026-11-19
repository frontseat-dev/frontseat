package build

import (
	"context"
	"fmt"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/exec/download"
	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

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
	Rewrites map[string]string
}

// Config is the source, plugins and services a build daemon owns.
type Config struct {
	Workspace string
	Mounted   bool
	Builds    string
	Plugins   [][]byte
	Grid      Grid
	Platform  map[string]string
	Telemetry *telemetry.Signals
}

// New opens the grid and plugin runtime, then serves the build and source
// APIs. Close joins runs before releasing those owned services.
func New(ctx context.Context, config Config) (*Daemon, error) {
	ctx = config.Telemetry.Context(ctx)
	grid, err := dial(ctx, config.Grid)
	if err != nil {
		return nil, err
	}
	var downloads download.Fetcher
	closeDownloads := func() {}
	if config.Grid.Assets != "" {
		client, err := download.Dial(ctx, download.Config{Address: config.Grid.Assets, Instance: config.Grid.Instance, Rewrites: config.Grid.Rewrites})
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
		Workspace: config.Workspace, Mounted: config.Mounted, Builds: config.Builds,
		Plugins: loaded, Executor: grid, Downloads: downloads, Platform: config.Platform, Telemetry: config.Telemetry,
	})
	if err != nil {
		release()
		return nil, err
	}
	daemon.release, daemon.Plugins = release, names
	return daemon, nil
}

// A grid started beside the daemon may take a moment to answer.
func dial(ctx context.Context, config Grid) (*reapi.Grid, error) {
	if config.Address == "" {
		return nil, fmt.Errorf("no grid: give --grid")
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		grid, err := reapi.Dial(ctx, reapi.Config{Address: config.Address, Instance: config.Instance})
		if err == nil || time.Now().After(deadline) {
			return grid, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
