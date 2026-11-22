package release_test

import (
	"context"
	"errors"
	"fmt"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/release"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

func ref(written string) entity.Ref {
	r, err := entity.Parse(written)
	if err != nil {
		panic(err)
	}
	return r
}

// company is a workspace to release: greet and log, each alone; kiosk, a
// system of banner and ticker, in the domain retail; storefront, a
// system of hello in services and gateway in platform.
func company(said ...config.Release) release.Workspace {
	component := func(name, repository string, partOf ...string) release.Entity {
		e := release.Entity{Ref: ref(name), Repository: repository, Roots: []string{name}}
		for _, each := range partOf {
			e.PartOf = append(e.PartOf, ref(each))
		}
		return e
	}
	return release.Workspace{
		Entities: []release.Entity{
			component("greet", "services"), component("log", "platform"),
			component("banner", "services", "system:kiosk"), component("ticker", "services", "system:kiosk"),
			component("hello", "services", "system:storefront"), component("gateway", "platform", "system:storefront"),
			{Ref: ref("system:kiosk"), Repository: "services", PartOf: []entity.Ref{ref("domain:retail")}},
			{Ref: ref("system:storefront"), Repository: "services"},
			{Ref: ref("system:empty"), Repository: "services"},
			{Ref: ref("domain:retail"), Repository: "services"},
		},
		Said:   said,
		Listed: map[string][]string{"services": {"go", "release"}, "platform": {"go", "release"}},
		DependsOn: map[entity.Ref][]entity.Ref{
			ref("banner"): {ref("greet")}, ref("ticker"): {ref("greet")}, ref("hello"): {ref("greet")}, ref("gateway"): {ref("greet")},
		},
	}
}

func says(entity string, settings ...string) config.Release {
	out := config.Release{File: "services/catalog-info.yaml", Entity: ref(entity), Settings: map[string]string{}}
	for i := 0; i+1 < len(settings); i += 2 {
		out.Settings[settings[i]] = settings[i+1]
	}
	return out
}

// repository is a repository's history, as a test says it.
type repository struct {
	branch string
	head   string
	// time is when head was committed, in seconds since the epoch.
	time    int64
	tags    []vcs.Tag
	changes []vcs.Change
	// asked records what Changes was asked.
	since       string
	directories []string
}

type histories map[string]*repository

func (h histories) Standing(_ context.Context, name string) (vcs.Standing, error) {
	r, ok := h[name]
	if !ok {
		return vcs.Standing{}, fmt.Errorf("no repository %s", name)
	}
	return vcs.Standing{Branch: r.branch, Revision: r.head, DefaultBranch: "main", Time: r.time}, nil
}

func (h histories) Tags(_ context.Context, name string) ([]vcs.Tag, error) { return h[name].tags, nil }

func (h histories) Changes(_ context.Context, name, since string, directories []string) ([]vcs.Change, error) {
	// What is recorded is the question of the whole unit, the widest.
	if len(directories) >= len(h[name].directories) {
		h[name].since, h[name].directories = since, directories
	}
	return h[name].changes, nil
}

// TagName refuses what git refuses in these tests: a name with a lock
// suffix on a part of it, or a part that begins with a dot.
func (histories) TagName(_ context.Context, name string) error {
	for _, part := range strings.Split(name, "/") {
		if strings.HasSuffix(part, ".lock") || strings.HasPrefix(part, ".") || part == "" {
			return errors.New("is no name git takes for a tag")
		}
	}
	return nil
}

// on is the services repository on a branch, with tags of greet at
// earlier commits and changes since.
func on(branch string, tags []string, messages ...string) histories {
	r := &repository{branch: branch, head: "head"}
	for i, tag := range tags {
		r.tags = append(r.tags, vcs.Tag{Name: tag, Revision: fmt.Sprintf("tagged%d", i), Reachable: true})
	}
	for i, message := range messages {
		r.changes = append(r.changes, vcs.Change{Revision: fmt.Sprintf("c%d", i), Message: message, Time: int64(i + 1)})
	}
	return histories{"services": r, "platform": {branch: branch, head: "head"}}
}

// strategies answers as Release Flow and conventional commits do, said
// again here so that the planner is held to what it is told and not to
// the plugin.
type strategies struct {
	branchAsked, versionAsked int
	// prereleases makes every branch that is no release branch release
	// a prerelease, the default branch heading for the next minor, as
	// Release Flow says it.
	prereleases bool
}

func (*strategies) Serves(plugin string, branching bool, name string) bool {
	return plugin == "release" && ((branching && name == "release-flow") || (!branching && name == "conventional-commits"))
}

func (s *strategies) Branch(_ context.Context, _ string, r *pluginv1.BranchRequest) (*pluginv1.BranchResponse, error) {
	s.branchAsked++
	if line, ok := strings.CutPrefix(r.GetBranch(), "release/"); ok {
		return &pluginv1.BranchResponse{Releases: true, Line: line, Channel: line}, nil
	}
	if s.prereleases {
		channel := strings.Map(func(r rune) rune {
			if r == '/' || r == '_' || r == '.' {
				return '-'
			}
			return r
		}, r.GetBranch())
		out := &pluginv1.BranchResponse{Releases: true, Prerelease: true, Channel: channel}
		if r.GetBranch() == r.GetDefaultBranch() {
			out.AtLeast = typev1.Bump_BUMP_MINOR
		}
		return out, nil
	}
	return &pluginv1.BranchResponse{Reason: r.GetBranch() + " releases nothing"}, nil
}

func (s *strategies) Version(_ context.Context, _ string, r *pluginv1.VersionRequest) (*pluginv1.VersionResponse, error) {
	s.versionAsked++
	out := &pluginv1.VersionResponse{Bump: typev1.Bump_BUMP_NONE}
	var major, minor, patch int
	if r.GetLastVersion() == "" {
		out.Version = r.GetInitial()
		return out, nil
	}
	fmt.Sscanf(r.GetLastVersion(), "%d.%d.%d", &major, &minor, &patch)
	for _, c := range r.GetCommits() {
		switch {
		case strings.HasPrefix(c.GetMessage(), "feat"):
			out.Bump = typev1.Bump_BUMP_MINOR
		case strings.HasPrefix(c.GetMessage(), "fix") && out.Bump != typev1.Bump_BUMP_MINOR:
			out.Bump = typev1.Bump_BUMP_PATCH
		default:
			continue
		}
		out.Reasons = append(out.Reasons, &pluginv1.VersionReason{Commit: c.GetId(), Bump: out.Bump, Summary: c.GetMessage()})
	}
	switch out.Bump {
	case typev1.Bump_BUMP_MINOR:
		out.Version = fmt.Sprintf("%d.%d.0", major, minor+1)
	case typev1.Bump_BUMP_PATCH:
		out.Version = fmt.Sprintf("%d.%d.%d", major, minor, patch+1)
	}
	return out, nil
}

func asking(names ...string) *buildv1.PlanReleaseRequest {
	r := &buildv1.PlanReleaseRequest{}
	for _, name := range names {
		r.Entities = append(r.Entities, entity.Proto(ref(name)))
	}
	return r
}

// plannedUnits are the names of a plan's units, in its order.
func plannedUnits(plan *buildv1.ReleasePlan) string {
	var units []string
	for _, unit := range plan.GetUnits() {
		units = append(units, unit.GetEntity().GetName())
	}
	return strings.Join(units, " ")
}

// plansUnits fails where planning what is named is refused, or plans
// units other than those wanted.
func plansUnits(t *testing.T, name string, w release.Workspace, history histories, named []string, want string) {
	t.Helper()
	plan, err := release.Plan(context.Background(), w, &strategies{}, history, asking(named...))
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return
	}
	if units := plannedUnits(plan); units != want {
		t.Errorf("%s: the units are %s, want %s", name, units, want)
	}
}

func unitOf(t *testing.T, plan *buildv1.ReleasePlan, name string) *buildv1.ReleaseUnit {
	t.Helper()
	for _, unit := range plan.GetUnits() {
		if unit.GetEntity().GetName() == name {
			return unit
		}
	}
	t.Fatalf("the plan has no unit %s: %v", name, plan)
	return nil
}

// greetOf is greet as its release is planned over a history, with what
// the catalog says of releasing; nil, the failure said by the case's
// name, where planning is refused.
func greetOf(t *testing.T, name string, answering *strategies, history histories, said ...config.Release) *buildv1.ReleaseUnit {
	t.Helper()
	plan, err := release.Plan(context.Background(), company(said...), answering, history, asking("greet"))
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return nil
	}
	return unitOf(t, plan, "greet")
}

