package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	connect "connectrpc.com/connect/v2"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/common/confine"
	runnerv1 "github.com/frontseat-dev/frontseat/common/runner/runnerapi/frontseat/runner/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
	"github.com/frontseat-dev/frontseat/core/httpserver"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
)

const attemptLabel = "dev.frontseat.runner-attempt"

// attempt owns its private creation admission, records and directories.
type attempt struct {
	r                  *runner
	id, mine, hostPath string
	paths              paths
	description        *runnerv1.RunnerSandbox
	created            int
	creating           *httpserver.Requests
}

func (r *runner) run(ctx context.Context, request []byte) ([]byte, error) {
	p, err := pathsOf(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	d, err := r.described(p.root)
	if err != nil {
		return nil, err
	}
	// A whole closure reserves its slots at once. No attempt holds a provider
	// while waiting for admission for one of its dependencies.
	r.admitOnce.Do(func() { r.admission = semaphore.NewWeighted(64) })
	weight := int64(len(d.Services) + 1)
	if err := r.admission.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		r.admission.Release(weight)
		return nil, err
	}
	id := hex.EncodeToString(bytes)
	a := &attempt{r: r, id: id, mine: filepath.Join(r.own, id), hostPath: path.Join(path.Base(r.own), id), paths: p, description: d}
	// Passed through by the gateway's runtime, to bind what is beneath.
	if err := os.Mkdir(a.mine, 0o711); err != nil {
		r.admission.Release(weight)
		return nil, err
	}
	a.creating, err = r.creations.begin(a.id)
	if err != nil {
		_ = os.Remove(a.mine)
		r.admission.Release(weight)
		return nil, err
	}
	r.removing.Add(1)
	defer func() {
		go func() {
			defer r.removing.Done()
			gone, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if a.remove(gone) {
				r.admission.Release(weight)
			} else {
				log.Printf("frontseat-runner: retaining %d occupied slots for unfinished cleanup of %s", weight, a.id)
			}
		}()
	}()
	// Build and validate every mount before creating the first sandbox.
	specs, err := a.specs()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	for i, spec := range specs {
		sb, err := a.create(ctx, spec)
		if err != nil {
			return nil, err
		}
		if err := r.running(ctx, sb); err != nil {
			return nil, err
		}
		name := "test"
		if i < len(d.Services) {
			name = d.Services[i].Name
		}
		if err := a.routesReady(ctx, name); err != nil {
			return nil, err
		}
		if i < len(d.Services) {
			if err := a.ready(ctx, sb, d.Services[i]); err != nil {
				return nil, status.Errorf(codes.FailedPrecondition, "service %s: %v", d.Services[i].Entity, err)
			}
			continue
		}
		socket := filepath.Join(a.mine, "test", "runner")
		if err := r.listening(ctx, sb, socket); err != nil {
			return nil, err
		}
		// The test's sandbox writes the directory of its runner's socket:
		// it is reached as a socket itself, never through a link left there,
		// on every connection.
		conn, err := grpc.NewClient("passthrough:///runner", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(raw{})),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return confine.DialSocket(ctx, socket) }))
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		var answer []byte
		err = conn.Invoke(ctx, "/buildbarn.runner.Runner/Run", &request, &answer)
		return answer, err
	}
	return nil, fmt.Errorf("no test runner was prepared")
}

func (a *attempt) create(ctx context.Context, spec *sandboxv1.SandboxSpec) (string, error) {
	if a.creating == nil {
		return "", fmt.Errorf("creation attempt is not registered")
	}
	index := a.created
	a.created++
	spec.Labels = map[string]string{label: "1", attemptLabel: a.id, "dev.frontseat.runner-order": fmt.Sprintf("%03d", index)}
	request := &sandboxv1.CreateSandboxRequest{Spec: spec, RequestId: fmt.Sprintf("%s-%d", a.id, index)}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(creationHeader, a.id)
	answer, err := a.r.sandboxes.CreateSandbox(ctx, request)
	if err != nil {
		return "", status.Errorf(codes.Unavailable, "creating service sandbox: %v", err)
	}
	sb := answer.GetSandbox()
	if sb.GetId() == "" || sb.GetSpec().GetLabels()[attemptLabel] != a.id {
		return "", fmt.Errorf("creation answered with no owned sandbox")
	}
	return sb.Id, nil
}

func (a *attempt) remove(ctx context.Context) bool {
	if a.creating == nil {
		return false
	}
	select {
	case <-a.r.creations.close(a.id, a.creating):
	case <-ctx.Done():
		return false
	}
	// Every admitted Create has returned from its server handler. No late
	// request can enter, so the private records are now a complete inventory.
	// Retry one transient deletion, without ever passing an undeleted consumer.
	for range 2 {
		sandboxes, err := a.r.labelled(ctx, attemptLabel, a.id)
		if err != nil {
			return false
		}
		slices.SortFunc(sandboxes, func(x, y *sandboxv1.Sandbox) int {
			return strings.Compare(y.GetSpec().GetLabels()["dev.frontseat.runner-order"], x.GetSpec().GetLabels()["dev.frontseat.runner-order"])
		})
		removed := true
		for _, sb := range sandboxes {
			if sb.GetSpec().GetLabels()[attemptLabel] != a.id {
				return false
			}
			if _, err := a.r.sandboxes.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: sb.Id}); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
				log.Printf("frontseat-runner: removing %s: %v", sb.Id, err)
				removed = false
				break
			}
		}
		if removed {
			// Its own to delete: the directory this attempt made, under
			// its own random name, which no sandbox holds any more.
			if err := os.RemoveAll(a.mine); err != nil {
				log.Printf("frontseat-runner: removing attempt files: %v", err)
				return false
			}
			return true
		}
	}
	return false
}

