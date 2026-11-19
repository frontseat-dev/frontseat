package stack

import (
	_ "embed"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/frontseat-dev/frontseat/local/ports"
)

// seccomp is the seccomp profile the stack's container runs under:
// Podman's own (containers-common 0.70.0, Apache-2.0), which lets
// rootless Podman nest containers inside it, where the runtime's default
// refuses pivot_root and keyctl.
//
//go:embed seccomp.json
var seccomp []byte

// Container is the stack's one container on a runtime of Docker's
// command line: its name, its image, and the volume it keeps everything
// in.
type Container struct {
	Name, Image, Volume string
	// Publish is whether its ports are published on this machine's
	// loopback; a second stack beside the first, as a test starts, has
	// none.
	Publish bool
	// Labels are what it is found by, beside its name: a test's run.
	Labels map[string]string
}

// published are the ports of the container published on this machine's
// loopback, each where local/ports says.
var published = []int{ports.ControlPlane, ports.Grid, ports.Assets, ports.OpenShell}

// Arguments are what `docker run` is told to start the container, the
// seccomp profile written at profile first (Profile). It is given what
// rootless Podman, FUSE and slirp4netns need inside it and nothing more:
// one capability, two devices, Podman's seccomp profile, an unmasked
// /proc, which nested containers mount anew; nothing of this machine is
// mounted, and every port is on its loopback alone.
func (c Container) Arguments(profile string) []string {
	arguments := []string{"run", "--detach", "--name", c.Name,
		"--cap-add", "SYS_ADMIN",
		"--device", "/dev/fuse",
		"--device", "/dev/net/tun",
		"--security-opt", "seccomp=" + profile,
		"--security-opt", "apparmor=unconfined",
		"--security-opt", "systempaths=unconfined",
		"--cgroupns", "private",
		"--volume", c.Volume + ":/var/lib/frontseat",
		// This machine, by the name Docker Desktop and OrbStack give it and
		// Linux's runtimes give only when told: where init reads the
		// address it tells the gateway a sandbox's names of this machine
		// lead to (machineAddress), where the person's own services are
		// and the grid's ports are published.
		"--add-host", "host.docker.internal:host-gateway",
	}
	for _, name := range slices.Sorted(maps.Keys(c.Labels)) {
		arguments = append(arguments, "--label", name+"="+c.Labels[name])
	}
	for _, port := range published {
		if !c.Publish {
			break
		}
		at := strconv.Itoa(port)
		arguments = append(arguments, "--publish", net.JoinHostPort("127.0.0.1", at)+":"+at)
	}
	return append(arguments, c.Image)
}

// Profile writes the container's seccomp profile into dir, where the
// runtime reads it when it starts the container, and returns its path.
func Profile(dir string) (string, error) {
	at := filepath.Join(dir, "seccomp.json")
	return at, os.WriteFile(at, seccomp, 0o644)
}