func TestUnderReleaseFlowAUnitMovesOnItsBranchsLine(t *testing.T) {
	for name, c := range map[string]struct {
		history histories
		said    []config.Release
		next    string
		reason  buildv1.ReasonKind
	}{
		"a fix on a release branch":                {on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim"), nil, "1.26.1", buildv1.ReasonKind_REASON_KIND_OWN_CHANGE},
		"a branch cut for a line not yet released": {on("release/1.27", []string{"greet/v1.26.3"}, "fix: trim"), nil, "1.27.0", buildv1.ReasonKind_REASON_KIND_OWN_CHANGE},
		"a feature on a branch cut for a new line": {on("release/1.27", []string{"greet/v1.26.3"}, "feat: by name"), nil, "1.27.0", buildv1.ReasonKind_REASON_KIND_OWN_CHANGE},
		"never released, on a release branch":      {on("release/0.77", nil, "fix: trim"), nil, "0.77.0", buildv1.ReasonKind_REASON_KIND_FIRST_RELEASE},
		"never released, its initial on the line":  {on("release/0.77", nil, "fix: trim"), []config.Release{says("greet", "initial-version", "0.77.3")}, "0.77.3", buildv1.ReasonKind_REASON_KIND_FIRST_RELEASE},
		"the default branch":                       {on("main", []string{"greet/v1.26.0"}, "feat: by name"), nil, "", buildv1.ReasonKind_REASON_KIND_BRANCH},
		"nothing changed since its last release":   {on("release/1.26", []string{"greet/v1.26.0"}), nil, "", buildv1.ReasonKind_REASON_KIND_UNCHANGED},
		"changes that release nothing":             {on("release/1.26", []string{"greet/v1.26.0"}, "docs: more"), nil, "", buildv1.ReasonKind_REASON_KIND_UNCHANGED},
		"the highest of its tags is its last":      {on("release/1.26", []string{"greet/v1.26.0", "greet/v1.26.4", "greet/v1.26.2"}, "fix: trim"), nil, "1.26.5", buildv1.ReasonKind_REASON_KIND_OWN_CHANGE},
		"another unit's tags are not its own":      {on("release/1.26", []string{"greet/v1.26.0", "log/v1.26.9", "greeting/v1.26.9", "greet/v1.26.9-rc.1", "greet/v1.26", "greet/vnext"}, "fix: trim"), nil, "1.26.1", buildv1.ReasonKind_REASON_KIND_OWN_CHANGE},
	} {
		greet := greetOf(t, name, &strategies{}, c.history, c.said...)
		if greet == nil {
			continue
		}
		if greet.GetNextVersion() != c.next || len(greet.GetReasons()) == 0 || greet.GetReasons()[0].GetKind() != c.reason {
			t.Errorf("%s: next is %q for %v, want %q for %v", name, greet.GetNextVersion(), greet.GetReasons(), c.next, c.reason)
		}
		if (greet.GetTag() != "") != (c.next != "") || (c.next != "" && greet.GetTag() != "greet/v"+c.next) {
			t.Errorf("%s: its tag is %q", name, greet.GetTag())
		}
	}
}

func TestWhatLeavesABranchsLineOrIsNotNewIsRefused(t *testing.T) {
	elsewhere := on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim")
	elsewhere["services"].tags = append(elsewhere["services"].tags, vcs.Tag{Name: "greet/v1.26.1", Revision: "elsewhere", Reachable: false})
	above := on("release/1.26", []string{"greet/v1.27.2"}, "fix: trim")
	detached := on("", nil, "fix: trim")
	for name, c := range map[string]struct {
		history histories
		said    []config.Release
		says    string
	}{
		"a feature on a release branch":           {on("release/1.26", []string{"greet/v1.26.0"}, "feat: by name"), nil, "greet 1.27.0 leaves the line 1.26 of release/1.26"},
		"an initial version off the line":         {on("release/0.77", nil, "fix: trim"), []config.Release{says("greet", "initial-version", "0.1.0")}, "greet: spec.frontseat.release.initial-version: 0.1.0 is not on the line 0.77 of release/0.77"},
		"a version tagged on another commit":      {elsewhere, nil, "greet 1.26.1 is not new: greet/v1.26.1 names another commit"},
		"a last release above the line":           {above, nil, "greet has no release on the line 1.26 of release/1.26, and is released as 1.27.2 already"},
		"a repository on no branch":               {detached, nil, "services stands on no branch"},
		"a strategy no listed plugin serves":      {on("release/1.26", nil), []config.Release{says("greet", "versioning", "release:guesswork")}, "no plugin the repository lists serves the versioning strategy release:guesswork"},
		"a strategy of a plugin it does not list": {on("release/1.26", nil), []config.Release{says("greet", "branching", "other:flow")}, "services lists no other plugin; list it to release greet"},
	} {
		_, err := release.Plan(context.Background(), company(c.said...), &strategies{}, c.history, asking("greet"))
		mistaken(t, name, err, c.says)
	}
	unlisted := company()
	unlisted.Listed["services"] = []string{"go"}
	if _, err := release.Plan(context.Background(), unlisted, &strategies{}, on("release/1.26", nil), asking("greet")); err == nil || !strings.Contains(err.Error(), "services lists no release plugin; list it to release greet") {
		t.Errorf("a repository that lists no release plugin: %v", err)
	}
}

func TestACommitThatCarriesAUnitsTagIsReleased(t *testing.T) {
	history := on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim")
	history["services"].tags[0].Revision = "head"
	plan, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("greet"))
	if err != nil {
		t.Fatal(err)
	}
	greet := unitOf(t, plan, "greet")
	if greet.GetReasons()[0].GetKind() != buildv1.ReasonKind_REASON_KIND_RELEASED || greet.GetNextVersion() != "" || greet.GetLastVersion() != "1.26.0" || greet.GetTag() != "greet/v1.26.0" {
		t.Errorf("a released unit is planned as %v", greet)
	}
}

func TestAUnitIsAskedOfItsMembersDirectoriesSinceItsLastRelease(t *testing.T) {
	history := on("release/1.26", []string{"kiosk/v1.26.0"}, "fix: trim")
	plan, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("banner"))
	if err != nil {
		t.Fatal(err)
	}
	kiosk := unitOf(t, plan, "kiosk")
	if got := strings.Join(history["services"].directories, " "); got != "banner ticker" || history["services"].since != "tagged0" {
		t.Errorf("history was asked of %q since %q", got, history["services"].since)
	}
	if len(kiosk.GetMembers()) != 2 || kiosk.GetGrouping() != buildv1.Grouping_GROUPING_SYNCED || kiosk.GetNextVersion() != "1.26.1" || kiosk.GetTag() != "kiosk/v1.26.1" {
		t.Errorf("a synced group is planned as %v", kiosk)
	}
	// The plan is of every repository of the workspace: what kiosk is
	// built from is in platform too.
	if at := plan.GetCommits(); len(at) != 2 || at[0].GetRepository() != "platform" || at[1].GetRepository() != "services" || at[1].GetCommit() != "head" {
		t.Errorf("the plan is from %v", plan.GetCommits())
	}
}

func TestWhatIsNamedSaysWhichUnitsAreReleased(t *testing.T) {
	independent := []config.Release{says("system:kiosk", "grouping", "independent")}
	left := []config.Release{says("banner", "grouping", "independent")}
	for name, c := range map[string]struct {
		said  []config.Release
		named []string
		units string
	}{
		"an entity alone":                    {nil, []string{"greet"}, "greet"},
		"a member, with its synced group":    {nil, []string{"ticker"}, "kiosk"},
		"a synced group":                     {nil, []string{"system:kiosk"}, "kiosk"},
		"a group and its member, once":       {nil, []string{"system:kiosk", "banner"}, "kiosk"},
		"what is depended on comes first":    {nil, []string{"system:kiosk", "greet"}, "greet kiosk"},
		"an independent system, its members": {independent, []string{"system:kiosk"}, "banner ticker"},
		"a member of an independent system":  {independent, []string{"ticker"}, "ticker"},
		"a member that left its group":       {left, []string{"banner"}, "banner"},
		"the group a member left":            {left, []string{"system:kiosk"}, "kiosk"},
	} {
		plansUnits(t, name, company(c.said...), on("release/1.0", nil, "fix: trim"), c.named, c.units)
	}
	plan, err := release.Plan(context.Background(), company(left...), &strategies{}, on("release/1.0", nil, "fix: trim"), asking("system:kiosk"))
	if err != nil || len(unitOf(t, plan, "kiosk").GetMembers()) != 1 {
		t.Errorf("a group a member left still holds it: %v, %v", plan, err)
	}
	both, err := release.Plan(context.Background(), company(), &strategies{}, on("release/1.0", nil, "fix: trim"), asking("system:kiosk", "greet"))
	if err != nil || len(unitOf(t, both, "kiosk").GetDependsOn()) != 1 || unitOf(t, both, "kiosk").GetDependsOn()[0].GetName() != "greet" {
		t.Errorf("kiosk depends on greet in the plan: %v, %v", both, err)
	}
}

