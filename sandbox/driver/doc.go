// Package driver is the port between the sandbox API and whatever makes
// sandboxes. A driver supplies a sandbox's lifecycle and a way to reach a
// port inside it, and says what it can enforce. Everything inside a
// sandbox is the program's that runs there, the same on every provider.
//
//	openshell     NVIDIA OpenShell's gateway, on whatever it stands on;
//	              what makes every sandbox a program makes
//	drivertest    what any driver is tested with: the conformance every
//	              driver is held to, a sandbox of the test's own, made and
//	              ended, and a driver whose sandboxes are held in memory
package driver
