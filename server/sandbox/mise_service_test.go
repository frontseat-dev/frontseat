package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestAToolServiceOfAWrongArchiveChecksumIsRefused(t *testing.T) {
	root := caddySource(t)
	path := filepath.Join(root, "mise.lock")
	held, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wrong := string(held)
	for _, checksum := range []string{
		"b9d88bec4254d0a98bd415ad60f97f37e4222dec96235c00b442437f5e303a32",
		"94391dfefe1f278ac8f387ab86162f0e88d87ff97df367f360e51e3cda3df56f",
	} {
		if strings.Count(wrong, checksum) != 1 {
			t.Fatal("fixture's checksum to corrupt was not found exactly once")
		}
		wrong = strings.Replace(wrong, checksum, strings.Repeat("0", 64), 1)
	}
	if err := os.WriteFile(path, []byte(wrong), 0o644); err != nil {
		t.Fatal(err)
	}
	action, summary := runArtifact(t, root, "//proxy:custom.prepare")
	if action.GetOutcome() == buildv1.Outcome_OUTCOME_PASSED || !strings.Contains(strings.ToLower(summary), "checksum mismatch") {
		t.Fatalf("wrong lock was not refused for its checksum: %s", summary)
	}
	// The successful service test below is the allowed control with the same
	// native release and the original lock, not a host-installed substitute.
}

// The server is the real Caddy release installed by mise on a worker, exported
// by a finite preparation action, then run directly in its own sandbox. Neither
// the service nor its client receives mise, a tool cache or network egress.
func TestAPinnedToolServiceCanDependOnABuiltApplication(t *testing.T) {
	testrequired.Stack(t)
	tool := buildCaddy(t)
	app, identity := buildApplication(t)
	p := newServiceRoutes(t)
	pg := newPostgres(p)
	a, b := pg.allocate(), pg.allocate()
	aa, ar := application(p, pg, a, app)
	ab, br := application(p, pg, b, app)
	proxyA, routeA := caddy(p, tool, ar)
	proxyB, routeB := caddy(p, tool, br)
	ca, cb := applicationClient(p, routeA), applicationClient(p, routeB)
	for _, id := range []string{ca, cb, proxyA, proxyB} {
		withoutDatabase(p, id)
	}
	expectHTTP(p, ca, "/build", identity)
	expectHTTP(p, ca, "/value?set=11", "11")
	expectHTTP(p, cb, "/value?set=22", "22")
	expectHTTP(p, ca, "/value", "11")
	// The pinned binary executes, and the service process has no authority
	// to fetch another tool or reconfigure itself through a local admin API.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	version, err := p.client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: proxyA,
		Arguments: []string{"gosu", "65534:65534", "/tool/caddy", "version"}, Timeout: durationpb.New(10 * time.Second)})
	if err != nil || version.GetExitCode() != 0 || !strings.HasPrefix(string(version.GetOutput()), "v2.11.2 ") {
		t.Fatalf("mise-prepared server version: %v: %s", err, version.GetOutput())
	}
	if code, out := p.run(proxyA, "dial", "tcp", "127.0.0.1:2019"); code == 0 || !strings.Contains(out, "connection refused") {
		t.Fatalf("Caddy administration should be disabled: exit %d: %s", code, out)
	}
	if code, out := p.run(proxyA, "write", "/tool/caddy", "changed"); code == 0 || !strings.Contains(out, "read-only file system") {
		t.Fatalf("service modified its prepared tool: exit %d: %s", code, out)
	}
	p.proveBlocked(proxyA)
	ended(p, proxyA)
	ended(p, aa)
	pg.release(a)
	expectHTTP(p, cb, "/value", "22")
	if code, _, err := fetched(p, ca, "/value"); err != nil || code == 0 {
		t.Fatalf("ended proxy still serves: exit %d: %v", code, err)
	}
	// Removing the downstream application while Caddy still runs cannot be
	// mistaken for readiness: a live proxy must report the failed upstream.
	ended(p, ab)
	if code, out, err := fetched(p, cb, "/value"); err != nil || code == 0 || !strings.Contains(out, "502") {
		t.Fatalf("missing upstream did not produce a gateway failure: exit %d: %v: %s", code, err, out)
	}
	ended(p, proxyB)
	t.Log("mise-installed Caddy → grid-built application → isolated PostgreSQL; native version, immutable tool, no admin/egress and independent teardown passed")
}

func caddy(p *serviceRoutes, tool, upstream string) (string, string) {
	p.t.Helper()
	route := p.route()
	config := p.t.TempDir()
	wrote(p.t, config, "caddy.json", `{
  "admin": {"disabled": true, "config": {"persist": false}},
  "apps": {"http": {"servers": {"proxy": {
    "listen": ["unix//route/service.sock|0666"],
    "automatic_https": {"disable": true},
    "routes": [{"handle": [{"handler": "reverse_proxy", "upstreams": [{"dial": "unix//upstream/service.sock"}]}]}]
  }}}}
}`)
	spec := p.spec(nil, route, false)
	spec.Arguments = []string{"gosu", "65534:65534", "/tool/caddy", "run", "--config", "/config/caddy.json"}
	spec.Environment = map[string]string{"HOME": "/tmp", "XDG_DATA_HOME": "/tmp/data", "XDG_CONFIG_HOME": "/tmp/config"}
	spec.Volumes = append(spec.Volumes,
		&sandboxv1.Volume{Name: "tool", Kind: sandboxv1.Volume_KIND_HOST, Source: tool, Path: "/tool", ReadOnly: true},
		&sandboxv1.Volume{Name: "config", Kind: sandboxv1.Volume_KIND_HOST, Source: config, Path: "/config", ReadOnly: true},
		&sandboxv1.Volume{Name: "upstream", Kind: sandboxv1.Volume_KIND_HOST, Source: upstream, Path: "/upstream", ReadOnly: true})
	id := p.create(spec)
	p.waitFor(id, []string{"dial", "unix", "/route/service.sock"})
	return id, route
}

func buildCaddy(t *testing.T) string {
	t.Helper()
	return buildArtifact(t, caddySource(t), "//proxy:custom.prepare", "runtime/caddy", "caddy")
}

func caddySource(t *testing.T) string {
	t.Helper()
	testgrid.Address(t)
	root := fixtureRoot(t)
	testpins.Caddy(t, root)
	wrote(t, root, "frontseat.yaml", "plugins:\n  backstage: {}\n")
	wrote(t, root, "proxy/stamp", unique())
	wrote(t, root, "proxy/catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: proxy
spec:
  type: service
  lifecycle: experimental
  owner: team
  frontseat:
    actions:
      prepare:
        command: [sh, -c, 'test -s stamp && mkdir -p runtime && cp "$(command -v caddy)" runtime/caddy && chmod 555 runtime/caddy']
        tools: [caddy]
        inputs: [stamp]
        outputs: [runtime/caddy]
`)
	return root
}
