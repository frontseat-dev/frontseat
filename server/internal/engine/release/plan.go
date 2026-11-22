package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"google.golang.org/protobuf/proto"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

// History is what planning asks of a repository's history, by the
// workspace's name for the repository.
type History interface {
	Standing(ctx context.Context, repository string) (vcs.Standing, error)
	Tags(ctx context.Context, repository string) ([]vcs.Tag, error)
	Changes(ctx context.Context, repository, since string, directories []string) ([]vcs.Change, error)
	TagName(ctx context.Context, name string) error
}

// Strategies are the release strategies the daemon's plugins serve.
type Strategies interface {
	// Serves says whether a plugin serves a branching or a versioning
	// strategy by a name.
	Serves(plugin string, branching bool, name string) bool
	Branch(ctx context.Context, plugin string, request *pluginv1.BranchRequest) (*pluginv1.BranchResponse, error)
	Version(ctx context.Context, plugin string, request *pluginv1.VersionRequest) (*pluginv1.VersionResponse, error)
}

// Refusal is a request refused for what it asks, not for what the
// workspace says.
type Refusal struct {
	// Unknown says it names an entity the workspace does not hold;
	// Unserved that it asks for what is not served yet. Otherwise the
	// request itself is mistaken.
	Unknown, Unserved bool
	Message           string
}

func (r *Refusal) Error() string { return r.Message }

// WrongAnswer is a strategy's answer that the plugin contract does not
// let it give: its plugin's fault, not the request's and not the
// workspace's.
type WrongAnswer struct{ Message string }

func (w *WrongAnswer) Error() string { return w.Message }

func mistaken(format string, a ...any) error {
	return &model.Mistake{Message: fmt.Sprintf(format, a...)}
}

// Planned is a plan, with what carrying it out needs of it: the
// repository each of its units is in.
type Planned struct {
	Plan         *buildv1.ReleasePlan
	Repositories map[entity.Ref]string
}

// Plan says what releasing the entities a request names would do now. It
// changes nothing.
func Plan(ctx context.Context, w Workspace, strategies Strategies, history History, request *buildv1.PlanReleaseRequest) (*buildv1.ReleasePlan, error) {
	planned, err := Prepare(ctx, w, strategies, history, request)
	if err != nil {
		return nil, err
	}
	return planned.Plan, nil
}

