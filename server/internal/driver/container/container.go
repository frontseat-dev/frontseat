package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
)

// The label every sandbox this driver makes carries.
const sandboxLabel = "dev.frontseat.sandbox"

// Driver makes sandboxes as containers, through the docker command.
type Driver struct{}

var _ driver.Driver = Driver{}

// Name implements driver.Driver.
func (Driver) Name() string { return "container" }

// The label a sandbox's further containers carry: the sandbox's id. They
// do not carry sandboxLabel, so that none is taken for a sandbox.
const ofLabel = "dev.frontseat.sandbox.of"

var _ driver.Execer = Driver{}

// Capabilities implements driver.Driver. A pause freezes the container's
// processes where they are, so it keeps their memory as well as the disk.
// A sandbox that reaches nothing has a network of its own that holds a
// loopback alone, and its further containers share it.
func (Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{
		PauseKeepsDisk: true,
		Output:         true,
		EgressOpen:     true,
		EgressBlock:    true,
		Volumes:        []driver.VolumeKind{driver.Ephemeral, driver.Host},
		Containers:     true,
		StartupGate:    true,
		VolumeSubpaths: true,
	}
}

// further is the name of a sandbox's further container.
func further(id, name string) string { return id + "-" + name }

// Apply implements driver.Driver.
func (d Driver) Apply(ctx context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if err := driver.Admit(d, record); err != nil {
		return driver.Sandbox{}, err
	}
	if record.Egress.Block && len(record.Ports) > 0 {
		return driver.Sandbox{}, &driver.Unsupported{Driver: d.Name(), Capability: "a published port of a sandbox that reaches nothing"}
	}
	now, err := d.Observe(ctx, record.ID)
	if errors.Is(err, driver.ErrNotFound) {
		if len(record.Wrapper) > 0 {
			// The wrapper stands as the entrypoint, and runs the image's
			// own and its command after it.
			entrypoint, command, _, err := Started(ctx, "docker", record.Image)
			if err != nil {
				return driver.Sandbox{}, err
			}
			given := record.Arguments
			if len(given) == 0 {
				given = command
			}
			record.Arguments = append(append(slices.Clone(record.Wrapper[1:]), entrypoint...), given...)
		}
		args, made := runArguments(record)
		if made != nil {
			return driver.Sandbox{}, made
		}
		if _, err := docker(ctx, args...); err != nil {
			return driver.Sandbox{}, err
		}
		if gate := record.BeforeContainers; gate != nil {
			waiting, cancel := context.WithTimeout(ctx, gate.Timeout)
			exit, output, _, err := d.Exec(waiting, record.ID, "", gate.Arguments, 4096)
			cancel()
			if err != nil || exit != 0 {
				cleaning, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
				defer stop()
				_ = d.Delete(cleaning, record.ID)
				return driver.Sandbox{}, fmt.Errorf("startup gate failed (exit %d): %v: %s", exit, err, strings.TrimSpace(string(output)))
			}
		}
		// Its further containers join its network once it is there; one
		// that cannot be made ends the sandbox, as nothing would run
		// weaker in its place.
		for _, c := range record.Containers {
			args, err := furtherArguments(record.ID, c)
			if err == nil {
				_, err = docker(ctx, args...)
			}
			if err != nil {
				d.Delete(context.WithoutCancel(ctx), record.ID)
				return driver.Sandbox{}, err
			}
		}
		now, err = d.Observe(ctx, record.ID)
	}
	if err != nil {
		return driver.Sandbox{}, err
	}
	if now.State == driver.Ending {
		return driver.Sandbox{}, fmt.Errorf("container: %s: %w", record.ID, driver.ErrEnding)
	}
	verb := ""
	switch {
	case record.Paused && now.State == driver.Running:
		verb = "pause"
	case !record.Paused && now.State == driver.Paused:
		verb = "unpause"
	default:
		return now, nil
	}
	names, err := furthers(ctx, record.ID)
	if err != nil {
		return driver.Sandbox{}, err
	}
	if _, err := docker(ctx, append([]string{verb, record.ID}, names...)...); err != nil {
		return driver.Sandbox{}, err
	}
	return d.Observe(ctx, record.ID)
}

