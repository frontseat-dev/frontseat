package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/core/refusal"
	"github.com/frontseat-dev/frontseat/sdk/go"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/workspace/server/backstage"
)

// catalogOfTheTests is what the tests' Backstage holds: gateway, in the
// repository platform, depends on greet, in services; payments is in
// code; stranger is at a host no workspace of the tests reaches.
const catalogOfTheTests = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: gateway
  annotations:
    backstage.io/source-location: url:https://forge.example/acme/platform/tree/main/gateway/
spec:
  dependsOn: [component:greet]
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: greet
  annotations:
    backstage.io/source-location: url:https://forge.example/acme/services/tree/main/greet/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: payments
  annotations:
    backstage.io/source-location: url:https://forge.example/acme/code.git/tree/main/payments
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: stranger
  annotations:
    backstage.io/source-location: url:https://elsewhere.example/acme/stranger/tree/main/
`

// resolving has a control plane resolve entities through a fake
// Backstage, as frontseat-local's --backstage does.
func resolving(t *testing.T, fake backstagetest.Served) func(*Config) {
	t.Helper()
	catalog, err := backstage.New(backstage.Config{Base: fake.URL, Token: fake.Token, Loopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return func(c *Config) { c.Entities = catalog }
}

// toldAgain waits until a daemon was told so many times, and returns
// what it was told last.
func toldAgain(t *testing.T, daemon *managing, times int) *sourcev1.SourceSpec {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, _ := daemon.was()
		daemon.mu.Lock()
		spec := daemon.spec
		daemon.mu.Unlock()
		if n >= times {
			return spec
		}
		if time.Now().After(deadline) {
			t.Fatalf("its daemon was told %d times, want %d", n, times)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// byEntity asks for a workspace of entities.
func byEntity(names ...string) *workspacev1.WorkspaceSpec {
	spec := &workspacev1.WorkspaceSpec{}
	for _, name := range names {
		spec.Entities = append(spec.Entities, &typev1.EntityRef{Name: name})
	}
	return spec
}

// A control plane its operator gave no Backstage resolves no entities:
// a workspace asked for by one is refused UNIMPLEMENTED, by name, and
// nothing is made; so is one brought more by one. The control is the
// same asked of a control plane given one.
func TestAWorkspaceOfEntitiesIsRefusedWhereNoneAreServed(t *testing.T) {
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "by-entity", Spec: byEntity("gateway")})
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeUnimplemented || reason != "ENTITIES_NOT_SERVED" || domain != refusal.Workspace || !strings.Contains(err.Error(), "--backstage") {
		t.Errorf("a workspace of entities where none are served: %v (%s of %s)", err, reason, domain)
	}
	if len(s.asked()) != 0 {
		t.Errorf("a refused workspace made the sandboxes %v", s.asked())
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "by-name", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "by-name")
	more := of("https://forge.example/acme/code.git")
	more.Entities = byEntity("payments").Entities
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "by-name", Spec: more}); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("an entity added where none are served: %v", err)
	}

	given := over(t)
	control, _, _ := served(t, given, t.TempDir(), resolving(t, backstagetest.Serve(t, catalogOfTheTests)))
	if _, err := control.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "by-entity", Spec: byEntity("gateway")}); err != nil {
		t.Errorf("the control, a control plane given a Backstage: %v", err)
	}
}

// An entity resolves on the control plane to the repositories that hold
// it and the components it depends on, which its daemon is told by name
// with where each entity is, and which its sandbox reaches alone: the
// catalog's host is not among what it reaches. The status says what
// each resolved to.
func TestAnEntityResolvesToTheRepositoriesItsSandboxReachesAlone(t *testing.T) {
	s := over(t)
	fake := backstagetest.Serve(t, catalogOfTheTests)
	client, _, _ := served(t, s, t.TempDir(), resolving(t, fake))
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Spec: byEntity("gateway")}); err != nil {
		t.Fatal(err)
	}
	ready := waited(t, client, "gateway")
	holds := toldAgain(t, s.daemon(s.of("gateway")), 1)
	var repositories, located []string
	for _, r := range holds.GetRepositories() {
		repositories = append(repositories, r.GetName()+"="+r.GetRemote()+"@"+r.GetRef()+":"+strings.Join(r.GetRoots(), ","))
	}
	for _, l := range holds.GetEntities() {
		located = append(located, l.GetRef().GetKind()+":"+l.GetRef().GetNamespace()+"/"+l.GetRef().GetName()+"@"+l.GetRepository()+"/"+l.GetRoot())
	}
	if got := strings.Join(repositories, " "); got != "platform=https://forge.example/acme/platform@main:gateway services=https://forge.example/acme/services@main:greet" {
		t.Errorf("its daemon was told the repositories %s", got)
	}
	if got := strings.Join(located, " "); got != "component:default/gateway@platform/gateway component:default/greet@services/greet" {
		t.Errorf("its daemon was told the entities are %s", got)
	}
	if entities := ready.GetStatus().GetEntities(); len(entities) != 2 || entities[0].GetRepository() != "platform" || entities[1].GetRoot() != "greet" {
		t.Errorf("its status says its entities are %v", entities)
	}
	rules := s.sandbox(s.of("gateway")).GetEgress().GetRules()
	var hosts []string
	for _, rule := range rules {
		hosts = append(hosts, rule.GetHost())
		for _, request := range rule.GetRequests() {
			if !strings.HasPrefix(request.GetPath(), "/acme/platform/") && !strings.HasPrefix(request.GetPath(), "/acme/services/") {
				t.Errorf("its sandbox reaches %s %s, which is no repository the entity resolved to", rule.GetHost(), request.GetPath())
			}
		}
	}
	if strings.Join(hosts, " ") != "forge.example:443 grid.example:8980 assets.example:8983" {
		t.Errorf("its sandbox reaches %v, want the forge, the grid and its assets alone", hosts)
	}
	if fake.URL == "" || strings.Contains(strings.Join(hosts, " "), strings.TrimPrefix(fake.URL, "http://")) {
		t.Errorf("its sandbox reaches the catalog")
	}
}

// What a workspace's entities resolved to is kept with it, and every
// later telling of its daemon is from it: after the catalog changes, a
// daemon started again, and a control plane started again, tell what
// was resolved when it was asked for. The control is a workspace asked
// for after the change, which resolves to what the catalog says now.
func TestAResolutionIsKeptAndNotDoneAgain(t *testing.T) {
	s := over(t)
	fake := backstagetest.Serve(t, catalogOfTheTests)
	state := t.TempDir()
	client, _, first := served(t, s, state, resolving(t, fake))
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "kept", Spec: byEntity("payments")}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "kept")
	toldAgain(t, s.daemon("sbx_1"), 1)
	fake.Set(t, strings.Replace(catalogOfTheTests, "code.git/tree/main/payments", "code.git/tree/main/moved", 1))
	asked := len(fake.Asked())
	s.daemon("sbx_1").restarted()
	again := toldAgain(t, s.daemon("sbx_1"), 2)
	if again.GetEntities()[0].GetRoot() != "payments" || again.GetRepositories()[0].GetRoots()[0] != "payments" {
		t.Errorf("a daemon started again was told %v, want what was resolved", again)
	}
	first.Close()
	after, _, _ := served(t, s, state, resolving(t, fake))
	s.daemon("sbx_1").restarted()
	recovered := toldAgain(t, s.daemon("sbx_1"), 3)
	if recovered.GetEntities()[0].GetRoot() != "payments" || recovered.GetRepositories()[0].GetRoots()[0] != "payments" {
		t.Errorf("under a control plane started again, its daemon was told %v", recovered)
	}
	if described, err := after.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "kept"}); err != nil || described.GetWorkspace().GetStatus().GetEntities()[0].GetRoot() != "payments" {
		t.Errorf("a control plane started again says its entities are %v (%v)", described.GetWorkspace().GetStatus().GetEntities(), err)
	}
	if now := len(fake.Asked()); now != asked {
		t.Errorf("the catalog was asked %d times more, want it not asked again", now-asked)
	}
	if _, err := after.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "fresh", Spec: byEntity("payments")}); err != nil {
		t.Fatal(err)
	}
	waited(t, after, "fresh")
	if fresh := toldAgain(t, s.daemon(s.of("fresh")), 1); fresh.GetEntities()[0].GetRoot() != "moved" {
		t.Errorf("the control, a workspace asked for after the change, was told %v", fresh)
	}
}

// What the catalog refuses is refused by its reason, in the workspace
// API's domain, and nothing is made: a name it does not hold, one that
// names several, an entity in a repository whose address holds a
// secret, and a catalog that does not answer.
func TestWhatTheCatalogRefusesIsRefusedByName(t *testing.T) {
	documents := catalogOfTheTests + `---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: gateway
  namespace: other
  annotations:
    backstage.io/source-location: url:https://forge.example/acme/other/tree/main/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: secretive
  annotations:
    backstage.io/source-location: url:https://someone:a-made-up-secret@forge.example/acme/code.git/tree/main/
