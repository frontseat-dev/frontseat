// Package workspaces is a control plane that can be composed: workspaces
// served over the sandboxes an OpenShell gateway makes
// (frontseat.workspace.v1alpha1, and each person's credentials), as
// frontseat-server serves them, and what frontseat-enterprise composes
// around and never copies. What a control plane is made of is said here,
// in plain types; how it is made is this repository's own
// (workspace/server/controlplane), and no one outside names it.
//
// A control plane serves local workspaces, whose repositories are cloned
// in their sandboxes. Given what serves stored repositories
// (WithSources), it serves remote ones too, whose repositories are held
// from a snapshot store and mounted in their sandboxes; given none, it
// refuses a workspace that asks for one by name, STORE_NOT_SERVED. This
// repository holds no store: the port is all of it that is here
// (workspace/server/workspaces/sources).
package workspaces

import (
	"net/http"

	"github.com/frontseat-dev/frontseat/common/vcs/chosen"
	"github.com/frontseat-dev/frontseat/workspace/server/backstage"
	"github.com/frontseat-dev/frontseat/workspace/server/controlplane"
	"github.com/frontseat-dev/frontseat/workspace/server/workspaces/sources"
)

// Gateway is the OpenShell gateway that makes a control plane's
// sandboxes.
type Gateway struct {
	// Address is where it is asked, over https, as a URL.
	Address string
	// Identity is the directory holding what the gateway is known by and
	// knows the control plane by: ca.crt, tls.crt and tls.key.
	Identity string
	// Runtime is the command line of the container runtime the gateway
	// makes its sandboxes with (docker, podman), which is asked what an
	// image is started with.
	Runtime string
	// Compute is the name of the gateway's compute driver (docker,
	// podman), which a directory given to a sandbox is said to the
	// gateway under; empty for Runtime's. A control plane on another
	// machine than its gateway says the gateway's: a directory said
	// under another name is bound nowhere, and the gateway says nothing.
	Compute string
}

// Grid is the grid every workspace of a control plane is built on.
type Grid struct {
	// Hosts are where a build reaches it, each host:port: the grid, and
	// its asset service.
	Hosts []string
	// Arguments are the flags that tell a box's supervisor of the grid
	// (frontseat-box --help), each one argument written with its "=".
	Arguments []string
}

// Backstage is the Backstage a control plane resolves the entities a
// workspace is asked for through. It is read by BackstageAt.
type Backstage struct {
	catalog *backstage.Catalog
}

// BackstageAt is the Backstage at an address, https://HOST[:PORT][/PATH],
// asked with the token a file holds, where one is named: one line, the
// file its reader's alone, read now, once. Loopback lets it be at this
// machine, and asked over http there, for a local stack and tests.
func BackstageAt(base, tokenFile string, loopback bool) (*Backstage, error) {
	var token string
	if tokenFile != "" {
		read, err := backstage.ReadToken(tokenFile)
		if err != nil {
			return nil, err
		}
		token = read
	}
	catalog, err := backstage.New(backstage.Config{Base: base, Token: token, Loopback: loopback})
	if err != nil {
		return nil, err
	}
	return &Backstage{catalog: catalog}, nil
}

// Config is what a control plane is told by whoever serves it.
type Config struct {
	// State is a directory of the control plane's own, where it keeps
	// its sandboxes' and its workspaces' records, for the one that
	// comes after.
	State string
	// OwnerToken is the file the token of the one person it answers,
	// its owner, is in: one is minted there where there is none.
	OwnerToken string
	// Box is the image a workspace's sandbox runs where none is asked
	// for; empty where one must be.
	Box string
	// Grid is the grid every workspace is built on.
	Grid Grid
	// Backstage is what entities are resolved through; nil for none,
	// and a workspace asked for by entity is then refused,
	// ENTITIES_NOT_SERVED.
	Backstage *Backstage
}

// Option is what a control plane is given beside what every one is.
type Option func(*options)

type options struct {
	sources sources.Sources
}

// WithSources gives the control plane what serves stored repositories:
// its workspaces may then be remote, each stored repository held and
// mounted by it, and bound in the workspace's sandbox by the gateway,
// which must be let bind a directory of its machine. Without it, a spec
// that names a stored repository, a task or a landing is refused
// UNIMPLEMENTED, STORE_NOT_SERVED, and every workspace is local.
func WithSources(s sources.Sources) Option {
	return func(o *options) { o.sources = s }
}

// Plane is a control plane that is serving.
type Plane struct {
	// Handler serves its API to callers it knows: workspaces, each
	// person's credentials, and the ways to each workspace's shell and
	// the ports of its sandbox.
	http.Handler
	stop func()
}

// Close stops working on workspaces, and ends every connection the
// control plane is carrying to one. The workspaces stay as they are.
func (p *Plane) Close() { p.stop() }

// New is a control plane over an OpenShell gateway: what frontseat-server
// serves, and what frontseat-enterprise composes around. Its clients
// reach it at public, a URL. A gateway that does not answer is an error
// here, and nothing is served over it.
func New(public string, gateway Gateway, config Config, given ...Option) (*Plane, error) {
	callers, err := controlplane.OwnerToken(config.OwnerToken)
	if err != nil {
		return nil, err
	}
	told, over := composed(public, gateway, config, given)
	told.Callers = callers
	plane, err := controlplane.OverGateway(told, over)
	if err != nil {
		return nil, err
	}
	return &Plane{Handler: plane, stop: plane.Close}, nil
}

// composed is what the control plane and its gateway are told, of what
// New is given: everything but who its callers are, which a file says.
// A control plane given what serves stored repositories has its gateway
// bind the directories that come of it into its workspaces' sandboxes;
// one given none binds nothing of its machine into any.
func composed(public string, gateway Gateway, config Config, given []Option) (controlplane.Config, controlplane.Gateway) {
	var o options
	for _, option := range given {
		option(&o)
	}
	told := controlplane.Config{
		PublicURL: public, State: config.State, Box: config.Box,
		Grid:     controlplane.Grid{Hosts: config.Grid.Hosts, Arguments: config.Grid.Arguments},
		Fetching: chosen.System(), Sources: o.sources,
	}
	// A nil catalog is no resolver at all, and not one that is nil.
	if config.Backstage != nil {
		told.Entities = config.Backstage.catalog
	}
	return told, controlplane.Gateway{Address: gateway.Address, Identity: gateway.Identity, Runtime: gateway.Runtime, Compute: gateway.Compute, HostVolumes: o.sources != nil}
}
