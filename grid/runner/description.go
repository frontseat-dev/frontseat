package runner

import (
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/frontseat-dev/frontseat/common/confine"
	runnerv1 "github.com/frontseat-dev/frontseat/common/runner/runnerapi/frontseat/runner/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

const mostServices = 32

func (r *runner) checkDescription(d *runnerv1.RunnerSandbox) error {
	if len(d.GetServices()) > mostServices {
		return fmt.Errorf("an action may start at most %d services", mostServices)
	}
	before := map[string]*runnerv1.RunnerService{}
	entities := map[string]bool{}
	for _, s := range d.GetServices() {
		if !testservice.Named(s.GetName()) || before[s.GetName()] != nil {
			return fmt.Errorf("a service has no unique plain name")
		}
		ref, err := entity.Parse(s.GetEntity())
		if err != nil || ref.String() != s.GetEntity() || (ref.Kind != "component" && ref.Kind != "resource") || entities[s.GetEntity()] {
			return fmt.Errorf("a service has no unique canonical Component or Resource identity")
		}
		entities[s.GetEntity()] = true
		if s.Program == nil {
			if ref.Kind != "resource" {
				return fmt.Errorf("a Component starts a prepared program")
			}
			err = testservice.Check(s.GetService())
		} else {
			err = testservice.Program(s.GetService())
			if err == nil {
				err = r.checkProgram(s)
			}
		}
		if err != nil {
			return fmt.Errorf("service %s: %w", s.GetName(), err)
		}
		if s.GetService().GetTimeout().AsDuration() <= 0 || len(s.GetService().GetExports()) > 0 {
			return fmt.Errorf("a service has a readiness deadline and exports nothing here")
		}
		if ref.Kind == "resource" && len(s.GetService().GetApis()) != 0 {
			return fmt.Errorf("a Resource binds no API")
		}
		if err := checkRoutes(s.GetRoutes(), before, s.GetService().GetPorts()); err != nil {
			return fmt.Errorf("service %s: %w", s.GetName(), err)
		}
		before[s.GetName()] = s
	}
	if err := checkRoutes(d.GetRoutes(), before, nil); err != nil {
		return err
	}
	// Validate the whole closure before changing image names or creating anything.
	for _, s := range d.GetServices() {
		if s.Program == nil {
			image, err := r.proxied(s.Service.Image)
			if err != nil {
				return err
			}
			s.Service.Image = image
		}
	}
	return nil
}

func (r *runner) checkProgram(s *runnerv1.RunnerService) error {
	if len(s.GetService().GetEnvironment()) != 0 {
		return fmt.Errorf("a prepared program sets its environment only inside confinement")
	}
	p := s.Program
	if p.Root != path.Join(testservice.RuntimeDirectory, s.Name) {
		return fmt.Errorf("a program has no private prepared root")
	}
	if !filepath.IsLocal(p.Directory) || path.Clean(p.Directory) != p.Directory || strings.Contains(p.Directory, "\\") {
		return fmt.Errorf("a program's working directory is not plain and local")
	}
	if p.Image != r.image || p.Platform["container-image"] != "docker://"+p.Image || p.Platform["OSFamily"] != "linux" || p.Platform["ISA"] != map[string]string{"arm64": "aarch64", "amd64": "x86-64"}[runtime.GOARCH] {
		return fmt.Errorf("a program requires its preparation image and platform")
	}
	for key, value := range p.Platform {
		// A program is sent where the network is reached, as the daemon
		// sends one to be run (stage); as a test's service it reaches
		// nothing all the same, its sandbox's egress being blocked.
		if key == "network" && value == "egress" {
			continue
		}
		if key != "container-image" && key != "OSFamily" && key != "ISA" {
			return fmt.Errorf("a program's platform has a property that is not its image, system or architecture")
		}
	}
	layout, err := confine.ProgramLayout(p.Arguments)
	if err != nil {
		return err
	}
	up, err := filepath.Rel("/"+p.Directory, "/")
	if err != nil || p.Arguments[2] != filepath.ToSlash(up) {
		return fmt.Errorf("a program's command leaves its prepared root")
	}
	for _, dir := range []string{layout.Home, layout.Temporary} {
		if dir == "." || path.Clean(dir) != dir || strings.Contains(dir, "\\") {
			return fmt.Errorf("a program's writable directory is not plain and private")
		}
	}
	if overlaps(layout.Home, layout.Temporary) {
		return fmt.Errorf("a program's home and temporary directory overlap")
	}
	return nil
}

func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func checkRoutes(routes []*runnerv1.RunnerRoute, before map[string]*runnerv1.RunnerService, own map[string]int32) error {
	if len(routes) > 128 {
		return fmt.Errorf("too many direct service routes")
	}
	ports := map[int32]string{}
	for _, port := range own {
		ports[port] = "listening"
	}
	seen := map[string]bool{}
	for _, route := range routes {
		provider := before[route.GetService()]
		if provider == nil || provider.GetService().GetPorts()[route.GetPort()] == 0 {
			return fmt.Errorf("a route names no earlier provider endpoint")
		}
		endpoint := route.Service + "/" + route.Port
		if route.LocalPort < 1 || route.LocalPort > 65535 || (ports[route.LocalPort] != "" && ports[route.LocalPort] != endpoint) {
			return fmt.Errorf("a route has no distinct local endpoint")
		}
		key := endpoint + "/" + route.Api
		if seen[key] {
			return fmt.Errorf("a route is repeated")
		}
		seen[key], ports[route.LocalPort] = true, endpoint
		ref, _ := entity.Parse(provider.Entity)
		if ref.Kind == "component" {
			binding := provider.Service.Apis[route.Api]
			if binding == nil || binding.Port != route.Port {
				return fmt.Errorf("an API route grants a port outside that API")
			}
		} else if route.Api != "" {
			return fmt.Errorf("a Resource route names no API")
		}
	}
	return nil
}
