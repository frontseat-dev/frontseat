package openshell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	client "github.com/ai-openshell/sdk-go/openshell/v1"

	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
)

// identity is the label a sandbox carries its id under: a sandbox's
// name at the gateway is spelled as the gateway allows, and the id is
// what it is found by.
const identity = "frontseat.dev/sandbox"

// Config is what the driver is given.
type Config struct {
	// Address is the gateway's, as host:port or a URL.
	Address string
	// Identity is the directory that holds what the gateway is known by
	// and what it knows this driver by: ca.crt, the gateway's authority,
	// and tls.crt and tls.key, a certificate of that authority and its
	// key. A gateway answers whoever proves such a certificate and no one
	// else, since whoever it answers may have any credential it keeps
	// sent where they choose; the key is worth as much.
	Identity string
	// Workspace is the gateway's workspace the sandboxes are made in;
	// empty for its default.
	Workspace string
	// Image says what an image is started with, as its own configuration
	// has it: the gateway starts what it is told and no more, and sets no
	// variable an image sets. Nil, a sandbox is started with its record's
	// arguments and environment alone.
	Image func(ctx context.Context, image string) (Started, error)
	// Writable are the directories what runs in a sandbox may write,
	// beside its temporary directory: a box's workspace and what it
	// keeps.
	Writable []string
	// Compute is the compute driver the gateway makes sandboxes with, as
	// its configuration names it (podman, docker): where it is one that
	// binds a directory of its host, a sandbox may be given one (a host
	// volume), which the gateway must be let bind (enable_bind_mounts).
	// Empty, or any other, no host volume is taken.
	Compute string
	// ReadAll is whether a sandbox may read all its image holds, not only
	// the system's directories: a service's image keeps what it runs where
	// it likes (/docker-entrypoint-initdb.d, /usr/local, /var).
	ReadAll bool
	// Private is whether a sandbox may write nothing but what is bound
	// into it: no working directory and no temporary directory. A sandbox
	// that runs one action after another, as a runner sandbox of the grid
	// does, then keeps nothing of one for the next.
	Private bool
}

// Started is what an image is started with.
type Started struct {
	// Entrypoint is what runs, and Command what it is given where a
	// sandbox names no arguments of its own.
	Entrypoint, Command []string
	// Environment is what the image sets.
	Environment map[string]string
}

// Driver makes sandboxes with an OpenShell gateway.
type Driver struct {
	config  Config
	gateway *client.Client
}

// New returns a driver that asks the gateway at an address.
func New(config Config) (*Driver, error) {
	asked := client.Config{Address: config.Address}
	if config.Identity != "" {
		asked.TLS = &client.TLSConfig{
			CAFile:   filepath.Join(config.Identity, "ca.crt"),
			CertFile: filepath.Join(config.Identity, "tls.crt"),
			KeyFile:  filepath.Join(config.Identity, "tls.key"),
		}
	}
	gateway, err := client.NewClient(asked)
	if err != nil {
		return nil, fmt.Errorf("openshell: the gateway at %s: %w", config.Address, err)
	}
	if config.Workspace == "" {
		config.Workspace = "default"
	}
	return &Driver{config: config, gateway: gateway}, nil
}

// unmade is an id no sandbox carries: asking after it is answered with
// nothing, by a gateway that knows who asks.
const unmade = "none-is-so-named"

// Answers asks the gateway something only a caller it knows is answered:
// which sandboxes it holds. It is how a server finds, before it serves
// anyone, that no gateway is at the address, or that the gateway there
// does not know its certificate.
func (d *Driver) Answers(ctx context.Context) error {
	if _, err := d.gateway.Sandboxes().ListAll(ctx, d.config.Workspace, client.ListOptions{LabelSelector: identity + "=" + unmade}); err != nil {
		return fmt.Errorf("openshell: the gateway at %s does not answer this server: %w", d.config.Address, err)
	}
	return nil
}

// Close lets go of the gateway.
func (d *Driver) Close() error { return d.gateway.Close() }

func (d *Driver) Name() string { return "openshell" }

