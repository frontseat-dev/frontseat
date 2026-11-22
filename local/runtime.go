package local

import (
	"context"
	"os/exec"
	"strings"
)

// ArchitectureAsked are the arguments of the container runtime's command
// (docker) that ask it its architecture: the one asking of it, which
// says too whether the runtime answers.
func ArchitectureAsked() []string { return []string{"info", "--format", "{{.Architecture}}"} }

// Architecture asks the container runtime its architecture, as it says
// it: aarch64, x86_64.
func Architecture(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", ArchitectureAsked()...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GOARCH is Go's name of an architecture the container runtime says
// (Architecture), and whether it is one it is known to say: arm64 for
// aarch64 or arm64, amd64 for x86_64 or amd64, and amd64, not known, for
// any other.
func GOARCH(said string) (goarch string, known bool) {
	switch said {
	case "aarch64", "arm64":
		return "arm64", true
	case "x86_64", "amd64":
		return "amd64", true
	}
	return "amd64", false
}

// Podman is a command of the Podman of a container of the stack's image
// (Container, or a test's stack beside it), run as frontseat, whose
// Podman it is, with what it is given on its standard input passed on:
// arguments are Podman's.
func Podman(ctx context.Context, container string, arguments ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", "-u", "frontseat", "-e", "XDG_RUNTIME_DIR=/run/user/1000", "-e", "HOME=/home/frontseat", container, "podman"}, arguments...)...)
}
