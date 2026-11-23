// Package build_test states what frontseat supports of workspaces and
// builds, as specs a person can read, and proves each one against a real
// daemon on the real grid.
//
// A spec acts as whoever uses frontseat does: through the build API and
// nothing beneath it. It knows the API and not how the daemon is made, so
// the specs hold whatever the daemon is made of. This file is what the
// specs are written with; the files beside it are the specs.
package build_test

import "github.com/frontseat-dev/frontseat/sdk/go/entity"

import (
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"bytes"
	"encoding/hex"
	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/specs/buildtest"
	"github.com/frontseat-dev/frontseat/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/testsupport/testtelemetry"
	"github.com/frontseat-dev/frontseat/testsupport/testwire"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

// A scenario binds assertions and fixture helpers to one native Go test.
// Only immutable fixture data is shared between tests.
type scenario struct {
	testing.TB
	*WithT
	company *Source
	// rewrites are what each daemon of the scenario is told to fetch a
	// download from elsewhere by: a spec's own origin, where it counts
	// what is asked (testorigin).
	rewrites map[string]string
}

var suiteDirectory string
var companyMu sync.Mutex
var companyForge testforge.Forge

// companyBackstage is the fake Backstage of the company's entities, which
// every spec that changes nothing of it shares; it is served until the
// specs end.
var companyBackstage backstagetest.Served

func TestMain(m *testing.M) {
	testfiles.Suite("frontseat-build-specs-", func(root string) int {
		suiteDirectory = root
		exit := m.Run()
		if companyBackstage.URL != "" {
			companyBackstage.Close()
		}
		return exit
	})
}

func newScenario(t *testing.T) *scenario {
	t.Helper()
	tb := testrequired.TB(t)
	testgrid.Address(tb)
	companyMu.Lock()
	defer companyMu.Unlock()
	if companyForge.Dir == "" {
		dir, err := os.MkdirTemp(suiteDirectory, "company-")
		if err != nil {
			t.Fatal(err)
		}
		// Publish only after successful setup; Fatal must not poison a Once.
		forge := testforge.MakeAt(tb, dir, "file://"+dir)
		companyBackstage, _ = backstagetest.Start(tb, forge.Entities(tb))
		companyForge = forge
	}
	spec := &scenario{TB: tb, WithT: NewWithT(t)}
	spec.company = &Source{scenario: spec, Forge: companyForge, backstage: &companyBackstage}
	return spec
}

// company is a small company's source, which every spec that changes
// nothing of it shares.
//
// The repository "services" holds the entities hello and greet, each in a
// directory of its own name, and a docs directory that belongs to no
// entity; hello depends on greet. The repository "platform" holds gateway
// and log, and a notes directory; gateway depends on greet, which is in
// the other repository. The system storefront is hello and gateway; the
// team team-edge owns gateway and log. The catalog also knows unplaced,
// and does not say where its source is.
//
// The repository "lab" holds entities that misbehave on purpose: broken,
// whose code the linter refuses and nothing else does; failing, which has
// a test that fails; slow, whose test takes two minutes; caller, whose
// test fails if it can open a connection; snoop, whose test fails if it
// can reach the runner of its sandbox or write its slot. It also holds
// entities with actions of their own, which own_actions_test.go and
// needs_test.go describe. The repository "bare" pins no tools, and holds
// unpinned.

// source is a company's source: its repositories and its catalog.
type Source struct {
	*scenario

	testforge.Forge
	// backstage is the fake Backstage its entities are resolved through;
	// nil until a spec first resolves one.
	backstage *backstagetest.Served
	// plugins are the plugins a repository lists and the lines that set
	// their options, where a spec changed what it lists.
	plugins map[string]map[string][]string
	// releases are the lines an entity's catalog file says of how it is
	// released, under spec.frontseat.release, where a spec said any.
	releaseSays map[string][]string
	// required are the components an entity's module requires beyond
	// those the company's does, where a spec added any.
	required map[string][]string
	// joined are the components a spec put in a system the company does
	// not have, each with that system; domainOf is each such system's
	// domain.
	joined, domainOf map[string]string
	// subdomainOf is the domain each domain a spec made a subdomain is
	// part of, as spec.subdomainOf says.
	subdomainOf map[string]string
	// elsewhere are the domains a spec has described in platform's
	// catalog, not in services'.
	elsewhere map[string]bool
}

// changedSource returns source of a spec's own, to change before a
// workspace is made from it.
func (spec *scenario) changedSource() *Source {
	spec.Helper()
	return &Source{scenario: spec, Forge: testforge.Make(spec.TB), plugins: map[string]map[string][]string{}, releaseSays: map[string][]string{}, required: map[string][]string{}, joined: map[string]string{}, domainOf: map[string]string{}, subdomainOf: map[string]string{}, elsewhere: map[string]bool{}}
}

// says has a file of one of the source's repositories hold the content,
// as a commit on main, and commits nothing where it holds it already: a
// fixture says the same thing more than once as it is put together.
func (s *Source) says(repository, file, content string) {
	s.scenario.Helper()
	held, err := os.ReadFile(filepath.Join(s.Dir, repository, filepath.FromSlash(file)))
	if err == nil && string(held) == content {
		return
	}
	s.Write(s.scenario.TB, repository, file, content)
}

// listing writes a repository's frontseat.yaml from what the spec said.
func (s *Source) listing(repository string) {
	s.scenario.Helper()
	var file strings.Builder
	file.WriteString("plugins:")
	if len(s.plugins[repository]) == 0 {
		file.WriteString(" {}")
	}
	file.WriteString("\n")
	names := make([]string, 0, len(s.plugins[repository]))
	for name := range s.plugins[repository] {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(s.plugins[repository][name]) == 0 {
			file.WriteString("  " + name + ": {}\n")
			continue
		}
		file.WriteString("  " + name + ":\n")
		for _, line := range s.plugins[repository][name] {
			file.WriteString("    " + line + "\n")
		}
	}
	s.says(repository, "frontseat.yaml", file.String())
}

// lists makes a repository list a plugin, beside the Go plugin it lists
// already, with a line that sets one of its options where one is given.
func (s *Source) lists(repository, plugin string, setting ...string) *Source {
	s.scenario.Helper()
	if s.plugins[repository] == nil {
		s.plugins[repository] = map[string][]string{"go": nil, "backstage": nil}
	}
	s.plugins[repository][plugin] = append(s.plugins[repository][plugin], setting...)
	s.listing(repository)
	return s
}

// sets makes a repository set an option of the Go plugin.
func (s *Source) sets(repository, option, value string) *Source {
	s.scenario.Helper()
	return s.lists(repository, "go", option+": "+value)
}

// listsNoPlugins makes a repository list no plugin at all.
func (s *Source) listsNoPlugins(repository string) *Source {
	s.scenario.Helper()
	s.plugins[repository] = map[string][]string{}
	s.listing(repository)
	return s
}

// saysNothing takes a repository's frontseat.yaml away.
func (s *Source) saysNothing(repository string) *Source {
	s.scenario.Helper()
	s.Remove(s.scenario.TB, repository, "frontseat.yaml")
	return s
}

// homes are where the company's entities live: repository and directory.
var homes = map[string][2]string{
	"hello":   {"services", "hello"},
	"greet":   {"services", "greet"},
	"gateway": {"platform", "gateway"},
	"log":     {"platform", "log"},
	"banner":  {"services", "banner"},
	"ticker":  {"services", "ticker"},
}

// entitySets makes an entity set an option of the Go plugin, in its own
// catalog file.
func (s *Source) entitySets(entity, option, value string) *Source {
	s.scenario.Helper()
	home := homes[entity]
	// gateway and hello call greet, and their catalog files say so, as
	// frontseat would have written them.
	depends := ""
	if entity == "gateway" || entity == "hello" {
		depends = "  dependsOn: [component:greet]\n"
	}
	s.Write(s.scenario.TB, home[0], home[1]+"/catalog-info.yaml",
		"apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+entity+
			"\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n"+depends+"  frontseat:\n    plugins:\n      go:\n        "+option+": "+value+"\n")
	return s
}

// entitySays gives an entity a catalog file of its own, in its
// directory, whose spec holds the lines given.
func (s *Source) entitySays(entity string, spec ...string) *Source {
	s.scenario.Helper()
	home := homes[entity]
	file := "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: " + entity + "\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n"
	for _, line := range spec {
		file += "  " + line + "\n"
	}
	s.Write(s.scenario.TB, home[0], home[1]+"/catalog-info.yaml", file)
	return s
}

// workspace is a daemon and the source it holds.
type Workspace struct {
	*scenario

	source *Source
	// at is the daemon's build API; directory is where its source is.
	at, directory string
}

// platformTimeoutWorkspace is a workspace of gateway where platform sets
// test-timeout to 2m.
func (spec *scenario) platformTimeoutWorkspace() *Workspace {
	spec.Helper()
	return spec.changedSource().sets("platform", "test-timeout", "2m").workspaceOf("gateway")
}

// outOfStepGateway is a workspace of gateway whose catalog file says
// nothing of what it depends on.
func (spec *scenario) outOfStepGateway() *Workspace {
	spec.Helper()
	return spec.changedSource().entitySays("gateway").workspaceOf("gateway")
}

// chainWithActions is a build of chain whose catalog file says the
// actions given, the lines beneath frontseat's actions.
func (spec *scenario) chainWithActions(actions string) *Build {
	spec.Helper()
	source := spec.changedSource()
	source.Write(spec.TB, "lab", "chain/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: chain\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n"+actions)
	return source.workspaceOf("chain").build()
}

// firstRetainedToolRun is the first build of tool's compile, in a
// workspace of tool whose builds are observed, after main.go was given
// words no build has seen. It holds that the compile was executed, that
// the evaluation asked the plugins and kept nothing from before, that the
// plan was not retained, and that the trace has real dependency edges.
func (spec *scenario) firstRetainedToolRun(compile string) (*testtelemetry.Collector, *Workspace, *Build, map[string]*tracepb.Span) {
	spec.Helper()
	collector := spec.observeBuilds()
	w := spec.workspaceOf("tool")
	w.write("lab/tool/main.go", toolMain(rand.Text()))
	first := w.build(of("./lab/tool"), only("compile"))
	expectRetainedBuildSucceeded(first)
	spec.Expect(first.cameFrom(compile)).To(Equal("executed"))
	firstStages := spec.retainedRunStages(collector, first)
	spec.expectEvaluateStage(firstStages.evaluate, false, true)
	spec.Expect(spec.requiredSpanBool(firstStages.plan, "retained")).To(BeFalse())
	spec.Expect(spec.fetches(first)).NotTo(BeEmpty(), "the dependency trace control must have real edges")
	firstActions := spec.actionSpans(collector, first)
	spec.expectTraceDependencies(first, firstActions)
	return collector, w, first, firstActions
}

// gatewayForEachProfile is a workspace of gateway built for development
// and then for production.
func (spec *scenario) gatewayForEachProfile() (forDevelopment, forProduction *Build) {
	spec.Helper()
	workspace := spec.workspaceOf("gateway")
	return workspace.build(), workspace.build(production())
}

// locksPflagWrongly gives tool's go.sum a checksum of pflag that is not
// pflag's.
func (w *Workspace) locksPflagWrongly() *Workspace {
	w.scenario.Helper()
	return w.write("lab/tool/go.sum", w.scenario.wrongChecksum(testforge.ToolLock, "github.com/spf13/pflag", false))
}

// misspeltOptionBuild is a build of gateway where platform sets
// test-timout, an option the go plugin does not take.
func (spec *scenario) misspeltOptionBuild() *Build {
	spec.Helper()
	return spec.changedSource().sets("platform", "test-timout", "2m").workspaceOf("gateway").build()
}

// emptyWorkspace returns a workspace that holds nothing yet.
func (s *Source) emptyWorkspace() *Workspace {
	s.scenario.Helper()
	directory := s.scenario.TB.TempDir()
	return &Workspace{scenario: s.scenario, source: s, directory: directory, at: buildtest.Start(s.scenario.TB, directory)}
}

func (spec *scenario) emptyWorkspace() *Workspace {
	spec.Helper()
	return spec.company.emptyWorkspace()
}

func (spec *scenario) jestWorkspace() *Workspace {
	spec.Helper()
	directory := spec.TB.TempDir()
	testpins.Node(spec.TB, directory)
	w := &Workspace{scenario: spec, directory: directory, at: buildtest.Mounted(spec.TB, directory)}
	w.write("frontseat.yaml", "plugins: {npm: {}, jest: {}, backstage: {}}\n")
	spec.addJestSuite(w)
	return w
}

func (spec *scenario) addJestSuite(w *Workspace) {
	spec.Helper()
	w.write("web/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: web}\nspec: {type: service, lifecycle: production, owner: team}\n")
	files, err := os.ReadDir("testdata/jest")
	spec.Expect(err).NotTo(HaveOccurred())
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join("testdata/jest", file.Name()))
		spec.Expect(err).NotTo(HaveOccurred())
		w.write("web/"+file.Name(), string(content))
	}
	w.write("web/run.test.cjs", fmt.Sprintf("test('this run', () => console.log(%q));\n", fmt.Sprint(time.Now().UnixNano())))
}