func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{
		PauseKeepsDisk:  true,
		EgressBlock:     true,
		EgressAllowList: true,
		EgressRules:     true,
		// A credential is kept only by a gateway that answers no one but
		// whoever proves its certificate: one that answers anyone would
		// hand what it keeps to anyone.
		Credentials: d.config.Identity != "",
		Volumes:     volumesOf(d.config.Compute),
		// A subpath is bound as the directory it names, checked here.
		VolumeSubpaths: len(volumesOf(d.config.Compute)) > 0,
		// What a sandbox prints goes to its container, which the compute
		// driver under the gateway holds: the gateway hands back its
		// supervisor's account of it and none of its own output.
	}
}

// startedWith is the command and the environment a sandbox is started
// with: the wrapper, then the image's entrypoint and the arguments (or the
// image's own command), and the image's environment with the record's
// over it. Where what the image is started with is not known, the record's
// arguments and environment alone, after the wrapper.
func startedWith(record driver.Sandbox, started *Started) ([]string, map[string]string) {
	command, environment := record.Arguments, record.Environment
	if started != nil {
		given := record.Arguments
		if len(given) == 0 {
			given = started.Command
		}
		command = append(slices.Clone(started.Entrypoint), given...)
		environment = maps.Clone(started.Environment)
		if environment == nil {
			environment = map[string]string{}
		}
		maps.Copy(environment, record.Environment)
	}
	return append(slices.Clone(record.Wrapper), command...), environment
}

// longest is how long a name the gateway takes.
const longest = 19

// named is a sandbox's name at the gateway: its id, spelled as a DNS
// label is, where that is one the gateway takes; otherwise a name made
// of the id's digest, the same for the same id. The id itself is kept in
// a label, which is what a sandbox is found by.
func named(id string) string {
	spelled := strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(id))
	if label(spelled) {
		return spelled
	}
	sum := sha256.Sum256([]byte(id))
	return "fs-" + hex.EncodeToString(sum[:])[:longest-3]
}

// label is whether a name is a DNS label the gateway takes.
func label(name string) bool {
	if name == "" || len(name) > longest || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return name[0] >= 'a' && name[0] <= 'z'
}

func (d *Driver) Apply(ctx context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if err := driver.Admit(d, record); err != nil {
		return driver.Sandbox{}, err
	}
	name := named(record.ID)
	held, err := d.find(ctx, record.ID)
	switch {
	case errors.Is(err, driver.ErrNotFound):
		var started *Started
		if d.config.Image != nil {
			told, err := d.config.Image(ctx, record.Image)
			if err != nil {
				return driver.Sandbox{}, fmt.Errorf("openshell: what %s is started with: %w", record.Image, err)
			}
			started = &told
		}
		command, environment := startedWith(record, started)
		fetching, err := d.ruled(ctx, record)
		if err != nil {
			return driver.Sandbox{}, err
		}
		policy, err := d.policy(record, fetching)
		if err != nil {
			return driver.Sandbox{}, err
		}
		// The id is what a sandbox is found by: no label a caller gives
		// stands in its place.
		labels := map[string]string{}
		for k, v := range record.Labels {
			labels[k] = v
		}
		labels[identity] = record.ID
		template := &client.SandboxTemplate{Image: record.Image}
		bound, err := mounts(d.config.Compute, record.Volumes)
		if err != nil {
			return driver.Sandbox{}, err
		}
		if bound != nil {
			template.DriverConfig = bound
		}
		spec := &client.SandboxSpec{Environment: environment, Template: template, Policy: policy, Command: command, Providers: fetching.providers}
		if _, err := d.gateway.Sandboxes().Create(ctx, d.config.Workspace, name, spec, client.WithLabels(labels)); err != nil {
			if client.IsAlreadyExists(err) {
				return driver.Sandbox{}, fmt.Errorf("openshell: the gateway holds a sandbox named %s that is not %s", name, record.ID)
			}
			return driver.Sandbox{}, fmt.Errorf("openshell: making %s: %w", record.ID, err)
		}
	case err != nil:
		return driver.Sandbox{}, err
	case held.Status.Phase == client.SandboxDeleting:
		return driver.Sandbox{}, fmt.Errorf("openshell: %s: %w", record.ID, driver.ErrEnding)
	case record.Paused && !stopped(held.Status.Phase):
		// The gateway stops only what is ready.
		if _, err := d.gateway.Sandboxes().WaitReady(ctx, d.config.Workspace, name); err != nil {
			return driver.Sandbox{}, d.ending(ctx, record.ID, fmt.Errorf("openshell: %s was not ready to be paused: %w", record.ID, err))
		}
		if _, err := d.gateway.Sandboxes().Stop(ctx, d.config.Workspace, name); err != nil {
			return driver.Sandbox{}, d.ending(ctx, record.ID, fmt.Errorf("openshell: pausing %s: %w", record.ID, err))
		}
	case !record.Paused && stopped(held.Status.Phase):
		// And starts only what has stopped.
		if _, err := d.gateway.Sandboxes().WaitStopped(ctx, d.config.Workspace, name); err != nil {
			return driver.Sandbox{}, d.ending(ctx, record.ID, fmt.Errorf("openshell: %s had not stopped: %w", record.ID, err))
		}
		if _, err := d.gateway.Sandboxes().Start(ctx, d.config.Workspace, name); err != nil {
			return driver.Sandbox{}, d.ending(ctx, record.ID, fmt.Errorf("openshell: resuming %s: %w", record.ID, err))
		}
	}
	now, err := d.Observe(ctx, record.ID)
	if err == nil && now.State == driver.Ending {
		return driver.Sandbox{}, fmt.Errorf("openshell: %s: %w", record.ID, driver.ErrEnding)
	}
	return now, err
}

