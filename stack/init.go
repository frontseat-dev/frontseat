package stack

import (
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Init is what the stack's container starts with (frontseat-stack init),
// while it is the container's one process: it gives the user frontseat a
// cgroup subtree of its own, as systemd's Delegate= gives a user's
// session, lays out the stack's one state directory, and becomes the
// program it is told (s6-overlay's /init). Done later, beside processes
// already started, moving the root's processes out races with what
// starts meanwhile, and the controllers cannot be given.
type Init struct {
	// Cgroups is where cgroup v2 is mounted (/sys/fs/cgroup).
	Cgroups string
	// State is the stack's one state directory (/var/lib/frontseat): the
	// container's one volume.
	State string
	// Runtime is frontseat's runtime directory (/run/user/<uid>), which
	// its Podman keeps its socket in.
	Runtime string
	// Frontseat and BB are the users the stack's processes run as: the
	// one that owns Podman, the gateway and the control plane, and the
	// one Buildbarn's servers run as.
	Frontseat, BB Owner
	// Gateway is the OpenShell gateway's configuration: the one the
	// image holds, and the one the gateway reads, written from it.
	Gateway GatewayConfig
	// Resolver is how the container's names are resolved: by a resolver
	// of its own, which keeps what it is told and asks again.
	Resolver Resolver

	sys system
}

// GatewayConfig is the OpenShell gateway's configuration as init writes
// it: Template's, with this machine's address, as the container runtime
// names it in Hosts (host.docker.internal), for where a sandbox's names of
// this machine lead (host_gateway_ip). Where the runtime names none, the
// template's stands.
type GatewayConfig struct {
	Template, Config, Hosts string
}

// Owner is a user and its group, by number.
type Owner struct{ UID, GID int }

// system is what Init asks of the kernel. Linux's is the kernel's
// (init_linux.go); a test's records what it is asked.
type system interface {
	pid() int
	chown(path string, owner Owner) error
	remountWritable(path string) error
	shareSubtree(path string) error
	exec(program []string) error
}

// controllers are those the stack's processes are limited by, in the
// order cgroup.subtree_control is told them.
var controllers = []string{"cpu", "memory", "pids"}

// Run delegates the cgroups, lays out the state, and becomes program: it
// returns only where it could not.
func (i Init) Run(program []string) error {
	if len(program) == 0 {
		return errors.New("init is told the program it becomes")
	}
	if err := i.delegate(); err != nil {
		return fmt.Errorf("delegating cgroups: %w", err)
	}
	if err := i.lay(); err != nil {
		return fmt.Errorf("laying out %s: %w", i.State, err)
	}
	if err := i.Gateway.write(); err != nil {
		return fmt.Errorf("writing the gateway's configuration: %w", err)
	}
	if err := i.Resolver.write(); err != nil {
		return fmt.Errorf("giving the container its own resolver: %w", err)
	}
	return i.sys.exec(program)
}

// delegate moves this process, the container's only one, into a leaf of
// its own, so that the root may give its children controllers, and gives
// frontseat a subtree with every controller the kernel has of those the
// stack uses.
func (i Init) delegate() error {
	if err := i.sys.remountWritable(i.Cgroups); err != nil {
		return err
	}
	leaf := filepath.Join(i.Cgroups, "init")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(leaf, "cgroup.procs"), []byte(strconv.Itoa(i.sys.pid())), 0o644); err != nil {
		return err
	}
	read, err := os.ReadFile(filepath.Join(i.Cgroups, "cgroup.controllers"))
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for _, name := range strings.Fields(string(read)) {
		has[name] = true
	}
	var given []string
	for _, name := range controllers {
		if has[name] {
			given = append(given, "+"+name)
		}
	}
	enable := []byte(strings.Join(given, " "))
	if err := os.WriteFile(filepath.Join(i.Cgroups, "cgroup.subtree_control"), enable, 0o644); err != nil {
		return err
	}
	frontseat := filepath.Join(i.Cgroups, "frontseat")
	for _, dir := range []string{frontseat, filepath.Join(frontseat, "podman"), filepath.Join(frontseat, "runner")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(frontseat, "cgroup.subtree_control"), enable, 0o644); err != nil {
		return err
	}
	// frontseat is given what systemd's Delegate= gives a user: each of
	// its directories, and in each the files that place processes and make
	// children, and none of the files that limit it, which stay root's.
	for _, dir := range []string{frontseat, filepath.Join(frontseat, "podman"), filepath.Join(frontseat, "runner")} {
		for _, path := range []string{dir, filepath.Join(dir, "cgroup.procs"), filepath.Join(dir, "cgroup.threads"), filepath.Join(dir, "cgroup.subtree_control")} {
			if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err := i.sys.chown(path, i.Frontseat); err != nil {
				return err
			}
		}
	}
	return nil
}

