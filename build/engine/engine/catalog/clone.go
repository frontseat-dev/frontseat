package catalog

import (
	"slices"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"google.golang.org/protobuf/proto"
)

// CloneDescriptors copies catalog facts and the contexts used to reconcile them.
func CloneDescriptors(descriptors []Descriptor) []Descriptor {
	out := slices.Clone(descriptors)
	for i := range out {
		out[i].Roots = slices.Clone(out[i].Roots)
		out[i].Relations = slices.Clone(out[i].Relations)
		out[i].Context = proto.CloneOf(out[i].Context)
	}
	return out
}

// Clone gives a caller its own catalog, including roots returned by Entities.
func (g *Graph) Clone() *Graph {
	if g == nil {
		return nil
	}
	out := &Graph{nodes: make(map[entity.Ref]*Node, len(g.nodes)), declared: CloneDescriptors(g.declared), ordered: slices.Clone(g.ordered)}
	for ref, node := range g.nodes {
		v := *node
		v.Roots = slices.Clone(v.Roots)
		v.Relations = slices.Clone(v.Relations)
		out.nodes[ref] = &v
	}
	for i, node := range out.ordered {
		out.ordered[i] = out.nodes[node.Ref]
	}
	return out
}
