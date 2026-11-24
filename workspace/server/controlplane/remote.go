package controlplane

import (
	"context"
	"errors"
	"log"
	"path"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/told"
	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/core/plain"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/workspace/server/workspaces/sources"
)

// A workspace is local or remote. A local one clones its repositories in
// its sandbox, and is what every control plane serves. A remote one
// holds repositories from a snapshot store (its spec's stored, task and
// land), mounted in its sandbox and not cloned: what serves them is
// behind a port (sources.Sources), which a control plane is given or is
// not. This one knows no store. Given none, it refuses a spec that asks
// for a remote workspace by name, before anything is made. Given one, it
// has each stored repository held once, when the workspace is made,
// gives the sandbox the volumes that came of it, tells the workspace's
// supervisor each is mounted, and no credential of the store, and has
// what is held let go of once the workspace's sandbox is gone.

// storeNotServed is the reason a control plane given nothing that serves
// stored repositories refuses a remote workspace by.
const storeNotServed = "STORE_NOT_SERVED"

// workdir is where a box holds its workspace: the directory each
// repository is in, by its name (stack/box.Dockerfile).
const workdir = "/work"

// remote is whether a spec asks for a remote workspace: by a stored
// repository, a task or a landing.
func remote(spec *workspacev1.WorkspaceSpec) bool {
	return len(spec.GetStored()) > 0 || spec.GetTask() != "" || spec.GetLand()
}

// storeServed refuses a spec that asks for a remote workspace where the
// control plane was given nothing that serves stored repositories: by
// name, and before anything is asked of anyone or made.
func (s *service) storeServed(spec *workspacev1.WorkspaceSpec) error {
	if remote(spec) && s.Sources == nil {
		return refusedFor(connect.CodeUnimplemented, storeNotServed, "this server serves no remote workspace, which holds its repositories from a snapshot store, on a task: it was given nothing that serves stored repositories, and its workspaces are local, their repositories cloned in them")
	}
	return nil
}

// storedOf are the stored repositories a spec asks a workspace to hold,
// as what serves them is told: those it names, each on its own task or
// else the workspace's, and, where the spec names a task, those its
// entities resolved to, which are then cloned no more: what is left to
// clone is returned beside them. Each is held to what it must be before
// anything is asked of the store: a name of the store's written
// plainly, at a ref or on a task and never both, held in one directory
// of the workspace that nothing else it holds has.
func (s *service) storedOf(spec *workspacev1.WorkspaceSpec, resolved told.Resolved) ([]sources.Stored, told.Resolved, error) {
	if !remote(spec) {
		return nil, resolved, nil
	}
	var asked []sources.Stored
	for _, r := range spec.GetStored() {
		each := sources.Stored{Repository: r.GetRepository(), At: r.GetAt(), Name: r.GetName(), Roots: r.GetRoots(), Task: r.GetTask()}
		if each.Name == "" {
			each.Name = each.Repository
		}
		if each.Task == "" && each.At == "" {
			each.Task = spec.GetTask()
		}
		asked = append(asked, each)
	}
	if spec.GetTask() != "" {
		for _, c := range resolved.Checkouts {
			roots := c.Roots
			if spec.GetWholeRepositories() {
				roots = nil
			}
			asked = append(asked, sources.Stored{Repository: c.Name, Name: c.Name, Roots: roots, Task: spec.GetTask()})
		}
		resolved.Checkouts = nil
	}
	if len(asked) == 0 {
		return nil, resolved, malformed("a task and a landing are said of a workspace that holds stored repositories, and this one is asked for none")
	}
	named := map[string]bool{}
	for _, r := range spec.GetRepositories() {
		named[s.nameOf(r.GetRemote())] = true
	}
	for _, c := range resolved.Checkouts {
		named[c.Name] = true
	}
	for _, each := range asked {
		switch {
		case !plain.Text(each.Repository):
			return nil, resolved, malformed("a stored repository is named as the store names it, in letters, digits and . _ - + /")
		case each.At != "" && each.Task != "":
			return nil, resolved, malformed("a stored repository is held at a ref or a changeset, or on a task, and one is asked for with both")
		case each.At != "" && !plain.Text(each.At), each.Task != "" && !plain.Text(each.Task):
			return nil, resolved, malformed("a ref, a changeset and a task of the store are written plainly, in letters, digits and . _ - + /")
		case record.Directory(each.Name) != nil:
			return nil, resolved, malformed("a stored repository is held in one directory of the workspace, by its name there: give a name to one the store names otherwise than a directory is named")
		case named[each.Name]:
			return nil, resolved, malformed("two repositories asked for would be held in one directory of the workspace: give one of them another name")
		}
		for _, root := range each.Roots {
			if record.Beneath(each.Name, "the directory", root) != nil {
				return nil, resolved, malformed("a directory taken of a stored repository is beneath it")
			}
		}
		named[each.Name] = true
	}
	return asked, resolved, nil
}