// layout is each directory of the state, by its path beneath it, and who
// owns it. Buildbarn's servers own their storage; frontseat owns what
// its Podman, the gateway and the control plane keep; the worker, which
// runs as root, owns the rest.
func (i Init) layout() []struct {
	path  string
	owner *Owner
	mode  fs.FileMode
} {
	return []struct {
		path  string
		owner *Owner
		mode  fs.FileMode
	}{
		{"storage-cas/persistent_state", &i.BB, 0o755},
		{"storage-ac/persistent_state", &i.BB, 0o755},
		{"storage-fsac/persistent_state", &i.BB, 0o755},
		{"worker-kept/cas/persistent_state", nil, 0o700},
		// The worker's directory holds each slot's build directory and
		// runner socket: root's, so that nothing but root makes or names
		// what the worker mounts into.
		{"worker", nil, 0o755},
		{"bb", nil, 0o755},
		{"containers", &i.Frontseat, 0o700},
		{"openshell", &i.Frontseat, 0o700},
		{"controlplane", &i.Frontseat, 0o700},
	}
}

// lay makes the state's directories with their owners, frontseat's
// runtime directory, and the worker's directory a mount of its own whose
// mounts are shared, so that a build directory the worker mounts there is
// seen where it is bound into a sandbox.
func (i Init) lay() error {
	for _, each := range i.layout() {
		at := filepath.Join(i.State, each.path)
		if err := os.MkdirAll(at, 0o755); err != nil {
			return err
		}
		top := filepath.Join(i.State, strings.SplitN(each.path, "/", 2)[0])
		if err := os.Chmod(top, each.mode); err != nil {
			return err
		}
		if each.owner == nil {
			continue
		}
		// Each directory along the path is its owner's, and nothing
		// beneath it is touched: what is there is its owner's already,
		// as it left it (Podman's store holds files of its users'
		// subordinate ids, which would become frontseat's own).
		for at := top; ; {
			if err := i.sys.chown(at, *each.owner); err != nil {
				return err
			}
			if at == filepath.Join(i.State, each.path) {
				break
			}
			rel, err := filepath.Rel(at, filepath.Join(i.State, each.path))
			if err != nil {
				return err
			}
			at = filepath.Join(at, strings.SplitN(rel, string(filepath.Separator), 2)[0])
		}
	}
	// The directories above frontseat's runtime directory are open to
	// every user to pass through, as /run/user is: one made private
	// would keep frontseat out of its own.
	if err := os.MkdirAll(filepath.Dir(i.Runtime), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(i.Runtime, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(i.Runtime, 0o700); err != nil {
		return err
	}
	if err := i.sys.chown(i.Runtime, i.Frontseat); err != nil {
		return err
	}
	return i.sys.shareSubtree(filepath.Join(i.State, "worker"))
}

// hostGatewayLine is the template's line saying where a sandbox's names
// of this machine lead, which write replaces.
const hostGatewayLine = "host_gateway_ip"

// write writes the gateway's configuration: the template's, with this
// machine's address where the runtime names one. It is root's, and read
// by all.
func (g GatewayConfig) write() error {
	template, err := os.ReadFile(g.Template)
	if err != nil {
		return err
	}
	lines := strings.Split(string(template), "\n")
	if at := machineAddress(g.Hosts); at != "" {
		said := false
		for n, line := range lines {
			if name, _, found := strings.Cut(line, "="); found && strings.TrimSpace(name) == hostGatewayLine {
				lines[n] = hostGatewayLine + ` = "` + at + `"`
				said = true
			}
		}
		if !said {
			return fmt.Errorf("%s says no %s for init to say", g.Template, hostGatewayLine)
		}
	}
	if err := os.MkdirAll(filepath.Dir(g.Config), 0o755); err != nil {
		return err
	}
	return os.WriteFile(g.Config, []byte(strings.Join(lines, "\n")), 0o644)
}

// machineAddress is the address a hosts file gives host.docker.internal,
// the name container runtimes give this machine: its first of IPv4,
// which a sandbox's network routes, else its first; empty where it gives
// none, or one that is no address.
func machineAddress(hosts string) string {
	content, err := os.ReadFile(hosts)
	if err != nil {
		return ""
	}
	found := ""
	for _, line := range strings.Split(string(content), "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) < 2 || !slices.Contains(fields[1:], "host.docker.internal") {
			continue
		}
		address, err := netip.ParseAddr(fields[0])
		if err != nil || address.IsUnspecified() || address.IsLoopback() {
			continue
		}
		if address.Is4() {
			return address.String()
		}
		if found == "" {
			found = address.String()
		}
	}
	return found
}

// Resolver is the container's own resolver (Unbound): what the
// container's resolv.conf names, and so what is asked by the container's
// processes that read it and by the sandboxes' supervisors, whose
// resolv.conf Podman writes from the container's, each of which asks its
// one nameserver once, waits two seconds, and does not ask again. The
// runtime's resolver drops and refuses what many supervisors ask of it at
// once; this one keeps what it is told, asks again where an answer is
// lost, and serves an answer it has where the runtime's says nothing.
// Init points it at the nameservers the runtime gave the container
// (Forward) and the container at it (Config, the container's
// resolv.conf).
type Resolver struct {
	// Config is the container's resolv.conf, which the runtime writes.
	Config string
	// Forward is the resolver's configuration of where it asks: the
	// nameservers the runtime gave the container.
	Forward string
	// Listen is the address the resolver is asked at.
	Listen string
}

// write points the resolver at the runtime's nameservers and the
// container at the resolver. A container started again holds what init
// wrote before, the resolver alone: it is left as it is, and so is where
// the resolver asks.
func (r Resolver) write() error {
	if r.Config == "" {
		return nil
	}
	read, err := os.ReadFile(r.Config)
	if err != nil {
		return err
	}
	// What init wrote before, in a container started again, is known by
	// its first line: a nameserver the runtime gave, whatever its
	// address, is the runtime's.
	if strings.HasPrefix(string(read), resolverHeader) {
		if _, err := os.Stat(r.Forward); err == nil {
			return nil
		}
		return fmt.Errorf("%s is init's own, and %s, where the container's resolver asks, is gone", r.Config, r.Forward)
	}
	var upstream, kept []string
	for _, line := range strings.Split(string(read), "\n") {
		// What follows # or ; is a comment, as resolv.conf reads it.
		said, _, _ := strings.Cut(line, "#")
		said, _, _ = strings.Cut(said, ";")
		fields := strings.Fields(said)
		switch {
		case len(fields) >= 2 && fields[0] == "nameserver":
			upstream = append(upstream, fields[1])
		case len(fields) > 0 && (fields[0] == "search" || fields[0] == "options" || fields[0] == "domain"):
			kept = append(kept, strings.TrimSpace(said))
		}
	}
	listen, err := netip.ParseAddr(r.Listen)
	if err != nil {
		return fmt.Errorf("the container's resolver is asked at %q, which is no address", r.Listen)
	}
	var asked []netip.Addr
	for _, address := range upstream {
		parsed, err := netip.ParseAddr(address)
		if err != nil {
			return fmt.Errorf("%s names %q, which is no address", r.Config, address)
		}
		// One of a link's own, which the resolver would read otherwise
		// than the runtime meant (a zone, a port after it), is passed by,
		// as one the resolver cannot ask from the container.
		if parsed.Zone() != "" {
			continue
		}
		parsed = parsed.Unmap()
		// The resolver's own address, or every address, given by the
		// runtime, would have it ask itself.
		if parsed == listen || parsed.IsUnspecified() {
			return fmt.Errorf("%s names %s, where the container's own resolver is asked: it would ask itself", r.Config, address)
		}
		asked = append(asked, parsed)
	}
	if len(asked) == 0 {
		return fmt.Errorf("%s names no nameserver for the container's resolver to ask", r.Config)
	}
	forward := "# Where the container's resolver asks: the runtime's nameservers, as\n# frontseat-stack init found them in " + r.Config + ".\nforward-zone:\n\tname: \".\"\n"
	for _, address := range asked {
		forward += "\tforward-addr: " + address.String() + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(r.Forward), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(r.Forward, []byte(forward), 0o644); err != nil {
		return err
	}
	config := resolverHeader + "nameserver " + r.Listen + "\n"
	for _, line := range kept {
		config += line + "\n"
	}
	return os.WriteFile(r.Config, []byte(config), 0o644)
}

// resolverHeader begins the resolv.conf init writes: what it is known by
// in a container started again.
const resolverHeader = "# The container's own resolver (frontseat-stack init).\n"
