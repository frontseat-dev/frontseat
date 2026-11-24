package controlplane

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/core/refusal"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/workspace/server/workspaces/sources"
	"github.com/frontseat-dev/frontseat/workspace/server/workspaces/sourcestest"
)

// whose is who the tests' owner is to the control plane: whoever presents
// the owner's token (OwnerToken).
const whose = "local"

// mountedBeneath is where the tests' stored repositories are said to be
// mounted, on the machine sandboxes are made on.
const mountedBeneath = "/var/lib/a-store/mounted"

// serving gives a control plane what serves stored repositories, held in
// memory.
func serving(held *sourcestest.Memory) func(*Config) {
	return func(c *Config) { c.Sources = held }
}

// onATask asks for a remote workspace: one stored repository, on a task,
// landing.
func onATask() *workspacev1.WorkspaceSpec {
	return &workspacev1.WorkspaceSpec{Stored: []*workspacev1.StoredRepository{{Repository: "services"}}, Task: "fix-greeting", Land: true}
}

// A control plane given nothing that serves stored repositories serves
// no remote workspace: a spec that names a stored repository, a task or
// a landing is refused UNIMPLEMENTED, STORE_NOT_SERVED, when a workspace
// is asked for and when one is brought more, and nothing is made: no
// sandbox, no workspace. The control is the same request to a control
// plane given what serves them, which makes the workspace.
func TestARemoteWorkspaceIsRefusedByNameWhereNoStoreIsServed(t *testing.T) {
	ctx := context.Background()
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), nil)
	for what, spec := range map[string]*workspacev1.WorkspaceSpec{
		"a stored repository": {Stored: []*workspacev1.StoredRepository{{Repository: "services"}}},
		"a task":              {Repositories: of("https://forge.example/acme/code.git").Repositories, Task: "fix-greeting"},
		"a landing":           {Repositories: of("https://forge.example/acme/code.git").Repositories, Land: true},
		"all three":           onATask(),
	} {
		_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: spec})
		if !refusedAsNotServed(err, "STORE_NOT_SERVED") {
			reason, domain := frontseat.Reason(err)
			t.Errorf("a workspace asked for with %s where no store is served: %v (%s of %s), want unimplemented, STORE_NOT_SERVED, %s", what, err, reason, domain, refusal.Workspace)
		}
	}
	if len(s.asked()) != 0 {
		t.Errorf("a refused workspace made the sandboxes %v", s.asked())
	}
	if _, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "remote"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a refused workspace is there: %v", err)
	}
	// A local workspace of the same control plane, brought more by a spec
	// that asks for the store.
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "local", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "local")
	more := of("https://forge.example/acme/code.git")
	more.Stored = onATask().GetStored()
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "local", Spec: more}); !refusedAsNotServed(err, "STORE_NOT_SERVED") {
		t.Errorf("a stored repository added where no store is served: %v, want unimplemented, STORE_NOT_SERVED", err)
	}
	if told, _ := s.supervisor(s.of("local")).was(); told != 1 {
		t.Errorf("the refused spec was told the supervisor: it was told %d times", told)
	}

	given, held := over(t), sourcestest.New(mountedBeneath)
	control, _, _ := served(t, given, t.TempDir(), serving(held))
	if made, err := control.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: onATask()}); err != nil || !made.GetCreated() {
		t.Fatalf("the control, a control plane given what serves stored repositories: %v, %v", made, err)
	}
	if ready := waited(t, control, "remote"); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || len(given.asked()) != 1 {
		t.Errorf("the control is %v, in the sandboxes %v", ready.GetStatus(), given.asked())
	}
}