// entities reads entities as a spec names them: by name alone, a
// component, or with their kind before it, as in "system:storefront".
func (spec *scenario) entities(names ...string) []*typev1.EntityRef {
	spec.Helper()
	var out []*typev1.EntityRef
	for _, name := range names {
		ref, err := entity.Parse(name)
		spec.Expect(err).NotTo(HaveOccurred())
		out = append(out, entity.Proto(ref))
	}
	return out
}

// bring has the daemon bring what a request names, and returns why it
// would not.
func (w *Workspace) bring(request *workspacev1.WorkspaceSpec) error {
	// The control plane resolves entities through its Backstage and tells
	// a daemon what to hold, and no client does: the test support does
	// both here, through a fake Backstage of the source's entities.
	var catalog backstagetest.Served
	if w.source != nil {
		catalog = w.source.catalog()
	}
	return buildtest.Bring(context.Background(), w.at, request, catalog)
}

// catalog is the fake Backstage the source's entities are resolved
// through, served from when it is first asked for until the spec ends.
func (s *Source) catalog() backstagetest.Served {
	s.scenario.Helper()
	if s.backstage == nil {
		served := backstagetest.Serve(s.scenario.TB, s.Entities(s.scenario.TB))
		s.backstage = &served
	}
	return *s.backstage
}

// daemon is the workspace's daemon, as the SDK reaches it.
func (w *Workspace) daemon() *frontseat.Workspace {
	fs, err := frontseat.New(w.at)
	if err != nil {
		w.Fatal(err)
	}
	return fs.Here()
}

// buildsAt is the builds of the daemon at an endpoint, over the wire,
// for what a spec asks that the SDK does not serve: a request's own
// fields, a filter.
func buildsAt(at string) buildv1alpha1connect.BuildServiceClient {
	return buildv1alpha1connect.NewBuildServiceClient(testwire.Conn(at, nil))
}

// releasesAt is the releases of the daemon at an endpoint, over the
// wire.
func releasesAt(at string) buildv1alpha1connect.ReleaseServiceClient {
	return buildv1alpha1connect.NewReleaseServiceClient(testwire.Conn(at, nil))
}

// add brings more entities into a workspace that holds some.
func (w *Workspace) add(names ...string) *Workspace {
	w.scenario.Helper()
	w.scenario.Expect(w.bring(&workspacev1.WorkspaceSpec{Entities: w.scenario.entities(names...)})).To(Succeed())
	return w
}

// makingAWorkspaceOf is why a workspace could not be made from entities;
// nil where it could.
func (spec *scenario) makingAWorkspaceOf(names ...string) error {
	spec.Helper()
	return spec.emptyWorkspace().bring(&workspacev1.WorkspaceSpec{Entities: spec.entities(names...)})
}

// BeRefusedSaying holds of a refusal that says a thing.
func BeRefusedSaying(claim string) OmegaMatcher { return MatchError(ContainSubstring(claim)) }

// workspaceOf returns a workspace made from entities: of each repository
// that holds them, the directories they need.
func (s *Source) workspaceOf(names ...string) *Workspace {
	s.scenario.Helper()
	return s.emptyWorkspace().add(names...)
}

func (spec *scenario) workspaceOf(names ...string) *Workspace {
	spec.Helper()
	return spec.company.workspaceOf(names...)
}

func (spec *scenario) catalogWorkspace() *Workspace {
	spec.Helper()
	source := spec.changedSource().lists("platform", "backstage")
	source.Write(spec.TB, "platform", "gateway/catalog-info.yaml", catalogFixture)
	return source.workspaceOf("gateway")
}

const catalogFixture = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: gateway}
spec:
  type: service
  lifecycle: production
  owner: team
  system: shop
  providesApis: [orders]
  dependsOn: [component:greet, resource:database]