// Prepare plans, for whoever carries the plan out.
func Prepare(ctx context.Context, w Workspace, strategies Strategies, history History, request *buildv1.PlanReleaseRequest) (*Planned, error) {
	if len(request.GetEntities()) == 0 {
		return nil, &Refusal{Message: "name what to release"}
	}
	resolved, err := Resolve(w)
	if err != nil {
		return nil, err
	}
	// The units asked for: each named entity's unit, once.
	named := map[entity.Ref]bool{}
	asked := map[entity.Ref]bool{}
	held := make([]entity.Ref, 0, len(resolved.entities))
	for ref := range resolved.entities {
		held = append(held, ref)
	}
	for _, each := range request.GetEntities() {
		// What a person typed finds its entity: a kind left out is a
		// component, and a namespace left out is any (entity.Pattern).
		typed := entity.PatternFromProto(each)
		if err := typed.Validate(); err != nil {
			return nil, &Refusal{Message: fmt.Sprintf("%q names no entity: %v", typed, err)}
		}
		ref, found, err := typed.Find(held)
		if err != nil {
			return nil, &Refusal{Message: err.Error()}
		}
		if !found {
			return nil, &Refusal{Unknown: true, Message: fmt.Sprintf("the workspace holds no entity %s", typed)}
		}
		if err := ref.Validate(); err != nil {
			return nil, &Refusal{Message: fmt.Sprintf("%q names no entity: %v", ref, err)}
		}
		if named[ref] {
			return nil, &Refusal{Message: fmt.Sprintf("%s is named twice", ref)}
		}
		named[ref] = true
		settings, released := resolved.Of(ref)
		if err := resolved.refused(ref); err != nil {
			return nil, err
		}
		switch {
		case ref.Kind == kindDomain && !settings.Synced:
			// A domain that is no synced group is released as each of its
			// systems would be named, a system with no members aside.
			var units []entity.Ref
			for _, system := range resolved.systems[ref] {
				if s := resolved.settings[system]; !s.Synced {
					units = append(units, independents(resolved, system)...)
				} else if len(resolved.Members(s.Unit)) > 0 {
					units = append(units, s.Unit)
				}
			}
			if len(units) == 0 {
				return nil, mistaken("%s has no members to release", ref)
			}
			for _, unit := range units {
				asked[unit] = true
			}
		case !released:
			return nil, mistaken("%s has no source to release", ref)
		case ref.Kind == kindSystem && !settings.Synced:
			// An independent system is released as its members are, each
			// on its own.
			members := independents(resolved, ref)
			if len(members) == 0 {
				return nil, mistaken("%s has no members to release", ref)
			}
			for _, member := range members {
				asked[member] = true
			}
		default:
			asked[settings.Unit] = true
		}
	}
	units := make([]entity.Ref, 0, len(asked))
	for unit := range asked {
		units = append(units, unit)
	}
	sort.Slice(units, func(i, j int) bool { return units[i].String() < units[j].String() })
	dependsOn := unitDependencies(w, resolved, units)
	units, err = ordered(units, dependsOn)
	if err != nil {
		return nil, err
	}
	plan := &buildv1.ReleasePlan{}
	// meant is the plan as it was before any of it was carried out: a
	// unit the commit carries the tag of already is in it as it was
	// planned to move. The etag names that, so that it is the same before
	// a release and after, and releasing again completes it.
	meant := &buildv1.ReleasePlan{}
	in := map[entity.Ref]string{}
	commits := map[string]string{}
	// A repository is asked where it stands once for a plan, so that
	// every unit of it is planned from one commit.
	asked1 := &once{history: history, stood: map[string]vcs.Standing{}, tagged: map[string][]vcs.Tag{}}
	for _, unit := range units {
		for _, each := range append([]entity.Ref{unit}, resolved.Members(unit)...) {
			if err := resolved.refused(each); err != nil {
				return nil, err
			}
		}
		planned, repository, standing, err := planUnit(ctx, w, resolved, strategies, asked1, unit, false)
		if err != nil {
			return nil, err
		}
		planned.DependsOn = entity.Protos(dependsOn[unit])
		plan.Units = append(plan.Units, planned)
		commits[repository], in[unit] = standing.Revision, repository
		before := planned
		if reasons := planned.GetReasons(); len(reasons) > 0 && reasons[0].GetKind() == buildv1.ReasonKind_REASON_KIND_RELEASED {
			// As it would be planned were its tag not there yet, but only
			// where the one tag of its own the commit carries is the tag
			// that plan would write: then the commit is that plan, carried
			// out. Any other tag there, one a person wrote or a
			// prerelease, is another state of the repository, and the plan
			// is named as it is. So is one whose earlier plan cannot be
			// said: a strategy that fails then fails the etag's sameness,
			// never the plan.
			own := ownTagsAt(resolved.settings[unit], unit, asked1.tagged[repository], standing.Revision)
			without, _, _, err := planUnit(ctx, w, resolved, strategies, asked1, unit, true)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == nil && without.GetNextVersion() != "" && len(own) == 1 && own[0] == without.GetTag() {
				without.DependsOn = planned.DependsOn
				before = without
			}
		}
		meant.Units = append(meant.Units, before)
	}
	// A plan is of every repository of the workspace, not only those its
	// units are in: what a unit is built from is in the others too, and a
	// plan of other commits there is another plan.
	for repository := range w.Listed {
		if _, planned := commits[repository]; planned {
			continue
		}
		standing, _, err := asked1.of(ctx, repository)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, mistaken("the history of %s could not be read: %v", repository, err)
		}
		commits[repository] = standing.Revision
	}
	repositories := make([]string, 0, len(commits))
	for repository := range commits {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	for _, repository := range repositories {
		plan.Commits = append(plan.Commits, &buildv1.RepositoryCommit{Repository: repository, Commit: commits[repository]})
	}
	meant.Commits = plan.Commits
	if plan.Etag, err = etag(request, meant); err != nil {
		return nil, err
	}
	return &Planned{Plan: plan, Repositories: in}, nil
}

// independents are the entities of an independent system, each a unit
// of its own.
func independents(resolved *Resolved, system entity.Ref) []entity.Ref {
	var out []entity.Ref
	for _, ref := range resolved.order {
		if s := resolved.settings[ref]; s.Unit == ref && s.Group == system && ref != system {
			out = append(out, ref)
		}
	}
	return out
}

// unitDependencies folds what entities depend on onto the plan's units:
// a unit depends on another where a member of the one depends on a
// member of the other.
func unitDependencies(w Workspace, resolved *Resolved, units []entity.Ref) map[entity.Ref][]entity.Ref {
	in := map[entity.Ref]bool{}
	for _, unit := range units {
		in[unit] = true
	}
	out := map[entity.Ref][]entity.Ref{}
	for _, unit := range units {
		seen := map[entity.Ref]bool{}
		for _, member := range resolved.Members(unit) {
			for _, other := range w.DependsOn[member] {
				on := resolved.settings[other].Unit
				if on != unit && in[on] && !seen[on] {
					seen[on] = true
					out[unit] = append(out[unit], on)
				}
			}
		}
		sort.Slice(out[unit], func(i, j int) bool { return out[unit][i].String() < out[unit][j].String() })
	}
	return out
}

