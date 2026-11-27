// Package ports are the ports frontseat's programs are reached on, each
// said once, here: a box's (its supervisor's source API and its shell)
// and the local stack's (one container of stack/image). A box's
// supervisor listens on a box's by default and the control plane opens
// and dials them; the command line asks the stack's control plane, or,
// inside a box (Lock), the build daemon it starts there for itself, on
// a port of the loopback that daemon chooses and says (Builds); the
// stack's image's files and the box's Dockerfile, which cannot read Go,
// are held to these by its test (local, ports_test). A port that only
// the processes of the stack's container dial among themselves, on its
// loopback (the grid's storage and scheduler, the gateway's health), is
// no port frontseat's programs are reached on: its image's files say
// it, and it is not here.
package ports

// Lock is the one lock of a box's sandbox, at one path whatever its
// supervisor is told: the supervisor holds it for as long as it runs and
// writes the name of its workspace in it. The command line finds it
// there, and knows by it that it runs inside a workspace's sandbox,
// where what it builds is told and kept at Settings and Builds.
const Lock = "/var/lib/frontseat/box.lock"

// Settings is where, in a box's sandbox, a build daemon is told its
// settings (build.yaml): its supervisor writes them there from what the
// control plane told it, and a command that builds there reads them
// there.
const Settings = "/var/lib/frontseat/build.yaml"

// Builds is where, in a box's sandbox, the build daemons the command
// line starts there keep their state, each in a directory named for the
// directory it builds: what it says of where it serves (daemon.json)
// among it. It is the sandbox's own, and its user's to write.
const Builds = "/var/lib/frontseat/build"

// A box's ports: what a workspace's sandbox is reached on.
const (
	// Source is where a box's supervisor serves its source API, which is
	// internal: the control plane dials it, and leads no caller there.
	// Inside the box it is no boundary.
	Source = 7601
	// Shell is where a box's supervisor serves SSH, to the key it is
	// given.
	Shell = 2222
)

// The local stack's ports, each on this machine's loopback.
const (
	// ControlPlane is where the stack's control plane (frontseat-server)
	// serves the workspace API.
	ControlPlane = 7700
	// Grid is where the stack's grid serves the Remote Execution API.
	Grid = 8980
	// Assets is where the stack's asset service serves the Remote Asset
	// API, on the host the grid is on.
	Assets = 8983
	// OpenShell is where the stack's OpenShell gateway answers.
	OpenShell = 8090
	// Portal is where the stack's portal (portal/, a Backstage
	// repository) serves its app and its backend: the port Backstage's
	// backend serves on where it is told none.
	Portal = 7007
	// Scheduler is where, on the loopback of the stack's container and
	// not this machine's, the grid's scheduler says its state: which
	// workers it has.
	Scheduler = 8984
	// Resolver is where, on the loopback of the stack's container and
	// not this machine's, the container's own resolver is asked: the
	// port every resolver is asked at, which resolv.conf cannot name.
	Resolver = 53
)

// ResolverAddress is the address the container's own resolver is asked
// at, on the container's loopback (Resolver).
const ResolverAddress = "127.0.0.1"
