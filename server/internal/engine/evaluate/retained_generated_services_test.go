package evaluate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type readiness struct {
	t       *testing.T
	root    string
	plugins []*pluginhost.Plugin
	files   *source.Keeper
	session *Session
}

func newReadiness(t *testing.T, root string, names ...string) *readiness {
	t.Helper()
	host, err := pluginhost.New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	r := &readiness{t: t, root: root, files: &source.Keeper{}}
	for _, name := range names {
		module, err := testmodule.Plugin(name)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := host.Load(t.Context(), module)
		if err != nil {
			t.Fatal(err)
		}
		r.plugins = append(r.plugins, loaded)
	}
	r.session = newSession(t, r.plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
		src, err := r.files.Take(ctx, source.Tree{Root: r.root, Paths: map[string]string{"": "."}})
		return src, nil, err
	})
	t.Cleanup(r.session.Close) // Session joins before its plugin owner closes.
	return r
}

func (r *readiness) fresh() (Evaluation, error) {
	return Evaluate(r.t.Context(), retainedTake(r.t, &source.Keeper{}, r.root), r.plugins, &Answers{}, pluginv1.Profile_PROFILE_DEVELOPMENT, nil)
}

func (r *readiness) facts() Evaluation {
	r.t.Helper()
	got, err := r.session.Facts(r.t.Context())
	if err != nil {
		r.t.Fatalf("retained facts: %v", err)
	}
	want, err := r.fresh()
	if err != nil {
		r.t.Fatalf("ordinary facts control: %v", err)
	}
	a, b := retainedSemantics(r.t, got, want)
	if !bytes.Equal(a, b) {
		r.t.Fatal("retained facts differ from ordinary evaluation")
	}
	return got
}

func (r *readiness) build(target string) BuildPlan {
	r.t.Helper()
	targets := []string{target}
	got, err := r.session.Build(r.t.Context(), retainedTake(r.t, r.files, r.root), pluginv1.Profile_PROFILE_DEVELOPMENT, nil, targets, nil)
	if err != nil {
		r.t.Fatalf("retained build: %v", err)
	}
	fresh, err := r.fresh()
	if err != nil {
		r.t.Fatalf("ordinary evaluation: %v", err)
	}
	needed, asked, err := plan.Of(fresh.Actions, fresh.Entities, targets, nil)
	if err != nil {
		r.t.Fatalf("ordinary selection: %v", err)
	}
	// Fixture comparison includes all exported execution payloads, not only
	// labels. Unknown protobuf fields are covered by earlier ownership tests.
	a, err := json.Marshal(selectedPlan{Needed: got.Needed, Asked: got.Asked, Entities: got.Entities})
	if err != nil {
		r.t.Fatal(err)
	}
	b, err := json.Marshal(selectedPlan{Needed: needed, Asked: asked, Entities: fresh.Entities})
	if err != nil {
		r.t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		r.t.Fatal("retained execution selection differs from ordinary evaluation")
	}
	return got
}

func (r *readiness) refuses(target, contains string) {
	r.t.Helper()
	_, got := r.session.Build(r.t.Context(), retainedTake(r.t, r.files, r.root), pluginv1.Profile_PROFILE_DEVELOPMENT, nil, []string{target}, nil)
	_, want := r.fresh()
	if got == nil || want == nil || got.Error() != want.Error() || !strings.Contains(got.Error(), contains) {
		r.t.Fatalf("global refusal mismatch: retained=%v ordinary=%v; require %q", got, want, contains)
	}
}

func readinessAction(t *testing.T, actions []plan.Action, label string) *plan.Action {
	t.Helper()
	for i := range actions {
		if actions[i].Label.String() == label {
			return &actions[i]
		}
	}
	t.Fatalf("fixture did not declare %s", label)
	return nil
}