`
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), resolving(t, backstagetest.Serve(t, documents)))
	ctx := context.Background()
	for name, want := range map[string]struct {
		code   connect.Code
		reason string
	}{
		"nobody":    {connect.CodeNotFound, "ENTITY_NOT_FOUND"},
		"gateway":   {connect.CodeInvalidArgument, "ENTITY_AMBIGUOUS"},
		"secretive": {connect.CodeInvalidArgument, "ENTITY_INVALID"},
	} {
		_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "refused", Spec: byEntity(name)})
		if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != want.code || reason != want.reason || domain != refusal.Workspace || strings.Contains(err.Error(), "a-made-up-secret") {
			t.Errorf("%s: %v (%s of %s), want %v %s", name, err, reason, domain, want.code, want.reason)
		}
	}
	away := backstagetest.Serve(t, catalogOfTheTests)
	gone, _, _ := served(t, s, t.TempDir(), resolving(t, away))
	away.Close()
	_, err := gone.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "refused", Spec: byEntity("gateway")})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeUnavailable || reason != "ENTITIES_UNAVAILABLE" {
		t.Errorf("a catalog that does not answer: %v", err)
	}
	if len(s.asked()) != 0 {
		t.Errorf("what was refused made the sandboxes %v", s.asked())
	}
}

// What a catalog names is held as a workspace holds a repository it is
// asked for, and no further: a repository told twice by one name, from
// two branches, is refused by name; one that is a path of the sandbox's
// own is refused; and a workspace that pushes pushes to the repositories
// it named alone, not to those its entities resolved to. The controls:
// the same repository at the same branch, and the fetch of what was
// resolved.
func TestWhatTheCatalogNamesIsHeldAsARepositoryAskedFor(t *testing.T) {
	documents := catalogOfTheTests + `---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: inside
  annotations:
    backstage.io/source-location: url:file:///var/lib/frontseat/tree/main/