---
apiVersion: backstage.io/v1alpha1
kind: Group
metadata: {name: team}
spec: {type: team, parent: org, children: [], members: [alex]}
---
apiVersion: backstage.io/v1alpha1
kind: Group
metadata: {name: org}
spec: {type: organization, children: [team]}
---
apiVersion: backstage.io/v1alpha1
kind: User
metadata: {name: alex}
spec: {memberOf: [team]}
---
apiVersion: backstage.io/v1alpha1
kind: System
metadata: {name: shop}
spec: {owner: team, domain: commerce}
---
apiVersion: backstage.io/v1alpha1
kind: Domain
metadata: {name: commerce}
spec: {owner: team}
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata: {name: orders}
spec: {type: openapi, lifecycle: production, owner: team, system: shop, definition: openapi-document}
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: database}
spec: {type: database, owner: team, system: shop}
`

func (w *Workspace) catalogEntities() map[string]*buildv1.Entity {
	w.scenario.Helper()
	listed, err := frontseat.Collect(w.daemon().Entities(context.Background()))
	w.scenario.Expect(err).NotTo(HaveOccurred())
	result := map[string]*buildv1.Entity{}
	for _, catalogEntity := range listed {
		ref := entity.FromProto(catalogEntity.GetRef()).String()
		w.scenario.Expect(result).NotTo(HaveKey(ref), "catalog has a duplicate entity")
		result[ref] = catalogEntity
	}
	return result
}

// wholeWorkspaceOf returns a workspace made from entities, with their
// repositories whole.
func (spec *scenario) wholeWorkspaceOf(names ...string) *Workspace {
	spec.Helper()
	w := spec.emptyWorkspace()
	spec.Expect(w.bring(&workspacev1.WorkspaceSpec{Entities: spec.entities(names...), WholeRepositories: true})).To(Succeed())
	return w
}

// workspaceOfRepository returns a workspace made from a repository named
// directly.
func (spec *scenario) workspaceOfRepository(name string) *Workspace {
	spec.Helper()
	w := spec.emptyWorkspace()
	spec.Expect(w.bring(&workspacev1.WorkspaceSpec{
		Repositories: []*workspacev1.RepositorySource{{Remote: w.source.Repository(name), Ref: "main"}},
	})).To(Succeed())
	return w
}

// workspaceSomethingElseMade returns a workspace made as a platform makes
// one before a daemon starts: it clones what an entity needs and writes
// down what it brought, and only then is the daemon started.
func (spec *scenario) workspaceSomethingElseMade(entity, repository string) *Workspace {
	spec.Helper()
	directory := spec.TB.TempDir()
	remote, at := spec.company.Repository(repository), filepath.Join(directory, repository)
	spec.git(directory, "clone", "--quiet", "--filter=blob:none", "--sparse", remote, at)
	spec.git(at, "sparse-checkout", "set", "--cone", "--", entity)
	commit := spec.git(at, "rev-parse", "HEAD")
	manifest := "version: 1\nrepositories:\n  - name: " + repository +
		"\n    remote: " + remote + "\n    commit: " + commit + "\n    roots:\n      - " + entity + "\n"
	spec.Expect(os.MkdirAll(filepath.Join(directory, ".frontseat"), 0o755)).To(Succeed())
	spec.Expect(os.WriteFile(filepath.Join(directory, ".frontseat", "workspace.yaml"), []byte(manifest), 0o644)).To(Succeed())
	return &Workspace{scenario: spec, source: spec.company, directory: directory, at: buildtest.Start(spec.TB, directory)}
}

func (spec *scenario) git(directory string, args ...string) string {
	spec.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	out, err := command.CombinedOutput()
	spec.Expect(err).NotTo(HaveOccurred(), "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// held is what the workspace says it holds, by repository.
func (w *Workspace) held() map[string]*workspacev1.Repository {
	w.scenario.Helper()
	got, err := buildtest.Held(context.Background(), w.at)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	out := map[string]*workspacev1.Repository{}
	for _, r := range got.GetRepositories() {
		out[r.GetName()] = r
	}
	return out
}

// repositories are the repositories the workspace holds, sorted.
func (w *Workspace) repositories() []string {
	w.scenario.Helper()
	var names []string
	for name := range w.held() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// directoriesOf are the directories of a repository the workspace holds;
// none, for a repository it holds whole or not at all.
func (w *Workspace) directoriesOf(repository string) []string {
	w.scenario.Helper()
	return w.held()[repository].GetRoots()
}

// holdsAllOf is whether the workspace holds a repository whole.
func (w *Workspace) holdsAllOf(repository string) bool {
	w.scenario.Helper()
	held, there := w.held()[repository]
	return there && len(held.GetRoots()) == 0
}

// has is whether a file or directory is in the workspace.
func (w *Workspace) has(path string) bool {
	_, err := os.Stat(filepath.Join(w.directory, filepath.FromSlash(path)))
	return err == nil
}

// file is what a file of the workspace says.
func (w *Workspace) file(path string) string {
	w.scenario.Helper()
	content, err := os.ReadFile(filepath.Join(w.directory, filepath.FromSlash(path)))
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(string(content))
}

// write changes or adds a file of the workspace, as someone working in
// it does.
func (w *Workspace) write(path, content string) *Workspace {
	w.scenario.Helper()
	at := filepath.Join(w.directory, filepath.FromSlash(path))
	w.scenario.Expect(os.MkdirAll(filepath.Dir(at), 0o755)).To(Succeed())
	w.scenario.Expect(os.WriteFile(at, []byte(content+"\n"), 0o644)).To(Succeed())
	return w
}

// remove deletes a file of the workspace.
func (w *Workspace) remove(path string) *Workspace {
	w.scenario.Helper()
	w.scenario.Expect(os.Remove(filepath.Join(w.directory, filepath.FromSlash(path)))).To(Succeed())
	return w
}

// commit commits what was changed, in each repository, and pushes
// nothing.
func (w *Workspace) commit() *Workspace {
	w.scenario.Helper()
	for name := range w.held() {
		w.scenario.git(filepath.Join(w.directory, name), "add", "--all")
		w.scenario.git(filepath.Join(w.directory, name), "-c", "user.name=someone", "-c", "user.email=someone@example.com", "commit", "--quiet", "--allow-empty", "-m", "work in progress")
	}
	return w
}

// setting is an option as a project is built with it: its value, and the
// file that sets it, or "the plugin" where nothing does.
type Setting struct {
	value any
	from  string
}

// projectFailure observes a rejected graph request over the public API.
func (w *Workspace) projectFailure() *statuspb.Status {
	w.scenario.Helper()
	_, err := frontseat.Collect(w.daemon().Projects(context.Background()))
	w.scenario.Expect(err).To(HaveOccurred())
	var failure *connect.Error
	w.scenario.Expect(errors.As(err, &failure)).To(BeTrue())
	status := &statuspb.Status{Code: int32(failure.Code()), Message: failure.Message()}
	for _, detail := range failure.Details() {
		status.Details = append(status.Details, connectproto.ErrorDetailToAny(detail))
	}
	return status
}

// option returns an option of the Go plugin as a project is built with
// it.
func (w *Workspace) option(project, name string) Setting {
	w.scenario.Helper()
	for _, found := range w.projects() {
		if found.GetRef().GetRepository()+"/"+found.GetRef().GetRoot() != project || found.GetRef().GetPlugin() != "go" {
			continue
		}
		for _, held := range found.GetOptions() {
			if held.GetName() == name {
				// Where it is set is said from the workspace: its
				// repository's name and the file.
				from := "the plugin"
				if at := held.GetOrigin(); at != nil {
					from = path.Join(at.GetRepository(), at.GetPath())
				}
				value := held.GetValue()
				switch v := value.GetValue().(type) {
				case *typev1.OptionValue_StringValue:
					return Setting{value: v.StringValue, from: from}
				case *typev1.OptionValue_BoolValue:
					return Setting{value: v.BoolValue, from: from}
				case *typev1.OptionValue_IntValue:
					return Setting{value: v.IntValue, from: from}
				case *typev1.OptionValue_StringListValue:
					return Setting{value: v.StringListValue.GetValues(), from: from}
				default:
					w.scenario.Fatal("graph option has no typed value")
				}
			}
		}
	}
	w.scenario.Fatal(fmt.Sprintf("%s is built with no option %s", project, name))
	return Setting{}
}

// Requirement is something a project requires, as the workspace says it
// when asked: what, where the ecosystem's own files say so, and the
// project of the workspace that provides it, or "outside the workspace".
type Requirement struct{ what, saidAt, at, providedBy string }

// dependsOn are the entities an entity depends on, as references, as
// the workspace says when asked. Nothing is run to ask.
func (w *Workspace) dependsOn(entity string) []string {
	w.scenario.Helper()
	listed, err := frontseat.Collect(w.daemon().Entities(context.Background()))
	w.scenario.Expect(err).NotTo(HaveOccurred())
	for _, found := range listed {
		if found.GetRef().GetName() != entity {
			continue
		}
		var out []string
		for _, on := range found.GetDependsOn() {
			out = append(out, written(on))
		}
		return out
	}
	w.scenario.Fatal("the workspace holds no entity " + entity)
	return nil
}

// ownerOf is the entity that owns a project, as its reference.
func (w *Workspace) ownerOf(project string) string {
	w.scenario.Helper()
	for _, found := range w.projects() {
		if found.GetRef().GetRepository()+"/"+found.GetRef().GetRoot() == project {
			return written(found.GetOwner())
		}
	}
	w.scenario.Fatal("the workspace holds no project " + project)
	return ""
}

// requires are what a project requires. Nothing is run to ask.
func (w *Workspace) requires(project string) []Requirement {
	w.scenario.Helper()
	for _, found := range w.projects() {
		if found.GetRef().GetRepository()+"/"+found.GetRef().GetRoot() != project {
			continue
		}
		var out []Requirement
		for _, required := range found.GetRequires() {
			by := "outside the workspace"
			if provider := required.GetProviderProject(); provider != nil {
				by = filepath.ToSlash(filepath.Join(provider.GetRepository(), provider.GetRoot()))
			}
			out = append(out, Requirement{what: required.GetCoordinate(), saidAt: fmt.Sprintf("%s:%d", path.Join(required.GetOrigin().GetRepository(), required.GetOrigin().GetPath()), required.GetOrigin().GetLine()), at: required.GetVersion(), providedBy: by})
		}
		return out
	}
	w.scenario.Fatal("the workspace holds no project " + project)
	return nil
}

// asking is how a build is asked for.
type asking func(*buildv1.CreateBuildRequest)

// production asks for a build as what will be released is built.
func production() asking { return withProfile(typev1.Profile_PROFILE_PRODUCTION) }

// withProfile asks for a build with a profile, by name.
func withProfile(profile typev1.Profile) asking {
	return func(r *buildv1.CreateBuildRequest) { r.Profile = profile }
}

// fixing asks for what is out of step to be written as it should be,
// and not to fail the build.
func fixing() asking {
	return func(r *buildv1.CreateBuildRequest) { r.Fix = true }
}

// of points a build at things: an entity's name, a path, or an action as
// it is printed.
func of(things ...string) asking {
	return func(r *buildv1.CreateBuildRequest) { r.Targets = things }
}

// only keeps the actions of the names given.
func only(names ...string) asking {
	return func(r *buildv1.CreateBuildRequest) { r.Only = names }
}

// testsOnly keeps the actions that run tests, as their plugins say, and
// what they need: what `frontseat test` asks for.
func testsOnly() asking {
	return func(r *buildv1.CreateBuildRequest) { r.Tests = true }
}

// build is a build, and the workspace it was asked of.
type Build struct {
	*scenario

	of  *Workspace
	run *buildv1.Build
}

// ask asks for a build and returns at once, or says why it was refused.
func (w *Workspace) ask(how ...asking) (*Build, error) {
	request := &buildv1.CreateBuildRequest{}
	for _, each := range how {
		each(request)
	}
	created, err := buildsAt(w.at).CreateBuild(context.Background(), request)
	return &Build{scenario: w.scenario, of: w, run: created.GetBuild()}, err
}

// start asks for a build and leaves it running. Whatever the spec goes
// on to do, the build is not left running when the specs are over.
func (w *Workspace) start(how ...asking) *Build {
	w.scenario.Helper()
	b, err := w.ask(how...)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	w.scenario.Cleanup(func() {
		_, _ = buildsAt(w.at).CancelBuild(context.Background(), &buildv1.CancelBuildRequest{BuildId: b.run.GetId()})
	})
	return b
}

// build asks for a build and follows it to its end.
func (w *Workspace) build(how ...asking) *Build {
	w.scenario.Helper()
	b, err := w.ask(how...)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return b.ended()
}

// buildFor is a build given longer than most to end: this repository's
// own, whose Rust is compiled from its lock for every action.
func (w *Workspace) buildFor(limit time.Duration, how ...asking) *Build {
	w.scenario.Helper()
	b, err := w.ask(how...)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return b.endedWithin(limit)
}

// succeeded holds that the build succeeded, saying what it diagnosed
// where it did not.
func (b *Build) succeeded() *Build {
	b.scenario.Helper()
	b.scenario.Expect(b.outcome()).To(Equal("SUCCESS"), b.diagnostics())
	return b
}

// ended follows the build to its end.
func (b *Build) ended() *Build {
	b.scenario.Helper()
	return b.endedWithin(5 * time.Minute)
}

// endedWithin follows the build to its end, or fails when it has not
// ended within the limit.
func (b *Build) endedWithin(limit time.Duration) *Build {
	b.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	ended, err := b.of.daemon().BuildByID(b.run.GetId()).Wait(ctx)
	b.scenario.Expect(err).NotTo(HaveOccurred())
	b.run = ended
	return b
}

// cancel stops the build, and follows it to its end.
func (b *Build) cancel() *Build {
	b.scenario.Helper()
	_, err := b.of.daemon().BuildByID(b.run.GetId()).Cancel(context.Background())
	b.scenario.Expect(err).NotTo(HaveOccurred())
	return b.ended()
}

// outcome is how the build ended, in Bazel's names: SUCCESS,
// BUILD_FAILURE, TESTS_FAILED, INTERRUPTED.
func (b *Build) outcome() string {
	return strings.TrimPrefix(b.run.GetExitCode().String(), "EXIT_CODE_")
}

// says is what the build says of why it ended as it did.
func (b *Build) says() string { return b.run.GetFailure() }

// passed is how many of its actions passed.
func (b *Build) passed() int { return int(b.run.GetCounts().GetPassed()) }

// ranOnAWorker is how many of its actions a worker ran, the rest being
// answered from what the grid kept.
func (b *Build) ranOnAWorker() int { return int(b.run.GetCost().GetActionsExecuted()) }

// profile is the name of the profile it was built with.
func (b *Build) profile() typev1.Profile { return b.run.GetProfile() }

// askingAfter is why nothing could be said of a build, by its name.
func (w *Workspace) askingAfter(id string) error {
	_, err := w.daemon().BuildByID(id).Describe(context.Background())
	return err
}

// state is what the build is doing now.
func (b *Build) state() string {
	b.scenario.Helper()
	now, err := b.of.daemon().BuildByID(b.run.GetId()).Describe(context.Background())
	b.scenario.Expect(err).NotTo(HaveOccurred())
	return strings.ToLower(strings.TrimPrefix(now.GetState().String(), "STATE_"))
}

// results are what the build says of its actions: those that ended in
// any of the outcomes, or all where none is given.
func (b *Build) results(outcomes ...buildv1.Outcome) []*buildv1.ActionResult {
	b.scenario.Helper()
	// Every page of them.
	var all []*buildv1.ActionResult
	for token := ""; ; {
		listed, err := buildsAt(b.of.at).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, PageToken: token, BuildId: b.run.GetId()})
		b.scenario.Expect(err).NotTo(HaveOccurred())
		for _, action := range listed.GetActions() {
			if len(outcomes) == 0 || slices.Contains(outcomes, action.GetOutcome()) {
				all = append(all, action)
			}
		}
		if token = listed.GetNextPageToken(); token == "" {
			return all
		}
	}
}

// actions are the actions the build ran, as they are printed, sorted.
func (b *Build) actions() []string {
	b.scenario.Helper()
	return labels(b.results())
}

// failed are the actions that failed, sorted.
func (b *Build) failed() []string {
	b.scenario.Helper()
	return labels(b.results(buildv1.Outcome_OUTCOME_FAILED))
}

func labels(results []*buildv1.ActionResult) []string {
	var out []string
	for _, result := range results {
		out = append(out, result.GetLabel())
	}
	sort.Strings(out)
	return out
}

// result is what the build says of one of its actions.
func (b *Build) result(label string) *buildv1.ActionResult {
	b.scenario.Helper()
	for _, result := range b.results() {
		if result.GetLabel() == label {
			return result
		}
	}
	b.scenario.Fatal("the build ran no action " + label)
	return nil
}

// outcomeOf is how one of the build's actions ended: passed, failed,
// skipped, or error; unspecified for one that never ended.
func (b *Build) outcomeOf(label string) string {
	b.scenario.Helper()
	return strings.ToLower(strings.TrimPrefix(b.result(label).GetOutcome().String(), "OUTCOME_"))
}

// cameFrom is where the result of one of the build's actions came from:
// executed, by a worker; grid_cache; or daemon, which did it itself.
func (b *Build) cameFrom(label string) string {
	b.scenario.Helper()
	return strings.ToLower(strings.TrimPrefix(b.result(label).GetProvenance().String(), "PROVENANCE_"))
}

// printed is what one of the build's actions printed, each line after
// the last.
func (b *Build) printed(label string) string {
	b.scenario.Helper()
	logs, err := frontseat.Collect(b.of.daemon().BuildByID(b.run.GetId()).Logs(context.Background(), label))
	b.scenario.Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, line := range logs {
		out = append(out, line.GetText())
	}
	return strings.Join(out, "\n")
}

// asked are the addresses a fetch asked, as the go command printed them:
// each line that begins "# get".
func asked(printed string) []string {
	var out []string
	for _, line := range strings.Split(printed, "\n") {
		if address, got := strings.CutPrefix(line, "# get "); got {
			out = append(out, strings.Fields(address)[0])
		}
	}
	return out
}

// needsOf are the actions one of the build's actions waits for, as the
// build planned it.
func (b *Build) needsOf(label string) []string {
	b.scenario.Helper()
	for _, event := range b.events(0) {
		for _, planned := range event.GetPlanned().GetActions() {
			if planned.GetLabel() == label {
				return planned.GetNeeds()
			}
		}
	}
	b.scenario.Fatal("the build planned no action " + label)
	return nil
}

// wrongChecksum is a lock that says a module's archive or metadata is something
// other than what was published: its checksum, with its first four
// characters changed. It is written as a workspace's file is.
func (spec *scenario) wrongChecksum(lock, module string, metadata bool) string {
	spec.Helper()
	var out []string
	changed := false
	for _, line := range strings.Split(strings.TrimSuffix(lock, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == module && strings.HasSuffix(fields[1], "/go.mod") == metadata {
			line = fields[0] + " " + fields[1] + " h1:AAAA" + strings.TrimPrefix(fields[2], "h1:")[4:]
			changed = true
		}
		out = append(out, line)
	}
	spec.Expect(changed).To(BeTrue(), "the lock holds no requested checksum of %s", module)
	return strings.Join(out, "\n")
}

// ownerOf is the entity that owns one of the build's actions, as its
// reference.
func (b *Build) ownerOf(label string) string {
	b.scenario.Helper()
	return written(b.result(label).GetEntity())
}

// written is an entity's reference as it is written: kind:namespace/name;
// empty for none.
func written(ref *typev1.EntityRef) string {
	if ref == nil {
		return ""
	}
	return entity.FromProto(ref).String()
}

// saysOf is what the build says of why one of its actions ended as it
// did.
func (b *Build) saysOf(label string) string {
	b.scenario.Helper()
	return b.result(label).GetMessage()
}

// action is what the grid knows one of the build's actions by: two
// builds share an action when they know it by the same name.
func (b *Build) action(label string) string {
	b.scenario.Helper()
	for _, result := range b.results() {
		if result.GetLabel() == label {
			b.scenario.Expect(result.GetActionDigest().GetHash()).NotTo(BeEmpty(), "%s was never sent to the grid", label)
			return result.GetActionDigest().GetHash()
		}
	}
	b.scenario.Fatal("the build ran no action " + label)
	return ""
}

// events are the build's events from a sequence to its end.
func (b *Build) events(from int64) []*buildv1.BuildEvent {
	b.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var out []*buildv1.BuildEvent
	for event, err := range b.of.daemon().BuildByID(b.run.GetId()).Events(ctx, from) {
		b.scenario.Expect(err).NotTo(HaveOccurred())
		out = append(out, event)
	}
	return out
}

// started returns once the run's events say an action has started: the
// run has read its source by then.
func (b *Build) started(label string) {
	b.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	stream, err := buildsAt(b.of.at).StreamBuildEvents(ctx, &buildv1.StreamBuildEventsRequest{BuildId: b.run.GetId()})
	b.scenario.Expect(err).NotTo(HaveOccurred())
	defer stream.Close()
	for {
		got, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			b.scenario.Fatal("the run ended before " + label + " started")
		}
		b.scenario.Expect(err).NotTo(HaveOccurred())
		for _, event := range got.GetEvents() {
			if event.GetActionStarted().GetLabel() == label {
				return
			}
		}
	}
}

// covers are the projects the build planned actions of, sorted.
func (b *Build) covers() []string {
	b.scenario.Helper()
	seen := map[string]bool{}
	var projects []string
	for _, event := range b.events(0) {
		for _, planned := range event.GetPlanned().GetActions() {
			project, _, _ := strings.Cut(strings.TrimPrefix(planned.GetLabel(), "//"), ":")
			if !seen[project] {
				seen[project] = true
				projects = append(projects, project)
			}
		}
	}
	sort.Strings(projects)
	return projects
}

// eventNumbers are the numbers of the build's events, in the order it
// told them.
func (b *Build) eventNumbers() []int {
	b.scenario.Helper()
	var out []int
	for _, event := range b.events(0) {
		out = append(out, int(event.GetSequence()))
	}
	return out
}

// counting is the numbers from one on, as many as are asked for.
func counting(howMany int) []int {
	var out []int
	for n := 1; n <= howMany; n++ {
		out = append(out, n)
	}
	return out
}

// lastEventIsItsEnd is whether the last thing the build told is that it
// finished.
func (b *Build) lastEventIsItsEnd() bool {
	b.scenario.Helper()
	told := b.events(0)
	return len(told) > 0 && told[len(told)-1].GetFinished() != nil
}

// followedAgainAfter is the number of the first event told to whoever
// follows the build again, having seen the events up to a number.
func (b *Build) followedAgainAfter(seen int) int {
	b.scenario.Helper()
	told := b.events(int64(seen) + 1)
	b.scenario.Expect(told).NotTo(BeEmpty(), "nothing was told after event %d", seen)
	return int(told[0].GetSequence())
}

// observeBuilds receives telemetry from the daemon a spec starts afterwards.
func (spec *scenario) observeBuilds() *testtelemetry.Collector {
	spec.Helper()
	t := spec.TB
	collector := testtelemetry.Start(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.Endpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "50")
	t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "50")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "50")
	return collector
}

func spanAttribute(span *tracepb.Span, key string) string {
	if span != nil {
		for _, attr := range span.Attributes {
			if attr.Key == key {
				return attr.Value.GetStringValue()
			}
		}
	}
	return ""
}

// actionSpans waits for the final run span as the export control, then requires
// one action span for each action in this run's API plan.
func (spec *scenario) actionSpans(collector *testtelemetry.Collector, b *Build) map[string]*tracepb.Span {
	spec.Helper()
	var out map[string]*tracepb.Span
	spec.Eventually(func(g Gomega) {
		all := collector.TraceSpansFor("cicd.pipeline.run.id", b.run.GetId())
		var id []byte
		for _, span := range all {
			if spanAttribute(span, "cicd.pipeline.run.id") == b.run.GetId() {
				id = span.TraceId
			}
		}
		g.Expect(id).NotTo(BeEmpty(), "the run span must reach the collector")
		out = map[string]*tracepb.Span{}
		for _, span := range all {
			if bytes.Equal(span.TraceId, id) {
				if label := spanAttribute(span, "cicd.pipeline.task.name"); label != "" {
					g.Expect(out).NotTo(HaveKey(label))
					out[label] = span
				}
			}
		}
		g.Expect(out).To(HaveLen(len(b.actions())), "every planned action must have a span")
	}, 5*time.Second).Should(Succeed())
	return out
}

func (spec *scenario) expectTraceDependencies(b *Build, spans map[string]*tracepb.Span) {
	spec.Helper()
	for label, span := range spans {
		var linked, wanted []string
		for _, link := range span.Links {
			spec.Expect(link.TraceId).To(Equal(span.TraceId), "a dependency must belong to this command")
			linked = append(linked, hex.EncodeToString(link.SpanId))
		}
		for _, need := range b.needsOf(label) {
			spec.Expect(spans).To(HaveKey(need))
			wanted = append(wanted, hex.EncodeToString(spans[need].SpanId))
		}
		spec.Expect(linked).To(ConsistOf(wanted), "dependency links of %s", label)
	}
}

type retainedStages struct {
	evaluate *tracepb.Span
	plan     *tracepb.Span
}

// retainedRunStages finds evaluation spans by the run ID returned by the API,
// not by assumptions about the daemon's in-memory session implementation.
func (spec *scenario) retainedRunStages(collector *testtelemetry.Collector, b *Build) retainedStages {
	spec.Helper()
	var out retainedStages
	spec.Eventually(func(g Gomega) {
		all := collector.TraceSpansFor("cicd.pipeline.run.id", b.run.GetId())
		var traceID []byte
		for _, span := range all {
			if spanAttribute(span, "cicd.pipeline.run.id") == b.run.GetId() {
				traceID = span.TraceId
				break
			}
		}
		g.Expect(traceID).NotTo(BeEmpty(), "this run's span must reach the collector")
		out = retainedStages{}
		for _, span := range all {
			if !bytes.Equal(span.TraceId, traceID) {
				continue
			}
			switch span.GetName() {
			case "evaluate":
				g.Expect(out.evaluate).To(BeNil(), "one evaluation span per run")
				out.evaluate = span
			case "plan":
				g.Expect(out.plan).To(BeNil(), "one planning span per run")
				out.plan = span
			}
		}
		g.Expect(out.evaluate).NotTo(BeNil(), "the run must report evaluation")
		g.Expect(out.plan).NotTo(BeNil(), "the build must report planning")
	}, 5*time.Second).Should(Succeed())
	return out
}

// Typed telemetry observations are shared with the sandbox specs.
func (spec *scenario) requiredSpanBool(span *tracepb.Span, key string) bool {
	spec.Helper()
	return testtelemetry.RequireBool(spec.TB, span, key)
}

func (spec *scenario) requiredSpanInt(span *tracepb.Span, key string) int64 {
	spec.Helper()
	return testtelemetry.RequireInt(spec.TB, span, key)
}

func (spec *scenario) expectEvaluateStage(span *tracepb.Span, retained, didAsk bool) {
	spec.Helper()
	spec.Expect(spec.requiredSpanBool(span, "retained")).To(Equal(retained))
	asked := spec.requiredSpanInt(span, "asked")
	if didAsk {
		spec.Expect(asked).To(BeNumerically(">", 0))
	} else {
		spec.Expect(asked).To(BeZero())
	}
	spec.Expect(spec.requiredSpanInt(span, "again")).To(BeZero())
}

// rootsOf names the directories of projects, relative to their repository.
func rootsOf(projects []*buildv1.Project) []string {
	roots := make([]string, 0, len(projects))
	for _, project := range projects {
		roots = append(roots, project.GetRef().GetRoot())
	}
	return roots
}

// checkingEntity is a mounted repository holding one entity at its root
// with one check of its own, and its fix.
func (spec *scenario) checkingEntity(name string, check, fix []string) *Workspace {
	spec.Helper()
	directory := spec.TB.TempDir()
	workspace := &Workspace{scenario: spec, directory: directory, at: buildtest.Mounted(spec.TB, directory)}
	workspace.write("frontseat.yaml", "plugins: {go: {}, backstage: {}}\n")
	workspace.write("catalog-info.yaml", entityWithActions(map[string]ownCheck{name: {check: check, fix: fix}}))
	return workspace
}

// ownCheckout is this repository as a clone of it would hold it once what
// is here is committed: every file git tracks or would track, uncommitted
// ones included, leaving out only what the repository's own .gitignore
// files name, never what this machine's git is told to ignore. Nothing a
// build left behind is in it. Its frontseat.yaml, .frontseatignore and
// catalog files are what the daemon reads; the spec writes nothing. The
// daemon and directory are released with the spec.
func (spec *scenario) ownCheckout() *Workspace {
	spec.Helper()
	root, err := testmodule.Root()
	spec.Expect(err).NotTo(HaveOccurred())
	listing := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-per-directory=.gitignore")
	listing.Dir = root
	listed, err := listing.Output()
	spec.Expect(err).NotTo(HaveOccurred())
	directory := spec.TB.TempDir()
	for _, name := range strings.Split(strings.TrimSuffix(string(listed), "\x00"), "\x00") {
		from, to := filepath.Join(root, name), filepath.Join(directory, name)
		info, err := os.Lstat(from)
		if errors.Is(err, os.ErrNotExist) {
			continue // listed, then deleted, and not yet committed as gone
		}
		spec.Expect(err).NotTo(HaveOccurred())
		spec.Expect(os.MkdirAll(filepath.Dir(to), 0o755)).To(Succeed())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(from)
			spec.Expect(err).NotTo(HaveOccurred())
			spec.Expect(os.Symlink(target, to)).To(Succeed())
			continue
		}
		content, err := os.ReadFile(from)
		spec.Expect(err).NotTo(HaveOccurred())
		spec.Expect(os.WriteFile(to, content, info.Mode().Perm())).To(Succeed())
	}
	// Every plugin this repository's frontseat.yaml names, as the daemon
	// in a box loads them.
	buf, err := testmodule.Plugin("buf")
	spec.Expect(err).NotTo(HaveOccurred())
	cargo, err := testmodule.Plugin("cargo")
	spec.Expect(err).NotTo(HaveOccurred())
	github, err := testmodule.Plugin("github")
	spec.Expect(err).NotTo(HaveOccurred())
	return &Workspace{scenario: spec, directory: directory, at: buildtest.Mounted(spec.TB, directory, buf, cargo, github)}
}

func (spec *scenario) nativeRuntimeInputs() *Workspace {
	spec.Helper()
	workspace := spec.goWorkspace()
	workspace.write("frontseat.yaml", "plugins: {go: {test-shards: 2}, backstage: {}}\n")
	workspace.write("app/go.mod", "module example.com/runtime\n\ngo 1.27.3\n")
	workspace.write("app/catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: app}
spec:
  type: tests
  lifecycle: experimental
  owner: team
  frontseat:
    test:
      inputs: [config.txt, '//fixture:data.txt']
      tools: [go]
      environment: {FIXTURE: '../../fixture/data.txt', CONFIG: '../config.txt'}
`)
	workspace.write("app/config.txt", "component data")
	workspace.write("fixture/catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: fixture}
spec:
  type: library
  lifecycle: experimental
  owner: team
  frontseat:
    actions:
      prepare:
        command: [cp, source.txt, data.txt]
        inputs: [source.txt]
        outputs: [data.txt]
`)
	workspace.write("app/cases/runtime_test.go", `package cases
import ("fmt"; "os"; "os/exec"; "runtime"; "strings"; "testing")
func check(t *testing.T) {
  t.Helper()
  fixture, err := os.ReadFile(os.Getenv("FIXTURE"))
  if err != nil || !strings.HasPrefix(string(fixture), "fixture for this run:") { t.Fatalf("fixture: %q, %v", fixture, err) }
  config, err := os.ReadFile(os.Getenv("CONFIG"))
  if err != nil || string(config) != "component data\n" { t.Fatalf("config: %q, %v", config, err) }
  version, err := exec.Command("go", "version").CombinedOutput()
  if err != nil || !strings.Contains(string(version), runtime.Version()) { t.Fatalf("pinned tool: %q, %v", version, err) }
  fmt.Println("ran " + t.Name())
}
func TestFirst(t *testing.T) { check(t) }
func TestSecond(t *testing.T) { check(t) }
func TestThird(t *testing.T) { check(t) }
func TestFourth(t *testing.T) { check(t) }
`)
	return workspace
}

// goWorkspace is an empty mounted checkout pinned to Go, with a daemon
// that loads the plugins given beside this repository's own.
func (spec *scenario) goWorkspace(plugins ...[]byte) *Workspace {
	spec.Helper()
	directory := spec.TB.TempDir()
	testpins.Go(spec.TB, directory)
	return &Workspace{scenario: spec, directory: directory, at: buildtest.Mounted(spec.TB, directory, plugins...)}
}

// embeddedModule includes a similarly named neighbor so that reading a literal
// directory as a pattern cannot silently select another project's directives.
func (spec *scenario) embeddedModule(root string) *Workspace {
	spec.Helper()
	workspace := spec.goWorkspace()
	workspace.write(root+"/go.mod", "module example.com/embedded\n\ngo 1.27\n")
	workspace.write(root+"/main.go", "package main\n\nimport _ \"embed\"\n\n//go:embed asset.txt\nvar asset string\n\nfunc main() { println(asset) }")
	workspace.write(root+"/asset.txt", "the correct asset\n")
	workspace.write("pkg1/other.go", "package other\n\nimport _ \"embed\"\n\n//go:embed wrong.txt\nvar wrong string")
	workspace.write("pkg1/wrong.txt", "the neighboring asset\n")
	return workspace
}

// generatedGo is a Buf workspace with handwritten module metadata and a Go
// consumer; generated code is deliberately absent from its checkout.
func (spec *scenario) generatedGo() *Workspace {
	spec.Helper()
	t := spec.TB
	dir := t.TempDir()
	testforge.GeneratedGo(t, dir)
	module, err := testmodule.Plugin("buf")
	spec.Expect(err).NotTo(HaveOccurred())
	w := &Workspace{scenario: spec, at: buildtest.Mounted(t, dir, module), directory: dir}
	return w
}

// generatedElsewhere is a Buf workspace in the Component api that generates
// into lib/gen, a directory of the Go module of the Component lib, which
// says in its catalog file that api generates it, and a Go consumer app of
// lib. No generated file is in its checkout.
func (spec *scenario) generatedElsewhere() *Workspace {
	spec.Helper()
	t := spec.TB
	dir := t.TempDir()
	testforge.GeneratedElsewhere(t, dir)
	module, err := testmodule.Plugin("buf")
	spec.Expect(err).NotTo(HaveOccurred())
	return &Workspace{scenario: spec, at: buildtest.Mounted(t, dir, module), directory: dir}
}

// breakGeneratorChecksum changes only the official generator's locked hashes.
// Parsing the section avoids corrupting Buf or Go and proves this new backend
// is held to its lock by the worker's own mise installation.
func (w *Workspace) breakGeneratorChecksum() {
	w.scenario.Helper()
	lock := w.file("mise.lock")
	before, section, found := strings.Cut(lock, "[[tools.protoc-gen-go]]")
	w.scenario.Expect(found).To(BeTrue())
	section, after, following := strings.Cut(section, "\n[[tools.")
	lines := strings.Split(section, "\n")
	changed := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "checksum = ") {
			lines[i] = "checksum = \"sha256:" + strings.Repeat("0", 64) + "\""
			changed = true
		}
	}
	w.scenario.Expect(changed).To(BeTrue())
	if following {
		after = "\n[[tools." + after
	}
	w.write("mise.lock", before+"[[tools.protoc-gen-go]]"+strings.Join(lines, "\n")+after)
}

// colocatedProjects loads the shared SDK fixture beside the real Go plugin.
// Their declarations share one literal directory but remain separate projects.
func (spec *scenario) colocatedProjects(root string) *Workspace {
	spec.Helper()
	module, err := testmodule.SDKExample("cohabiting")
	spec.Expect(err).NotTo(HaveOccurred())
	workspace := spec.goWorkspace(module)
	workspace.write("frontseat.yaml", "plugins: {go: {}, cohabiting: {}}\n")
	workspace.write(filepath.Join(root, "go.mod"), "module example.com/application\n\ngo 1.27\n")
	workspace.write(filepath.Join(root, "main.go"), "package main\n\nfunc main() {}")
	workspace.write(filepath.Join(root, "cohabiting.toml"), "provides = \"example.com/secondary\"\nrequires = \"example.com/application\"\n")
	return workspace
}

// runtimeInputs loads an SDK contract fixture whose real worker commands read
// separately selected source and artifact providers. It names no ordering edges.
func (spec *scenario) runtimeInputs() *Workspace {
	spec.Helper()
	module, err := testmodule.SDKExample("runtime_inputs")
	spec.Expect(err).NotTo(HaveOccurred())
	workspace := spec.goWorkspace(module)
	workspace.write("frontseat.yaml", "plugins: {input-fixture: {}}\n")
	for directory, role := range map[string]string{"app": "consumer", "lib": "source", "fixture": "runtime"} {
		workspace.write(directory+"/input-fixture.toml", "role = "+fmt.Sprintf("%q", role)+"\n")
	}
	return workspace
}

func (w *Workspace) projects() []*buildv1.Project {
	w.scenario.Helper()
	listed, err := frontseat.Collect(w.daemon().Projects(context.Background()))
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return listed
}

// diagnostics collects the actual action failures through the API.
func (b *Build) diagnostics() string {
	b.scenario.Helper()
	out := b.says()
	for _, r := range b.results() {
		if r.GetOutcome() != buildv1.Outcome_OUTCOME_PASSED {
			out += "\n" + r.GetLabel() + ": " + r.GetMessage() + "\n" + b.printed(r.GetLabel())
		}
	}
	return out
}

// cargoWorkspace is the Cargo plugin's fixture workspace, pinned as this
// repository is: hello depends on greet by path, greet on one locked
// crate through the workspace's dependencies.
func (spec *scenario) cargoWorkspace() *Workspace {
	spec.Helper()
	return spec.fixtureWorkspace("cargo", "plugins:\n  cargo: {}\n")
}

// apkoWorkspace is the apko plugin's fixture workspace, pinned as this
// repository is: app is the smallest image Wolfi makes, locked for two
// architectures. It is also a component with an action of its own,
// which reads the image built for arm64 and prints what marks an OCI
// layout, so that a spec sees what the build left through the API.
func (spec *scenario) apkoWorkspace() *Workspace {
	spec.Helper()
	workspace := spec.fixtureWorkspace("apko", "plugins:\n  apko: {}\n  backstage: {}\n")
	return workspace.write("app/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: app\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      layout:\n        command: [cat, oci/aarch64/oci-layout, oci/aarch64/index.json]\n        inputs: [\":apko.image/aarch64\"]\n")
}

// fixtureWorkspace is a plugin's fixture (plugins/<name>/testdata/valid)
// as a mounted checkout, pinned as this repository is, with the
// frontseat.yaml given and a daemon that loads the plugin.
func (spec *scenario) fixtureWorkspace(plugin, configuration string) *Workspace {
	spec.Helper()
	directory := spec.TB.TempDir()
	testpins.Own(spec.TB, directory)
	root, err := testmodule.Root()
	spec.Expect(err).NotTo(HaveOccurred())
	fixture := filepath.Join(root, "plugins", plugin, "testdata", "valid")
	spec.Expect(filepath.WalkDir(fixture, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(fixture, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(directory, rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(directory, rel), content, 0o644)
	})).To(Succeed())
	spec.Expect(os.WriteFile(filepath.Join(directory, "frontseat.yaml"), []byte(configuration), 0o644)).To(Succeed())
	module, err := testmodule.Plugin(plugin)
	spec.Expect(err).NotTo(HaveOccurred())
	return &Workspace{scenario: spec, directory: directory, at: buildtest.MountedWith(spec.TB, directory, buildtest.Options{Plugins: [][]byte{module}, Rewrites: spec.rewrites})}
}

// releasable is the company's source, as releasing needs it. services
// and platform list the catalog's plugin and the release plugin, and every
// component has a catalog file of its own. services also holds the system
// kiosk, of banner and ticker, two programs that call greet, all three in
// services: kiosk is the synced group the specs release, a unit of one
// repository. storefront, of hello in services and gateway in platform,
// is a system across two repositories, which no unit may be. kiosk is in
// the domain retail; storefront is in none. greet is in no system, and
// nor is log, in platform. Nothing says how anything is released until a
// spec has it say so.
func (spec *scenario) releasable() *Source {
	spec.Helper()
	s := spec.changedSource()
	for _, repository := range []string{"services", "platform"} {
		s.lists(repository, "backstage").lists(repository, "release")
	}
	const calls = "package main\n\nimport \"example.com/greet\"\n\nfunc main() { println(greet.Hello()) }\n"
	for _, program := range []string{"banner", "ticker"} {
		s.says("services", program+"/"+program+".go", calls)
	}
	for _, component := range []string{"hello", "greet", "gateway", "log", "banner", "ticker"} {
		s.releases(component)
	}
	s.groupsSay()
	return s
}

// syncedRetail is the source made to release, where the domain retail
// says its systems version together, and holds lobby, of greet, beside
// kiosk: a domain of two systems, each with members, in services.
func (spec *scenario) syncedRetail() *Source {
	spec.Helper()
	return spec.releasable().domainHolds("retail", "lobby", "greet").releases("domain:retail", "grouping: synced")
}

// systemOf is the system a component of the release fixture is part of;
// empty for none.
func (s *Source) systemOf(component string) string {
	if system, joined := s.joined[component]; joined {
		return system
	}
	switch component {
	case "hello", "gateway":
		return "storefront"
	case "banner", "ticker":
		return "kiosk"
	}
	return ""
}

// releases has an entity's catalog file say, under
// spec.frontseat.release, the lines given, each "key: value"; with none,
// it writes the file as it is. An entity is named as a spec names it:
// greet, system:kiosk, domain:retail. A component's file and its go.mod
// say what it depends on, as frontseat would have written them.
func (s *Source) releases(entity string, lines ...string) *Source {
	s.scenario.Helper()
	s.releaseSays[entity] = append(s.releaseSays[entity], lines...)
	if strings.Contains(entity, ":") {
		s.groupsSay()
		return s
	}
	home, known := homes[entity]
	s.scenario.Expect(known).To(BeTrue(), "the company has no component %s", entity)
	spec := "  type: service\n  lifecycle: production\n  owner: team\n"
	if system := s.systemOf(entity); system != "" {
		spec += "  system: " + system + "\n"
	}
	depends := s.dependsOn(entity)
	if len(depends) > 0 {
		spec += "  dependsOn: [component:" + strings.Join(depends, ", component:") + "]\n"
	}
	s.says(home[0], home[1]+"/catalog-info.yaml",
		"apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+entity+"\nspec:\n"+spec+releaseLines(s.releaseSays[entity]))
	module := "module example.com/" + entity + "\n\ngo 1.27\n"
	if len(depends) > 0 {
		module += "\nrequire (\n"
		for _, each := range depends {
			module += "\texample.com/" + each + " v0.0.0\n"
		}
		module += ")\n"
	}
	s.says(home[0], home[1]+"/go.mod", module)
	return s
}

// domainHolds has components of the company leave whatever system they
// are in and be part of a system of a domain, a system the company does
// not have: retail, beside kiosk, or a domain of its own.
func (s *Source) domainHolds(domain, system string, components ...string) *Source {
	s.scenario.Helper()
	s.domainOf[system] = domain
	for _, component := range components {
		s.joined[component] = system
		s.releases(component)
	}
	s.groupsSay()
	return s
}

// subdomain has a domain be part of another, as Backstage's
// spec.subdomainOf says.
func (s *Source) subdomain(domain, of string) *Source {
	s.scenario.Helper()
	s.subdomainOf[domain] = of
	s.groupsSay()
	return s
}

// describedElsewhere has a domain be described in platform's catalog,
// so that a workspace of services alone does not hold it.
func (s *Source) describedElsewhere(domain string) *Source {
	s.scenario.Helper()
	s.elsewhere[domain] = true
	s.groupsSay()
	return s
}

// dependsOn are the components an entity's module requires: greet, for
// the programs that call it, and what a spec added.
func (s *Source) dependsOn(entity string) []string {
	var out []string
	switch entity {
	case "hello", "gateway", "banner", "ticker":
		out = append(out, "greet")
	}
	return append(out, s.required[entity]...)
}

// groupsSay writes the catalog file of the systems and the domains, with
// what each says of how it is released. kiosk is retail's; storefront is
// in no domain; a system a spec put components in is the domain's it
// named; any other system a spec named and the fixture does not have is
// in no domain, and has no members; and a domain a spec named holds what
// a spec put in it. Every one is described in services, but a domain a
// spec had described elsewhere, which is described in platform.
func (s *Source) groupsSay() {
	s.scenario.Helper()
	domain := func(name string) string {
		out := "apiVersion: backstage.io/v1alpha1\nkind: Domain\nmetadata:\n  name: " + name + "\nspec:\n  owner: team\n"
		if of := s.subdomainOf[name]; of != "" {
			out += "  subdomainOf: " + of + "\n"
		}
		return out + releaseLines(s.releaseSays["domain:"+name])
	}
	file := domain("retail") +
		"---\napiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: kiosk\nspec:\n  owner: team\n  domain: retail\n" + releaseLines(s.releaseSays["system:kiosk"]) +
		"---\napiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: storefront\nspec:\n  owner: team\n" + releaseLines(s.releaseSays["system:storefront"])
	named := map[string]bool{}
	for name := range s.releaseSays {
		named[name] = true
	}
	for system, in := range s.domainOf {
		named["system:"+system], named["domain:"+in] = true, true
	}
	for in, of := range s.subdomainOf {
		named["domain:"+in], named["domain:"+of] = true, true
	}
	elsewhere := ""
	said := make([]string, 0, len(named))
	for name := range named {
		said = append(said, name)
	}
	sort.Strings(said)
	for _, name := range said {
		if name, ok := strings.CutPrefix(name, "domain:"); ok {
			switch {
			case s.elsewhere[name]:
				elsewhere += "---\n" + domain(name)
			case name != "retail":
				file += "---\n" + domain(name)
			}
			continue
		}
		system, ok := strings.CutPrefix(name, "system:")
		if !ok || system == "kiosk" || system == "storefront" {
			continue
		}
		in := ""
		if s.domainOf[system] != "" {
			in = "  domain: " + s.domainOf[system] + "\n"
		}
		file += "---\napiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: " + system + "\nspec:\n  owner: team\n" + in + releaseLines(s.releaseSays[name])
	}
	s.says("services", "catalog-info.yaml", file)
	if elsewhere != "" {
		s.says("platform", "catalog-info.yaml", strings.TrimPrefix(elsewhere, "---\n"))
	}
}

// releaseLines is spec.frontseat.release holding the lines given, as a
// catalog file's spec writes it; nothing for none.
func releaseLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := "  frontseat:\n    release:\n"
	for _, line := range lines {
		out += "      " + line + "\n"
	}
	return out
}

// comment is a line a file of its kind allows as a comment, and that
// changes nothing else of what it says.
func (spec *scenario) comment(path, text string) string {
	switch filepath.Ext(path) {
	case ".go":
		// After a blank line, as gofmt has a comment that follows a
		// declaration: a release builds what it tags, format check and all.
		return "\n// " + text + "\n"
	case ".yaml", ".yml", ".toml", ".mod":
		if filepath.Ext(path) == ".mod" {
			return "// " + text + "\n"
		}
		return "# " + text + "\n"
	case ".md":
		return "<!-- " + text + " -->\n"
	}
	spec.Fatal("the specs do not know how to comment " + path)
	return ""
}

// commits commits a change to files of one repository of the workspace,
// with a message, as someone working in it does. Each path is the
// repository's name and the file's path in it; the change is a comment
// added at the file's end, in the file's own syntax.
func (w *Workspace) commits(message string, paths ...string) *Workspace {
	w.scenario.Helper()
	w.scenario.Expect(paths).NotTo(BeEmpty())
	repository, _, _ := strings.Cut(paths[0], "/")
	for _, path := range paths {
		w.scenario.Expect(path).To(HavePrefix(repository+"/"), "one commit is of one repository")
		w.appends(path, w.scenario.comment(path, strings.SplitN(message, "\n", 2)[0]))
	}
	return w.commitsAll(repository, message)
}

// appends adds the text given at the end of a file of the workspace.
func (w *Workspace) appends(path, text string) *Workspace {
	w.scenario.Helper()
	at := filepath.Join(w.directory, filepath.FromSlash(path))
	held, err := os.ReadFile(at)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	w.scenario.Expect(os.WriteFile(at, append(held, []byte(text)...), 0o644)).To(Succeed())
	return w
}

// releaseWorkspace is a workspace of the source made to release: the
// repositories services and platform, each whole.
func (s *Source) releaseWorkspace() *Workspace {
	s.scenario.Helper()
	return s.releaseWorkspaceOf("services", "platform")
}

// releaseWorkspaceOf is a workspace of the source made to release, of
// the repositories named alone, each whole.
func (s *Source) releaseWorkspaceOf(repositories ...string) *Workspace {
	s.scenario.Helper()
	w := s.emptyWorkspace()
	spec := &workspacev1.WorkspaceSpec{}
	for _, repository := range repositories {
		spec.Repositories = append(spec.Repositories, &workspacev1.RepositorySource{Remote: s.Repository(repository), Ref: "main"})
	}
	s.scenario.Expect(w.bring(spec)).To(Succeed())
	return w
}

// changesGreet commits a change to greet's source with the message given.
func (w *Workspace) changesGreet(message string) *Workspace {
	w.scenario.Helper()
	return w.commits(message, "services/greet/greet.go")
}

// tagged tags where a repository of the workspace stands, as an earlier
// release would have: tagged("services", "greet/v1.2.3").
func (w *Workspace) tagged(repository, tag string) *Workspace {
	w.scenario.Helper()
	w.scenario.git(filepath.Join(w.directory, repository), "tag", tag)
	return w
}

// onBranch makes a branch where a repository of the workspace stands,
// and works on it.
func (w *Workspace) onBranch(repository, branch string) *Workspace {
	w.scenario.Helper()
	w.scenario.git(filepath.Join(w.directory, repository), "switch", "--quiet", "--create", branch)
	return w
}

// tagsAtHead are the tags that name the commit a repository of the
// workspace stands at.
func (w *Workspace) tagsAtHead(repository string) []string {
	w.scenario.Helper()
	return strings.Fields(w.scenario.git(filepath.Join(w.directory, repository), "tag", "--points-at", "HEAD"))
}

// releaseRequest is what releasing names: entities, and whether their
// dependents come too.
type releaseRequest func(*buildv1.PlanReleaseRequest)

// releaseOf names what to release, as a spec names entities.
func (spec *scenario) releaseOf(names ...string) releaseRequest {
	return func(r *buildv1.PlanReleaseRequest) { r.Entities = spec.entities(names...) }
}

// Plan is what releasing would do, and what was asked to get it.
type Plan struct {
	*scenario

	of      *Workspace
	request *buildv1.PlanReleaseRequest
	plan    *buildv1.ReleasePlan
}

// planning asks what releasing would do, or says why it was refused.
func (w *Workspace) planning(how ...releaseRequest) (*Plan, error) {
	request := &buildv1.PlanReleaseRequest{}
	for _, each := range how {
		each(request)
	}
	planned, err := releasesAt(w.at).PlanRelease(context.Background(), request)
	return &Plan{scenario: w.scenario, of: w, request: request, plan: planned.GetPlan()}, err
}

// plan asks what releasing would do.
func (w *Workspace) plan(how ...releaseRequest) *Plan {
	w.scenario.Helper()
	p, err := w.planning(how...)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return p
}

// expectGreetBeforeKiosk holds that a plan of kiosk and greet in the
// workspace lists greet, which kiosk depends on, before kiosk.
func (spec *scenario) expectGreetBeforeKiosk(w *Workspace) {
	spec.Helper()
	plan := w.plan(spec.releaseOf("system:kiosk", "greet"))

	spec.Expect(plan.units()).To(Equal([]string{"greet", "kiosk"}))
	spec.Expect(plan.unit("kiosk").dependsOn()).To(Equal([]string{"greet"}))
}

// planAfterGreetChanges commits a change to greet and plans greet again.
func (w *Workspace) planAfterGreetChanges() *Plan {
	w.scenario.Helper()
	w.changesGreet("fix: trim the greeting")
	return w.plan(w.scenario.releaseOf("greet"))
}

// releasingAPlanOfGreetThatMoved plans greet, commits a change to it,
// and asks for the plan to be released, which says why it was refused;
// then it plans greet again and releases that.
func (w *Workspace) releasingAPlanOfGreetThatMoved() (*Shipped, error) {
	w.scenario.Helper()
	stale := w.plan(w.scenario.releaseOf("greet"))
	w.changesGreet("fix: trim the greeting")

	_, refusal := stale.releasing()
	fresh := w.plan(w.scenario.releaseOf("greet")).release()
	return fresh, refusal
}

// unitOf is the unit named in a plan of releasing it.
func (w *Workspace) unitOf(name string) *Unit {
	w.scenario.Helper()
	return w.plan(w.scenario.releaseOf(name)).unit(name)
}

// etag is what the plan is released by.
func (p *Plan) etag() string { return p.plan.GetEtag() }

// units are the plan's units, by name, in its order.
func (p *Plan) units() []string {
	var out []string
	for _, unit := range p.plan.GetUnits() {
		out = append(out, unit.GetEntity().GetName())
	}
	return out
}

// unit is one of the plan's units, by name.
func (p *Plan) unit(name string) *Unit {
	p.scenario.Helper()
	for _, unit := range p.plan.GetUnits() {
		if unit.GetEntity().GetName() == name {
			return &Unit{unit}
		}
	}
	p.scenario.Fatal("the plan has no unit " + name + "; it has " + strings.Join(p.units(), ", "))
	return nil
}

// Unit is what gets one version and one tag.
type Unit struct{ unit *buildv1.ReleaseUnit }

// members are the names of the entities that take its version.
func (u *Unit) members() []string { return names(u.unit.GetMembers()) }

// group is the name of the group it versions with.
func (u *Unit) group() string { return u.unit.GetGroup().GetName() }

// grouping is how its group versions: "synced" or "independent".
func (u *Unit) grouping() string {
	return strings.ToLower(strings.TrimPrefix(u.unit.GetGrouping().String(), "GROUPING_"))
}

// last is its last release; empty if it has none.
func (u *Unit) last() string { return u.unit.GetLastVersion() }

// next is what releasing makes it; empty where it holds.
func (u *Unit) next() string { return u.unit.GetNextVersion() }

// tag is the tag releasing writes.
func (u *Unit) tag() string { return u.unit.GetTag() }

// dependsOn are the names of the plan's units it depends on.
func (u *Unit) dependsOn() []string { return names(u.unit.GetDependsOn()) }

// channel is what it is published under.
func (u *Unit) channel() string { return u.unit.GetChannel() }

// why are its reasons, heaviest first, each in a line: "own change: feat:
// page", "upstream: greet 1.3.0", "hold: greet 1.3.0", "unchanged",
// "first release", "released", "branch: <why>".
func (u *Unit) why() []string {
	var out []string
	for _, reason := range u.unit.GetReasons() {
		kind := strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(reason.GetKind().String(), "KIND_")), "_", " ")
		switch reason.GetKind() {
		case buildv1.ReleaseReason_KIND_OWN_CHANGE, buildv1.ReleaseReason_KIND_BRANCH:
			out = append(out, kind+": "+reason.GetSummary())
		default:
			out = append(out, kind)
		}
	}
	return out
}

// names are entities' names.
func names(refs []*typev1.EntityRef) []string {
	var out []string
	for _, ref := range refs {
		out = append(out, ref.GetName())
	}
	return out
}

// Shipped is a release, and the workspace it was made in.
type Shipped struct {
	*scenario

	of      *Workspace
	release *buildv1.Release
}

// releasing releases what the plan says, by its etag, and follows the
// release to its end; or says why it was refused.
func (p *Plan) releasing() (*Shipped, error) {
	p.scenario.Helper()
	created, err := releasesAt(p.of.at).CreateRelease(context.Background(), &buildv1.CreateReleaseRequest{Entities: p.request.GetEntities(), Etag: p.plan.GetEtag()})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ended, err := p.of.daemon().ReleaseByID(created.GetRelease().GetId()).Wait(ctx)
	p.scenario.Expect(err).NotTo(HaveOccurred())
	return &Shipped{scenario: p.scenario, of: p.of, release: ended}, nil
}

// release releases what the plan says.
func (p *Plan) release() *Shipped {
	p.scenario.Helper()
	shipped, err := p.releasing()
	p.scenario.Expect(err).NotTo(HaveOccurred())
	return shipped
}

// release plans a release and makes it.
func (w *Workspace) release(how ...releaseRequest) *Shipped {
	w.scenario.Helper()
	return w.plan(how...).release()
}

// state is where the release is: "succeeded", "failed".
func (s *Shipped) state() string {
	return strings.ToLower(strings.TrimPrefix(s.release.GetState().String(), "STATE_"))
}

// tagsOf are the tags the release wrote of a unit, each
// "<repository>:<tag>".
func (s *Shipped) tagsOf(unit string) []string {
	s.scenario.Helper()
	for _, released := range s.release.GetUnits() {
		if released.GetEntity().GetName() == unit {
			var out []string
			for _, tag := range released.GetTags() {
				out = append(out, tag.GetRepository()+":"+tag.GetName())
			}
			return out
		}
	}
	s.scenario.Fatal("the release made nothing of " + unit)
	return nil
}

// already is whether the release found a unit released already.
func (s *Shipped) already(unit string) bool {
	s.scenario.Helper()
	for _, released := range s.release.GetUnits() {
		if released.GetEntity().GetName() == unit {
			return released.GetAlreadyReleased()
		}
	}
	s.scenario.Fatal("the release made nothing of " + unit)
	return false
}

// built is the production build the release made of its units.
func (s *Shipped) built() *Build {
	s.scenario.Helper()
	described, err := s.of.daemon().BuildByID(s.release.GetBuildId()).Describe(context.Background())
	s.scenario.Expect(err).NotTo(HaveOccurred())
	return &Build{scenario: s.scenario, of: s.of, run: described}
}

// releaseSetting is one of an entity's release settings as it resolves,
// through the graph API: its value, and the entity that set it, or "the
// default" where none did.
func (w *Workspace) releaseSetting(entity, key string) Setting {
	w.scenario.Helper()
	release := w.entityNamed(entity).GetRelease()
	w.scenario.Expect(release).NotTo(BeNil(), "%s is released as nothing", entity)
	values := map[string]any{
		"grouping":   strings.ToLower(strings.TrimPrefix(release.GetGrouping().String(), "GROUPING_")),
		"tag":        release.GetTag(),
		"initial":    release.GetInitial(),
		"versioning": release.GetVersioningStrategy(),
		"branching":  release.GetBranchingStrategy(),
	}
	value, known := values[key]
	w.scenario.Expect(known).To(BeTrue(), "there is no release setting %s", key)
	from := "the default"
	for _, origin := range release.GetSetBy() {
		if origin.GetKey() == key {
			from = written(origin.GetEntity())
		}
	}
	return Setting{value: value, from: from}
}

// entityNamed is the entity of a name, through the graph API.
func (w *Workspace) entityNamed(entity string) *buildv1.Entity {
	w.scenario.Helper()
	listed, err := frontseat.Collect(w.daemon().Entities(context.Background()))
	w.scenario.Expect(err).NotTo(HaveOccurred())
	for _, each := range listed {
		if each.GetRef().GetName() == entity {
			return each
		}
	}
	w.scenario.Fatal("the workspace holds no entity " + entity)
	return nil
}

// releasedAs is what an entity is released as, through the graph API:
// the name of its unit.
func (w *Workspace) releasedAs(entity string) string {
	w.scenario.Helper()
	return w.entityNamed(entity).GetRelease().GetUnit().GetName()
}

// switchTo works on a branch a repository of the workspace has already.
func (w *Workspace) switchTo(repository, branch string) *Workspace {
	w.scenario.Helper()
	w.scenario.git(filepath.Join(w.directory, repository), "switch", "--quiet", branch)
	return w
}

// breaks commits a change to a Go file of the workspace that does not
// compile, as a fix that was not tried.
func (w *Workspace) breaks(path string) *Workspace {
	w.scenario.Helper()
	repository, _, _ := strings.Cut(path, "/")
	return w.appends(path, "\nfunc broken( {\n").commitsAll(repository, "fix: untried")
}

// edits changes a file of the workspace and commits nothing.
func (w *Workspace) edits(path string) *Workspace {
	w.scenario.Helper()
	return w.appends(path, w.scenario.comment(path, "not committed"))
}

// commitsAll commits what a repository of the workspace has uncommitted.
func (w *Workspace) commitsAll(repository, message string) *Workspace {
	w.scenario.Helper()
	w.scenario.git(filepath.Join(w.directory, repository), "add", "--all")
	w.scenario.git(filepath.Join(w.directory, repository), "-c", "user.name=someone", "-c", "user.email=someone@example.com", "commit", "--quiet", "-m", message)
	return w
}

// releasedBy is a release as the API describes it by its id; or why
// none has that id.
func (w *Workspace) releasedBy(id string) (*buildv1.Release, error) {
	w.scenario.Helper()
	return w.daemon().ReleaseByID(id).Describe(context.Background())
}

// refusedAs is how the API refused a call, as its status is written:
// "invalid_argument", "not_found", "failed_precondition",
// "unimplemented".
func (spec *scenario) refusedAs(refusal error) string {
	spec.Helper()
	spec.Expect(refusal).To(HaveOccurred())
	return connect.CodeOf(refusal).String()
}

// onRelease is the source made to release, in a workspace whose services
// stand on a release branch of a line: Release Flow, the default.
func (spec *scenario) onRelease(line string) *Workspace {
	spec.Helper()
	return spec.releasable().releaseWorkspace().onBranch("services", "release/"+line)
}

// greeterWorkspace is a Go module whose test reads what the service at
// GREETER_ADDR answers, and the catalog that says what it needs.
func (spec *scenario) greeterWorkspace(component, resource string) *Workspace {
	spec.Helper()
	workspace := spec.goWorkspace()
	workspace.write("frontseat.yaml", "plugins: {go: {}, backstage: {}, container: {}, postgres: {}}\n")
	workspace.write("catalog-info.yaml", component+"---\n"+resource)
	workspace.write("svc/go.mod", "module example.com/svc\n\ngo 1.27\n")
	// Of this run alone: what the test proves is proven by a test that
	// ran, never by an answer the grid kept from a run before.
	workspace.write("svc/run_test.go", fmt.Sprintf("package svc\n\nimport \"testing\"\n\nfunc TestOfThisRun(t *testing.T) { t.Log(%q) }\n", fmt.Sprint(time.Now().UnixNano())))
	workspace.write("svc/greeter_test.go", greeterTest)
	return workspace
}

// greeterTest is a test that reaches the greeter where GREETER_ADDR says,
// and that nothing else is reached.
const greeterTest = `package svc

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGreeter(t *testing.T) {
	addr := os.Getenv("GREETER_ADDR")
	if addr == "" {
		t.Fatal("no GREETER_ADDR: the service was not given")
	}
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	b, _ := io.ReadAll(c)
	if got := strings.TrimSpace(string(b)); got != "hello" {
		t.Fatalf("the service answered %q", got)
	}
}

// What the test reaches is its services, and nothing beyond them: not
// the hosts an action that fetches is let reach, which a test that ran
// on an egress slot would.
func TestNothingElseIsReached(t *testing.T) {
	if os.Getenv("GREETER_ADDR") == "" {
		t.Skip("no services, and the action has no network at all")
	}
	for _, outside := range []string{"github.com:443", "1.1.1.1:80"} {
		if c, err := net.DialTimeout("tcp", outside, 3*time.Second); err == nil {
			c.Close()
			t.Fatalf("%s, outside the sandbox, was reached", outside)
		}
	}
}
`

// applicationServiceWorkspace exercises the ordinary inferred Go program and
// catalog API relation, with a Resource that only its provider may reach.
func (spec *scenario) applicationServiceWorkspace() *Workspace {
	spec.Helper()
	component := greeterComponent(false) + "  consumesApis: [api:default/orders]\n"
	w := spec.greeterWorkspace(component, greeterResource(greeterService))
	w.write("orders/catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: orders}
spec:
  type: service
  lifecycle: production
  owner: team
  providesApis: [api:default/orders, api:default/orders-admin]
  dependsOn: [resource:default/greeter]
  frontseat:
    service:
      program: ":go.run/orders"
      ports: {http: 8080, admin: 9000}
      ready: [{http: {port: http, path: /, body: hello}}, {http: {port: admin, path: /, body: private}}]
      apis:
        orders:
          port: http
          exports: {ORDERS_URL: "http://{host}:{port.http}"}
        api:default/orders-admin:
          port: admin
          exports: {ADMIN_URL: "http://{host}:{port.admin}"}
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata: {name: orders}
spec:
  type: openapi
  lifecycle: production
  owner: team
  definition: orders
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata: {name: orders-admin}
spec:
  type: openapi
  lifecycle: production
  owner: team
  definition: admin
`)
	w.write("orders/go.mod", "module example.com/orders\n\ngo 1.27\n")
	w.write("orders/main.go", `package main

import ("fmt"; "io"; "net"; "net/http"; "os"; "time")

func main() {
  // Fail at startup if the dependency is not already ready.
  c, err := net.DialTimeout("tcp", os.Getenv("GREETER_ADDR"), 3*time.Second)
  if err != nil { panic(err) }
  c.SetDeadline(time.Now().Add(3*time.Second))
  body, err := io.ReadAll(c)
  c.Close()
  if err != nil { panic(err) }
  go func() { if err := http.ListenAndServe("127.0.0.1:9000", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "private") })); err != nil { panic(err) } }()
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Provider-Version", "one"); fmt.Fprint(w, string(body)) })
  if err := http.ListenAndServe("127.0.0.1:8080", nil); err != nil { panic(err) }
}
`)
	w.write("svc/greeter_test.go", `package svc

import ("io"; "net"; "net/http"; "os"; "strings"; "testing"; "time")

func TestAPI(t *testing.T) {
  if os.Getenv("GREETER_ADDR") != "" { t.Fatal("transitive Resource binding was given to the test") }
  if os.Getenv("ADMIN_URL") != "" { t.Fatal("another API's binding was given to the test") }
  url := os.Getenv("ORDERS_URL")
  if url == "" { t.Fatal("no ORDERS_URL: the API provider was not given") }
  client := &http.Client{Timeout: 5*time.Second}
  response, err := client.Get(url)
  if err != nil { t.Fatal(err) }
  defer response.Body.Close()
  println("provider-version=" + response.Header.Get("X-Provider-Version"))
  body, err := io.ReadAll(response.Body)
  if err != nil || strings.TrimSpace(string(body)) != "hello" { t.Fatalf("provider: %q, %v", body, err) }
  // The provider's successful startup is the reachable Resource control.
  if c, err := net.DialTimeout("tcp", "127.0.0.1:7000", time.Second); err == nil {
    c.Close(); t.Fatal("transitive Resource route was given to the test")
  }
  // The provider's readiness check successfully calls this endpoint.
  if c, err := net.DialTimeout("tcp", "127.0.0.1:9000", time.Second); err == nil {
    c.Close(); t.Fatal("another API's endpoint was given to the test")
  }
}
`)
	return w
}

