package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frontseat-dev/frontseat/common/confine"
	runnerv1 "github.com/frontseat-dev/frontseat/common/runner/runnerapi/frontseat/runner/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
	"github.com/frontseat-dev/frontseat/core/poll"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// plainDirectory refuses links in every mount path. The paths come from a
// caller's input tree, while the directories beside it are the worker's.
func plainDirectory(root *os.Root, name string) error {
	if !filepath.IsLocal(name) || path.Clean(name) != name {
		return fmt.Errorf("a mount is not a plain local directory")
	}
	at := ""
	for _, part := range strings.Split(name, "/") {
		at = path.Join(at, part)
		info, err := root.Lstat(at)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("a mount path is not a directory or passes through a link")
		}
	}
	return nil
}

func (a *attempt) routes(name string, outgoing []*runnerv1.RunnerRoute) ([]string, []*sandboxv1.Volume, error) {
	// One Unix directory per endpoint. A consumer receives only those
	// directories it was granted, never the containing directory.
	var args = []string{confine.Confiner, "routes"}
	var volumes []*sandboxv1.Volume
	providers := map[string]*runnerv1.RunnerService{}
	for _, service := range a.description.Services {
		providers[service.Name] = service
	}
	grants := map[int32]bool{}
	incoming := func(routes []*runnerv1.RunnerRoute) {
		for _, route := range routes {
			if route.Service == name {
				grants[providers[name].Service.Ports[route.Port]] = true
			}
		}
	}
	for _, consumer := range a.description.Services {
		incoming(consumer.Routes)
	}
	incoming(a.description.Routes)
	// What the runner checks a service ready by, it reaches as a consumer
	// does: through a route to the port the check names.
	if provider, is := providers[name]; is {
		for _, check := range provider.GetService().GetReady() {
			if _, command := check.GetCheck().(*typev1.ServiceReady_Command); !command {
				grants[provider.GetService().GetPorts()[testservice.PortOf(check)]] = true
			}
		}
	}
	ports := make([]int32, 0, len(grants))
	for port := range grants {
		ports = append(ports, port)
	}
	slices.Sort(ports)
	for i, port := range ports {
		number := strconv.Itoa(int(port))
		dir := path.Join("routes", name, number)
		if err := a.directory(dir); err != nil {
			return nil, nil, err
		}
		inside := path.Join("/frontseat/routes", name, number)
		volumes = append(volumes, a.ownVolume(fmt.Sprintf("in-%d", i), dir, inside, false))
		args = append(args, "unix", path.Join(inside, "socket"), "tcp", "127.0.0.1:"+number)
	}
	seen := map[int32]bool{}
	mounted := map[string]bool{}
	for _, route := range outgoing {
		if seen[route.LocalPort] {
			continue
		}
		seen[route.LocalPort] = true
		dir := path.Join("routes", route.Service, strconv.Itoa(int(providers[route.Service].Service.Ports[route.Port])))
		inside := path.Join("/frontseat", dir)
		if !mounted[dir] {
			volumes = append(volumes, a.ownVolume(fmt.Sprintf("out-%d", len(mounted)), dir, inside, true))
			mounted[dir] = true
		}
		args = append(args, "tcp", "127.0.0.1:"+strconv.Itoa(int(route.LocalPort)), "unix", path.Join(inside, "socket"))
	}
	if (len(args)-2)/4 > 128 {
		return nil, nil, fmt.Errorf("a runtime needs more than 128 routes")
	}
	if name == "" {
		name = "test"
	}
	if err := a.directory(path.Join("ready", name)); err != nil {
		return nil, nil, err
	}
	volumes = append(volumes, a.ownVolume("ready", path.Join("ready", name), "/frontseat/ready", false))
	return args, volumes, nil
}

func (a *attempt) routesReady(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) {
		_, err := os.Stat(filepath.Join(a.mine, "ready", name, "listening"))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return false, nil
	})
	// Only the waiting gave up with ctx's error: a file is not asked
	// after with ctx.
	if err != nil && errors.Is(err, ctx.Err()) {
		return fmt.Errorf("service routes: %w", err)
	}
	return err
}

func (a *attempt) programVolumes(root *os.Root, s *runnerv1.RunnerService) ([]*sandboxv1.Volume, error) {
	p := s.Program
	layout, err := confine.ProgramLayout(p.Arguments)
	if err != nil {
		return nil, err
	}
	input := path.Join(a.paths.root, p.Root)
	if err := plainDirectory(root, input); err != nil {
		return nil, err
	}
	if err := plainDirectory(root, path.Join(input, p.Directory)); err != nil {
		return nil, err
	}
	for _, dir := range []string{"work", "home", "tmp"} {
		if err := a.directory(path.Join("programs", s.Name, dir)); err != nil {
			return nil, err
		}
	}
	volumes := []*sandboxv1.Volume{
		a.ownVolume("work", path.Join("programs", s.Name, "work"), "/runtime", false),
		{Name: "inputs", Kind: sandboxv1.Volume_KIND_HOST, Source: path.Join(a.r.host, "build"), Subpath: input, Path: "/runtime/root", ReadOnly: true},
		a.ownVolume("home", path.Join("programs", s.Name, "home"), path.Join("/runtime/root", layout.Home), false),
		a.ownVolume("tmp", path.Join("programs", s.Name, "tmp"), path.Join("/runtime/root", layout.Temporary), false),
	}
	// Preserve prepared HOME artifacts under the writable HOME. Bind each
	// prepared entry read-only so the service can create its own cache/config
	// without hiding tools or binaries that preparation placed there.
	home := path.Join(input, layout.Home)
	if _, err := root.Lstat(home); errors.Is(err, os.ErrNotExist) {
		return volumes, nil
	} else if err != nil {
		return nil, err
	}
	if err := plainDirectory(root, home); err != nil {
		return nil, err
	}
	dir, err := root.Open(home)
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return nil, err
	}
	for i, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return nil, fmt.Errorf("a prepared home entry is neither a plain file nor a directory")
		}
		volumes = append(volumes, &sandboxv1.Volume{Name: fmt.Sprintf("prepared-%d", i), Kind: sandboxv1.Volume_KIND_HOST, Source: path.Join(a.r.host, "build"), Subpath: path.Join(home, entry.Name()), Path: path.Join("/runtime/root", layout.Home, entry.Name()), ReadOnly: true})
	}
	return volumes, nil
}