// A remote workspace's stored repositories are held once, when it is
// made, by its name and its owner; its sandbox is given the directory
// held for each, at the repository's directory in the workspace, and
// nothing of the store: no way out to one, no argument and no variable
// of one, so no credential of it. Its supervisor is told each is
// mounted, at the changeset it stands at, and to clone nothing; the
// status says where each stands. Asked for again, it is the one there
// is, and is not held again. When it ends, what is held is let go of,
// once its sandbox is gone.
func TestARemoteWorkspaceIsHeldOnceMountedAndLetGoOfWhenItEnds(t *testing.T) {
	ctx := context.Background()
	s, held := over(t), sourcestest.New(mountedBeneath)
	client, _, _ := served(t, s, t.TempDir(), serving(held))
	spec := &workspacev1.WorkspaceSpec{Task: "fix-greeting", Land: true, Stored: []*workspacev1.StoredRepository{
		{Repository: "services", Roots: []string{"greet"}},
		{Repository: "acme/platform", Name: "platform", At: "release-1"},
		{Repository: "tools", Task: "tidy"},
	}}
	made, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Spec: spec})
	if err != nil || made.GetWorkspace().GetName() != "services" {
		t.Fatalf("a remote workspace: %v, %v, want one named for its first stored repository", made, err)
	}
	ready := waited(t, client, "services")
	// Held once, by its name and owner, each repository on its own task,
	// or the workspace's where it names neither a task nor where to stand.
	want := sources.Workspace{Name: "services", Owner: whose, Land: true, Stored: []sources.Stored{
		{Repository: "services", Name: "services", Roots: []string{"greet"}, Task: "fix-greeting"},
		{Repository: "acme/platform", Name: "platform", At: "release-1"},
		{Repository: "tools", Name: "tools", Task: "tidy"},
	}}
	if holds := held.Holds(); len(holds) != 1 || !same(holds[0], want) {
		t.Errorf("its stored repositories were held as %+v, want once, as %+v", holds, want)
	}
	// Its sandbox: the directories held for it, and nothing of a store.
	in := s.sandbox(s.of("services"))
	var volumes []string
	for _, v := range in.GetVolumes() {
		if v.GetKind() != sandboxv1.Volume_KIND_HOST || v.GetReadOnly() || v.GetSubpath() != "" {
			t.Errorf("the volume %v is no directory of the machine, bound whole to be written", v)
		}
		volumes = append(volumes, v.GetSource()+"="+v.GetPath())
	}
	if got, want := strings.Join(volumes, " "), mountedBeneath+"/services/services=/work/services "+mountedBeneath+"/services/platform=/work/platform "+mountedBeneath+"/services/tools=/work/tools"; got != want {
		t.Errorf("its sandbox is given the volumes %s, want %s", got, want)
	}
	local := over(t)
	plain, _, _ := served(t, local, t.TempDir(), nil)
	if _, err := plain.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "services", Spec: of("https://forge.example/acme/services.git")}); err != nil {
		t.Fatal(err)
	}
	waited(t, plain, "services")
	cloned := local.sandbox(local.of("services"))
	if !slices.Equal(in.GetArguments(), cloned.GetArguments()) || len(in.GetEnvironment()) != 0 {
		t.Errorf("its sandbox is told %v and %v, want what a local one is told and nothing of a store", in.GetArguments(), in.GetEnvironment())
	}
	for _, rule := range in.GetEgress().GetRules() {
		if rule.GetHost() != "grid.example:8980" && rule.GetHost() != "assets.example:8983" {
			t.Errorf("its sandbox reaches %s, want the grid alone", rule.GetHost())
		}
	}
	if len(cloned.GetVolumes()) != 0 {
		t.Errorf("a local workspace's sandbox is given the volumes %v", cloned.GetVolumes())
	}
	// Its supervisor: each repository mounted, and nothing to clone.
	told := toldAgain(t, s.supervisor(s.of("services")), 1)
	var mounted []string
	for _, r := range told.GetStored() {
		mounted = append(mounted, r.GetName()+"="+r.GetRepository()+"@"+r.GetChangeset()+":"+strings.Join(r.GetRoots(), ","))
	}
	if got, want := strings.Join(mounted, " "), "services=services@changeset-of-services-at-task/fix-greeting:greet platform=acme/platform@changeset-of-acme/platform-at-release-1: tools=tools@changeset-of-tools-at-task/tidy:"; got != want || len(told.GetRepositories()) != 0 {
		t.Errorf("its supervisor was told the stored repositories %s and to clone %v, want %s and nothing", got, told.GetRepositories(), want)
	}
	// Its status: where each stands.
	var stands []string
	for _, r := range ready.GetStatus().GetRepositories() {
		stands = append(stands, r.GetName()+"="+r.GetStored()+"@"+r.GetCommit()+" from "+r.GetRemote())
	}
	if got, want := strings.Join(stands, ", "), "services=services@changeset-of-services-at-task/fix-greeting from , platform=acme/platform@changeset-of-acme/platform-at-release-1 from , tools=tools@changeset-of-tools-at-task/tidy from "; ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || got != want {
		t.Errorf("it is %v and its status says it holds %s, want it ready, holding %s", ready.GetStatus().GetState(), got, want)
	}
	// Asked for again as it is, it is the one there is; with another
	// task, or a landing it was made without, it differs.
	if again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Spec: spec}); err != nil || again.GetCreated() || len(held.Holds()) != 1 {
		t.Errorf("asked for again: %v, %v, held %d times; want the one there is, held once", again.GetCreated(), err, len(held.Holds()))
	}
	another := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	another.Task = "another"
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "services", Spec: another}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("asked for again on another task: %v, want it to differ", err)
	}
	// Brought more, it is refused: what it holds is said when it is made.
	more := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	more.Repositories = of("https://forge.example/acme/code.git").Repositories
	moved := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	moved.Stored[1].At = "release-2"
	for what, changed := range map[string]*workspacev1.WorkspaceSpec{"a repository added": more, "a stored repository moved": moved} {
		_, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "services", Spec: changed})
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || (reason != "SPEC_FIXED" && reason != "SPEC_INCOMPLETE") {
			t.Errorf("%s: %v (%s), want it refused as fixed", what, err, reason)
		}
	}
	if len(held.Releases()) != 0 {
		t.Fatalf("what is held was let go of while the workspace is there: %+v", held.Releases())
	}
	// Ended: its sandbox goes, and then what is held is let go of.
	if _, err := client.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "services"}); err != nil {
		t.Fatal(err)
	}
	gone(t, client, "services")
	if released := held.Releases(); len(released) != 1 || released[0].Name != "services" || released[0].Owner != whose || held.Held("services") {
		t.Errorf("when it ended, what was let go of is %+v, want what was held for it, once, by its name and owner", released)
	}
	if ended := s.ended(); len(ended) != 1 {
		t.Errorf("ending it ended the sandboxes %v", ended)
	}
}

