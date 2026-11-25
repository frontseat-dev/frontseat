package driver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"

	"github.com/frontseat-dev/frontseat/core/plain"
)

// Driver makes sandboxes on one provider.
type Driver interface {
	// Name is the driver's name, e.g. "container".
	Name() string
	// Capabilities says what the driver can enforce. A sandbox that asks
	// for anything else never reaches the driver.
	Capabilities() Capabilities
	// Apply makes the sandbox, or brings the one there is to its record:
	// whether it runs or is paused. Applied twice, it changes nothing the
	// second time.
	Apply(ctx context.Context, record Sandbox) (Sandbox, error)
	// Observe returns the sandbox as it is now, or ErrNotFound. One that
	// was ended and that the provider still holds is Ending, never
	// ErrNotFound.
	Observe(ctx context.Context, id string) (Sandbox, error)
	// Delete ends the sandbox, and succeeds only once the provider holds
	// it no more: one that is not gone when Delete returns is an error,
	// and Delete is asked again until it succeeds. Meanwhile the sandbox
	// is Ending for as long as its provider is ending it, and as the
	// provider left it where it gave the ending up, which may be with
	// less than it had. Deleting one that is gone succeeds.
	Delete(ctx context.Context, id string) error
	// Dial connects to a port inside the sandbox. It is the one transport
	// a driver must supply: a box's source API, its shell and whatever
	// port of it a caller is carried to are each reached through it.
	Dial(ctx context.Context, id string, port int) (net.Conn, error)
}

// ErrNotFound is what Observe returns for a sandbox the provider does not
// hold. It is never said of one the provider still holds, however far
// its ending has come: whoever is told it takes the sandbox for gone.
var ErrNotFound = errors.New("driver: no such sandbox")

// ErrEnding is what Apply and Dial say of a sandbox that is Ending:
// nothing is asked of it but that it end. A command is run only in one
// that is Running.
var ErrEnding = errors.New("driver: the sandbox is ending")

// ErrInvalid is what a driver says of something it was asked that is
// not well formed: a host that is no host's name, a username it cannot
// take. It is the asker's to mend, where another failure is not.
var ErrInvalid = errors.New("driver: what was asked is not well formed")

// ErrHostInvalid is what a driver says of a credential's host that is
// no host's name. It is ErrInvalid too.
var ErrHostInvalid = fmt.Errorf("%w: the host is no host's name", ErrInvalid)

// ErrTaken is what a driver says of a credential kept under an id for
// another host than the one it is asked to keep one for.
var ErrTaken = errors.New("driver: the credential is kept for another host")

// ErrInUse is what a driver says of a credential a sandbox is still let
// through with: it is let go of once those sandboxes have ended.
var ErrInUse = errors.New("driver: a sandbox is still let through with the credential")

// Credentialed is a driver that keeps a credential outside its
// sandboxes, each under an id its caller chooses and for one host: a
// sandbox whose rule for that host names the id is let through with it,
// and never holds it.
type Credentialed interface {
	// Credential keeps a credential for a host under an id, in place of
	// the one it held under the id for the same host; one held under the
	// id for another host is ErrTaken, and a host the driver cannot send
	// it to is ErrHostInvalid. What a driver holds is the host and the
	// port the credential goes to, and the requests it goes with; that a
	// request to a host kept for https is encrypted is held only where the
	// driver says so. The OpenShell driver does not: see its package.
	Credential(ctx context.Context, id string, host Endpoint, username, secret string) error
	// Forget lets go of the credential under an id; one it holds none
	// under is no error, and one a sandbox is still let through with is
	// ErrInUse.
	Forget(ctx context.Context, id string) error
	// Kept says the host the credential under an id is for, who it is
	// of, and the variable a sandbox let through with it finds its
	// stand-in under; ErrNotFound where none is kept under the id.
	Kept(ctx context.Context, id string) (host Endpoint, username, variable string, err error)
}

// Endpoint is where a sandbox is let reach, or a credential is sent: a
// host's name, never an address, and a port, reached over https, or over
// http, unencrypted, where Clear says so.
type Endpoint struct {
	// Name is the host's name, in small letters.
	Name string
	// Port is its port.
	Port int
	// Clear is whether it is reached over http rather than https.
	Clear bool
}

// ParseEndpoint reads an endpoint as the sandbox API writes one: a
// name, or a name and a port, over https, with "https://" before it or
// not; or the same after "http://", over http. A port left out is the
// scheme's own. Anything else, as an address, a user, a path or a port
// that is none, is ErrInvalid, and the error does not repeat what was
// written.
func ParseEndpoint(written string) (Endpoint, error) {
	at, ok := plain.Ruled(written)
	if !ok {
		return Endpoint{}, fmt.Errorf("%w: a host is a name and a port, written plainly, with no user, path or address", ErrInvalid)
	}
	return Endpoint{Name: at.Name, Port: at.Port, Clear: at.Scheme == "http"}, nil
}

// String is the endpoint as ParseEndpoint reads it, port and all:
// name:port, or http://name:port where it is reached over http.
func (h Endpoint) String() string {
	at := net.JoinHostPort(h.Name, strconv.Itoa(h.Port))
	if h.Clear {
		return "http://" + at
	}
	return at
}

// credentialID is a credential's id: lower-case letters, digits and
// dashes, at most 63, beginning with a letter or a digit.
var credentialID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// CredentialID is whether an id is one a credential may be kept under.
func CredentialID(id string) bool { return credentialID.MatchString(id) }