// ordered puts each unit after every unit it depends on, and among
// those free to come next, in the order of their names.
func ordered(units []entity.Ref, dependsOn map[entity.Ref][]entity.Ref) ([]entity.Ref, error) {
	done := map[entity.Ref]bool{}
	var out []entity.Ref
	for len(out) < len(units) {
		moved := false
		for _, unit := range units {
			if done[unit] {
				continue
			}
			ready := true
			for _, on := range dependsOn[unit] {
				ready = ready && done[on]
			}
			if ready {
				done[unit], moved = true, true
				out = append(out, unit)
				break
			}
		}
		if !moved {
			var cycle []string
			for _, unit := range units {
				if !done[unit] {
					cycle = append(cycle, unit.String())
				}
			}
			return nil, mistaken("%s depend on each other in a cycle; no order releases them", strings.Join(cycle, ", "))
		}
	}
	return out, nil
}

// once asks a repository where it stands and for its tags one time.
type once struct {
	history History
	stood   map[string]vcs.Standing
	tagged  map[string][]vcs.Tag
}

func (o *once) of(ctx context.Context, repository string) (vcs.Standing, []vcs.Tag, error) {
	if standing, asked := o.stood[repository]; asked {
		return standing, o.tagged[repository], nil
	}
	standing, err := o.history.Standing(ctx, repository)
	if err != nil {
		return vcs.Standing{}, nil, err
	}
	tags, err := o.history.Tags(ctx, repository)
	if err != nil {
		return vcs.Standing{}, nil, err
	}
	o.stood[repository], o.tagged[repository] = standing, tags
	return standing, tags, nil
}

// spelling is what a unit's tags are spelled with, before the version
// and after it.
func spelling(settings Settings, unit entity.Ref) (before, after string) {
	before, after, _ = strings.Cut(strings.ReplaceAll(settings.Tag, "{unit}", unit.Name), "{version}")
	return before, after
}

// versionOf is the version a tag names, where it is a tag spelled as a
// unit's are.
func versionOf(tag, before, after string) (*semver.Version, bool) {
	if !strings.HasPrefix(tag, before) || !strings.HasSuffix(tag, after) || len(tag) < len(before)+len(after) {
		return nil, false
	}
	version, err := semver.StrictNewVersion(tag[len(before) : len(tag)-len(after)])
	return version, err == nil
}

// ownTagsAt are the tags of a unit's own that name a revision, of any
// version, a prerelease among them.
func ownTagsAt(settings Settings, unit entity.Ref, tags []vcs.Tag, revision string) []string {
	before, after := spelling(settings, unit)
	var out []string
	for _, tag := range tags {
		if _, own := versionOf(tag.Name, before, after); own && tag.Revision == revision {
			out = append(out, tag.Name)
		}
	}
	return out
}

// released is one release of a unit: a tag of its own, and the version
// it names.
type released struct {
	version   *semver.Version
	revision  string
	reachable bool
	// former is a release of what a member was released as before the
	// unit: its own, or its system's or its domain's.
	former bool
}

// formerSpellings are how what a unit's members were released as before
// it spelled their tags, before the version and after it: each member,
// and the system and the domain it is part of, other than the unit, by
// the unit's tag and by the default.
func formerSpellings(resolved *Resolved, settings Settings, unit entity.Ref) [][2]string {
	names := map[string]bool{}
	for _, member := range resolved.Members(unit) {
		seen := map[entity.Ref]bool{member: true}
		for queue := []entity.Ref{member}; len(queue) > 0; queue = queue[1:] {
			names[queue[0].Name] = true
			for _, parent := range resolved.entities[queue[0]].PartOf {
				if _, held := resolved.entities[parent]; held && !seen[parent] && (parent.Kind == kindSystem || parent.Kind == kindDomain) {
					seen[parent] = true
					queue = append(queue, parent)
				}
			}
		}
	}
	delete(names, unit.Name)
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var out [][2]string
	for _, name := range sorted {
		for _, pattern := range []string{settings.Tag, DefaultTag} {
			before, after, _ := strings.Cut(strings.ReplaceAll(pattern, "{unit}", name), "{version}")
			if spelled := [2]string{before, after}; !containsSpelling(out, spelled) {
				out = append(out, spelled)
			}
		}
	}
	return out
}

func containsSpelling(list [][2]string, s [2]string) bool {
	for _, each := range list {
		if each == s {
			return true
		}
	}
	return false
}