`
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), resolving(t, backstagetest.Serve(t, documents)))
	ctx := context.Background()
	elsewhere := byEntity("payments")
	elsewhere.Repositories = of("https://forge.example/acme/code.git").Repositories
	for what, spec := range map[string]*workspacev1.WorkspaceSpec{"one repository at two branches": elsewhere, "a path of the sandbox's own": byEntity("inside")} {
		_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "refused", Spec: spec})
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "ENTITY_INVALID" {
			t.Errorf("%s: %v, want it refused ENTITY_INVALID", what, err)
		}
	}
	if len(s.asked()) != 0 {
		t.Errorf("what was refused made the sandboxes %v", s.asked())
	}
	// A push is to repositories a workspace is given by their address:
	// one given entities alone is refused it as not served.
	unaddressed := byEntity("gateway")
	unaddressed.Push = true
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "refused", Spec: unaddressed}); !refusedAsNotServed(err, "PUSH_NOT_SERVED") {
		t.Errorf("a push by a workspace of entities alone: %v, want it refused as not served", err)
	}
	pushing := byEntity("gateway")
	pushing.Repositories = []*workspacev1.RepositorySource{{Remote: "https://forge.example/acme/code.git", Ref: "main"}}
	pushing.Entities = append(pushing.Entities, &typev1.EntityRef{Name: "payments"})
	pushing.Push = true
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "pushing", Spec: pushing}); err != nil {
		t.Fatalf("the control, the same repository at the same branch: %v", err)
	}
	waited(t, client, "pushing")
	for _, rule := range s.sandbox(s.of("pushing")).GetEgress().GetRules() {
		for _, request := range rule.GetRequests() {
			if strings.HasSuffix(request.GetPath(), "/a-push") && request.GetPath() != "/acme/code.git/a-push" {
				t.Errorf("a workspace pushes to %s, which a catalog named", request.GetPath())
			}
			if strings.HasPrefix(request.GetPath(), "/acme/platform/") {
				t.Logf("the control: %s is fetched", request.GetPath())
			}
		}
	}
}

// A workspace made of entities is brought more by a repository under a
// control plane started again without a Backstage: the entities it had
// are not resolved again, and only what is added would be. The control
// is an entity added there, refused by name.
func TestAWorkspaceOfEntitiesIsBroughtMoreWhenNoCatalogIsServed(t *testing.T) {
	s := over(t)
	state := t.TempDir()
	client, _, first := served(t, s, state, resolving(t, backstagetest.Serve(t, catalogOfTheTests)))
	ctx := context.Background()
	spec := byEntity("payments")
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "kept", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "kept")
	first.Close()
	after, _, _ := served(t, s, state, nil)
	waited(t, after, "kept")
	more := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	more.Repositories = []*workspacev1.RepositorySource{{Remote: "https://forge.example/acme/code.git", Ref: "main"}}
	if _, err := after.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "kept", Spec: more}); err != nil {
		t.Errorf("a repository added where no catalog is served: %v", err)
	}
	more.Entities = append(more.Entities, &typev1.EntityRef{Name: "gateway"})
	if _, err := after.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "kept", Spec: more}); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("the control, an entity added where no catalog is served: %v", err)
	}
}