func TestARequestIsRefusedForWhatItAsks(t *testing.T) {
	history := on("release/1.0", nil, "fix: trim")
	if _, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("greet")); err != nil {
		t.Fatalf("a sound request was refused, so this test proves nothing: %v", err)
	}
	for name, c := range map[string]struct {
		request           *buildv1.PlanReleaseRequest
		says              string
		unknown, unserved bool
	}{
		"nothing named":        {asking(), "name what to release", false, false},
		"an entity twice":      {asking("greet", "component:default/greet"), "component:default/greet is named twice", false, false},
		"an entity not held":   {asking("ghost"), "the workspace holds no entity component:default/ghost", true, false},
		"an entity of no name": {&buildv1.PlanReleaseRequest{Entities: []*typev1.EntityRef{{Kind: "component", Namespace: "default"}}}, "names no entity", false, false},
	} {
		_, err := release.Plan(context.Background(), company(), &strategies{}, history, c.request)
		var refusal *release.Refusal
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), c.says) || refusal.Unknown != c.unknown || refusal.Unserved != c.unserved {
			t.Errorf("%s: want a refusal saying %q, got %v", name, c.says, err)
		}
	}
	// What names no entity says so once, and why.
	_, err := release.Plan(context.Background(), company(), &strategies{}, history, &buildv1.PlanReleaseRequest{Entities: []*typev1.EntityRef{{Kind: "component", Namespace: "default"}}})
	if err == nil || !strings.HasSuffix(err.Error(), `"component:default/" names no entity: a reference has a kind and a name`) || strings.Count(err.Error(), "names no entity") != 1 {
		t.Errorf("an entity of no name: %v", err)
	}
	for name, c := range map[string]struct {
		named, says string
	}{
		"a system across repositories": {"system:storefront", "storefront's members are in services and platform; a unit is released from one repository"},
		"a system with no members":     {"system:empty", "system:default/empty has no members to release"},
	} {
		_, err := release.Plan(context.Background(), company(), &strategies{}, history, asking(c.named))
		mistaken(t, name, err, c.says)
	}
}

// mistaken fails where err, of the case named, is not a mistake of what
// a repository says that says what is given.
func mistaken(t *testing.T, name string, err error, says string) {
	t.Helper()
	var mistake *model.Mistake
	if !errors.As(err, &mistake) || !strings.Contains(err.Error(), says) {
		t.Errorf("%s: want a mistake saying %q, got %v", name, says, err)
	}
}

func TestSettingsAreInheritedNearestFirstAndAMemberOfASyncedGroupSaysNone(t *testing.T) {
	resolved, err := release.Resolve(company(
		says("domain:retail", "initial-version", "2.0.0", "tag", "retail-{unit}-{version}"),
		says("system:kiosk", "initial-version", "1.0.0"),
		says("greet", "branching", "release:release-flow"),
	))
	if err != nil {
		t.Fatal(err)
	}
	banner, _ := resolved.Of(ref("banner"))
	if banner.Unit != ref("system:kiosk") || !banner.Synced || banner.Initial != "1.0.0" || banner.Tag != "retail-{unit}-{version}" ||
		banner.SetBy["initial-version"] != ref("system:kiosk") || banner.SetBy["tag"] != ref("domain:retail") {
		t.Errorf("a member of a synced group resolves to %+v", banner)
	}
	greet, _ := resolved.Of(ref("greet"))
	if greet.Unit != ref("greet") || greet.Group != ref("greet") || greet.Initial != release.DefaultInitial || greet.Tag != release.DefaultTag ||
		greet.Versioning != release.DefaultVersioning || greet.SetBy["branching"] != ref("greet") || len(greet.SetBy) != 1 {
		t.Errorf("an entity alone resolves to %+v", greet)
	}
	if retail, ok := resolved.Of(ref("domain:retail")); !ok || retail.Unit != (entity.Ref{}) || retail.Initial != "2.0.0" {
		t.Errorf("a domain resolves to %+v", retail)
	}
	for name, c := range map[string]struct {
		said []config.Release
		says string
	}{
		"a synced member that says a setting": {[]config.Release{says("banner", "initial-version", "3.0.0")}, "banner versions with kiosk, which says how it is released: spec.frontseat.release.initial-version"},
		"two units that spell tags alike":     {[]config.Release{says("greet", "tag", "v{version}"), says("system:kiosk", "tag", "v{version}")}, "component:default/greet and system:default/kiosk spell their tags alike in services: v{version}"},
		"an entity that says it twice":        {[]config.Release{says("greet", "initial-version", "1.0.0"), says("greet", "initial-version", "2.0.0")}, "says how it is released twice"},
	} {
		if _, err := release.Resolve(company(c.said...)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: want a mistake saying %q, got %v", name, c.says, err)
		}
	}
	// A member that leaves its group says its own settings.
	if _, err := release.Resolve(company(says("banner", "grouping", "independent", "initial-version", "3.0.0"))); err != nil {
		t.Errorf("a member that left its group may say how it is released: %v", err)
	}
}