// core is a version without its build metadata, which names no other
// version.
func core(v *semver.Version) string {
	if v.Prerelease() != "" {
		return fmt.Sprintf("%d.%d.%d-%s", v.Major(), v.Minor(), v.Patch(), v.Prerelease())
	}
	return fmt.Sprintf("%d.%d.%d", v.Major(), v.Minor(), v.Patch())
}

// highest is the highest of the releases that a rule keeps.
func highest(releases []released, keep func(released) bool) *released {
	var out *released
	for i := range releases {
		if keep(releases[i]) && (out == nil || releases[i].version.GreaterThan(out.version)) {
			out = &releases[i]
		}
	}
	return out
}

// planUnit says what releasing one unit would do.
//
// With untagged, a tag of its own on the commit it is planned from is
// taken not to be there: it is planned as it was before that tag.
func planUnit(ctx context.Context, w Workspace, resolved *Resolved, strategies Strategies, asked *once, unit entity.Ref, untagged bool) (*buildv1.ReleaseUnit, string, vcs.Standing, error) {
	none := vcs.Standing{}
	history := asked.history
	settings := resolved.settings[unit]
	members := resolved.Members(unit)
	if len(members) == 0 {
		if systems := resolved.systems[unit]; unit.Kind == kindDomain && len(systems) > 0 {
			left := true
			for _, system := range systems {
				left = left && resolved.settings[system].Group == system
			}
			if left {
				return nil, "", none, mistaken("%s says its systems version together, and each of them says it versions on its own: name them", unit)
			}
		}
		return nil, "", none, mistaken("%s has no members to release", unit)
	}
	// A unit is from one repository: where a group is described comes
	// first where its members are named.
	var repositories []string
	if home := resolved.entities[unit].Repository; home != "" && (unit.Kind == kindSystem || unit.Kind == kindDomain) {
		for _, member := range members {
			if resolved.entities[member].Repository == home {
				repositories = append(repositories, home)
				break
			}
		}
	}
	directoriesOf := func(member entity.Ref) []string {
		var out []string
		for _, root := range resolved.entities[member].Roots {
			if root == "" {
				root = "."
			}
			out = append(out, root)
		}
		return out
	}
	var directories []string
	for _, member := range members {
		if repository := resolved.entities[member].Repository; !contains(repositories, repository) {
			repositories = append(repositories, repository)
		}
		directories = append(directories, directoriesOf(member)...)
	}
	if len(repositories) > 1 {
		return nil, "", none, mistaken("%s's members are in %s; a unit is released from one repository", unit.Name, strings.Join(repositories[:len(repositories)-1], ", ")+" and "+repositories[len(repositories)-1])
	}
	repository := repositories[0]
	sort.Strings(directories)
	// Its strategies are those of plugins its repository lists.
	branching, versioning := strategyOf(settings.Branching), strategyOf(settings.Versioning)
	for _, each := range []struct {
		strategy  [2]string
		written   string
		branching bool
	}{{branching, settings.Branching, true}, {versioning, settings.Versioning, false}} {
		kind := "versioning"
		if each.branching {
			kind = "branching"
		}
		if !contains(w.Listed[repository], each.strategy[0]) {
			return nil, "", none, mistaken("%s lists no %s plugin; list it to release %s", repository, each.strategy[0], unit.Name)
		}
		if !strategies.Serves(each.strategy[0], each.branching, each.strategy[1]) {
			return nil, "", none, mistaken("no plugin the repository lists serves the %s strategy %s", kind, each.written)
		}
	}
	unread := func(err error) (*buildv1.ReleaseUnit, string, vcs.Standing, error) {
		if ctx.Err() != nil {
			return nil, "", none, ctx.Err()
		}
		return nil, "", none, mistaken("the history of %s could not be read: %v", repository, err)
	}
	standing, tags, err := asked.of(ctx, repository)
	if err != nil {
		return unread(err)
	}
	if standing.Branch == "" {
		return nil, "", none, mistaken("%s stands on no branch; a release is planned from a branch", repository)
	}
	before, after := spelling(settings, unit)
	// Its releases are the versions tags of its own name, wherever in the
	// repository they are: under Release Flow a line's tags are on its
	// release branch, and no other branch has them in its history. A
	// prerelease is no release to come after.
	var releases []released
	named := map[string]string{}
	for _, tag := range tags {
		version, own := versionOf(tag.Name, before, after)
		if !own {
			continue
		}
		if untagged && tag.Revision == standing.Revision {
			continue
		}
		named[core(version)] = tag.Revision
		if version.Prerelease() == "" {
			releases = append(releases, released{version: version, revision: tag.Revision, reachable: tag.Reachable})
		}
	}
	// A unit never released as itself continues above what its members
	// were released as before it: versions never go backwards. A
	// prerelease of its own is no release, and leaves it never released.
	if len(releases) == 0 {
		for _, spelled := range formerSpellings(resolved, settings, unit) {
			for _, tag := range tags {
				if version, theirs := versionOf(tag.Name, spelled[0], spelled[1]); theirs && version.Prerelease() == "" {
					releases = append(releases, released{version: version, revision: tag.Revision, reachable: tag.Reachable, former: true})
				}
			}
		}
	}
	planned := &buildv1.ReleaseUnit{
		Entity: entity.Proto(unit), Members: entity.Protos(members), Group: entity.Proto(settings.Group),
		Grouping:           buildv1.Grouping_GROUPING_INDEPENDENT,
		VersioningStrategy: settings.Versioning, BranchingStrategy: settings.Branching, Branch: standing.Branch, Bump: typev1.Bump_BUMP_NONE,
	}
	if settings.Synced {
		planned.Grouping = buildv1.Grouping_GROUPING_SYNCED
	}
	holds := func(kind buildv1.ReasonKind, summary string) (*buildv1.ReleaseUnit, string, vcs.Standing, error) {
		planned.Reasons = []*buildv1.ReleaseReason{{Kind: kind, Bump: typev1.Bump_BUMP_NONE, Summary: summary}}
		return planned, repository, standing, nil
	}
	// The commit it is planned from carries its tag: it is released, as
	// the highest version a tag there names.
	if here := highest(releases, func(r released) bool { return !r.former && r.revision == standing.Revision }); here != nil {
		planned.LastVersion = core(here.version)
		planned.Tag = before + core(here.version) + after
		return holds(buildv1.ReasonKind_REASON_KIND_RELEASED, "")
	}
	// changes are the commits since a release that changed a member's
	// files, each with the members whose files it changed.
	changes := func(since string) ([]*pluginv1.ReleaseCommit, error) {
		all, err := history.Changes(ctx, repository, since, directories)
		if err != nil {
			return nil, err
		}
		changed := map[string][]entity.Ref{}
		if len(members) == 1 {
			for _, change := range all {
				changed[change.Revision] = members
			}
		} else {
			for _, member := range members {
				of, err := history.Changes(ctx, repository, since, directoriesOf(member))
				if err != nil {
					return nil, err
				}
				for _, change := range of {
					changed[change.Revision] = append(changed[change.Revision], member)
				}
			}
		}
		var out []*pluginv1.ReleaseCommit
		for _, change := range all {
			out = append(out, &pluginv1.ReleaseCommit{Id: change.Revision, Repository: repository, Message: change.Message, Time: change.Time, Members: entity.Protos(changed[change.Revision])})
		}
		return out, nil
	}
	// A branch is asked with the unit's last release in its own history.
	facts := &pluginv1.ReleaseUnitFacts{Unit: entity.Proto(unit), Members: entity.Protos(members)}
	last := highest(releases, func(r released) bool { return r.reachable })
	since, lastVersion := "", ""
	if last != nil {
		since, lastVersion = last.revision, core(last.version)
	}
	commits, err := changes(since)
	if err != nil {
		return unread(err)
	}
	branch, err := strategies.Branch(ctx, branching[0], &pluginv1.BranchRequest{
		Strategy: branching[1], Unit: facts, Branch: standing.Branch, LastVersion: lastVersion, Commits: commits, DefaultBranch: standing.DefaultBranch,
	})
	if err != nil {
		return nil, "", none, err
	}
	line, err := checkBranch(settings.Branching, standing.Branch, unit, branch)
	if err != nil {
		return nil, "", none, err
	}
	if !branch.GetReleases() {
		planned.LastVersion = lastVersion
		return holds(buildv1.ReasonKind_REASON_KIND_BRANCH, branch.GetReason())
	}
	planned.Channel = branch.GetChannel()
	// plannedAfter makes a release the one the unit is planned after: its
	// commits are then those since that one, read again where it is
	// another than they were read since.
	plannedAfter := func(release *released) error {
		was := since
		since, lastVersion = "", ""
		if release != nil {
			since, lastVersion = release.revision, core(release.version)
		}
		if since == was {
			return nil
		}
		var err error
		commits, err = changes(since)
		return err
	}
	// On a line, its last release is the line's own, in the branch's
	// history. A line with none comes after the highest release below it,
	// wherever that one's tag is: under Release Flow it is on another
	// release branch, in no history but its own. A line that has only
	// later releases is not released after them.
	newLine := false
	if line != nil {
		on := highest(releases, func(r released) bool { return r.reachable && line.holds(r.version) })
		below := highest(releases, func(r released) bool { return line.above(r.version) })
		later := highest(releases, func(r released) bool { return !line.holds(r.version) && !line.above(r.version) })
		switch {
		case on != nil:
			last = on
		case below != nil:
			last, newLine = below, true
		case later != nil:
			return nil, "", none, mistaken("%s has no release on the line %s of %s, and is released as %s already: a line is released before a later one is", unit.Name, branch.GetLine(), standing.Branch, core(later.version))
		default:
			last = nil
		}
		if err := plannedAfter(last); err != nil {
			return unread(err)
		}
	}
	// A prerelease comes after the unit's highest release, wherever its
	// tag is: under Release Flow that is on a release branch, in no
	// history but its own, and a branch that does not reach it would
	// offer a prerelease of a version already released, which a semantic
	// version puts before it. Its commits are those of the branch that
	// release does not have.
	if branch.GetPrerelease() {
		last = highest(releases, func(released) bool { return true })
		if err := plannedAfter(last); err != nil {
			return unread(err)
		}
	}
	planned.LastVersion = lastVersion
	if last != nil && len(commits) == 0 {
		return holds(buildv1.ReasonKind_REASON_KIND_UNCHANGED, "")
	}
	initial, err := semver.StrictNewVersion(settings.Initial)
	if err != nil || initial.Prerelease() != "" || initial.Metadata() != "" {
		return nil, "", none, mistaken("%s: spec.frontseat.release.initial: %s is no version to release first: a major, a minor and a patch", unit.Name, settings.Initial)
	}
	// A first release on a line is at the line's first version, or at the
	// initial version a catalog file says, which is then on the line.
	if _, said := settings.SetBy["initial"]; last == nil && line != nil {
		if !said {
			initial = line.first()
		} else if !line.holds(initial) {
			return nil, "", none, mistaken("%s: spec.frontseat.release.initial: %s is not on the line %s of %s", unit.Name, initial, branch.GetLine(), standing.Branch)
		}
	}
	answered, err := strategies.Version(ctx, versioning[0], &pluginv1.VersionRequest{
		Strategy: versioning[1], Unit: facts, LastVersion: lastVersion, Initial: initial.String(), Commits: commits, Branch: branch,
	})
	if err != nil {
		return nil, "", none, err
	}
	if err := checkVersion(settings.Versioning, unit, answered, commits); err != nil {
		return nil, "", none, err
	}
	for _, reason := range answered.GetReasons() {
		planned.Reasons = append(planned.Reasons, &buildv1.ReleaseReason{
			Kind: buildv1.ReasonKind_REASON_KIND_OWN_CHANGE, Bump: reason.GetBump(), Summary: reason.GetSummary(),
			Commit: &buildv1.RepositoryCommit{Repository: repository, Commit: reason.GetCommit()},
		})
	}
	var next *semver.Version
	switch {
	case last == nil:
		// Whatever the strategy makes of a first release, it is the
		// initial version.
		next = initial
		planned.Reasons = append([]*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_FIRST_RELEASE, Bump: typev1.Bump_BUMP_NONE}}, planned.Reasons...)
	case newLine:
		// A branch cut for a line not yet released releases the line's
		// first version, whatever its commits call for.
		next = line.first()
		planned.Bump = bumpBetween(last.version, next)
	case answered.GetVersion() == "":
		return holds(buildv1.ReasonKind_REASON_KIND_UNCHANGED, "")
	default:
		if next, err = semver.StrictNewVersion(answered.GetVersion()); err != nil {
			return nil, "", none, &WrongAnswer{fmt.Sprintf("the versioning strategy %s answered %q for %s, which is no semantic version", settings.Versioning, answered.GetVersion(), unit.Name)}
		}
		if !next.GreaterThan(last.version) {
			return nil, "", none, &WrongAnswer{fmt.Sprintf("the versioning strategy %s answered %s for %s, which is not above its last release %s", settings.Versioning, next, unit.Name, core(last.version))}
		}
		if next.Prerelease() != "" || next.Metadata() != "" {
			return nil, "", none, &WrongAnswer{fmt.Sprintf("the versioning strategy %s answered %s for %s, which is more than three dotted numbers", settings.Versioning, next, unit.Name)}
		}
		planned.Bump = answered.GetBump()
	}
	if line != nil && !line.holds(next) {
		return nil, "", none, mistaken("%s %s leaves the line %s of %s", unit.Name, next, branch.GetLine(), standing.Branch)
	}
	// A prerelease is of the version its commits give, or of the one its
	// branch is heading for where that is further: a branch heading for a
	// line not cut yet says by how much at least. It is spelled as the
	// unit's settings say, from the branch and the commit planned at.
	tagged := true
	if branch.GetPrerelease() {
		if last != nil && branch.GetAtLeast() != typev1.Bump_BUMP_UNSPECIFIED {
			if least := moved(last.version, branch.GetAtLeast()); next.LessThan(least) {
				next = least
				planned.Bump = bumpBetween(last.version, next)
			}
		}
		spelled, err := config.Prerelease(settings.Prerelease, standing.Branch, standing.Revision, standing.Time)
		if err != nil {
			// The branch is said: it is what the person stands on, and
			// as often what makes the form spell no prerelease.
			return nil, "", none, mistaken("%s: spec.frontseat.release.prerelease, for the branch %q: %v", unit.Name, standing.Branch, err)
		}
		if next, err = semver.StrictNewVersion(next.String() + "-" + spelled); err != nil {
			return nil, "", none, mistaken("%s: spec.frontseat.release.prerelease, for the branch %q, spells what is no prerelease of a semantic version", unit.Name, standing.Branch)
		}
		planned.Prerelease, tagged = true, settings.PrereleaseTag
	}
	// A version is new: its tag names no other commit. And its tag is a
	// name the version control system takes, by its own rule.
	tag := before + next.String() + after
	if at, taken := named[core(next)]; taken && at != standing.Revision {
		return nil, "", none, mistaken("%s %s is not new: %s names another commit", unit.Name, next, tag)
	}
	if !tagged {
		planned.NextVersion, planned.Notes = next.String(), answered.GetNotes()
		return planned, repository, standing, nil
	}
	if err := history.TagName(ctx, tag); err != nil {
		if ctx.Err() != nil {
			return nil, "", none, ctx.Err()
		}
		return nil, "", none, mistaken("%s would be tagged %q, which %v; say another spec.frontseat.release.tag", unit.Name, tag, err)
	}
	planned.NextVersion, planned.Tag, planned.Notes = next.String(), tag, answered.GetNotes()
	return planned, repository, standing, nil
}

