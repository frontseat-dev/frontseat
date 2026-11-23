// Package wire turns the host's vocabulary into the plugin contract's
// messages and back, and a mistake in the source into the build API's
// diagnostic: evaluation crosses here, so that it spells no message's
// fields out for itself.
package wire

import (
	"github.com/frontseat-dev/frontseat/build/engine/model"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// ProjectRef is a complete project identity as the graph contract carries it.
func ProjectRef(ref model.ProjectRef) *buildv1.ProjectRef {
	return ProjectRefIn(&buildv1.ProjectRef{}, ref)
}

// ProjectRefIn writes a project's reference into one the caller made, as
// ProjectRef does, and returns it: for a caller that makes many at once.
func ProjectRefIn(into *buildv1.ProjectRef, ref model.ProjectRef) *buildv1.ProjectRef {
	into.Repository, into.Root, into.Plugin = ref.Repository, ref.Root, ref.Plugin
	return into
}

// Coordinate is a coordinate as the plugin contract carries it.
func Coordinate(c model.Coordinate) *pluginv1.Coordinate {
	return &pluginv1.Coordinate{Ecosystem: c.Ecosystem, Name: c.Name}
}

// ReadCoordinate is a coordinate a plugin named, as the host speaks of
// one.
func ReadCoordinate(c *pluginv1.Coordinate) model.Coordinate {
	return model.Coordinate{Ecosystem: c.GetEcosystem(), Name: c.GetName()}
}