// A workspace that lands and is told no task lands on one of its own
// name: its stored repositories are held on that task, and its spec says
// so. The control is one told a task, which is held on the task told.
func TestAWorkspaceThatLandsWithNoTaskNamedLandsOnOneOfItsOwnName(t *testing.T) {
	ctx := context.Background()
	s, held := over(t), sourcestest.New(mountedBeneath)
	client, _, _ := served(t, s, t.TempDir(), serving(held))
	landing := &workspacev1.WorkspaceSpec{Stored: []*workspacev1.StoredRepository{{Repository: "services"}}, Land: true}
	made, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "fix-it", Spec: landing})
	if err != nil || made.GetWorkspace().GetSpec().GetTask() != "fix-it" {
		t.Fatalf("a workspace that lands, told no task: %v, on the task %q; want the task of its own name", err, made.GetWorkspace().GetSpec().GetTask())
	}
	if holds := held.Holds(); len(holds) != 1 || holds[0].Stored[0].Task != "fix-it" || !holds[0].Land {
		t.Errorf("its repositories were held as %+v, want on the task fix-it, landing", holds)
	}
	if again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "fix-it", Spec: landing}); err != nil || again.GetCreated() {
		t.Errorf("asked for again as it was asked for: %v, %v; want the one there is", again.GetCreated(), err)
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "told", Spec: onATask()}); err != nil {
		t.Fatal(err)
	}
	if holds := held.Holds(); len(holds) != 2 || holds[1].Stored[0].Task != "fix-greeting" {
		t.Errorf("the control, told a task, was held as %+v", holds)
	}
}

// same is whether what was asked to be held for a workspace is what was
// wanted.
func same(got, want sources.Workspace) bool {
	return got.Name == want.Name && got.Owner == want.Owner && got.Land == want.Land && slices.EqualFunc(got.Stored, want.Stored, func(a, b sources.Stored) bool {
		return a.Repository == b.Repository && a.At == b.At && a.Name == b.Name && a.Task == b.Task && slices.Equal(a.Roots, b.Roots)
	})
}

