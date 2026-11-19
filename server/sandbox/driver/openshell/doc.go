// Package openshell is the driver for NVIDIA OpenShell: a sandbox is one
// of its sandboxes, made by its gateway on whatever its compute driver
// stands on (a container runtime, Kubernetes through agent-sandbox, a
// microVM), beside a supervisor that holds it to a policy.
//
// The driver is a mapping onto the gateway's API, through OpenShell's own
// Go client. What a sandbox may reach is written as OpenShell's network
// policy, a rule for each host; nothing is reached that is not named, so
// a sandbox that is to reach anything is refused. What runs in one is
// not root, is given no volume and no container beside it, and a paused
// one keeps its disk and not its memory.
package openshell