// databaseWorkspace is a component whose tests need a database: a
// Resource that says only what it is started as, and whatever is
// written beside that. Its test speaks to the database as a client of
// PostgreSQL does, far enough to know that one answered.
func (spec *scenario) databaseWorkspace(service string) *Workspace {
	spec.Helper()
	workspace := spec.goWorkspace()
	workspace.write("frontseat.yaml", "plugins: {go: {}, backstage: {}, container: {}, postgres: {}}\n")
	workspace.write("catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: svc}
spec:
  type: service
  lifecycle: production
  owner: team
  dependsOn: [resource:db]
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: db}
spec:
  type: database
  owner: team
  frontseat:
    service:
`+service)
	workspace.write("svc/go.mod", "module example.com/svc\n\ngo 1.27\n")
	workspace.write("svc/run_test.go", fmt.Sprintf("package svc\n\nimport \"testing\"\n\nfunc TestOfThisRun(t *testing.T) { t.Log(%q) }\n", fmt.Sprint(time.Now().UnixNano())))
	workspace.write("svc/database_test.go", `package svc

import (
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"
)

// A startup message, as a client of PostgreSQL sends first: the server
// answers 'R', asking the client to say who it is. The database is
// reached by what its Resource exports: the names its plugin says, or,
// where the Resource wrote its own, that name and none of the plugin's.
func TestTheDatabaseAnswers(t *testing.T) {
	address := os.Getenv("DATABASE")
	switch host, port := os.Getenv("PGHOST"), os.Getenv("PGPORT"); {
	case address != "" && (host != "" || port != ""):
		t.Fatalf("told both what was written, DATABASE=%s, and what the plugin would have said, PGHOST=%s", address, host)
	case address == "" && (host == "" || port == ""):
		t.Fatal("no PGHOST and PGPORT, and no DATABASE: the database was not given")
	case address == "":
		address = net.JoinHostPort(host, port)
	}
	c, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	// The role the server was started with, which a Resource that
	// writes its own exports need not tell.
	user := os.Getenv("PGUSER")
	if user == "" {
		user = "postgres"
	}
	body := []byte("user\x00" + user + "\x00database\x00" + user + "\x00\x00")
	message := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	message = binary.BigEndian.AppendUint32(message, 196608)
	if _, err := c.Write(append(message, body...)); err != nil {
		t.Fatal(err)
	}
	answer := make([]byte, 1)
	if _, err := c.Read(answer); err != nil {
		t.Fatal(err)
	}
	if answer[0] != 'R' {
		t.Fatalf("what answered is no PostgreSQL: its first byte is %q", answer[0])
	}
}
`)
	return workspace
}
