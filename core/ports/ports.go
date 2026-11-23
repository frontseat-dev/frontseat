// Package ports are the ports frontseat's programs are reached on, each
// said once, here: a box's (the daemon's build API, its source API and
// its shell) and the local stack's (one container of stack/image). The
// daemon listens on a box's by default and the control plane opens and
// dials them; the command line asks the stack's control plane, or,
// inside a box, the daemon there (Lock); the stack's image's files and
// the box's Dockerfile, which cannot read Go, are held to these by its
// test (local, ports_test). A port that only the processes of the
// stack's container dial among themselves, on its loopback (the grid's
// storage and scheduler, the gateway's health), is no port frontseat's
// programs are reached on: its image's files say it, and it is not here.
package ports

// Lock is the one lock of a box's sandbox, at one path whatever its
// daemon is told: the daemon holds it for as long as it runs and writes
// the name of its workspace in it. The command line finds it there, and
// knows by it that it runs inside a workspace's sandbox, whose daemon it
// asks on the loopback, at Daemon.
const Lock = "/var/lib/frontseat/daemon.lock"

// A box's ports: what a workspace's sandbox is reached on.
const (
	// Daemon is where a box's daemon serves the build API: what the
	// command line inside the box asks, and what the control plane leads
	// a workspace's build route to.
	Daemon = 7600
	// Source is where a box's daemon serves its source API, the daemon's
	// internal API: the control plane dials it, and leads no caller there.
	// Inside the box it is no boundary.
	Source = 7601
	// Shell is where a box's daemon serves SSH, to the key it is given.
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
	// Resolver is where, on the loopback of the stack's container and
	// not this machine's, the container's own resolver is asked: the
	// port every resolver is asked at, which resolv.conf cannot name.
	Resolver = 53
)

// ResolverAddress is the address the container's own resolver is asked
// at, on the container's loopback (Resolver).
const ResolverAddress = "127.0.0.1"
