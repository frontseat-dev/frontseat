// Package wire turns the host's vocabulary into the contract's messages
// and back. It is the one place where the two meet: the daemon, the
// command line and the specs all cross here, so that nothing spells a
// message's fields out for itself.
package wire

import (
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// ProjectRef is a complete project identity as the graph contract carries it.
func ProjectRef(ref model.ProjectRef) *buildv1.ProjectRef {
	return &buildv1.ProjectRef{Repository: ref.Repository, Root: ref.Root, Plugin: ref.Plugin}
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
