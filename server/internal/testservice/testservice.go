// Package testservice holds what is said of how a service is started
// for a test (frontseat.type.v1alpha1.Service) to what a service is. It
// is the one implementation of that: the daemon asks it of what a
// catalog entity wrote and of what a plugin answered, and a worker's
// runner asks it again of what it is told to start, since any client of
// the grid could have written that.
package testservice

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"

	"github.com/frontseat-dev/frontseat/server/internal/image"
	"github.com/frontseat-dev/frontseat/server/internal/plain"
)

// MostReady is the most a service may be given to be ready.
const MostReady = time.Hour

// RuntimeDirectory holds prepared service inputs. The runner masks this whole
// directory from the consuming test, including its own description.
const RuntimeDirectory = ".frontseat/runtime"

// RunnerDescription is the serialized RunnerSandbox in an action's root.
const RunnerDescription = RuntimeDirectory + "/runner.binpb"

// PortName is what a port is named by, as a pattern: the grammar an
// address in a service names a port by ("{port.http}") is built from it.
const PortName = `[a-z][a-z0-9-]*`

var (
	portName = regexp.MustCompile(`^` + PortName + `$`)
	// variable is a name of the environment: one the shell can export.
	variable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// serviceName is what names a service's container in its sandbox,
	// as the sandbox API takes it.
	serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// exportable is what a name the shell can export is, as a refusal says it.
const exportable = "letters, digits and underscores, beginning with no digit"

// Exportable reports whether a name is one the shell can export: what a
// variable of an environment is named by, wherever one is set.
func Exportable(name string) bool { return variable.MatchString(name) }

// Variable holds a name written under a key of a service (exports,
// environment) to one the shell can export.
func Variable(key, name string) error {
	if !Exportable(name) {
		return fmt.Errorf("service.%s: a name the shell can export is %s", key, exportable)
	}
	return nil
}

// Port holds one port of a service to what a port is: a name an address
// can name it by, and a number a listener is given.
func Port(name string, number int32) error {
	switch {
	case !portName.MatchString(name):
		return errors.New("service.ports: a port is named in lower-case letters, digits and dashes, beginning with a letter")
	case number < 1 || number > 65535:
		return errors.New("service.ports: a port is a number from 1 to 65535")
	}
	return nil
}

// Named reports whether a name can name a service in a sandbox: lower-case
// letters, digits and dashes, and not "main", which is the sandbox's own
// first container.
func Named(name string) bool { return serviceName.MatchString(name) && name != "main" }

// Shaped holds what a service says to the shape of one, whatever of it
// is yet to be said: each port a name and a port, each export and each
// variable it starts with a name the shell can export, each readiness
// check of a kind and whole, each endpoint an address of its own host
// and one port of its own, and a time to be ready of an hour at most.
// Its messages begin with the key of a catalog's service they are about,
// and say what the key takes and never what was written: a value may
// hold anything. Where several are wrong, the first by name is refused.
func Shaped(l *typev1.Service) error {
	// It runs as a user named by number, never root's, and writes
	// directories of its image named plainly, never the whole of it.
	if user := l.GetUser(); user != "" {
		if !plain.User(user) {
			return errors.New("service.user: a user by number, other than root's")
		}
	}
	if len(l.GetWritable()) > 32 {
		return fmt.Errorf("service.writable: %d directories, more than 32", len(l.GetWritable()))
	}
	for _, dir := range l.GetWritable() {
		if !plain.Absolute(dir) {
			return errors.New("service.writable: an absolute directory, plainly named, other than /")
		}
	}
	for _, api := range slices.Sorted(maps.Keys(l.GetApis())) {
		binding := l.GetApis()[api]
		ref, err := entity.Parse(api)
		if err != nil || ref.Kind != "api" || ref.String() != api {
			return errors.New("service.apis: an API is named by its whole reference, api:<namespace>/<name>")
		}
		if _, exists := l.GetPorts()[binding.GetPort()]; !exists {
			return fmt.Errorf("service.apis: %s names no listening port", api)
		}
		if len(binding.GetExports()) == 0 {
			return fmt.Errorf("service.apis: %s exports nothing", api)
		}
		for _, name := range slices.Sorted(maps.Keys(binding.GetExports())) {
			if !Exportable(name) {
				return fmt.Errorf("service.apis: %s exports under a name the shell cannot export: %s", api, exportable)
			}
			value := binding.GetExports()[name]
			rest := strings.ReplaceAll(strings.ReplaceAll(value, "{host}", ""), "{port."+binding.GetPort()+"}", "")
			if strings.ContainsAny(rest, "{}") {
				return fmt.Errorf("service.apis: %s export %s names something other than its own host and port", api, name)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(l.GetExports())) {
		if err := Variable("exports", name); err != nil {
			return err
		}
	}
	// What it starts with is named as what it exports is: a name that is
	// none would be the runtime's to refuse, when the test is started,
	// and one with an equals sign in it would set another.
	for _, name := range slices.Sorted(maps.Keys(l.GetEnvironment())) {
		if err := Variable("environment", name); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(l.GetPorts())) {
		if err := Port(name, l.GetPorts()[name]); err != nil {
			return err
		}
	}
	for i, ready := range l.GetReady() {
		switch c := ready.GetCheck().(type) {
		case *typev1.ServiceReady_Port:
			if c.Port == "" {
				return fmt.Errorf("service.ready[%d] is ready by a port it does not name", i)
			}
		case *typev1.ServiceReady_Http:
			if c.Http.GetPort() == "" || !strings.HasPrefix(c.Http.GetPath()+"/", "/") {
				return fmt.Errorf("service.ready[%d] is ready by an HTTP answer at no port or path", i)
			}
			if _, err := regexp.Compile(c.Http.GetBody()); err != nil {
				return fmt.Errorf("service.ready[%d]: the body an HTTP answer is matched by is a regular expression, as Go reads one", i)
			}
		case *typev1.ServiceReady_Command:
			if len(c.Command.GetArguments()) == 0 {
				return fmt.Errorf("service.ready[%d] is ready by a command that names nothing", i)
			}
		default:
			return fmt.Errorf("service.ready[%d] is one of port, http or command", i)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(l.GetEndpoints())) {
		if _, err := EndpointPort(name, l.GetEndpoints()[name]); err != nil {
			return err
		}
	}
	if t := l.GetTimeout(); t != nil {
		switch given := t.AsDuration(); {
		case !t.IsValid() || given < 0 || given%time.Second != 0:
			return errors.New("service.ready-timeout is a length of time more than zero, in whole seconds")
		case given > MostReady:
			return errors.New("service.ready-timeout is more than the hour a service may be given to be ready")
		}
	}
	return nil
}

// Check holds a service to what every service says, whoever said it:
// its shape, an image named with its registry and digest, a port, and
// readiness and endpoints that name ports it serves.
func Check(l *typev1.Service) error {
	if err := Shaped(l); err != nil {
		return err
	}
	if _, err := image.Read(l.GetImage()); err != nil {
		return fmt.Errorf("service.image %v", err)
	}
	return endpoints(l)
}

// Program checks runtime metadata for a prepared program. Its command and
// compatible image come from preparation, never an image entrypoint override.
func Program(l *typev1.Service) error {
	if err := Shaped(l); err != nil {
		return err
	}
	if l.GetImage() != "" || len(l.GetArguments()) != 0 {
		return errors.New("a service that is a program names no image or arguments; its program supplies the command")
	}
	return endpoints(l)
}

func endpoints(l *typev1.Service) error {
	if len(l.GetPorts()) == 0 {
		return errors.New("service names no port")
	}
	if len(l.GetReady()) == 0 {
		return errors.New("service says nothing of when it is ready")
	}
	for i, ready := range l.GetReady() {
		if port := PortOf(ready); port != "" {
			if _, served := l.GetPorts()[port]; !served {
				return fmt.Errorf("service.ready[%d] names a port it does not serve", i)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(l.GetEndpoints())) {
		port, _ := EndpointPort(name, l.GetEndpoints()[name])
		if _, served := l.GetPorts()[port]; !served {
			return fmt.Errorf("service.endpoints: %s names a port %s it does not serve", name, port)
		}
	}
	return nil
}

// endpointPort is a port an endpoint names: "{port.<name>}".
var endpointPort = regexp.MustCompile(`\{port\.(` + PortName + `)\}`)

// EndpointPort is the one port of its own an endpoint is reached at. An
// endpoint is named as a port is, and is an address made of "{host}",
// that port, and text with no brace in it; anything else is refused.
func EndpointPort(name, address string) (string, error) {
	if !portName.MatchString(name) {
		return "", errors.New("service.endpoints: an endpoint's name is lower-case letters, digits and dashes, beginning with a letter")
	}
	ports := map[string]bool{}
	for _, m := range endpointPort.FindAllStringSubmatch(address, -1) {
		ports[m[1]] = true
	}
	rest := endpointPort.ReplaceAllString(strings.ReplaceAll(address, "{host}", ""), "")
	switch {
	case !strings.Contains(address, "{host}"):
		return "", fmt.Errorf("service.endpoints: %s does not say {host}, where it is reached", name)
	case len(ports) != 1:
		return "", fmt.Errorf("service.endpoints: %s names %d ports; an endpoint is one port of its own, {port.<name>}", name, len(ports))
	case strings.ContainsAny(rest, "{}"):
		return "", fmt.Errorf("service.endpoints: %s names something other than its own host and one port of its own", name)
	}
	for port := range ports {
		return port, nil
	}
	return "", nil
}

// PortOf is the name of the port a readiness check asks, or nothing for
// one that asks none.
func PortOf(ready *typev1.ServiceReady) string {
	if http := ready.GetHttp(); http != nil {
		return http.GetPort()
	}
	return ready.GetPort()
}