// What a strategy may answer, beyond which it is not believed.
const (
	notesAtMost   = 64 << 10
	summaryAtMost = 512
)

var channelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// checkBranch holds a branching strategy's answer to the contract, and
// returns the line it holds the branch to. An answer that breaks it is
// the plugin's fault, not the workspace's.
func checkBranch(strategy, branch string, unit entity.Ref, answered *pluginv1.BranchResponse) (*line, error) {
	wrong := func(format string, a ...any) (*line, error) {
		return nil, &WrongAnswer{fmt.Sprintf("the branching strategy %s answered wrongly of %s on %s: %s", strategy, unit.Name, branch, fmt.Sprintf(format, a...))}
	}
	if !answered.GetReleases() {
		switch {
		case strings.TrimSpace(answered.GetReason()) == "":
			return wrong("the branch releases nothing, and it does not say why")
		case len(answered.GetReason()) > summaryAtMost || strings.ContainsAny(answered.GetReason(), "\r\n\x00"):
			return wrong("its reason is not one line of at most %d bytes", summaryAtMost)
		case answered.GetLine() != "" || answered.GetChannel() != "" || answered.GetPrerelease() || answered.GetAtLeast() != typev1.Bump_BUMP_UNSPECIFIED:
			return wrong("the branch releases nothing, and it says what it releases")
		}
		return nil, nil
	}
	// A prerelease is of the version its commits give, held to no line;
	// and only a prerelease says how far above the unit's releases it is.
	switch at := answered.GetAtLeast(); {
	case answered.GetPrerelease() && answered.GetLine() != "":
		return wrong("the branch releases a prerelease, and holds it to the line %q", answered.GetLine())
	case !answered.GetPrerelease() && at != typev1.Bump_BUMP_UNSPECIFIED:
		return wrong("the branch releases no prerelease, and says how far above the unit's releases one is")
	case at != typev1.Bump_BUMP_UNSPECIFIED && at != typev1.Bump_BUMP_PATCH && at != typev1.Bump_BUMP_MINOR && at != typev1.Bump_BUMP_MAJOR:
		return wrong("a prerelease is above the unit's releases by %v at least, which is no bump of a version", at)
	}
	if answered.GetReason() != "" {
		return wrong("the branch releases, and it gives a reason it does not")
	}
	if answered.GetChannel() != "" && !channelName.MatchString(answered.GetChannel()) {
		return wrong("the channel %q is no name", answered.GetChannel())
	}
	held, err := lineOf(answered.GetLine())
	if err != nil {
		return wrong("the line %q: %v", answered.GetLine(), err)
	}
	return held, nil
}