// ending is why something asked of a sandbox failed: driver.ErrEnding
// where the gateway came to delete it meanwhile, and the failure itself
// otherwise.
func (d *Driver) ending(ctx context.Context, id string, failed error) error {
	if held, err := d.find(ctx, id); err == nil && held.Status.Phase == client.SandboxDeleting {
		return fmt.Errorf("openshell: %s: %w", id, driver.ErrEnding)
	}
	return failed
}

// stopped is whether a phase is one of a paused sandbox.
func stopped(phase client.SandboxPhase) bool {
	return phase == client.SandboxStopped || phase == client.SandboxStopping
}

// find returns the sandbox of an id as the gateway holds it, and
// driver.ErrNotFound where it holds none of that id: one of the same
// name that another made, or that is of another id, is not it, and is
// neither read nor reached nor ended by its name. One the gateway is
// deleting is still held, and is returned: it is gone when the gateway
// holds it no more.
func (d *Driver) find(ctx context.Context, id string) (*client.Sandbox, error) {
	held, err := d.gateway.Sandboxes().Get(ctx, d.config.Workspace, named(id))
	if client.IsNotFound(err) {
		return nil, driver.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("openshell: asking after %s: %w", id, err)
	}
	if held.Labels[identity] != id {
		return nil, driver.ErrNotFound
	}
	return held, nil
}

// live returns the sandbox of an id that something may be asked of:
// driver.ErrEnding for one the gateway is deleting.
func (d *Driver) live(ctx context.Context, id string) (*client.Sandbox, error) {
	held, err := d.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if held.Status.Phase == client.SandboxDeleting {
		return nil, fmt.Errorf("openshell: %s: %w", id, driver.ErrEnding)
	}
	return held, nil
}

