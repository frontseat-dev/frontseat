package plain

import (
	"fmt"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"strings"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// Plan writes what releasing would do: each unit, what it moves from and
// to, the tag it would be given and why, or why it holds; and the plan's
// etag, which names it to whoever carries it out.
func (p Printer) Plan(plan *buildv1.ReleasePlan) {
	for _, unit := range plan.GetUnits() {
		name := named(unit.GetEntity())
		if members := unit.GetMembers(); len(members) > 1 {
			var names []string
			for _, member := range members {
				names = append(names, named(member))
			}
			name += "  (" + strings.Join(names, ", ") + ")"
		}
		reasons := unit.GetReasons()
		switch {
		case unit.GetNextVersion() != "":
			from := unit.GetLastVersion()
			if from == "" {
				from = "unreleased"
			}
			fmt.Fprintf(p.Out, "%s  %s -> %s  tag %s  on %s\n", name, shown(from), shown(unit.GetNextVersion()), shown(unit.GetTag()), shown(unit.GetBranch()))
			for _, reason := range reasons {
				p.reason(reason)
			}
		case len(reasons) > 0 && reasons[0].GetKind() == buildv1.ReasonKind_REASON_KIND_RELEASED:
			fmt.Fprintf(p.Out, "%s  released as %s  tag %s\n", name, shown(unit.GetLastVersion()), shown(unit.GetTag()))
		default:
			fmt.Fprintf(p.Out, "%s  holds%s: %s\n", name, shown(at(unit.GetLastVersion())), shown(held(reasons)))
		}
	}
	fmt.Fprintf(p.Out, "plan %s\n", shown(plan.GetEtag()))
}

func at(version string) string {
	if version == "" {
		return ""
	}
	return " at " + version
}

// reason writes one reason a unit moves, in a line of its own.
func (p Printer) reason(reason *buildv1.ReleaseReason) {
	switch reason.GetKind() {
	case buildv1.ReasonKind_REASON_KIND_FIRST_RELEASE:
		fmt.Fprintln(p.Out, "  its first release")
	case buildv1.ReasonKind_REASON_KIND_OWN_CHANGE:
		fmt.Fprintf(p.Out, "  %s  (%.12s)\n", shown(reason.GetSummary()), shown(reason.GetCommit().GetCommit()))
	default:
		fmt.Fprintf(p.Out, "  %s\n", strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(reason.GetKind().String(), "REASON_KIND_"), "_", " ")))
	}
}

// held says why a unit holds.
func held(reasons []*buildv1.ReleaseReason) string {
	if len(reasons) == 0 {
		return "no reason was given"
	}
	switch reason := reasons[0]; reason.GetKind() {
	case buildv1.ReasonKind_REASON_KIND_UNCHANGED:
		return "nothing of it changed since its last release"
	case buildv1.ReasonKind_REASON_KIND_BRANCH:
		return reason.GetSummary()
	default:
		return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(reason.GetKind().String(), "REASON_KIND_"), "_", " "))
	}
}

// Release writes what a release did: each unit and the tag it was given,
// and how the release ended. A release that tagged and did not publish
// says that its tags are still to be pushed.
func (p Printer) Release(release *buildv1.Release) {
	for _, unit := range release.GetUnits() {
		for _, tag := range unit.GetTags() {
			did := "tagged"
			if unit.GetAlready() {
				did = "was tagged already"
			}
			fmt.Fprintf(p.Out, "%s  %s  %s %s in %s  (%.12s)\n", named(unit.GetEntity()), shown(unit.GetVersion()), did, shown(tag.GetName()), shown(tag.GetRepository()), shown(tag.GetCommit()))
		}
	}
	state := strings.ToLower(strings.TrimPrefix(release.GetState().String(), "STATE_"))
	switch release.GetState() {
	case buildv1.Release_STATE_UNPUBLISHED:
		fmt.Fprintf(p.Out, "%s  unpublished: its tags are in the workspace and are not pushed\n", shown(release.GetId()))
	case buildv1.Release_STATE_FAILED:
		fmt.Fprintf(p.Out, "%s  FAILED: %s\n", shown(release.GetId()), shown(release.GetFailure()))
	default:
		fmt.Fprintf(p.Out, "%s  %s\n", shown(release.GetId()), state)
	}
}

// named is an entity as a line names it: a component of the default
// namespace by its name alone, and any other with what tells it apart,
// so that a system and a component of one name are two lines.
func named(ref *typev1.EntityRef) string {
	name := ref.GetName()
	if ns := ref.GetNamespace(); ns != "" && ns != "default" {
		name = ns + "/" + name
	}
	if kind := ref.GetKind(); kind != "" && kind != "component" {
		name = kind + ":" + name
	}
	return shown(name)
}

// Began writes that a release has begun, by its id, so that whoever is
// interrupted can find it again.
func (p Printer) Began(release string) {
	fmt.Fprintf(p.Out, "releasing %s\n", shown(release))
}