// checkVersion holds a versioning strategy's answer to the contract: a
// bump the contract names, reasons that are commits it was given, each
// said in a line, and notes of a size.
func checkVersion(strategy string, unit entity.Ref, answered *pluginv1.VersionResponse, commits []*pluginv1.ReleaseCommit) error {
	wrong := func(format string, a ...any) error {
		return &WrongAnswer{fmt.Sprintf("the versioning strategy %s answered wrongly of %s: %s", strategy, unit.Name, fmt.Sprintf(format, a...))}
	}
	bump := func(b typev1.Bump) bool { return b >= typev1.Bump_BUMP_NONE && b <= typev1.Bump_BUMP_MAJOR }
	given := map[string]bool{}
	for _, commit := range commits {
		given[commit.GetId()] = true
	}
	switch {
	case !bump(answered.GetBump()):
		return wrong("%d is no bump", answered.GetBump())
	case len(answered.GetNotes()) > notesAtMost || strings.ContainsRune(answered.GetNotes(), 0):
		return wrong("its notes are more than %d bytes, or are no text", notesAtMost)
	}
	seen := map[string]bool{}
	for _, reason := range answered.GetReasons() {
		switch {
		case !given[reason.GetCommit()]:
			return wrong("a reason names the commit %q, which is none of the unit's", reason.GetCommit())
		case seen[reason.GetCommit()]:
			return wrong("the commit %s is a reason twice", reason.GetCommit())
		case !bump(reason.GetBump()) || reason.GetBump() == typev1.Bump_BUMP_NONE:
			return wrong("the reason %s moves it by %d, which is no bump", reason.GetCommit(), reason.GetBump())
		case reason.GetBump() > answered.GetBump() && answered.GetVersion() != "" && answered.GetBump() != typev1.Bump_BUMP_NONE:
			return wrong("the reason %s is heavier than the bump it answered", reason.GetCommit())
		case reason.GetSummary() == "" || len(reason.GetSummary()) > summaryAtMost || strings.ContainsAny(reason.GetSummary(), "\r\n\x00"):
			return wrong("the summary of %s is not one line of at most %d bytes", reason.GetCommit(), summaryAtMost)
		}
		seen[reason.GetCommit()] = true
	}
	return nil
}