// policy is what a sandbox is held to: what it may write, and what its
// rules let it reach, each host by its name.
func (d *Driver) policy(record driver.Sandbox, ruled fetched) (*client.SandboxPolicy, error) {
	policy := &client.SandboxPolicy{
		Version: 1,
		Filesystem: &client.FilesystemPolicy{
			IncludeWorkdir: true,
			ReadOnly:       []string{"/usr", "/lib", "/bin", "/sbin", "/etc", "/opt", "/proc", "/dev/urandom"},
			// /dev/pts is where a terminal is made: a shell a person types
			// into has one, and what is made there is the sandbox's own.
			ReadWrite: append([]string{"/tmp", "/dev/null", "/dev/pts"}, d.config.Writable...),
		},
		// What it may write is held, or it does not run: a runtime that
		// cannot hold it is refused, not run without.
		Landlock:        &client.LandlockPolicy{Compatibility: "hard_requirement"},
		NetworkPolicies: map[string]client.NetworkPolicyRule{},
	}
	if d.config.ReadAll {
		policy.Filesystem.ReadOnly = []string{"/"}
	}
	// It runs as the user it names, never as root, and writes the
	// directories of its image it names beside the rest.
	if record.User != "" {
		policy.Process = &client.ProcessPolicy{RunAsUser: record.User, RunAsGroup: record.User}
	}
	policy.Filesystem.ReadWrite = append(policy.Filesystem.ReadWrite, record.Writable...)
	if d.config.Private {
		// What writes nothing but what is bound in writes no directory of
		// its image: one asked for is refused, never dropped.
		if len(record.Writable) > 0 {
			return nil, errors.New("openshell: a sandbox writes a directory of its image, and this gateway's sandboxes write what is bound in alone")
		}
		// The devices a program writes to without leaving anything, and
		// nothing else: what is bound in follows.
		policy.Filesystem.IncludeWorkdir = false
		policy.Filesystem.ReadWrite = []string{"/dev/null", "/dev/zero", "/dev/full"}
		// The authority OpenShell's proxy inspects what it lets through
		// with, which what reaches the network is to trust.
		policy.Filesystem.ReadOnly = append(policy.Filesystem.ReadOnly, "/run/openshell-supervisor-ca")
	}
	// What is bound in is read, or written, as the volume says, and
	// nothing more of the host's.
	for _, v := range record.Volumes {
		if v.ReadOnly {
			policy.Filesystem.ReadOnly = append(policy.Filesystem.ReadOnly, v.Path)
		} else {
			policy.Filesystem.ReadWrite = append(policy.Filesystem.ReadWrite, v.Path)
		}
	}
	if record.Egress.Block {
		return policy, nil
	}
	for name, rule := range ruled.rules {
		policy.NetworkPolicies[name] = rule
	}
	return policy, nil
}

// anyProgram is every program of a sandbox.
var anyProgram = []client.PolicyNetworkBinary{{Path: "/**"}}

// programs are the programs a rule is for: those named, or any where
// none is.
func programs(named []string) []client.PolicyNetworkBinary {
	if len(named) == 0 {
		return anyProgram
	}
	out := make([]client.PolicyNetworkBinary, len(named))
	for i, path := range named {
		out[i] = client.PolicyNetworkBinary{Path: path}
	}
	return out
}

func (d *Driver) Observe(ctx context.Context, id string) (driver.Sandbox, error) {
	held, err := d.find(ctx, id)
	if err != nil {
		return driver.Sandbox{}, err
	}
	return observed(held), nil
}

// observed is a sandbox this driver made, as the gateway holds it, in
// the port's words.
func observed(held *client.Sandbox) driver.Sandbox {
	out := driver.Sandbox{ID: held.Labels[identity], Labels: map[string]string{}}
	for k, v := range held.Labels {
		if k != identity {
			out.Labels[k] = v
		}
	}
	if held.Spec.Template != nil {
		out.Image = held.Spec.Template.Image
	}
	switch held.Status.Phase {
	case client.SandboxDeleting:
		out.State = driver.Ending
	case client.SandboxReady:
		out.State = driver.Running
	case client.SandboxStopped, client.SandboxStopping, client.SandboxSuspended, client.SandboxSuspending:
		out.State, out.Paused = driver.Paused, true
	case client.SandboxError, client.SandboxCompleted:
		out.State = driver.Failed
		out.Message = "the sandbox is " + strings.ToLower(string(held.Status.Phase))
		// Why, as the gateway says it.
		for _, condition := range held.Status.Conditions {
			if condition.Message != "" {
				out.Message += ": " + condition.Message
			}
		}
	default:
		out.State = driver.Starting
	}
	return out
}

