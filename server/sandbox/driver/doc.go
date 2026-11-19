// Package driver is the port between the sandbox API and whatever makes
// sandboxes. A driver supplies a sandbox's lifecycle and a way to reach a
// port inside it, and says what it can enforce. Everything inside a
// sandbox is the daemon's, the same on every provider.
//
//	container     a container runtime; the grid's runner's alone
//	openshell     NVIDIA OpenShell's gateway, on whatever it stands on;
//	              what makes every workspace's sandbox
//	drivertest    what any driver is tested with: the conformance every
//	              driver is held to, a sandbox of the test's own, made and
//	              ended, and a driver whose sandboxes are held in memory
package driver