func TestRetainedGeneratedAuthorityTransitions(t *testing.T) {
	root := t.TempDir()
	testforge.GeneratedGo(t, root)
	const pbFile = "api/gen/go/hello/v1/stale.pb.go"
	const connectFile = "api/gen/go/hello/v1/stale.connect.go"
	retainedWrite(t, root, pbFile, "package hello\n//go:embed pb-secret.txt\nvar pb string\n")
	retainedWrite(t, root, connectFile, "package hello\n//go:embed connect-secret.txt\nvar rpc string\n")
	r := newReadiness(t, root, "go", "buf")
	const target = "//api/gen/go:go.compile"
	check := func(pbGenerated, connectGenerated bool) BuildPlan {
		t.Helper()
		r.facts()
		got := r.build(target)
		a := readinessAction(t, got.Needed, target)
		if a.AcceptsSnapshot("", pbFile) == pbGenerated || a.AcceptsSnapshot("", connectFile) == connectGenerated {
			t.Fatal("retained generated authority does not match the real producer")
		}
		var read []string
		for _, group := range a.Sources {
			read = append(read, group.Patterns...)
		}
		if slices.Contains(read, "hello/v1/pb-secret.txt") == pbGenerated || slices.Contains(read, "hello/v1/connect-secret.txt") == connectGenerated {
			t.Fatalf("generated checkout embeds leaked, or handwritten embeds disappeared: %v", read)
		}
		return got
	}
	check(true, false)
	warm := r.build(target)
	if !warm.Work.Retained || !warm.Work.PlanRetained {
		t.Fatalf("missing retained-hit control: %+v", warm.Work)
	}
	a := readinessAction(t, warm.Needed, target)
	if len(a.GeneratedSources) == 0 || len(a.Artifacts) == 0 {
		t.Fatal("fixture has no real generated binding")
	}
	before := slices.Clone(a.GeneratedSources[0].Patterns)
	a.GeneratedSources[0].Patterns[0] = "**/*"
	a.Artifacts[0].Source.Patterns[0] = "**/*"
	if slices.Equal(a.GeneratedSources[0].Patterns, before) {
		t.Fatal("authority mutation control failed")
	}
	check(true, false)
	manifest, err := os.ReadFile(filepath.Join(root, "api/buf.gen.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	retainedWrite(t, root, "api/buf.gen.yaml", strings.Replace(string(manifest), "protoc-gen-go", "protoc-gen-connect-go", 1))
	check(false, true)
	retainedWrite(t, root, "api/buf.gen.yaml", strings.Replace(string(manifest), "out: gen/go", "out: ../../escape", 1))
	r.refuses("//app:go.compile", "is outside the repository")
	// Inside the repository and outside the project, it is refused for
	// want of the consent of a Component there.
	retainedWrite(t, root, "api/buf.gen.yaml", strings.Replace(string(manifest), "out: gen/go", "out: ../escape", 1))
	r.refuses("//app:go.compile", "which no Component owns to say it may")
	retainedWrite(t, root, "api/buf.gen.yaml", string(manifest))
	check(true, false)
	if err := os.Remove(filepath.Join(root, "api/buf.gen.yaml")); err != nil {
		t.Fatal(err)
	}
	check(false, false)
	retainedWrite(t, root, "api/buf.gen.yaml", string(manifest))
	check(true, false)
	retainedWrite(t, root, "frontseat.yaml", "plugins: {go: {}}\n")
	check(false, false)
	retainedWrite(t, root, "frontseat.yaml", "plugins: {go: {}, buf: {}}\n")
	check(true, false)
}

func TestRetainedServiceOwnershipAndConfiguration(t *testing.T) {
	root := retainedFixture(t, 2)
	retainedWrite(t, root, "frontseat.yaml", "plugins: {go: {}, backstage: {}, container: {}}\n")
	for _, dir := range []string{"m0000", "m0001"} {
		retainedWrite(t, root, dir+"/main_test.go", "package main\nimport \"testing\"\nfunc TestService(t *testing.T) {}\n")
	}
	component := func(i int, owns bool) {
		depends := ""
		if owns {
			depends = "  dependsOn: [resource:greeter]\n"
		}
		retainedWrite(t, root, fmt.Sprintf("m%04d/catalog-info.yaml", i), fmt.Sprintf("apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: component%d}\nspec:\n  type: service\n  lifecycle: experimental\n  owner: team\n%s", i, depends))
	}
	resource := func(port int) {
		retainedWrite(t, root, "catalog-info.yaml", fmt.Sprintf(`apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: greeter}
spec:
  type: service
  owner: team
  frontseat:
    service:
      as: container
      image: docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
      args: [nc, -lk, -p, "%d", -e, echo, hello]
      ports: {greet: %d}
      ready: [{port: greet}]
      exports: {GREETER_ADDR: "{host}:{port.greet}"}
`, port, port))
	}
	component(0, true)
	component(1, false)
	resource(7000)
	r := newReadiness(t, root, "go", "backstage", "container")
	const first = "//m0000:go.test/."
	const second = "//m0001:go.test/."
	check := func(owner string, port int) BuildPlan {
		t.Helper()
		facts := r.facts()
		for _, a := range facts.Actions {
			if a.Beside != nil && (!a.IsTest() || a.Label.Path != strings.TrimSuffix(strings.TrimPrefix(owner, "//"), ":go.test/.")) {
				t.Fatalf("service attached to wrong owner/action: %s", a.Label)
			}
		}
		got := r.build(owner)
		a := readinessAction(t, got.Needed, owner)
		if a.Beside == nil || a.Beside.Environment["GREETER_ADDR"] != fmt.Sprintf("127.0.0.1:%d", port) {
			t.Fatalf("stale/missing service binding: %+v", a.Beside)
		}
		var sandbox runnerv1.RunnerSandbox
		if err := proto.Unmarshal(a.Beside.Sandbox, &sandbox); err != nil {
			t.Fatal(err)
		}
		if len(sandbox.Services) != 1 || sandbox.Services[0].GetEntity() != "resource:default/greeter" || sandbox.Services[0].GetService().GetPorts()["greet"] != int32(port) {
			t.Fatalf("stale runner payload: %v", &sandbox)
		}
		if len(sandbox.Routes) != 1 || sandbox.Routes[0].GetService() != sandbox.Services[0].GetName() || sandbox.Routes[0].GetPort() != "greet" || sandbox.Routes[0].GetLocalPort() != int32(port) {
			t.Fatalf("stale service route: %v", &sandbox)
		}
		return got
	}
	check(first, 7000)
	warm := r.build(first)
	if !warm.Work.Retained || !warm.Work.PlanRetained {
		t.Fatalf("missing warm service control: %+v", warm.Work)
	}
	a := readinessAction(t, warm.Needed, first)
	before := bytes.Clone(a.Beside.Sandbox)
	a.Beside.Sandbox[0] ^= 0xff
	a.Beside.Environment["GREETER_ADDR"] = "caller-mutated"
	if bytes.Equal(a.Beside.Sandbox, before) {
		t.Fatal("service mutation control failed")
	}
	check(first, 7000)
	resource(7001)
	check(first, 7001)
	component(0, false)
	component(1, true)
	check(second, 7001)
	if got := r.build(first); readinessAction(t, got.Needed, first).Beside != nil {
		t.Fatal("removed service ownership survived retained evaluation")
	}
	resource(70000)
	r.refuses("//m0000:go.compile", "port")
	resource(7002)
	check(second, 7002)
	component(1, false)
	facts := r.facts()
	for _, a := range facts.Actions {
		if a.Beside != nil {
			t.Fatal("service binding survived removal of every owner")
		}
	}
	if got := r.build(second); readinessAction(t, got.Needed, second).Beside != nil {
		t.Fatal("selected action retained removed service")
	}
}
