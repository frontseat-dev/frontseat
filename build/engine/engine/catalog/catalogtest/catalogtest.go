// Package catalogtest writes the catalog files a test gives a workspace.
package catalogtest

import "strings"

// Component is the catalog file of a Backstage Component of a name, a
// service the team owns, whose spec.frontseat says what is given, written
// as it would be at the left margin; given nothing, it says nothing of
// frontseat.
func Component(name, frontseat string) string {
	file := "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: " + name + "\nspec:\n  type: service\n  lifecycle: experimental\n  owner: team\n"
	if frontseat == "" {
		return file
	}
	file += "  frontseat:\n"
	for line := range strings.Lines(frontseat) {
		file += "    " + line
	}
	return file
}

// Cycle is what spec.frontseat says of a component two of whose actions
// each read what the other leaves: a cycle no plan can be made of,
// though the graph can still be read.
const Cycle = `actions:
  first:
    command: ["true"]
    inputs: [":custom.second"]
    outputs: ["one"]
  second:
    command: ["true"]
    inputs: [":custom.first"]
    outputs: ["two"]
`