func runArguments(s driver.Sandbox) ([]string, error) {
	args := []string{"run", "--detach", "--name", s.ID, "--label", sandboxLabel + "=1"}
	if s.Egress.Block {
		// A network of its own, holding a loopback and nothing else.
		args = append(args, "--network", "none")
	} else {
		// The machine's own address has the same name under every runtime.
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
	}
	for k, v := range s.Labels {
		args = append(args, "--label", k+"="+v)
	}
	args = append(args, sized(s.Environment, s.MilliCPUs, s.MemoryBytes)...)
	for _, port := range s.Ports {
		args = append(args, "--publish", "127.0.0.1::"+strconv.Itoa(port))
	}
	mounts, err := mounted(s.Volumes)
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	if len(s.Wrapper) > 0 {
		args = append(args, "--entrypoint", s.Wrapper[0])
	}
	if s.User != "" {
		args = append(args, "--user", s.User+":"+s.User)
	}
	// What follows is the image and its arguments, never options of
	// docker run's: an image named "--privileged" is an image of that name.
	args = append(args, "--", s.Image)
	return append(args, s.Arguments...), nil
}

// furtherArguments starts one of a sandbox's further containers, in the
// sandbox's network, with nothing more than its image gives it.
func furtherArguments(id string, c driver.SandboxContainer) ([]string, error) {
	args := []string{"run", "--detach", "--name", further(id, c.Name), "--label", ofLabel + "=" + id, "--network", "container:" + id}
	args = append(args, sized(c.Environment, c.MilliCPUs, c.MemoryBytes)...)
	mounts, err := mounted(c.Volumes)
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	args = append(args, "--", c.Image)
	return append(args, c.Arguments...), nil
}

func sized(environment map[string]string, milliCPUs int, memory int64) []string {
	var args []string
	for k, v := range environment {
		args = append(args, "--env", k+"="+v)
	}
	if milliCPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(float64(milliCPUs)/1000, 'f', 3, 64))
	}
	if memory > 0 {
		args = append(args, "--memory", strconv.FormatInt(memory, 10))
	}
	return args
}

// mounted is how volumes are mounted. A subpath of a host directory is
// the directory beneath it, which --mount binds, refusing one that is not
// there; what --mount reads is split at commas, so none is passed.
func mounted(volumes []driver.Volume) ([]string, error) {
	var args []string
	for _, v := range volumes {
		switch {
		case v.Kind == driver.Ephemeral:
			args = append(args, "--volume", v.Path)
		case v.Subpath == "":
			// As a workspace's sandbox has always had it: --volume makes the
			// directory where it is not there yet, and --mount refuses.
			mount := v.Source + ":" + v.Path
			if v.ReadOnly {
				mount += ":ro"
			}
			args = append(args, "--volume", mount)
		default:
			if strings.ContainsAny(v.Path+v.Source+v.Subpath, ",\n") {
				return nil, errors.New("container: a volume names a path with a comma")
			}
			mount := "type=bind,source=" + path.Join(v.Source, v.Subpath) + ",target=" + v.Path
			if v.ReadOnly {
				mount += ",readonly"
			}
			args = append(args, "--mount", mount)
		}
	}
	return args, nil
}

