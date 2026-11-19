package controlplane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	connect "connectrpc.com/connect/v2"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/backstage"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/internal/source/told"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
)

// notServed is the reason a control plane that resolves no entities
// refuses a workspace asked for by one.
const notServed = "ENTITIES_NOT_SERVED"

// served refuses entities where the control plane resolves none: by name,
// and before anything is asked of anyone.
func (s *service) served(entities []*typev1.EntityRef) error {
	if len(entities) > 0 && s.Entities == nil {
		return refusedFor(connect.CodeUnimplemented, notServed, "this server resolves no entities; its operator gives it a Backstage (--backstage)")
	}
	return nil
}

// nameOf is the name a repository is given in a workspace where none is
// asked for: as its version control system names it.
func (s *service) nameOf(remote string) string {
	if s.Fetching == nil {
		return ""
	}
	return s.Fetching.NameOf(remote)
}

// codes are how each reason a resolution is refused by is answered.
var codes = map[string]connect.Code{
	backstage.NotFound:    connect.CodeNotFound,
	backstage.Ambiguous:   connect.CodeInvalidArgument,
	backstage.Invalid:     connect.CodeInvalidArgument,
	backstage.Unavailable: connect.CodeUnavailable,
}

// resolve resolves entities added to what a workspace resolved before,
// for its owner, and returns all of it, each repository named as the
// workspace names it: none where nothing is added. A resolution refused
// by name is refused with its reason, in the workspace API's domain.
func (s *service) resolve(ctx context.Context, owner string, added []*typev1.EntityRef, before told.Resolved) (told.Resolved, error) {
	if len(added) == 0 {
		return before, nil
	}
	if err := s.served(added); err != nil {
		return told.Resolved{}, err
	}
	typed := make([]entity.Ref, 0, len(added))
	for i, each := range added {
		ref := entity.FromTypedProto(each)
		if err := ref.ValidateTyped(); err != nil {
			return told.Resolved{}, refusedFor(connect.CodeInvalidArgument, backstage.Invalid, fmt.Sprintf("the entity asked for at %d is no entity's name: one is a name, kind:name or kind:namespace/name", i+1))
		}
		typed = append(typed, ref)
	}
	resolution, err := s.Entities.Resolve(ctx, owner, typed)
	if err != nil {
		var refused *backstage.Refused
		if errors.As(err, &refused) {
			if code, known := codes[refused.Reason]; known {
				return told.Resolved{}, refusedFor(code, refused.Reason, err.Error())
			}
		}
		return told.Resolved{}, refusedFor(connect.CodeUnavailable, backstage.Unavailable, "the catalog could not resolve the entities asked for")
	}
	checkouts, located, err := resolution.Named(s.nameOf)
	if err != nil {
		return told.Resolved{}, refusedFor(connect.CodeInvalidArgument, backstage.Invalid, err.Error())
	}
	out := told.Resolved{Checkouts: slices.Clone(before.Checkouts), Located: slices.Clone(before.Located)}
	for _, c := range checkouts {
		// A catalog names what a sandbox fetches, so what it names is
		// held to what a sandbox may fetch: a repository over http or
		// https from a host's name, never a path of the sandbox's own.
		if _, is := fetched(c.Remote); !is {
			return told.Resolved{}, refusedFor(connect.CodeInvalidArgument, backstage.Invalid, "the catalog says an entity's source is in the repository "+c.Name+", which is not fetched over http or https from a host's name")
		}
		out.Checkouts = merged(out.Checkouts, c)
	}
	for _, l := range located {
		if !slices.ContainsFunc(out.Located, func(had record.Located) bool { return had.Ref == l.Ref }) {
			out.Located = append(out.Located, l)
		}
	}
	return out, nil
}

// merged adds a checkout to those resolved: one of the same repository,
// at the same branch, takes its directories too, or all of it where
// either is whole.
func merged(checkouts []record.Checkout, c record.Checkout) []record.Checkout {
	for i, had := range checkouts {
		if had.Name != c.Name || had.Remote != c.Remote || had.Ref != c.Ref {
			// One of the name from elsewhere is refused by twice.
			continue
		}
		if len(had.Roots) == 0 || len(c.Roots) == 0 {
			checkouts[i].Roots = nil
			return checkouts
		}
		roots := slices.Clone(had.Roots)
		for _, root := range c.Roots {
			if !slices.Contains(roots, root) {
				roots = append(roots, root)
			}
		}
		sort.Strings(roots)
		checkouts[i].Roots = roots
		return checkouts
	}
	return append(checkouts, c)
}

// twice names a repository a workspace would be told twice, from
// another remote or at another branch: by its address and by an
// entity's, or by two entities'. A workspace holds one checkout of a
// repository by a name, and an entity is never placed in another's. It
// is "" where there is none.
func twice(spec *workspacev1.WorkspaceSpec, resolved told.Resolved, nameOf func(remote string) string) string {
	asked := told.Spec(spec, 0, resolved, nameOf)
	seen := map[string]*sourcev1.Checkout{}
	for _, c := range asked.GetRepositories() {
		if had, named := seen[c.GetName()]; named && (had.GetRemote() != c.GetRemote() || had.GetRef() != c.GetRef()) {
			return c.GetName()
		}
		seen[c.GetName()] = c
	}
	return ""
}

// heldOnce refuses a spec and what its entities resolved to that would
// tell a workspace one repository twice.
func (s *service) heldOnce(spec *workspacev1.WorkspaceSpec, resolved told.Resolved) error {
	if name := twice(spec, resolved, s.nameOf); name != "" {
		return refusedFor(connect.CodeInvalidArgument, backstage.Invalid, "the repository "+name+" would be held twice, from two remotes or at two branches, by what is asked for and where the catalog says its entities are: a workspace holds one checkout of a repository by a name")
	}
	return nil
}
