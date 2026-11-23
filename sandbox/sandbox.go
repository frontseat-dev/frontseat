package sandbox

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/frontseat-dev/frontseat/core/image"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/driver/openshell"
)

// Options are what a sandbox service is made with: where its clients
// reach it, where it keeps its records, and what makes its sandboxes,
// which is a driver or an OpenShell gateway, one of the two.
type Options struct {
	// PublicURL is the address clients reach the service at: a
	// sandbox's ports are served beneath it, at
	// PublicURL/sandboxes/<id>/ports/<port>/ over HTTP and
	// PublicURL/sandboxes/<id>/tunnels/<port> as a WebSocket.
	PublicURL string
	// State is a directory of the service's own, where it records each
	// sandbox it makes: a service made again over the same directory
	// takes up what the one before it made.
	State string
	// Driver makes the sandboxes, where the service is made over a
	// driver of the caller's.
	Driver driver.Driver
	// Gateway makes the sandboxes, where the service is made over an
	// OpenShell gateway.
	Gateway *Gateway
}

// Gateway is an OpenShell gateway a service makes its sandboxes with.
type Gateway struct {
	// Address is the gateway's, as host:port or a URL.
	Address string
	// Identity is the directory holding what the gateway is known by and
	// knows this server by: ca.crt, tls.crt and tls.key. A gateway that
	// does not answer this server is an error when the service is made,
	// and nothing is served over it.
	Identity string
	// Runtime is the command line of the container runtime the gateway
	// makes its sandboxes with (docker, podman), which is asked what an
	// image is started with.
	Runtime string
	// Writable are the directories what runs in a sandbox may write,
	// beside its temporary directory, and nowhere else.
	Writable []string
	// ReadAll is whether what runs in a sandbox may read all its image
	// holds, not only the system's directories, as a service's image
	// keeps what it runs where it likes.
	ReadAll bool
	// HostVolumes is whether a sandbox may be given a directory of the
	// gateway's host (a host volume), bound by Runtime: the gateway must
	// then be let bind them (enable_bind_mounts).
	HostVolumes bool
}

// answering is a driver that can say, before anything is served over
// it, whether its provider answers this server.
type answering interface {
	Answers(ctx context.Context) error
}

// New makes the sandbox service over what Options says makes its
// sandboxes, holding the sandboxes its state records. A driver that can
// say whether its provider answers, as a gateway's can, is asked first,
// and one that does not is an error.
//
// The service owns its driver from here: Close lets go of it, and New
// lets go of it where it fails.
func New(options Options) (*Server, error) {
	d := options.Driver
	switch {
	case d != nil && options.Gateway != nil:
		return nil, errors.New("sandbox: a service is made over a driver or a gateway, and not both")
	case options.Gateway != nil:
		made, err := overGateway(*options.Gateway)
		if err != nil {
			return nil, err
		}
		d = made
	case d == nil:
		return nil, errors.New("sandbox: a service is made over a driver or a gateway, and this one is given neither")
	}
	s, err := made(d, options)
	if err != nil {
		if closer, ok := d.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, err
	}
	return s, nil
}

// made is the service over a driver, once the driver has answered.
func made(d driver.Driver, options Options) (*Server, error) {
	if asked, ok := d.(answering); ok {
		asking, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := asked.Answers(asking); err != nil {
			return nil, err
		}
	}
	files, err := open(options.State)
	if err != nil {
		return nil, err
	}
	s := &Server{driver: d, publicURL: options.PublicURL, kept: files, records: map[string]*record{}, asked: map[string]*asking{}}
	if err := s.recover(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// overGateway is the driver of an OpenShell gateway, asking the
// container runtime the gateway makes its sandboxes with what an image
// is started with.
func overGateway(gateway Gateway) (*openshell.Driver, error) {
	config := openshell.Config{Address: gateway.Address, Identity: gateway.Identity, Writable: gateway.Writable, ReadAll: gateway.ReadAll}
	if gateway.HostVolumes {
		config.Compute = gateway.Runtime
	}
	config.Image = func(ctx context.Context, ref string) (openshell.Started, error) {
		if err := imageOf(ref); err != nil {
			return openshell.Started{}, err
		}
		entrypoint, command, environment, err := image.Started(ctx, gateway.Runtime, ref)
		return openshell.Started{Entrypoint: entrypoint, Command: command, Environment: environment}, err
	}
	return openshell.New(config)
}