// Sandbox is a sandbox's record and, as a driver returns it, its state.
type Sandbox struct {
	// ID is the sandbox's id.
	ID string
	// Image is the image it runs.
	Image string
	// Arguments replace the image's command.
	Arguments []string
	// Wrapper is a command its first process is run through: what runs is
	// the wrapper, then the image's entrypoint and the arguments (or the
	// image's own command). Empty for none.
	Wrapper []string
	// User is the user its first process runs as, by number, never root's;
	// empty for the sandbox's own.
	User string
	// Writable are the directories of its image it may write, each an
	// absolute path, beside its temporary directory and its volumes.
	Writable []string
	// Environment is the environment its first process starts with.
	Environment map[string]string
	// Labels are for finding it.
	Labels map[string]string
	// Egress is what it may reach.
	Egress Egress
	// Ports are the ports of it to make reachable.
	Ports []int
	// Volumes are the volumes mounted in it.
	Volumes []Volume
	// Containers are its further containers, on its network.
	Containers []SandboxContainer
	// BeforeContainers runs in the main container before further containers start.
	BeforeContainers *StartupGate
	// Paused is whether it is to be paused rather than running.
	Paused bool

	// State is where it is, as the driver observed it.
	State State
	// Message says, for one that failed, why.
	Message string
}

// State is where a sandbox is.
type State int

// The states of a sandbox.
const (
	Starting State = iota
	Running
	Paused
	Failed
	// Ending is one its provider is ending and still holds.
	Ending
)

// Egress is what a sandbox may reach.
type Egress struct {
	// Block allows nothing.
	Block bool
	// Listed allows only what Rules name, which may be nothing. It is said
	// apart from the rules, so that none is not taken for no limit.
	Listed bool
	// Rules are the hosts it may reach where Listed is set, each as far as
	// its rule lets: a host is named by one rule.
	Rules []Rule
}

// Rule is one host a sandbox may reach, and how far.
type Rule struct {
	// Host is the host, by its endpoint.
	Host Endpoint
	// Programs are the programs the rule is for, each by its whole path;
	// none for any program.
	Programs []string
	// Requests are the requests let through; none for any.
	Requests []Request
	// CredentialID names the credential kept for the host attached to
	// what the rule lets through; empty for none.
	CredentialID string
}

// Request is one request a rule lets through.
type Request struct {
	// Method is the HTTP method.
	Method string
	// Path is the path asked for, in full; a part of it after its first
	// that is "*" is any one part.
	Path string
	// Query holds, for a parameter that must be there, its one value.
	Query map[string]string
}

// Volume is a volume mounted in a sandbox.
type Volume struct {
	// Name names it within the sandbox.
	Name string
	// Path is where it is mounted.
	Path string
	// Kind is what becomes of it.
	Kind VolumeKind
	// Source names a host volume: a path on the provider's host.
	Source string
	// ReadOnly is whether the sandbox may only read it.
	ReadOnly bool
	// Subpath is the one directory of a host volume that is
	// mounted, relative to its root and never leaving it; empty for all
	// of it.
	Subpath string
}

// SandboxContainer is one of a sandbox's further containers: an image of its
// own, on the sandbox's network, started after the sandbox's first and
// ended with it.
type SandboxContainer struct {
	// Name names it within the sandbox.
	Name string
	// Image is the image it runs.
	Image string
	// Arguments replace the image's command.
	Arguments []string
	// Environment is the environment its first process starts with.
	Environment map[string]string
	// Volumes are the volumes mounted in it.
	Volumes []Volume
}

// Execer is a driver that can run a command in a running sandbox's
// container. A driver that is not one refuses ExecSandbox.
type Execer interface {
	// Exec runs args in the container (empty for the sandbox's first),
	// and returns its exit status and up to limit bytes of what it
	// printed, saying whether more was cut.
	Exec(ctx context.Context, id, container string, args []string, limit int) (exit int, output []byte, truncated bool, err error)
}

// Checker is a driver that says, before anything of a sandbox is made,
// whether what it is asked for is one it takes: the checks Apply makes
// before it makes anything, whose refusals are ErrInvalid where what was
// asked is not well formed. A sandbox it refuses is refused to whoever
// asked for it, and never made; one it takes may still fail to be made,
// as where its image cannot be pulled. Apply checks the same again.
type Checker interface {
	Check(ctx context.Context, record Sandbox) error
}

// VolumeKind is what becomes of a volume.
type VolumeKind int

// The kinds of volume a driver may offer.
const (
	// Ephemeral goes with the sandbox.
	Ephemeral VolumeKind = iota + 1
	// Host is a directory of the provider's host.
	Host
)

// Capabilities is what a driver can enforce.
type Capabilities struct {
	// StartupGate gates further containers on a bounded main-container command.
	StartupGate bool
	// PauseKeepsDisk is whether it can pause, keeping the disk.
	PauseKeepsDisk bool
	// Credentials is whether it keeps a credential outside its sandboxes
	// for what a sandbox fetches: whether it is Credentialed.
	Credentials bool
	// EgressOpen is whether a sandbox of its may reach anything: one that
	// names neither a block nor the hosts it may reach.
	EgressOpen bool
	// EgressBlock is whether it can allow nothing.
	EgressBlock bool
	// EgressAllowList is whether it can allow only some hosts.
	EgressAllowList bool
	// EgressRules is whether it can hold a host to the programs and the
	// requests a rule names.
	EgressRules bool
	// Volumes are the kinds of volume it can mount.
	Volumes []VolumeKind
	// Containers is whether a sandbox can hold further containers.
	Containers bool
	// VolumeSubpaths is whether it can mount one directory of a volume.
	VolumeSubpaths bool
}