func TestTheSameRequestAtTheSameCommitsComesToTheSameEtag(t *testing.T) {
	plan := func(history histories, names ...string) string {
		p, err := release.Plan(context.Background(), company(), &strategies{}, history, asking(names...))
		if err != nil {
			t.Fatal(err)
		}
		return p.GetEtag()
	}
	one := plan(on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim"), "greet")
	if again := plan(on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim"), "greet"); again != one || len(one) != 64 {
		t.Errorf("the same plan has the etags %q and %q", one, again)
	}
	later := on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim")
	later["services"].head = "later"
	for name, other := range map[string]string{
		"another commit":  plan(later, "greet"),
		"another request": plan(on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim"), "greet", "system:kiosk"),
		"another version": plan(on("release/1.26", []string{"greet/v1.26.0"}, "feat: more", "fix: trim"), "log"),
	} {
		if other == one {
			t.Errorf("%s has the same etag", name)
		}
	}
}

// Under Release Flow a line's tags are on its release branch, and no
// other branch has them in its history: a unit's last release is still
// its own, wherever its tag is.
func TestALinesLastReleaseIsItsOwnWhereverItsTagIs(t *testing.T) {
	tags := func(reachable bool, names ...string) []vcs.Tag {
		var out []vcs.Tag
		for i, name := range names {
			out = append(out, vcs.Tag{Name: name, Revision: fmt.Sprintf("r%d", i), Reachable: reachable})
		}
		return out
	}
	for name, c := range map[string]struct {
		branch     string
		tags       []vcs.Tag
		said       []config.Release
		messages   []string
		last, next string
		since      string
		first      bool
	}{
		"a new line, the earlier line's tags on its own branch": {"release/1.27", tags(false, "greet/v1.26.0", "greet/v1.26.1"), nil, []string{"feat: by name"}, "1.26.1", "1.27.0", "r1", false},
		"a new line, with an initial version said":              {"release/0.78", tags(false, "greet/v0.77.0"), []config.Release{says("greet", "initial-version", "0.77.0")}, []string{"fix: trim"}, "0.77.0", "0.78.0", "r0", false},
		"an earlier line maintained after a later one":          {"release/1.26", append(tags(true, "greet/v1.26.0", "greet/v1.26.1"), vcs.Tag{Name: "greet/v1.27.0", Revision: "later"}), nil, []string{"fix: trim"}, "1.26.1", "1.26.2", "r1", false},
		"never released at all":                                 {"release/1.27", nil, nil, []string{"fix: trim"}, "", "1.27.0", "", true},
	} {
		history := on(c.branch, nil, c.messages...)
		history["services"].tags = c.tags
		plan, err := release.Plan(context.Background(), company(c.said...), &strategies{}, history, asking("greet"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		greet := unitOf(t, plan, "greet")
		isFirst := greet.GetReasons()[0].GetKind() == buildv1.ReasonKind_REASON_KIND_FIRST_RELEASE
		if greet.GetLastVersion() != c.last || greet.GetNextVersion() != c.next || history["services"].since != c.since || isFirst != c.first {
			t.Errorf("%s: last %q, next %q, changes since %q, first %v; want %q, %q, %q, %v", name, greet.GetLastVersion(), greet.GetNextVersion(), history["services"].since, isFirst, c.last, c.next, c.since, c.first)
		}
	}
	// A tag of the line that is not in the branch's history is no release
	// of the branch: the line's first version is then taken, and not new.
	astray := on("release/1.26", nil, "fix: trim")
	astray["services"].tags = tags(false, "greet/v1.26.0")
	if _, err := release.Plan(context.Background(), company(), &strategies{}, astray, asking("greet")); err == nil || !strings.Contains(err.Error(), "greet 1.26.0 is not new: greet/v1.26.0 names another commit") {
		t.Errorf("a line whose tag is not in its history: %v", err)
	}
	// A line with no release of its own, when a later one is released, is
	// not released after it.
	history := on("release/1.26", nil, "fix: trim")
	history["services"].tags = tags(false, "greet/v1.27.0")
	if _, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("greet")); err == nil || !strings.Contains(err.Error(), "greet has no release on the line 1.26 of release/1.26, and is released as 1.27.0 already") {
		t.Errorf("a line released after a later one: %v", err)
	}
}

func TestOfTwoTagsOnTheCommitPlannedFromTheHigherIsItsRelease(t *testing.T) {
	history := on("release/1.0", nil)
	history["services"].tags = []vcs.Tag{{Name: "greet/v1.0.10", Revision: "head", Reachable: true}, {Name: "greet/v1.0.9", Revision: "head", Reachable: true}}
	plan, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("greet"))
	if err != nil {
		t.Fatal(err)
	}
	if greet := unitOf(t, plan, "greet"); greet.GetLastVersion() != "1.0.10" || greet.GetTag() != "greet/v1.0.10" {
		t.Errorf("it is released as %s, tagged %s", greet.GetLastVersion(), greet.GetTag())
	}
}

// A version is the same version whatever is written after its plus.
func TestBuildMetadataNamesNoOtherVersion(t *testing.T) {
	history := on("release/1.0", []string{"greet/v1.0.0"}, "fix: trim")
	history["services"].tags = append(history["services"].tags, vcs.Tag{Name: "greet/v1.0.1+b1", Revision: "elsewhere"})
	if _, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("greet")); err == nil || !strings.Contains(err.Error(), "greet 1.0.1 is not new") {
		t.Errorf("a version tagged with build metadata elsewhere: %v", err)
	}
}

func TestATagTheVersionControlSystemRefusesIsRefusedWhenPlanned(t *testing.T) {
	sound := company(says("greet", "tag", "{unit}-v{version}"))
	if _, err := release.Plan(context.Background(), sound, &strategies{}, on("release/1.0", nil, "fix: trim"), asking("greet")); err != nil {
		t.Fatalf("a sound tag was refused, so this test proves nothing: %v", err)
	}
	for _, pattern := range []string{".a/v{version}", "{unit}.lock/{version}", "x/.b/{version}"} {
		_, err := release.Plan(context.Background(), company(says("greet", "tag", pattern)), &strategies{}, on("release/1.0", nil, "fix: trim"), asking("greet"))
		var mistake *model.Mistake
		if !errors.As(err, &mistake) || !strings.Contains(err.Error(), "is no name git takes for a tag") {
			t.Errorf("%s: %v", pattern, err)
		}
	}
}

// wrong is a strategy that answers what the test has it answer.
type wrong struct {
	strategies
	branch  func(*pluginv1.BranchResponse)
	version func(*pluginv1.VersionResponse)
}

func (w *wrong) Branch(ctx context.Context, plugin string, r *pluginv1.BranchRequest) (*pluginv1.BranchResponse, error) {
	answer, err := w.strategies.Branch(ctx, plugin, r)
	if w.branch != nil {
		w.branch(answer)
	}
	return answer, err
}

func (w *wrong) Version(ctx context.Context, plugin string, r *pluginv1.VersionRequest) (*pluginv1.VersionResponse, error) {
	answer, err := w.strategies.Version(ctx, plugin, r)
	if w.version != nil {
		w.version(answer)
	}
	return answer, err
}

// What a strategy answers is checked before any of it is in a plan.
func TestAStrategysAnswerIsHeldToTheContract(t *testing.T) {
	plan := func(branch string, w *wrong) error {
		_, err := release.Plan(context.Background(), company(), w, on(branch, []string{"greet/v1.26.0"}, "fix: trim"), asking("greet"))
		return err
	}
	if err := plan("release/1.26", &wrong{}); err != nil {
		t.Fatalf("a sound answer was refused, so this test proves nothing: %v", err)
	}
	if err := plan("main", &wrong{}); err != nil {
		t.Fatalf("a sound answer that a branch releases nothing was refused: %v", err)
	}
	for name, c := range map[string]struct {
		branch string
		w      *wrong
	}{
		"a bump that is none":            {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Bump = 99 }}},
		"a reason's bump that is none":   {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Reasons[0].Bump = -5 }}},
		"a reason of a commit not given": {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Reasons[0].Commit = "elsewhere" }}},
		"a reason twice":                 {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Reasons = append(a.Reasons, a.Reasons[0]) }}},
		"a summary of two lines":         {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Reasons[0].Summary = "one\ntwo" }}},
		"no summary":                     {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Reasons[0].Summary = "" }}},
		"notes past their size":          {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Notes = strings.Repeat("x", 64<<10+1) }}},
		"a version below its last":       {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Version = "1.25.0" }}},
		"a version that is none":         {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Version = "next" }}},
		"a prerelease version":           {"release/1.26", &wrong{version: func(a *pluginv1.VersionResponse) { a.Version = "1.26.1-rc.1" }}},
		"a channel that is no name":      {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Channel = "any\nthing --option" }}},
		"a line that is none":            {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Line = "1" }}},
		"a line written two ways":        {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Line = "01.26" }}},
		"a reason beside a release":      {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Reason = "why not" }}},
		"nothing released, and no why":   {"main", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Reason = " " }}},
		"nothing released, on a line":    {"main", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Line = "1.26" }}},
		"a reason of two lines":          {"main", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Reason = "one\ntwo" }}},
		"nothing released, a prerelease": {"main", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Prerelease = true }}},
		"nothing released, and how far":  {"main", &wrong{branch: func(a *pluginv1.BranchResponse) { a.AtLeast = typev1.Bump_BUMP_MINOR }}},
		"a prerelease held to a line":    {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Prerelease = true }}},
		"how far, of no prerelease":      {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.AtLeast = typev1.Bump_BUMP_MINOR }}},
		"a prerelease above by no bump":  {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Prerelease, a.Line, a.AtLeast = true, "", 99 }}},
		"a prerelease above by none":     {"release/1.26", &wrong{branch: func(a *pluginv1.BranchResponse) { a.Prerelease, a.Line, a.AtLeast = true, "", typev1.Bump_BUMP_NONE }}},
	} {
		err := plan(c.branch, c.w)
		var mistake *model.Mistake
		if err == nil || errors.As(err, &mistake) {
			t.Errorf("%s: want the strategy's answer refused as its own fault, got %v", name, err)
		}
	}
}

// A commit is said to have changed the members whose files it changed,
// and no others.
type perMember struct{ histories }

func (h perMember) Changes(_ context.Context, _, _ string, directories []string) ([]vcs.Change, error) {
	both := []vcs.Change{{Revision: "c0", Message: "fix: the banner"}, {Revision: "c1", Message: "fix: both"}, {Revision: "c2", Message: "fix: the ticker"}}
	switch strings.Join(directories, " ") {
	case "banner":
		return both[:2], nil
	case "ticker":
		return both[1:], nil
	}
	return both, nil
}

type seeing struct {
	strategies
	commits []*pluginv1.ReleaseCommit
}

func (s *seeing) Version(ctx context.Context, plugin string, r *pluginv1.VersionRequest) (*pluginv1.VersionResponse, error) {
	s.commits = r.GetCommits()
	return s.strategies.Version(ctx, plugin, r)
}