// furthers are the names of a sandbox's further containers.
func furthers(ctx context.Context, id string) ([]string, error) {
	out, err := docker(ctx, "ps", "--all", "--format", "{{.Names}}", "--filter", "label="+ofLabel+"="+id)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// inspected is what docker says of a container.
type inspected struct {
	Config struct {
		Image  string
		Labels map[string]string
	}
	State struct {
		Status   string
		ExitCode int
		Error    string
	}
	NetworkSettings struct {
		Ports map[string][]struct{ HostPort string }
	}
}

// inspect returns what the runtime says of a container. Its engine is
// asked where it listens on this machine, since every request to a
// sandbox's port begins here and the docker command takes sixty
// milliseconds to say what the engine says in one; where the engine is
// not to be asked, refuses, or does not answer in time, the command is
// run.
func inspect(ctx context.Context, id string) (inspected, error) {
	if socket := engine(); socket != "" {
		found, err := askedOf(ctx, socket, id)
		if err == nil || errors.Is(err, driver.ErrNotFound) {
			return found, err
		}
	}
	return commandedFor(ctx, id)
}

// commandedFor returns what the docker command says of a container.
func commandedFor(ctx context.Context, id string) (inspected, error) {
	out, err := docker(ctx, "inspect", "--type", "container", id)
	if err != nil {
		return inspected{}, err
	}
	var found []inspected
	if err := json.Unmarshal(out, &found); err != nil || len(found) != 1 {
		return inspected{}, fmt.Errorf("container: reading what docker says of %s: %v", id, err)
	}
	return found[0], nil
}

// engine returns the socket the docker command's engine listens on,
// where that is a socket of this machine, and nothing otherwise. The
// command is asked once: which engine it speaks to is the machine's to
// say, and does not change while a server runs.
var engine = sync.OnceValue(func() string {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		out, err := osexec.Command("docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
		if err != nil {
			return ""
		}
		host = strings.TrimSpace(string(out))
	}
	socket, local := strings.CutPrefix(host, "unix://")
	if !local {
		return ""
	}
	return socket
})

// engineWithin is how long an engine is given to say what it says of a
// container.
const engineWithin = 5 * time.Second

// engines are the connections to engines, by socket: what is asked of
// one is asked over a connection that is kept.
var engines sync.Map

// askedOf returns what an engine says of a container: what the docker
// command prints of it, which is the engine's answer as it is.
func askedOf(ctx context.Context, socket, id string) (inspected, error) {
	kept, _ := engines.LoadOrStore(socket, &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
	}})
	// An engine that takes the connection and says nothing is not waited
	// on: it answers in a millisecond or it is not the one to ask, and
	// the command is run.
	ctx, cancel := context.WithTimeout(ctx, engineWithin)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://engine/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return inspected{}, err
	}
	answer, err := kept.(*http.Client).Do(request)
	if err != nil {
		return inspected{}, err
	}
	defer answer.Body.Close()
	switch answer.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return inspected{}, driver.ErrNotFound
	default:
		return inspected{}, fmt.Errorf("container: the engine answered %s for %s", answer.Status, id)
	}
	var found inspected
	if err := json.NewDecoder(answer.Body).Decode(&found); err != nil {
		return inspected{}, fmt.Errorf("container: reading what the engine says of %s: %w", id, err)
	}
	return found, nil
}

// Observe implements driver.Driver.
func (Driver) Observe(ctx context.Context, id string) (driver.Sandbox, error) {
	c, err := inspect(ctx, id)
	if err != nil {
		return driver.Sandbox{}, err
	}
	if c.Config.Labels[sandboxLabel] == "" {
		return driver.Sandbox{}, driver.ErrNotFound
	}
	s := driver.Sandbox{ID: id, Image: c.Config.Image, Labels: map[string]string{}}
	for k, v := range c.Config.Labels {
		if k != sandboxLabel {
			s.Labels[k] = v
		}
	}
	switch c.State.Status {
	case "running":
		s.State = driver.Running
	case "paused":
		s.State, s.Paused = driver.Paused, true
	case "created", "restarting":
		s.State = driver.Starting
	case "removing":
		// The runtime was asked to remove it and still holds it.
		s.State = driver.Ending
	default:
		s.State = driver.Failed
		s.Message = fmt.Sprintf("the container %s with exit code %d", c.State.Status, c.State.ExitCode)
		if c.State.Error != "" {
			s.Message += ": " + c.State.Error
		}
	}
	// A sandbox whose further container ended runs without what it was
	// made with: it has failed, and says which.
	if s.State == driver.Running {
		ended, err := docker(ctx, "ps", "--all", "--format", "{{.Names}} {{.Status}}", "--filter", "label="+ofLabel+"="+id, "--filter", "status=exited", "--filter", "status=dead")
		if err != nil {
			return driver.Sandbox{}, err
		}
		if line := strings.TrimSpace(string(ended)); line != "" {
			name, status, _ := strings.Cut(strings.Split(line, "\n")[0], " ")
			s.State, s.Message = driver.Failed, fmt.Sprintf("its container %s ended: %s", strings.TrimPrefix(name, id+"-"), status)
		}
	}
	return s, nil
}

