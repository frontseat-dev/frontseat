// Package toldtest tells a supervisor, for a test, what the control plane
// would: a workspace's spec with its entities resolved through a fake
// Backstage (backstagetest), as the control plane resolves them through
// its operator's.
package toldtest

import (
	"context"
	"errors"

	"github.com/frontseat-dev/frontseat/common/told"
	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/workspace/server/backstage"
)

// Spec is what a supervisor is told of a spec at a generation, its entities
// resolved through a fake Backstage, each repository named as nameOf
// names one. With no Backstage, a spec of entities is refused, as a
// control plane given none refuses it.
func Spec(ctx context.Context, spec *workspacev1.WorkspaceSpec, generation int64, catalog backstagetest.Served, nameOf func(remote string) string) (*sourcev1.SourceSpec, error) {
	resolved, err := resolve(ctx, spec.GetEntities(), catalog, nameOf)
	if err != nil {
		return nil, err
	}
	return told.Spec(spec, generation, resolved, nameOf), nil
}

func resolve(ctx context.Context, entities []*typev1.EntityRef, catalog backstagetest.Served, nameOf func(remote string) string) (told.Resolved, error) {
	if len(entities) == 0 {
		return told.Resolved{}, nil
	}
	if catalog.URL == "" {
		return told.Resolved{}, errors.New("the entities are resolved through no Backstage")
	}
	asked, err := backstage.New(backstage.Config{Base: catalog.URL, Token: catalog.Token, Loopback: true})
	if err != nil {
		return told.Resolved{}, err
	}
	typed := make([]entity.Pattern, 0, len(entities))
	for _, each := range entities {
		typed = append(typed, entity.PatternFromProto(each))
	}
	resolution, err := asked.Resolve(ctx, "local", typed)
	if err != nil {
		return told.Resolved{}, err
	}
	var out told.Resolved
	out.Checkouts, out.Located, err = resolution.Named(nameOf)
	return out, err
}
