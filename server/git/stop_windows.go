package git

import osexec "os/exec"

// stopTogether leaves git to be stopped alone where it is cancelled:
// Windows has no process group to stop it with. What it started is let
// go of by the delay Run waits.
func stopTogether(*osexec.Cmd) {}
