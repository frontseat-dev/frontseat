// Package ports are the ports frontseat's programs are reached on, each
// said once, here: a box's (its supervisor's source API and its shell)
// and the local stack's (one container of stack/image). A box's
// supervisor listens on a box's by default and the control plane opens
// and dials them; the command line asks the stack's control plane, or,
// inside a box (Lock), the build daemon it starts there for itself, on
// a port of the loopback that daemon chooses and says (Builds); the
// stack's image's files and the box's Dockerfile, which cannot read Go,
// are held to these by its test (local, ports_test). Where a repository
// something else mounted in a box says what it is, and what whatever
// mounts one keeps to, is here too (Mounted). A port that only
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

// Mounted is where, in a box's sandbox, each repository of the
// workspace that something else mounted there, and that was not cloned,
// has its layer: a directory at Mounted/<name>, <name> being the
// workspace's name for the repository, which stands at <name> in the
// workspace's directory. Whatever mounts a repository for a workspace
// keeps to what follows, and a build reads the repository by it
// (source.Mount and source.HashAttribute, in
// build/engine/engine/source, say what a build does with it).
//
// What is where:
//
//   - The repository is a directory at its name in the workspace's
//     directory, there before the sandbox's supervisor is told of it.
//     A build lists and reads it as it does any directory.
//   - Its layer, Mounted/<name>, is on a file system mounted read-only
//     in the sandbox, there when the sandbox is made: nothing in the
//     sandbox writes it, and nothing in the sandbox can mount one.
//   - The layer holds one file a build reads, stamp: one moment, the
//     mount's stamp, written as RFC 3339 with nanoseconds, in UTC, on
//     one line. A layer may hold more; a build reads nothing else of
//     it.
//
// What a file of the repository says of itself:
//
//   - A regular file that is as the mount gave it has the stamp as its
//     change time, to the nanosecond, and says the SHA-256 of what it
//     holds in the extended attribute user.frontseat.sha256, as 64
//     hexadecimal digits in lower case.
//   - A file the workspace made, or changed in any way (what it holds,
//     its mode, its times, an attribute of it, its name), is not shown
//     with the stamp as its change time from then on, whenever it was
//     changed and whatever the clock said then.
//   - The mount alone says the attribute and a change time: nothing in
//     the sandbox sets either.
//   - For as long as the layer says one stamp, a file shown with that
//     stamp as its change time holds what it held, at the size and with
//     the mode it had, when the stamp was first said. A mount that
//     comes to give anything else, as one moved to another state of
//     the repository does, says another stamp, one it never said
//     before, and shows no file with the one before; and once it has
//     answered anything of a file as it gives it now (its change time,
//     its size, its mode or the attribute), it answers nothing of the
//     file as it gave it before, to whoever asks the mount itself and
//     not what the kernel kept of an earlier answer.
//
// A mount that cannot hold to one of these says no stamp, or shows a
// file without the attribute: a build then reads the file, as it reads
// any.
const Mounted = "/var/lib/frontseat/mounted"

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