// A control plane that starts again goes on with a remote workspace
// from what it kept: its sandbox, made then, is given what was held when
// the workspace was asked for, which is not held again.
func TestAControlPlaneThatStartsAgainKeepsWhatWasHeldForARemoteWorkspace(t *testing.T) {
	ctx := context.Background()
	s, held, state := over(t), sourcestest.New(mountedBeneath), t.TempDir()
	// No sandbox is made while it is refused one.
	s.refuses = "the test's sandboxes are not made yet"
	client, _, plane := served(t, s, state, serving(held))
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: onATask()}); err != nil {
		t.Fatal(err)
	}
	plane.Close()
	s.mu.Lock()
	s.refuses = ""
	s.mu.Unlock()
	next, _, _ := served(t, s, state, serving(held))
	if _, err := next.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: onATask()}); err != nil {
		t.Fatal(err)
	}
	if ready := waited(t, next, "remote"); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Fatalf("after starting again it is %v", ready.GetStatus())
	}
	volumes := s.sandbox(s.of("remote")).GetVolumes()
	if len(volumes) != 1 || volumes[0].GetSource() != sourcestest.Mounted(mountedBeneath, "remote", "services") || len(held.Holds()) != 1 {
		t.Errorf("its sandbox is given %v, held %d times; want what was held when it was asked for, once", volumes, len(held.Holds()))
	}
}

// Where a remote workspace is asked for with a task, the repositories
// its entities are in are held from the store too, by the names the
// workspace gives them, with the directories of its entities alone, and
// none is cloned: its supervisor is told where each entity is, and no
// repository to clone, and its sandbox reaches no forge. The control is
// the same entity with no task, which is cloned, alone and beside a
// stored repository.
func TestTheRepositoriesOfARemoteWorkspacesEntitiesAreHeldAndNotCloned(t *testing.T) {
	ctx := context.Background()
	s, held := over(t), sourcestest.New(mountedBeneath)
	fake := backstagetest.Serve(t, catalogOfTheTests)
	client, _, _ := served(t, s, t.TempDir(), func(c *Config) { resolving(t, fake)(c); serving(held)(c) })
	spec := byEntity("gateway")
	spec.Task = "fix-greeting"
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Spec: spec}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "gateway")
	want := sources.Workspace{Name: "gateway", Owner: whose, Stored: []sources.Stored{
		{Repository: "platform", Name: "platform", Roots: []string{"gateway"}, Task: "fix-greeting"},
		{Repository: "services", Name: "services", Roots: []string{"greet"}, Task: "fix-greeting"},
	}}
	if holds := held.Holds(); len(holds) != 1 || !same(holds[0], want) {
		t.Errorf("its entities' repositories were held as %+v, want %+v", holds, want)
	}
	told := toldAgain(t, s.supervisor(s.of("gateway")), 1)
	if len(told.GetRepositories()) != 0 || len(told.GetStored()) != 2 || len(told.GetEntities()) != 2 || told.GetEntities()[0].GetRepository() != "platform" {
		t.Errorf("its supervisor was told to clone %v, that %v are mounted, and that its entities are %v", told.GetRepositories(), told.GetStored(), told.GetEntities())
	}
	for _, rule := range s.sandbox(s.of("gateway")).GetEgress().GetRules() {
		if strings.Contains(rule.GetHost(), "forge.example") {
			t.Errorf("its sandbox reaches the forge, %s, and clones nothing from it", rule.GetHost())
		}
	}
	// Brought an entity more, it is refused: its sandbox is given its
	// repositories when it is made.
	more := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	more.Entities = append(more.Entities, &typev1.EntityRef{Name: "payments"})
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "gateway", Spec: more}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an entity added to a remote workspace: %v, want it refused as fixed", err)
	}

	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "cloned", Spec: byEntity("gateway")}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "cloned")
	if control := toldAgain(t, s.supervisor(s.of("cloned")), 1); len(control.GetRepositories()) != 2 || len(control.GetStored()) != 0 || len(held.Holds()) != 1 {
		t.Errorf("the control, with no task: its supervisor was told to clone %v and that %v are mounted", control.GetRepositories(), control.GetStored())
	}
	// And with no task, beside a stored repository at a ref: the entity's
	// repositories are cloned, and that one alone is held.
	beside := byEntity("gateway")
	beside.Stored = []*workspacev1.StoredRepository{{Repository: "tools", At: "release-1"}}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "beside", Spec: beside}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "beside")
	if mixed := toldAgain(t, s.supervisor(s.of("beside")), 1); len(mixed.GetRepositories()) != 2 || len(mixed.GetStored()) != 1 || mixed.GetStored()[0].GetName() != "tools" {
		t.Errorf("with no task, beside a stored repository: its supervisor was told to clone %v and that %v are mounted", mixed.GetRepositories(), mixed.GetStored())
	}
}