// strategyOf is a strategy as a setting writes it, "<plugin>:<name>".
func strategyOf(written string) [2]string {
	plugin, name, _ := strings.Cut(written, ":")
	return [2]string{plugin, name}
}

// line is what every version a branch releases begins with.
type line struct{ major, minor uint64 }

// lineOf reads a line as a branching strategy answers one: "1.26", or
// empty for none.
func lineOf(written string) (*line, error) {
	if written == "" {
		return nil, nil
	}
	version, err := semver.StrictNewVersion(written + ".0")
	if err != nil || version.Prerelease() != "" || version.Metadata() != "" || fmt.Sprintf("%d.%d", version.Major(), version.Minor()) != written {
		return nil, fmt.Errorf("a line is a major and a minor version, as 1.26")
	}
	return &line{version.Major(), version.Minor()}, nil
}

func (l *line) first() *semver.Version { return semver.New(l.major, l.minor, 0, "", "") }

func (l *line) holds(v *semver.Version) bool {
	return v.Major() == l.major && v.Minor() == l.minor && v.Prerelease() == ""
}

// above says the line is above a version: the version is of an earlier
// line.
func (l *line) above(v *semver.Version) bool { return l.first().GreaterThan(v) && !l.holds(v) }

// moved is a version moved by a bump: the least version a prerelease
// that says it is that far above a release may be of. By a patch, where
// none is said.
func moved(from *semver.Version, by typev1.Bump) *semver.Version {
	var to semver.Version
	switch by {
	case typev1.Bump_BUMP_MAJOR:
		to = from.IncMajor()
	case typev1.Bump_BUMP_MINOR:
		to = from.IncMinor()
	default:
		to = from.IncPatch()
	}
	return &to
}

// bumpBetween is how far one version is from an earlier one.
func bumpBetween(from, to *semver.Version) typev1.Bump {
	switch {
	case to.Major() != from.Major():
		return typev1.Bump_BUMP_MAJOR
	case to.Minor() != from.Minor():
		return typev1.Bump_BUMP_MINOR
	}
	return typev1.Bump_BUMP_PATCH
}

// etag names a plan: a digest of what was asked, what the plan says and
// the commits it is planned from, so that the same request at the same
// commits comes to the same etag wherever it is planned.
func etag(request *buildv1.PlanReleaseRequest, plan *buildv1.ReleasePlan) (string, error) {
	sum := sha256.New()
	for _, message := range []proto.Message{request, plan} {
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(sum, "%d\n", len(raw))
		sum.Write(raw)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func contains(list []string, s string) bool {
	for _, each := range list {
		if each == s {
			return true
		}
	}
	return false
}