func (a *attempt) ownVolume(name, sub, at string, readOnly bool) *sandboxv1.Volume {
	return &sandboxv1.Volume{Name: name, Kind: sandboxv1.Volume_KIND_HOST, Source: a.r.host, Subpath: path.Join(a.hostPath, sub), Path: at, ReadOnly: readOnly}
}

func (a *attempt) bbVolume() *sandboxv1.Volume {
	return &sandboxv1.Volume{Name: "bb", Kind: sandboxv1.Volume_KIND_HOST, Source: a.r.bb, Path: "/bb", ReadOnly: true}
}

func (a *attempt) directory(name string) error {
	p := filepath.Join(a.mine, filepath.FromSlash(name))
	if err := os.MkdirAll(p, 0755); err != nil {
		return err
	}
	return os.Chmod(p, 0777)
}

func (a *attempt) specs() ([]*sandboxv1.SandboxSpec, error) {
	root, err := os.OpenRoot(filepath.Join(a.r.worker, "build"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := plainDirectory(root, a.paths.root); err != nil {
		return nil, err
	}
	if err := plainDirectory(root, path.Join(a.paths.root, testservice.RuntimeDirectory)); err != nil {
		return nil, err
	}
	for _, dir := range []string{"test", "hidden"} {
		if err := a.directory(dir); err != nil {
			return nil, err
		}
	}
	var specs []*sandboxv1.SandboxSpec
	for _, s := range a.description.Services {
		// The service's own image, run as it says, through its routes:
		// one workload, which the routes end with.
		routes, volumes, err := a.routes(s.Name, s.Routes)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, a.bbVolume())
		// As the user it says it runs as, writing what it says it writes.
		spec := &sandboxv1.SandboxSpec{Image: s.Service.Image, Wrapper: append(routes, "--"), Arguments: s.Service.Arguments, Environment: s.Service.Environment, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK},
			UserId: userID(s.GetService().GetUser()), Writable: s.GetService().GetWritable()}
		if s.Program != nil {
			spec.Image = a.r.image
			spec.Arguments = programArguments(s.Program, s.Program.Arguments)
			program, err := a.programVolumes(root, s)
			if err != nil {
				return nil, err
			}
			volumes = append(volumes, program...)
		}
		spec.Volumes = volumes
		specs = append(specs, spec)
	}
	// The test's: Frontseat's runner, through the test's routes, over the
	// action's directory of the worker's build directory alone, which runs
	// the action confined, as every runner sandbox's does.
	routes, volumes, err := a.routes("", a.description.Routes)
	if err != nil {
		return nil, err
	}
	volumes = append(volumes,
		&sandboxv1.Volume{Name: "action", Kind: sandboxv1.Volume_KIND_HOST, Source: path.Join(a.r.host, "build"), Subpath: a.paths.directory, Path: path.Join("/worker/build", a.paths.directory)},
		a.ownVolume("hidden", "hidden", path.Join("/worker/build", a.paths.root, testservice.RuntimeDirectory), true),
		a.ownVolume("runner", "test", "/runner", false), a.bbVolume())
	specs = append(specs, &sandboxv1.SandboxSpec{Image: a.r.image, Wrapper: append(routes, "--"), Volumes: volumes, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK},
		Arguments: []string{"/bb/frontseat-runner", "serve", "--listen", "/runner/runner", "--build", "/worker/build"}})
	return specs, nil
}

func programArguments(p *runnerv1.RunnerProgram, args []string) []string {
	return append([]string{confine.Confiner, "at", path.Join("/runtime/root", p.Directory)}, args[1:]...)
}

func programReadiness(p *runnerv1.RunnerProgram, command []string) ([]string, error) {
	spec, err := confine.ProgramLayout(p.Arguments)
	if err != nil {
		return nil, err
	}
	// Readiness runs in the same prepared environment; ordinary image tools
	// may probe it without becoming the long-running service program.
	spec.Program = false
	encoded, err := confine.Encode(spec)
	if err != nil {
		return nil, err
	}
	return programArguments(p, confine.Arguments(encoded, p.Arguments[2], command)), nil
}

// userID is the user a service says it runs as, by number, as a sandbox
// is told it; zero, its image's own, where it says none. What a service
// is held to (testservice) has refused any other.
func userID(user string) uint32 {
	id, err := strconv.ParseUint(user, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(id)
}