func TestACommitNamesTheMembersWhoseFilesItChanged(t *testing.T) {
	seen := &seeing{}
	if _, err := release.Plan(context.Background(), company(), seen, perMember{on("release/1.0", []string{"kiosk/v1.0.0"})}, asking("system:kiosk")); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, commit := range seen.commits {
		var members []string
		for _, member := range commit.GetMembers() {
			members = append(members, member.GetName())
		}
		got = append(got, commit.GetId()+":"+strings.Join(members, "+"))
	}
	if want := "c0:banner c1:banner+ticker c2:ticker"; strings.Join(got, " ") != want {
		t.Errorf("commits changed %v, want %s", got, want)
	}
}

// A repository is asked where it stands once for a plan.
type counting struct {
	histories
	stood map[string]int
}

func (c *counting) Standing(ctx context.Context, name string) (vcs.Standing, error) {
	c.stood[name]++
	return c.histories.Standing(ctx, name)
}

func TestEveryUnitOfARepositoryIsPlannedFromOneCommit(t *testing.T) {
	history := &counting{histories: on("release/1.0", nil, "fix: trim"), stood: map[string]int{}}
	if _, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("greet", "system:kiosk")); err != nil {
		t.Fatal(err)
	}
	if history.stood["services"] != 1 || history.stood["platform"] != 1 {
		t.Errorf("for one plan the repositories were asked where they stand %v times", history.stood)
	}
}

// subdomained is the company where the domain retail is a subdomain of
// the domain company, as Backstage's spec.subdomainOf says.
func subdomained(said ...config.Release) release.Workspace {
	w := company(said...)
	for i := range w.Entities {
		if w.Entities[i].Ref == ref("domain:retail") {
			w.Entities[i].PartOf = []entity.Ref{ref("domain:company")}
		}
	}
	w.Entities = append(w.Entities, release.Entity{Ref: ref("domain:company"), Repository: "services"})
	return w
}

// selfContained is the company where the domain retail says it is a
// subdomain of itself.
func selfContained() release.Workspace {
	w := company()
	for i := range w.Entities {
		if w.Entities[i].Ref == ref("domain:retail") {
			w.Entities[i].PartOf = []entity.Ref{ref("domain:retail")}
		}
	}
	return w
}

func TestWhatIsNotServedOfGroupingIsRefusedAsThat(t *testing.T) {
	history := on("release/1.0", nil, "fix: trim")
	if _, err := release.Plan(context.Background(), company(says("domain:retail", "grouping", "synced")), &strategies{}, history, asking("domain:retail")); err != nil {
		t.Fatalf("a domain in no domain was refused, so this test proves nothing: %v", err)
	}
	companySynced := says("domain:company", "grouping", "synced")
	retailSynced := says("domain:retail", "grouping", "synced")
	nested := "domain:default/retail is a subdomain of domain:default/company, and releasing a domain within a domain is not served yet"
	for name, c := range map[string]struct {
		w     release.Workspace
		named string
		says  string
	}{
		"a subdomain":                                       {subdomained(), "domain:retail", nested},
		"a domain that holds one":                           {subdomained(), "domain:company", nested},
		"a synced subdomain":                                {subdomained(retailSynced), "domain:retail", nested},
		"a member of a synced subdomain":                    {subdomained(retailSynced), "banner", nested},
		"a domain that says synced and holds one":           {subdomained(companySynced), "domain:company", nested},
		"a member of a subdomain of a synced domain":        {subdomained(companySynced), "banner", nested},
		"a system of a subdomain of a synced domain":        {subdomained(companySynced), "system:kiosk", nested},
		"a member of a subdomain where neither says synced": {subdomained(), "banner", nested},
		"a system that left a synced subdomain":             {subdomained(retailSynced, says("system:kiosk", "grouping", "independent")), "system:kiosk", nested},
		"a member that left a system of a synced subdomain": {subdomained(retailSynced, says("banner", "grouping", "independent")), "banner", nested},
		"a domain that is its own subdomain":                {selfContained(), "domain:retail", "domain:default/retail is a subdomain of domain:default/retail"},
		"a member of a domain that is its own subdomain":    {selfContained(), "banner", "domain:default/retail is a subdomain of domain:default/retail"},
	} {
		var refusal *release.Refusal
		_, err := release.Plan(context.Background(), c.w, &strategies{}, history, asking(c.named))
		if !errors.As(err, &refusal) || !refusal.Unserved || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: want it refused as not served, got %v", name, err)
		}
	}
	// What no domain within a domain holds is released as before.
	if _, err := release.Plan(context.Background(), subdomained(companySynced), &strategies{}, history, asking("greet", "system:storefront", "log")); err == nil || !strings.Contains(err.Error(), "storefront's members are in services and platform") {
		t.Errorf("what is in no domain beside a subdomain is refused with it: %v", err)
	}
	if _, err := release.Plan(context.Background(), subdomained(companySynced), &strategies{}, history, asking("greet", "log")); err != nil {
		t.Errorf("what is in no domain beside a subdomain is refused with it: %v", err)
	}
	for name, said := range map[string][]config.Release{
		"a member of an independent system that says synced": {says("system:kiosk", "grouping", "independent"), says("banner", "grouping", "synced")},
		"an entity of no system that says synced":            {says("greet", "grouping", "synced")},
		"a system of a synced domain that says a setting":    {says("domain:retail", "grouping", "synced"), says("system:kiosk", "tag", "k-{version}")},
		"a member of a synced domain that says a setting":    {says("domain:retail", "grouping", "synced"), says("banner", "initial-version", "3.0.0")},
		"a system of a synced domain that says synced":       {says("domain:retail", "grouping", "synced"), says("system:kiosk", "grouping", "synced")},
	} {
		if _, err := release.Resolve(company(said...)); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}

// A plan has the same etag before it is carried out and after: a unit
// whose tag the commit carries is named as it was planned to move.
func TestAnEtagIsTheSameBeforeAReleaseAndAfter(t *testing.T) {
	before := on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim")
	planned, err := release.Prepare(context.Background(), company(), &strategies{}, before, asking("greet"))
	if err != nil {
		t.Fatal(err)
	}
	if planned.Repositories[ref("greet")] != "services" {
		t.Errorf("greet is released from %q", planned.Repositories[ref("greet")])
	}
	after := on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim")
	after["services"].tags = append(after["services"].tags, vcs.Tag{Name: planned.Plan.GetUnits()[0].GetTag(), Revision: "head", Reachable: true})
	released, err := release.Plan(context.Background(), company(), &strategies{}, after, asking("greet"))
	if err != nil {
		t.Fatal(err)
	}
	if kind := released.GetUnits()[0].GetReasons()[0].GetKind(); kind != buildv1.ReasonKind_REASON_KIND_RELEASED {
		t.Fatalf("after its tag is written the unit is %v", kind)
	}
	if released.GetEtag() != planned.Plan.GetEtag() {
		t.Error("the plan has another etag once its tag is written")
	}
	// Another tag on that commit than the one planned is another plan.
	other := on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim", "fix: again")
	if different, err := release.Plan(context.Background(), company(), &strategies{}, other, asking("greet")); err != nil || different.GetEtag() == planned.Plan.GetEtag() {
		t.Errorf("a plan of other commits has the same etag: %v", err)
	}
}