// hold has what serves stored repositories hold those a workspace is
// asked for, and returns what came of it, held to what was asked: where
// each stands, and one directory for each, which its sandbox is given.
// Where it fails nothing is held: what was held and is not as asked is
// let go of. What the port said of a failure is logged and told to no
// caller, since it is whatever stands behind the port cared to say.
func (s *service) hold(ctx context.Context, w sources.Workspace) (sources.Held, error) {
	held, err := s.Sources.Hold(ctx, w)
	if err != nil {
		log.Printf("controlplane: the stored repositories of the workspace %s were not held: %v", w.Name, err)
		return sources.Held{}, refusedFor(connect.CodeUnavailable, "STORE_UNAVAILABLE", "what serves stored repositories did not hold those the workspace is asked for: nothing is made")
	}
	if err := asAsked(w.Stored, held); err != nil {
		s.release(ctx, w.Name, w.Owner)
		return sources.Held{}, connect.NewError(connect.CodeInternal, "what serves stored repositories held them otherwise than asked: "+err.Error())
	}
	return held, nil
}

// asAsked holds what was held to what was asked: each repository asked
// for stands at a changeset, and is one directory of the machine,
// written plainly, and nothing else is held or bound. What the port
// names reaches a sandbox's mounts and its supervisor, and is checked
// here first; nothing of it is repeated.
func asAsked(asked []sources.Stored, held sources.Held) error {
	stands, bound := map[string]bool{}, map[string]bool{}
	wanted := map[string]bool{}
	for _, each := range asked {
		wanted[each.Name] = true
	}
	for _, standing := range held.Repositories {
		if !wanted[standing.Name] || stands[standing.Name] || !plain.Text(standing.Repository) || !plain.Text(standing.Changeset) {
			return errors.New("a repository stands as none asked for, twice, or at what is no changeset of a repository")
		}
		stands[standing.Name] = true
	}
	for _, volume := range held.Volumes {
		if !wanted[volume.Repository] || bound[volume.Repository] || !plain.Absolute(volume.Source) {
			return errors.New("a directory is given for no repository asked for, twice, or is none named absolutely and plainly")
		}
		bound[volume.Repository] = true
	}
	if len(stands) != len(wanted) || len(bound) != len(wanted) {
		return errors.New("a repository asked for stands nowhere, or has no directory")
	}
	return nil
}

// release has what was held for a workspace let go of, and says whether
// it was: a workspace that holds nothing of the store has nothing to let
// go of.
func (s *service) release(ctx context.Context, name, owner string) error {
	if s.Sources == nil {
		return errors.New("the workspace holds stored repositories, and this control plane was given nothing that serves them: it is ended by one that was")
	}
	if err := s.Sources.Release(ctx, sources.Workspace{Name: name, Owner: owner}); err != nil {
		log.Printf("controlplane: what was held for the workspace %s was not let go of: %v", name, err)
		return errors.New("what serves stored repositories did not let go of what it holds for the workspace")
	}
	return nil
}

// bound are the volumes a workspace's sandbox is given for its stored
// repositories: each directory held for one, at the repository's
// directory in the workspace.
func bound(held sources.Held) []*sandboxv1.Volume {
	var out []*sandboxv1.Volume
	for _, volume := range held.Volumes {
		out = append(out, &sandboxv1.Volume{Name: "stored-" + volume.Repository, Kind: sandboxv1.Volume_KIND_HOST, Source: volume.Source, Path: path.Join(workdir, volume.Repository)})
	}
	return out
}

// said is what a workspace's supervisor is told to hold, at a
// generation: what its spec and its entities ask it to clone
// (told.Spec), and the stored repositories held for it, each mounted at
// the changeset it stands at, of which the supervisor clones none. It
// is told nothing of the store but their names.
func (s *service) said(spec *workspacev1.WorkspaceSpec, generation int64, resolved told.Resolved, held sources.Held) *sourcev1.SourceSpec {
	out := told.Spec(spec, generation, resolved, s.nameOf)
	for _, standing := range held.Repositories {
		out.Stored = append(out.Stored, &sourcev1.StoredSource{Name: standing.Name, Repository: standing.Repository, Changeset: standing.Changeset, Roots: standing.Roots})
	}
	return out
}

// holds is whether a workspace holds stored repositories.
func holds(held sources.Held) bool { return len(held.Repositories) > 0 }

// fixed says what a spec would add to a workspace that holds stored
// repositories; "" where it adds nothing. What such a workspace holds
// is said when it is made: its sandbox is given its repositories then.
func fixed(has, wanted *workspacev1.WorkspaceSpec) string {
	if len(wanted.GetRepositories()) != len(has.GetRepositories()) || len(wanted.GetEntities()) != len(has.GetEntities()) || wanted.GetWholeRepositories() != has.GetWholeRepositories() {
		return "the spec adds to a workspace that holds stored repositories, and what such a workspace holds is said when it is made"
	}
	return ""
}