// Exec implements driver.Execer.
func (Driver) Exec(ctx context.Context, id, container string, args []string, limit int) (int, []byte, bool, error) {
	target := id
	if container != "" {
		target = further(id, container)
	}
	commands.Add(1)
	var out limited
	out.limit = limit
	cmd := osexec.CommandContext(ctx, "docker", append([]string{"exec", target}, args...)...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exited *osexec.ExitError
	switch {
	case err == nil:
		return 0, out.buf.Bytes(), out.cut, nil
	case ctx.Err() != nil:
		return -1, out.buf.Bytes(), out.cut, ctx.Err()
	case errors.As(err, &exited):
		// docker exec exits 126, 127 or 125 for what it could not start,
		// and with the command's own status otherwise.
		return exited.ExitCode(), out.buf.Bytes(), out.cut, nil
	}
	return -1, nil, false, fmt.Errorf("container: docker exec: %w", err)
}

// limited keeps up to limit bytes of what is written, and says whether it
// cut any.
type limited struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
	cut   bool
}

func (l *limited) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.limit - l.buf.Len(); room < len(p) {
		l.cut = true
		if room > 0 {
			l.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return l.buf.Write(p)
}

// List implements driver.Driver.
func (d Driver) List(ctx context.Context, labels map[string]string) ([]driver.Sandbox, error) {
	args := []string{"ps", "--all", "--format", "{{.Names}}", "--filter", "label=" + sandboxLabel + "=1"}
	for k, v := range labels {
		args = append(args, "--filter", "label="+k+"="+v)
	}
	out, err := docker(ctx, args...)
	if err != nil {
		return nil, err
	}
	var found []driver.Sandbox
	for _, id := range strings.Fields(string(out)) {
		s, err := d.Observe(ctx, id)
		if errors.Is(err, driver.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = append(found, s)
	}
	return found, nil
}

// Delete implements driver.Driver.
func (Driver) Delete(ctx context.Context, id string) error {
	// The runtime holds every container on the machine. One that does
	// not carry the driver's label is not a sandbox, and is left alone.
	label, err := docker(ctx, "inspect", "--format", `{{index .Config.Labels "`+sandboxLabel+`"}}`, id)
	if errors.Is(err, driver.ErrNotFound) || err == nil && strings.TrimSpace(string(label)) != "1" {
		return nil
	}
	if err != nil {
		return err
	}
	// Its further containers first: they share its network, and Podman
	// keeps a container another still shares a network with.
	names, err := furthers(ctx, id)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		if _, err := docker(ctx, append([]string{"rm", "--force", "--volumes"}, names...)...); err != nil && !errors.Is(err, driver.ErrNotFound) {
			return err
		}
	}
	_, err = docker(ctx, "rm", "--force", "--volumes", id)
	if errors.Is(err, driver.ErrNotFound) {
		return nil
	}
	return err
}

// Logs implements driver.Driver.
func (Driver) Logs(ctx context.Context, id string, fromLine int64) (io.ReadCloser, error) {
	out, err := docker(ctx, "logs", id)
	if err != nil {
		return nil, err
	}
	for ; fromLine > 0 && len(out) > 0; fromLine-- {
		_, rest, _ := bytes.Cut(out, []byte("\n"))
		out = rest
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}

// Dial implements driver.Driver: a sandbox's port is published on the
// loopback of the machine the driver runs on.
func (Driver) Dial(ctx context.Context, id string, port int) (net.Conn, error) {
	c, err := inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	// What is reached is a sandbox: a container this driver did not make
	// is none, to this as to everything else the driver does.
	if _, made := c.Config.Labels[sandboxLabel]; !made {
		return nil, driver.ErrNotFound
	}
	if c.State.Status == "removing" {
		return nil, fmt.Errorf("container: %s: %w", id, driver.ErrEnding)
	}
	published := c.NetworkSettings.Ports[strconv.Itoa(port)+"/tcp"]
	if len(published) == 0 {
		return nil, fmt.Errorf("container: %s does not publish the port %d", id, port)
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", published[0].HostPort))
}

// answerWithin is how long the runtime is given to answer one command. A
// runtime that takes longer is not waited on: it is reported.
const answerWithin = 3 * time.Minute

// commands counts the docker commands run. A command takes tens of
// milliseconds to start, so what is done for every request to a sandbox
// runs none, and a test holds it to that.
var commands atomic.Int64

// docker runs the docker command and returns what it printed.
func docker(ctx context.Context, args ...string) ([]byte, error) {
	return runtimeCommand(ctx, "docker", args...)
}

// runtimeCommand runs a container runtime's command line, docker's or
// one that answers as docker's does (podman), and returns what it said.
func runtimeCommand(ctx context.Context, program string, args ...string) ([]byte, error) {
	commands.Add(1)
	ctx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := osexec.CommandContext(ctx, program, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("container: the runtime did not answer docker %s within %s", args[0], answerWithin)
		}
		said := strings.TrimSpace(stderr.String())
		if strings.Contains(said, "No such container") || strings.Contains(said, "No such object") || strings.Contains(said, "no such object") {
			return nil, fmt.Errorf("%w: docker %s: %s", driver.ErrNotFound, args[0], said)
		}
		return nil, fmt.Errorf("container: docker %s: %w: %s", args[0], err, said)
	}
	// Both streams are a container's output; everything else prints to
	// standard output alone.
	return append(stdout.Bytes(), stderr.Bytes()...), nil
}

// Started returns what an image the runtime holds is started with, as
// its configuration has it: its entrypoint, its command and the
// environment it sets. The runtime is the program of its command line,
// docker or podman, which answer alike. The image is one already checked
// to be named as an image is.
func Started(ctx context.Context, runtime, image string) (entrypoint, command []string, environment map[string]string, err error) {
	out, err := runtimeCommand(ctx, runtime, "image", "inspect", "--format", "{{json .Config}}", "--", image)
	if err != nil {
		// An image the runtime does not hold yet is pulled, once, as it
		// would be to run it.
		if _, pulled := runtimeCommand(ctx, runtime, "pull", "--quiet", "--", image); pulled != nil {
			return nil, nil, nil, err
		}
		if out, err = runtimeCommand(ctx, runtime, "image", "inspect", "--format", "{{json .Config}}", "--", image); err != nil {
			return nil, nil, nil, err
		}
	}
	var config struct {
		Entrypoint, Cmd, Env []string
	}
	if err := json.Unmarshal(out, &config); err != nil {
		return nil, nil, nil, fmt.Errorf("container: what the runtime says of %s: %w", image, err)
	}
	environment = map[string]string{}
	for _, each := range config.Env {
		name, value, _ := strings.Cut(each, "=")
		environment[name] = value
	}
	return config.Entrypoint, config.Cmd, environment, nil
}
