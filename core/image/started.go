package image

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// within is how long the container runtime is waited on for one answer.
const within = 3 * time.Minute

// Started returns what an image the container runtime holds is started
// with, as its configuration has it: its entrypoint, its command and the
// environment it sets. The runtime is the program of its command line,
// docker or podman, which answer alike; an image it does not hold yet is
// pulled, once, as it would be to run it. The image is one already
// checked to be named as an image is (Read), and is passed after "--".
func Started(ctx context.Context, runtime, image string) (entrypoint, command []string, environment map[string]string, err error) {
	inspect := []string{"image", "inspect", "--format", "{{json .Config}}", "--", image}
	out, err := asked(ctx, runtime, inspect...)
	if err != nil {
		if _, pulled := asked(ctx, runtime, "pull", "--quiet", "--", image); pulled != nil {
			return nil, nil, nil, err
		}
		if out, err = asked(ctx, runtime, inspect...); err != nil {
			return nil, nil, nil, err
		}
	}
	var config struct {
		Entrypoint, Cmd, Env []string
	}
	if err := json.Unmarshal(out, &config); err != nil {
		return nil, nil, nil, fmt.Errorf("image: what the runtime says of %s: %w", image, err)
	}
	environment = map[string]string{}
	for _, each := range config.Env {
		name, value, _ := strings.Cut(each, "=")
		environment[name] = value
	}
	return config.Entrypoint, config.Cmd, environment, nil
}

// asked runs the runtime's command line and returns what it printed.
func asked(ctx context.Context, runtime string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, runtime, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("image: %s %s: %w: %s", runtime, args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
