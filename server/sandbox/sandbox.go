package sandbox

import (
	"context"
	"net/http"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/image"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver/openshell"
)

// GatewayRunner is the sandbox service over an OpenShell gateway, as the
// runner of tests with services asks it, over a socket of its own: each
// test's sandbox and each of its services' is one of the gateway's, which
// may read all its image holds, and write what it says it writes and what
// is bound in. The runtime is the gateway's container runtime's command
// line.
func GatewayRunner(publicURL, gateway, identity, runtime, state string) (http.Handler, error) {
	s, err := overGateway(publicURL, openshell.Config{Address: gateway, Identity: identity, ReadAll: true, Compute: runtime}, runtime, state)
	if err != nil {
		return nil, err
	}
	return s.served(), nil
}

// Gateway returns the sandbox service over an OpenShell gateway, for
// whatever is built on it: sandboxes in the network's terms and nothing
// of what lives in one. What runs in a sandbox may write the directories
// named, and nowhere else.
//
// The identity is the directory holding what the gateway is known by and
// knows this server by (ca.crt, tls.crt, tls.key). A gateway that does
// not answer this server is an error here, and nothing is served over it.
func Gateway(publicURL, gateway, identity, runtime, state string, writable []string) (Service, error) {
	return overGateway(publicURL, openshell.Config{Address: gateway, Identity: identity, Writable: writable}, runtime, state)
}

// overGateway is the sandbox service over a gateway its driver is
// configured for, asking the container runtime whose command line is
// runtime what an image is started with.
func overGateway(publicURL string, config openshell.Config, runtime, state string) (*service, error) {
	// The gateway makes its sandboxes with a container runtime, the one
	// whose command line is runtime (docker, podman), which is asked what
	// an image is started with.
	started := func(ctx context.Context, ref string) (openshell.Started, error) {
		if err := imageOf(ref); err != nil {
			return openshell.Started{}, err
		}
		entrypoint, command, environment, err := image.Started(ctx, runtime, ref)
		return openshell.Started{Entrypoint: entrypoint, Command: command, Environment: environment}, err
	}
	config.Image = started
	d, err := openshell.New(config)
	if err != nil {
		return nil, err
	}
	asking, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Answers(asking); err != nil {
		_ = d.Close()
		return nil, err
	}
	s, err := newService(d, publicURL, state)
	if err != nil {
		_ = d.Close()
		return nil, err
	}
	return s, nil
}
