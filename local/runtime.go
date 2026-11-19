package local

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Architecture returns the container runtime's architecture, as it says
// it (`docker info`), or, where it does not answer, why. It is the one
// way the box's build, the gate and the test support ask whether the
// runtime answers.
func Architecture(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.Architecture}}").Output()
	if err != nil {
		return "", fmt.Errorf("the container runtime does not answer: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Podman is the container runtime's arguments that run the Podman of a
// container of the stack's image (Container, or one a test started)
// with the arguments given, as frontseat, whose Podman it is, with what
// it reads its own state by: what hands the box to it and what asks it
// of a sandbox run it so.
func Podman(container string, arguments ...string) []string {
	return append([]string{"exec", "-i", "-u", "frontseat", "-e", "XDG_RUNTIME_DIR=/run/user/1000", "-e", "HOME=/home/frontseat", container, "podman"}, arguments...)
}