// What is asked of the store is held to what it must be before anything
// is asked of it: a stored repository at a ref and on a task both, one
// with no name the store gives, two in one directory of the workspace,
// one whose name there is no directory's, and a task or a landing with
// no stored repository are each refused, and nothing is held or made.
// The control is one asked for as it must be.
func TestWhatIsAskedOfTheStoreIsHeldBeforeItIsAsked(t *testing.T) {
	ctx := context.Background()
	s, held := over(t), sourcestest.New(mountedBeneath)
	client, _, _ := served(t, s, t.TempDir(), serving(held))
	stored := func(each ...*workspacev1.StoredRepository) *workspacev1.WorkspaceSpec {
		return &workspacev1.WorkspaceSpec{Stored: each}
	}
	both := of("https://forge.example/acme/services.git")
	both.Stored = stored(&workspacev1.StoredRepository{Repository: "services"}).GetStored()
	task := of("https://forge.example/acme/services.git")
	task.Task = "fix-greeting"
	for what, spec := range map[string]*workspacev1.WorkspaceSpec{
		"a ref and a task":                    stored(&workspacev1.StoredRepository{Repository: "services", At: "main", Task: "fix-greeting"}),
		"no name of the store's":              stored(&workspacev1.StoredRepository{Name: "services"}),
		"a name that is a path":               stored(&workspacev1.StoredRepository{Repository: "acme/services"}),
		"a name that climbs":                  stored(&workspacev1.StoredRepository{Repository: "services", Name: ".."}),
		"a directory outside it":              stored(&workspacev1.StoredRepository{Repository: "services", Roots: []string{"../elsewhere"}}),
		"two of one name":                     stored(&workspacev1.StoredRepository{Repository: "services"}, &workspacev1.StoredRepository{Repository: "other", Name: "services"}),
		"one a clone has the name of":         both,
		"a task and nothing of the store":     task,
		"a ref that is not written plainly":   stored(&workspacev1.StoredRepository{Repository: "services", At: "main\nelsewhere"}),
		"a name of the store's with a space":  stored(&workspacev1.StoredRepository{Repository: "the services", Name: "services"}),
		"a task that is not written plainly":  stored(&workspacev1.StoredRepository{Repository: "services", Task: "fix it"}),
		"a workspace's task not so written":   {Stored: stored(&workspacev1.StoredRepository{Repository: "services"}).GetStored(), Task: "fix it"},
		"the workspace's own record's place":  stored(&workspacev1.StoredRepository{Repository: "services", Name: ".frontseat"}),
		"a name with what a path is split by": stored(&workspacev1.StoredRepository{Repository: "services", Name: "a/b"}),
	} {
		_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: spec})
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "SPEC_INVALID" {
			t.Errorf("%s: %v (%s), want it refused as no spec", what, err, reason)
		}
	}
	if len(held.Holds()) != 0 || len(s.asked()) != 0 {
		t.Errorf("what was refused was held %d times and made the sandboxes %v", len(held.Holds()), s.asked())
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: stored(&workspacev1.StoredRepository{Repository: "acme/services", Name: "services", At: "main"})}); err != nil || len(held.Holds()) != 1 {
		t.Errorf("the control, one asked for as it must be: %v, held %d times", err, len(held.Holds()))
	}
}

