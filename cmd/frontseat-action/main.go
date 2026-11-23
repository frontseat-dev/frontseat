// Command frontseat-action is what a worker runs for every action, before
// its command: it makes the action's home in its root and runs the command
// with its environment, in the runner sandbox that confines it. All it does is common/confine's (Run),
// whose form names it. It is built statically, so that nothing a dynamic
// loader reads of its environment runs in it, and installed beside the
// runner, at /bb/frontseat-action-<form>.
package main

import "github.com/frontseat-dev/frontseat/common/confine"

func main() { confine.Run() }