func (d *Driver) Delete(ctx context.Context, id string) error {
	name := named(id)
	// Only what this driver made, of this id, is its to end.
	held, err := d.gateway.Sandboxes().Get(ctx, d.config.Workspace, name)
	if client.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("openshell: asking after %s: %w", id, err)
	}
	if held.Labels[identity] != id {
		return nil
	}
	if _, err := d.gateway.Sandboxes().Delete(ctx, d.config.Workspace, name); err != nil && !client.IsNotFound(err) {
		return fmt.Errorf("openshell: ending %s: %w", id, err)
	}
	// It is gone when the gateway no longer holds it.
	deadline := time.Now().Add(time.Minute)
	return poll.Until(ctx, 100*time.Millisecond, func() (bool, error) {
		if _, err := d.gateway.Sandboxes().Get(ctx, d.config.Workspace, name); client.IsNotFound(err) {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("openshell: %s was not gone a minute after it was ended", id)
		}
		return false, nil
	})
}

func (d *Driver) Dial(ctx context.Context, id string, port int) (net.Conn, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("openshell: %d is no port", port)
	}
	if _, err := d.live(ctx, id); err != nil {
		return nil, err
	}
	// The stream lives as long as the connection, not as long as the
	// dialling: it is ended by its own closing, which first lets go of
	// whatever of it is still being written.
	lasting, end := context.WithCancel(context.WithoutCancel(ctx))
	stream, err := d.gateway.TCP().Forward(lasting, d.config.Workspace, named(id), uint32(port))
	if err != nil {
		end()
		if client.IsNotFound(err) {
			return nil, driver.ErrNotFound
		}
		return nil, fmt.Errorf("openshell: reaching port %d of %s: %w", port, id, err)
	}
	return &forwarded{ReadWriteCloser: stream, end: end, sandbox: id, port: port}, nil
}

// Exec runs a command in a sandbox and returns how it exited and what it
// printed, up to a limit: what is printed past it is read and let go, so
// that a command that prints without end fills nothing here. What it
// printed and what it complained of come as the gateway sends them, in
// that order. It is started as it is written, with no login shell.
func (d *Driver) Exec(ctx context.Context, id, container string, args []string, limit int) (int, []byte, bool, error) {
	if container != "" {
		return 0, nil, false, &driver.Unsupported{Driver: d.Name(), Capability: "further containers"}
	}
	if _, err := d.live(ctx, id); err != nil {
		return 0, nil, false, err
	}
	stream, err := d.gateway.Exec().Stream(ctx, d.config.Workspace, named(id), args, client.ExecOptions{NoLoginShell: true})
	if err != nil {
		return 0, nil, false, fmt.Errorf("openshell: running a command in %s: %w", id, err)
	}
	defer stream.Close()
	var printed []byte
	truncated := false
	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, nil, false, fmt.Errorf("openshell: running a command in %s: %w", id, err)
		}
		if chunk == nil {
			continue
		}
		if limit > 0 && len(printed)+len(chunk.Data) > limit {
			printed = append(printed, chunk.Data[:limit-len(printed)]...)
			truncated = true
			continue
		}
		if !truncated {
			printed = append(printed, chunk.Data...)
		}
	}
	exit, err := stream.ExitCode()
	if err != nil {
		return 0, nil, false, fmt.Errorf("openshell: how a command in %s exited: %w", id, err)
	}
	return exit, printed, truncated, nil
}

// forwarded is a port of a sandbox, reached through the gateway. It is
// a stream and no socket: it has no address but what it is of, and no
// deadline, which it says to whoever sets one.
type forwarded struct {
	io.ReadWriteCloser
	end     context.CancelFunc
	sandbox string
	port    int
}

// Close ends the stream: first whatever is still being written to it,
// which would otherwise hold its closing, then the stream.
func (f *forwarded) Close() error {
	f.end()
	return f.ReadWriteCloser.Close()
}

type address string

func (a address) Network() string { return "openshell" }
func (a address) String() string  { return string(a) }

func (f *forwarded) LocalAddr() net.Addr              { return address("gateway") }
func (f *forwarded) RemoteAddr() net.Addr             { return address(f.sandbox + ":" + strconv.Itoa(f.port)) }
func (f *forwarded) SetDeadline(time.Time) error      { return os.ErrNoDeadline }
func (f *forwarded) SetReadDeadline(time.Time) error  { return os.ErrNoDeadline }
func (f *forwarded) SetWriteDeadline(time.Time) error { return os.ErrNoDeadline }