// answers is what serves stored repositories answering a test's own way.
type answers struct {
	*sourcestest.Memory
	holds func(sources.Workspace) sources.Held
}

func (a answers) Hold(ctx context.Context, w sources.Workspace) (sources.Held, error) {
	if _, err := a.Memory.Hold(ctx, w); err != nil {
		return sources.Held{}, err
	}
	return a.holds(w), nil
}

// A workspace whose stored repositories could not be held is not made:
// it is refused UNAVAILABLE, STORE_UNAVAILABLE, with nothing of what was
// said of it, no sandbox is made and no workspace kept. What was held
// otherwise than asked, a directory that is none of the machine's named
// plainly, a repository that stands nowhere or one that was not asked
// for, reaches no sandbox either, and is let go of. The control is the
// same workspace once its repositories are held as asked.
func TestAWorkspaceWhoseStoredRepositoriesAreNotHeldAsAskedIsNotMade(t *testing.T) {
	ctx := context.Background()
	s, held := over(t), sourcestest.New(mountedBeneath)
	answering := answers{Memory: held}
	client, _, _ := served(t, s, t.TempDir(), func(c *Config) { c.Sources = &answering })
	const said = "s3cret-of-the-store"
	held.Fail(errors.New("the store said " + said))
	_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: onATask()})
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeUnavailable || reason != "STORE_UNAVAILABLE" || domain != refusal.Workspace || strings.Contains(err.Error(), said) {
		t.Errorf("a workspace whose repositories were not held: %v (%s of %s), want unavailable, STORE_UNAVAILABLE, and nothing of what was said", err, reason, domain)
	}
	held.Fail(nil)
	sound := func(w sources.Workspace) sources.Held {
		return sources.Held{
			Repositories: []sources.Standing{{Name: "services", Repository: "services", Changeset: "c1"}},
			Volumes:      []sources.Volume{{Repository: "services", Source: mountedBeneath + "/services"}},
		}
	}
	change := func(how func(*sources.Held)) func(sources.Workspace) sources.Held {
		return func(w sources.Workspace) sources.Held {
			out := sound(w)
			how(&out)
			return out
		}
	}
	for what, holds := range map[string]func(sources.Workspace) sources.Held{
		"a directory that climbs":       change(func(h *sources.Held) { h.Volumes[0].Source = mountedBeneath + "/../../etc" }),
		"a directory named relatively":  change(func(h *sources.Held) { h.Volumes[0].Source = "services" }),
		"the whole machine":             change(func(h *sources.Held) { h.Volumes[0].Source = "/" }),
		"no directory":                  change(func(h *sources.Held) { h.Volumes = nil }),
		"a directory for another":       change(func(h *sources.Held) { h.Volumes[0].Repository = "another" }),
		"a second directory":            change(func(h *sources.Held) { h.Volumes = append(h.Volumes, h.Volumes[0]) }),
		"a repository standing nowhere": change(func(h *sources.Held) { h.Repositories = nil }),
		"a repository at no changeset":  change(func(h *sources.Held) { h.Repositories[0].Changeset = "" }),
		"a changeset of two lines":      change(func(h *sources.Held) { h.Repositories[0].Changeset = "c1\n--flag" }),
		"a repository not asked for":    change(func(h *sources.Held) { h.Repositories[0].Name = "another" }),
	} {
		answering.holds = holds
		before := len(held.Releases())
		_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: onATask()})
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("%s: %v, want it refused", what, err)
		}
		if len(held.Releases()) != before+1 {
			t.Errorf("%s: what was held was not let go of", what)
		}
	}
	if len(s.asked()) != 0 {
		t.Errorf("what was not held as asked made the sandboxes %v", s.asked())
	}
	if _, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "remote"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a workspace that was not made is there: %v", err)
	}
	answering.holds = sound
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "remote", Spec: onATask()}); err != nil {
		t.Fatalf("the control, its repositories held as asked: %v", err)
	}
	if ready := waited(t, client, "remote"); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || len(s.asked()) != 1 {
		t.Errorf("the control is %v", ready.GetStatus())
	}
}