// Only the tag a plan would write makes the commit that plan carried
// out. Any other tag of the unit there is another state, with another
// etag: a plan that was seen is never carried out as a version it did
// not name.
func TestATagThePlanDidNotNameIsAnotherPlan(t *testing.T) {
	planned, err := release.Plan(context.Background(), company(), &strategies{}, on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim"), asking("greet"))
	if err != nil || planned.GetUnits()[0].GetNextVersion() != "1.26.1" {
		t.Fatalf("the plan is %v, %v", planned, err)
	}
	with := func(tags ...string) *buildv1.ReleasePlan {
		history := on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim")
		for _, tag := range tags {
			history["services"].tags = append(history["services"].tags, vcs.Tag{Name: tag, Revision: "head", Reachable: true})
		}
		plan, err := release.Plan(context.Background(), company(), &strategies{}, history, asking("greet"))
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	if carried := with("greet/v1.26.1"); carried.GetEtag() != planned.GetEtag() {
		t.Fatal("the plan carried out has another etag, so this test proves nothing")
	}
	// A prerelease tagged there is no release: the plan is as it was.
	if same := with("greet/v1.26.1-rc.1"); same.GetEtag() != planned.GetEtag() || same.GetUnits()[0].GetNextVersion() != "1.26.1" {
		t.Errorf("a prerelease tag changed the plan: %v", same.GetUnits()[0])
	}
	for name, tags := range map[string][]string{
		"a version a person tagged":               {"greet/v1.26.9"},
		"the planned tag beside another":          {"greet/v1.26.1", "greet/v1.26.9"},
		"the planned tag beside a prerelease":     {"greet/v1.26.1", "greet/v1.26.1-rc.1"},
		"the planned version with build metadata": {"greet/v1.26.1+hand"},
	} {
		if other := with(tags...); other.GetEtag() == planned.GetEtag() {
			t.Errorf("%s: the commit has the etag of the plan that named 1.26.1; it says %v", name, other.GetUnits()[0])
		}
	}
}

// A plan is of the commits of every repository of the workspace: another
// commit in one no unit is in is another plan.
func TestAPlanIsOfEveryRepositoryOfTheWorkspace(t *testing.T) {
	one, err := release.Plan(context.Background(), company(), &strategies{}, on("release/1.0", nil, "fix: trim"), asking("greet"))
	if err != nil {
		t.Fatal(err)
	}
	moved := on("release/1.0", nil, "fix: trim")
	moved["platform"].head = "later"
	other, err := release.Plan(context.Background(), company(), &strategies{}, moved, asking("greet"))
	if err != nil {
		t.Fatal(err)
	}
	if other.GetEtag() == one.GetEtag() {
		t.Error("a repository greet is built from moved, and the plan has the same etag")
	}
}

// retailed is the company where storefront's hello is instead part of
// lobby, a second system of the domain retail beside kiosk.
func retailed(said ...config.Release) release.Workspace {
	w := company(said...)
	for i := range w.Entities {
		if w.Entities[i].Ref == ref("hello") {
			w.Entities[i].PartOf = []entity.Ref{ref("system:lobby")}
		}
	}
	w.Entities = append(w.Entities, release.Entity{Ref: ref("system:lobby"), Repository: "services", PartOf: []entity.Ref{ref("domain:retail")}})
	return w
}

func TestADomainThatSaysSyncedIsOneUnitOfEveryMemberOfItsSystems(t *testing.T) {
	synced := says("domain:retail", "grouping", "synced", "initial-version", "2.0.0")
	resolved, err := release.Resolve(retailed(synced))
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range []string{"domain:retail", "system:kiosk", "system:lobby", "banner", "ticker", "hello"} {
		s, _ := resolved.Of(ref(each))
		if s.Unit != ref("domain:retail") || s.Group != ref("domain:retail") || !s.Synced || s.Initial != "2.0.0" || s.SetBy["initial-version"] != ref("domain:retail") {
			t.Errorf("%s resolves to %+v", each, s)
		}
	}
	if members := resolved.Members(ref("domain:retail")); len(members) != 3 {
		t.Errorf("the domain's members are %v", members)
	}
	history := on("release/2.0", []string{"retail/v2.0.0", "kiosk/v2.0.4"}, "fix: trim")
	for name, named := range map[string][]string{
		"the domain":                 {"domain:retail"},
		"a system of it":             {"system:lobby"},
		"a member of a system of it": {"banner"},
		"two of them, once":          {"domain:retail", "system:kiosk", "hello"},
	} {
		plan, err := release.Plan(context.Background(), retailed(synced), &strategies{}, history, asking(named...))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(plan.GetUnits()) != 1 {
			t.Errorf("%s: the units are %v", name, plan.GetUnits())
			continue
		}
		retail := unitOf(t, plan, "retail")
		if len(retail.GetMembers()) != 3 || retail.GetGrouping() != buildv1.Grouping_GROUPING_SYNCED || retail.GetGroup().GetName() != "retail" ||
			retail.GetLastVersion() != "2.0.0" || retail.GetNextVersion() != "2.0.1" || retail.GetTag() != "retail/v2.0.1" {
			t.Errorf("%s: the domain is planned as %v", name, retail)
		}
	}
	// A domain's change is asked of every member's directories.
	if got := strings.Join(history["services"].directories, " "); got != "banner hello ticker" {
		t.Errorf("history was asked of %q", got)
	}
	// A system that says independent leaves the domain, and versions its
	// members on their own.
	left := retailed(synced, says("system:lobby", "grouping", "independent"))
	plan, err := release.Plan(context.Background(), left, &strategies{}, history, asking("domain:retail", "system:lobby"))
	if err != nil {
		t.Fatal(err)
	}
	if plannedUnits(plan) != "hello retail" || len(unitOf(t, plan, "retail").GetMembers()) != 2 || unitOf(t, plan, "hello").GetGroup().GetName() != "lobby" {
		t.Errorf("a system that left its domain is planned as %v", plan.GetUnits())
	}
	// A domain is a unit, and a unit is released from one repository.
	across := retailed(synced)
	for i := range across.Entities {
		if across.Entities[i].Ref == ref("gateway") {
			across.Entities[i].PartOf = []entity.Ref{ref("system:lobby")}
		}
	}
	_, err = release.Plan(context.Background(), across, &strategies{}, history, asking("domain:retail"))
	mistaken(t, "a domain across repositories", err, "retail's members are in services and platform; a unit is released from one repository")
}

func TestADomainThatSaysNothingIsReleasedAsEachOfItsSystems(t *testing.T) {
	history := on("release/1.0", nil, "fix: trim")
	for name, c := range map[string]struct {
		said  []config.Release
		units string
	}{
		"its synced systems, each a unit": {nil, "kiosk lobby"},
		"an independent one's members":    {[]config.Release{says("system:lobby", "grouping", "independent")}, "hello kiosk"},
		"a member that left one":          {[]config.Release{says("banner", "grouping", "independent")}, "kiosk lobby"},
	} {
		plansUnits(t, name, retailed(c.said...), history, []string{"domain:retail"}, c.units)
	}
	if retail, _ := release.Resolve(retailed()); retail == nil {
		t.Fatal("no resolution")
	} else if s, _ := retail.Of(ref("domain:retail")); s.Unit != (entity.Ref{}) || s.Synced {
		t.Errorf("a domain that says nothing resolves to %+v", s)
	}
}

func TestADomainWithNothingToReleaseIsRefusedByName(t *testing.T) {
	empty := func(said ...config.Release) release.Workspace {
		w := company(said...)
		w.Entities = append(w.Entities, release.Entity{Ref: ref("domain:empty"), Repository: "services"},
			release.Entity{Ref: ref("system:hollow"), Repository: "services", PartOf: []entity.Ref{ref("domain:empty")}})
		return w
	}
	for name, said := range map[string][]config.Release{
		"synced":         {says("domain:empty", "grouping", "synced")},
		"saying nothing": nil,
	} {
		_, err := release.Plan(context.Background(), empty(said...), &strategies{}, on("release/1.0", nil, "fix: trim"), asking("domain:empty"))
		mistaken(t, name, err, "domain:default/empty has no members to release")
	}
}

// The release line of a system continues as a domain's of the same name:
// a unit's last release is found by the tags its name spells, so a
// repository whose system frontseat became a domain frontseat, of
// several systems, releases after frontseat/v0.77.0.
func TestADomainContinuesTheTagLineOfASystemOfItsName(t *testing.T) {
	w := release.Workspace{
		Entities: []release.Entity{
			{Ref: ref("domain:frontseat"), Repository: "services"},
			{Ref: ref("system:build"), Repository: "services", PartOf: []entity.Ref{ref("domain:frontseat")}},
			{Ref: ref("system:workspace"), Repository: "services", PartOf: []entity.Ref{ref("domain:frontseat")}},
			{Ref: ref("server"), Repository: "services", Roots: []string{"server"}, PartOf: []entity.Ref{ref("system:build")}},
			{Ref: ref("client"), Repository: "services", Roots: []string{"client"}, PartOf: []entity.Ref{ref("system:workspace")}},
		},
		Said:   []config.Release{says("domain:frontseat", "grouping", "synced")},
		Listed: map[string][]string{"services": {"release"}},
	}
	history := on("release/0.77", []string{"frontseat/v0.77.0"}, "fix: trim")
	delete(history, "platform")
	plan, err := release.Plan(context.Background(), w, &strategies{}, history, asking("domain:frontseat"))
	if err != nil {
		t.Fatal(err)
	}
	frontseat := unitOf(t, plan, "frontseat")
	if frontseat.GetLastVersion() != "0.77.0" || frontseat.GetNextVersion() != "0.77.1" || frontseat.GetTag() != "frontseat/v0.77.1" || len(frontseat.GetMembers()) != 2 {
		t.Errorf("the domain is planned as %v", frontseat)
	}
}

func TestASystemOfASyncedDomainThatSaysSyncedIsRefusedByName(t *testing.T) {
	history := on("release/1.0", nil, "fix: trim")
	_, err := release.Plan(context.Background(), retailed(says("domain:retail", "grouping", "synced"), says("system:kiosk", "grouping", "synced")), &strategies{}, history, asking("domain:retail"))
	mistaken(t, "a system that says synced", err, "kiosk is part of the synced domain retail, which groups it: say independent to release it on its own, or nothing")
	// Saying nothing, it versions with its domain; saying independent, on
	// its own.
	plansUnits(t, "a system that says nothing", retailed(says("domain:retail", "grouping", "synced")), history, []string{"system:kiosk"}, "retail")
	plansUnits(t, "a system that says independent", retailed(says("domain:retail", "grouping", "synced"), says("system:kiosk", "grouping", "independent")), history, []string{"system:kiosk"}, "banner ticker")
	// A synced system in a domain that says nothing is the group it is.
	plansUnits(t, "a synced system of a domain that groups nothing", retailed(says("system:kiosk", "grouping", "synced")), history, []string{"system:kiosk"}, "kiosk")
}

// unheld is the company where what an entity is part of is named and is
// not held: the workspace was opened without its catalog.
func unheld(entityName, partOf string) release.Workspace {
	w := company(says("domain:retail", "grouping", "synced"))
	var kept []release.Entity
	for _, each := range w.Entities {
		if each.Ref == ref(partOf) {
			continue
		}
		if each.Ref == ref(entityName) {
			each.PartOf = []entity.Ref{ref(partOf)}
		}
		kept = append(kept, each)
	}
	w.Entities = kept
	return w
}

func TestWhatAnEntityIsPartOfThatTheWorkspaceDoesNotHoldIsRefusedByName(t *testing.T) {
	history := on("release/1.0", nil, "fix: trim")
	plansUnits(t, "the catalog held", company(says("domain:retail", "grouping", "synced")), history, []string{"banner"}, "retail")
	for name, c := range map[string]struct {
		w     release.Workspace
		named string
		says  string
	}{
		"a member of a system of a domain not held": {unheld("system:kiosk", "domain:retail"), "banner", "system:default/kiosk is part of domain:default/retail, which this workspace does not hold: open the workspace with its catalog"},
		"a system of a domain not held":             {unheld("system:kiosk", "domain:retail"), "system:kiosk", "system:default/kiosk is part of domain:default/retail, which this workspace does not hold"},
		"a member of a system not held":             {unheld("banner", "system:kiosk"), "banner", "component:default/banner is part of system:default/kiosk, which this workspace does not hold"},
		"a domain within a domain not held":         {unheld("domain:retail", "domain:company"), "banner", "domain:default/retail is part of domain:default/company, which this workspace does not hold"},
	} {
		_, err := release.Plan(context.Background(), c.w, &strategies{}, history, asking(c.named))
		mistaken(t, name, err, c.says)
	}
	// What is part of nothing unheld is released as before.
	plansUnits(t, "an entity beside one", unheld("system:kiosk", "domain:retail"), history, []string{"greet"}, "greet")
	// The graph still says how each is released.
	if _, err := release.Resolve(unheld("system:kiosk", "domain:retail")); err != nil {
		t.Errorf("a workspace that does not hold a group fails resolving: %v", err)
	}
}

func TestVersionsNeverGoBackwardsAUnitContinuesAboveWhatItsMembersWereReleasedAs(t *testing.T) {
	synced := says("domain:retail", "grouping", "synced")
	for name, c := range map[string]struct {
		w       release.Workspace
		history histories
		named   string
		unit    string
		last    string
		next    string
		tag     string
	}{
		"a domain above its system's release":         {company(synced), on("release/2.3", []string{"kiosk/v2.3.0"}, "fix: trim"), "domain:retail", "retail", "2.3.0", "2.3.1", "retail/v2.3.1"},
		"a domain above its member's own release":     {company(synced), on("release/2.3", []string{"banner/v2.3.4", "kiosk/v2.3.0"}, "fix: trim"), "domain:retail", "retail", "2.3.4", "2.3.5", "retail/v2.3.5"},
		"a system above its member's own release":     {company(), on("release/1.4", []string{"ticker/v1.4.2"}, "fix: trim"), "system:kiosk", "kiosk", "1.4.2", "1.4.3", "kiosk/v1.4.3"},
		"a domain on a new line above its system's":   {company(synced), on("release/2.4", []string{"kiosk/v2.3.0"}, "fix: trim"), "domain:retail", "retail", "2.3.0", "2.4.0", "retail/v2.4.0"},
		"a unit's own release, before its members'":   {company(synced), on("release/2.3", []string{"retail/v2.3.1", "kiosk/v2.3.7"}, "fix: trim"), "domain:retail", "retail", "2.3.1", "2.3.2", "retail/v2.3.2"},
		"a prerelease of its own is no release of it": {company(synced), on("release/2.3", []string{"retail/v2.3.1-main-20261120101500", "kiosk/v2.3.0"}, "fix: trim"), "domain:retail", "retail", "2.3.0", "2.3.1", "retail/v2.3.1"},
		"an entity no group's release reaches":        {company(synced), on("release/1.0", []string{"kiosk/v2.3.0"}, "fix: trim"), "greet", "greet", "", "1.0.0", "greet/v1.0.0"},
		"a domain whose members were never released":  {company(synced), on("release/1.0", []string{"greet/v3.0.0"}, "fix: trim"), "domain:retail", "retail", "", "1.0.0", "retail/v1.0.0"},
	} {
		plan, err := release.Plan(context.Background(), c.w, &strategies{}, c.history, asking(c.named))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		unit := unitOf(t, plan, c.unit)
		if unit.GetLastVersion() != c.last || unit.GetNextVersion() != c.next || unit.GetTag() != c.tag {
			t.Errorf("%s: planned %s -> %s as %s, want %s -> %s as %s", name, unit.GetLastVersion(), unit.GetNextVersion(), unit.GetTag(), c.last, c.next, c.tag)
		}
	}
	// A member's former release on the commit planned from is not the
	// unit's: it is not released already.
	history := on("release/2.3", []string{"kiosk/v2.3.0"}, "fix: trim")
	history["services"].tags[0].Revision = "head"
	history["services"].changes = nil
	plan, err := release.Plan(context.Background(), company(synced), &strategies{}, history, asking("domain:retail"))
	if err != nil {
		t.Fatal(err)
	}
	if retail := unitOf(t, plan, "retail"); retail.GetReasons()[0].GetKind() == buildv1.ReasonKind_REASON_KIND_RELEASED || retail.GetTag() == "retail/v2.3.0" {
		t.Errorf("a domain is released as its system's tag: %v", retail)
	}
}

func TestADomainWhoseEverySystemLeftItIsRefusedSayingSo(t *testing.T) {
	history := on("release/1.0", nil, "fix: trim")
	left := retailed(says("domain:retail", "grouping", "synced"), says("system:kiosk", "grouping", "independent"), says("system:lobby", "grouping", "independent"))
	_, err := release.Plan(context.Background(), left, &strategies{}, history, asking("domain:retail"))
	mistaken(t, "every system left", err, "domain:default/retail says its systems version together, and each of them says it versions on its own: name them")
	plansUnits(t, "one system left", retailed(says("domain:retail", "grouping", "synced"), says("system:kiosk", "grouping", "independent")), history, []string{"domain:retail"}, "retail")
}

func TestAComponentThatLeftASystemOfASyncedDomainIsItsOwnUnitInItsSystem(t *testing.T) {
	resolved, err := release.Resolve(company(says("domain:retail", "grouping", "synced"), says("banner", "grouping", "independent")))
	if err != nil {
		t.Fatal(err)
	}
	banner, _ := resolved.Of(ref("banner"))
	if banner.Unit != ref("banner") || banner.Group != ref("system:kiosk") || banner.Synced || banner.SetBy["grouping"] != ref("banner") {
		t.Errorf("a component that left resolves to %+v", banner)
	}
	if members := resolved.Members(ref("domain:retail")); len(members) != 1 || members[0] != ref("ticker") {
		t.Errorf("the domain's members are %v", members)
	}
	plan, err := release.Plan(context.Background(), company(says("domain:retail", "grouping", "synced"), says("banner", "grouping", "independent")), &strategies{}, on("release/1.0", nil, "fix: trim"), asking("banner"))
	if err != nil {
		t.Fatal(err)
	}
	if unit := unitOf(t, plan, "banner"); unit.GetGroup().GetName() != "kiosk" || unit.GetGrouping() != buildv1.Grouping_GROUPING_INDEPENDENT || len(unit.GetMembers()) != 1 {
		t.Errorf("a component that left is planned as %v", unit)
	}
}

// at is a history standing at a commit made at a time, in UTC.
func (h histories) at(revision string, year int, month time.Month, day, hour, minute, second int) histories {
	h["services"].head, h["services"].time = revision, time.Date(year, month, day, hour, minute, second, 0, time.UTC).Unix()
	return h
}

// unreached is a history none of whose tags its branch reaches: under
// Release Flow a release's tag is on its release branch.
func (h histories) unreached() histories {
	for i := range h["services"].tags {
		h["services"].tags[i].Reachable = false
	}
	return h
}

// A branch that releases nothing stable releases a prerelease of the
// version it is heading for: the version its commits give, above every
// release of the unit wherever its tag is, named by the branch and by
// when the commit planned at was made, as the unit's settings spell it.
// It has no tag unless the unit says a prerelease is tagged. The control
// is the release branch, whose release is as it was.
func TestABranchThatReleasesNothingStableReleasesAPrerelease(t *testing.T) {
	const commit = "0123abcdef0123abcdef0123abcdef0123abcdef"
	then := func(h histories) histories { return h.at(commit, 2026, time.November, 21, 10, 15, 0) }
	for name, c := range map[string]struct {
		history histories
		said    []config.Release
		next    string
		tag     string
		channel string
	}{
		"named by the branch and its commit's time":       {then(on("feat/greet_by.name", []string{"greet/v1.26.0"}, "feat: by name")), nil, "1.27.0-feat-greet-by-name-20261121101500", "", "feat-greet-by-name"},
		"a fix on a branch is a patch":                    {then(on("fix/trim", []string{"greet/v1.26.0"}, "fix: trim")), nil, "1.26.1-fix-trim-20261121101500", "", "fix-trim"},
		"the default branch heads for the next minor":     {then(on("main", []string{"greet/v1.26.0"}, "fix: trim")), nil, "1.27.0-main-20261121101500", "", "main"},
		"above a release its branch does not reach":       {then(on("next", []string{"greet/v1.26.0", "greet/v1.26.4"}, "fix: trim").unreached()), nil, "1.26.5-next-20261121101500", "", "next"},
		"the default branch, above one it does not reach": {then(on("main", []string{"greet/v1.26.4"}, "feat: by name").unreached()), nil, "1.27.0-main-20261121101500", "", "main"},
		"never released": {then(on("main", nil, "feat: by name")), nil, "0.1.0-main-20261121101500", "", "main"},
		"a prerelease before it is no last release":    {then(on("main", []string{"greet/v1.26.0", "greet/v1.27.0-main-20261120101500"}, "feat: by name")), nil, "1.27.0-main-20261121101500", "", "main"},
		"as the unit spells it":                        {then(on("main", []string{"greet/v1.26.0"}, "feat: by name")), []config.Release{says("greet", "prerelease.format", "{branch}-{sha}.{epoch}")}, "1.27.0-main-0123abc.1795256100", "", "main"},
		"tagged where the unit says so":                {then(on("main", []string{"greet/v1.26.0"}, "feat: by name")), []config.Release{says("greet", "prerelease.tagged", "true")}, "1.27.0-main-20261121101500", "greet/v1.27.0-main-20261121101500", "main"},
		"what its commits do not move is not released": {then(on("main", []string{"greet/v1.26.0"}, "docs: more")), nil, "", "", "main"},
		"control: a release branch releases as it did": {then(on("release/1.26", []string{"greet/v1.26.0"}, "fix: trim")), nil, "1.26.1", "greet/v1.26.1", "1.26"},
	} {
		greet := greetOf(t, name, &strategies{prereleases: true}, c.history, c.said...)
		if greet == nil {
			continue
		}
		if greet.GetNextVersion() != c.next || greet.GetTag() != c.tag || greet.GetChannel() != c.channel {
			t.Errorf("%s: next is %q, tagged %q, on the channel %q; want %q, %q, %q", name, greet.GetNextVersion(), greet.GetTag(), greet.GetChannel(), c.next, c.tag, c.channel)
		}
		if want := strings.Contains(c.next, "-"); greet.GetPrerelease() != want {
			t.Errorf("%s: it says it is a prerelease: %v", name, greet.GetPrerelease())
		}
	}
	// It is planned from the unit's highest release, in its branch's
	// history or not, and says so: what moved it, and from what.
	unreached := greetOf(t, "from a release its branch does not reach", &strategies{prereleases: true}, then(on("main", []string{"greet/v1.26.4"}, "feat: by name").unreached()))
	if unreached.GetLastVersion() != "1.26.4" || unreached.GetBump() != typev1.Bump_BUMP_MINOR || unreached.GetReasons()[0].GetKind() != buildv1.ReasonKind_REASON_KIND_OWN_CHANGE {
		t.Errorf("planned from %q by %v for %v; want from 1.26.4, by a minor, for its own change", unreached.GetLastVersion(), unreached.GetBump(), unreached.GetReasons())
	}
	// A fix on the default branch is a minor, since that is where the
	// branch is heading; and what moves nothing holds, reached or not.
	if heading := greetOf(t, "a fix on the default branch", &strategies{prereleases: true}, then(on("main", []string{"greet/v1.26.4"}, "fix: trim").unreached())); heading.GetNextVersion() != "1.27.0-main-20261121101500" || heading.GetBump() != typev1.Bump_BUMP_MINOR {
		t.Errorf("a fix on the default branch is %q by %v", heading.GetNextVersion(), heading.GetBump())
	}
	if held := greetOf(t, "nothing moved since a release it does not reach", &strategies{prereleases: true}, then(on("main", []string{"greet/v1.26.4"}, "docs: more").unreached())); held.GetNextVersion() != "" || held.GetReasons()[0].GetKind() != buildv1.ReasonKind_REASON_KIND_UNCHANGED {
		t.Errorf("what moved nothing is released as %q for %v", held.GetNextVersion(), held.GetReasons())
	}
	// A later commit's is later, as a semantic version orders them.
	earlier, later := semver.MustParse("1.27.0-main-20261121101500"), semver.MustParse("1.27.0-main-20261121101501")
	if !later.GreaterThan(earlier) || !semver.MustParse("1.27.0").GreaterThan(later) {
		t.Errorf("%s does not come after %s and before 1.27.0", later, earlier)
	}
	// Refused: a form that makes no prerelease of a semantic version
	// once it is filled, and a prerelease's tag that names another commit.
	for name, c := range map[string]struct {
		history histories
		said    []config.Release
		says    string
	}{
		"a number written with a zero before it": {then(on("main", []string{"greet/v1.26.0"}, "feat: by name")), []config.Release{says("greet", "prerelease.format", "0{epoch}")}, "greet: spec.frontseat.release.prerelease.format, for the branch \"main\": spells what is no prerelease of a semantic version, whose parts are"},
		"a tag that names another commit":        {then(on("main", []string{"greet/v1.26.0", "greet/v1.27.0-main-20261121101500"}, "feat: by name")), []config.Release{says("greet", "prerelease.tagged", "true")}, `greet 1.27.0-main-20261121101500 is not new: greet/v1.27.0-main-20261121101500 names another commit`},
	} {
		if _, err := release.Plan(context.Background(), company(c.said...), &strategies{prereleases: true}, c.history, asking("greet")); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want %q", name, err, c.says)
		}
	}
}

// Of several settings a member of a synced group says that are its
// group's to say, the first by name is the one refused, every time.
func TestWhichSettingOfAMemberIsRefusedDoesNotChangeBetweenReads(t *testing.T) {
	for range 50 {
		_, err := release.Resolve(company(says("banner", "tag", "{unit}@{version}", "initial-version", "3.0.0", "versioning", "release:conventional-commits", "branching", "release:release-flow")))
		if err == nil || !strings.Contains(err.Error(), "spec.frontseat.release.branching is the group's to say") {
			t.Fatalf("the setting refused: %v", err)
		}
	}
}
