// Package told is a workspace's spec as its daemon is told it through
// the daemon's source API (frontseat.source.v1alpha1), and what a daemon
// says it holds as a workspace's status: the one translation between the
// workspace API and the source API, for the control plane, which is the
// one writer of what a workspace holds, and for the test support that
// stands in for it.
package told

import (
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
)

// The reasons a daemon refuses a source by, as ErrorInfo of the source
// API's domain: what the control plane acts on, and relays under its own,
// never the words.
const (
	// Populating is a source told while another is being brought.
	Populating = "SOURCE_POPULATING"
	// Mounted is a source told to a daemon that was given a checkout.
	Mounted = "SOURCE_MOUNTED"
	// Missing is a source that names nothing to bring.
	Missing = "SOURCE_MISSING"
	// Unfit is a repository that cannot be held where the source is.
	Unfit = "SOURCE_UNFIT"
	// GenerationInvalid is a source told at no generation.
	GenerationInvalid = "GENERATION_INVALID"
)

// Resolved is what a workspace's entities resolved to, as the control
// plane resolved them once and keeps them: the repositories to clone,
// each named as the workspace names it, and where each entity is among
// them.
type Resolved struct {
	Checkouts []record.Checkout `json:"checkouts,omitempty"`
	Located   []record.Located  `json:"located,omitempty"`
}

// Spec is what a workspace's spec asks its daemon to hold, at a
// generation: the repositories it was asked for, each named as it was or
// as nameOf names it from its remote, and those its entities resolved
// to, whole where the spec asks for them whole; and where its entities
// are. Nothing of what it may do or who asks is told.
func Spec(spec *workspacev1.WorkspaceSpec, generation int64, resolved Resolved, nameOf func(remote string) string) *sourcev1.SourceSpec {
	out := &sourcev1.SourceSpec{Generation: generation}
	for _, r := range spec.GetRepositories() {
		name := r.GetName()
		if name == "" {
			name = nameOf(r.GetRemote())
		}
		out.Repositories = append(out.Repositories, &sourcev1.Checkout{Remote: r.GetRemote(), Ref: r.GetRef(), Roots: r.GetRoots(), Name: name})
	}
	for _, c := range resolved.Checkouts {
		roots := c.Roots
		if spec.GetWhole() {
			roots = nil
		}
		out.Repositories = append(out.Repositories, &sourcev1.Checkout{Remote: c.Remote, Ref: c.Ref, Roots: roots, Name: c.Name})
	}
	for _, l := range resolved.Located {
		out.Located = append(out.Located, &sourcev1.LocatedEntity{Ref: entity.Proto(l.Ref), Repository: l.Repository, Root: l.Root})
	}
	return out
}

// Entities are where a workspace's entities are, as its status says it.
func Entities(resolved Resolved) []*workspacev1.LocatedEntity {
	var out []*workspacev1.LocatedEntity
	for _, l := range resolved.Located {
		out = append(out, &workspacev1.LocatedEntity{Ref: entity.Proto(l.Ref), Repository: l.Repository, Root: l.Root})
	}
	return out
}

// Status is what a daemon says it holds, as a workspace's status says
// it.
func Status(held *sourcev1.Source) *workspacev1.WorkspaceStatus {
	out := &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING, Failure: held.GetFailure()}
	// A state this does not know is not said to be ready.
	switch held.GetState() {
	case sourcev1.Source_STATE_READY:
		out.State = workspacev1.WorkspaceStatus_STATE_READY
	case sourcev1.Source_STATE_POPULATING:
		out.State = workspacev1.WorkspaceStatus_STATE_POPULATING
	case sourcev1.Source_STATE_FAILED:
		out.State = workspacev1.WorkspaceStatus_STATE_FAILED
	}
	for _, r := range held.GetRepositories() {
		out.Repositories = append(out.Repositories, &workspacev1.Repository{Name: r.GetName(), Remote: r.GetRemote(), Commit: r.GetCommit(), Roots: r.GetRoots()})
	}
	return out
}
