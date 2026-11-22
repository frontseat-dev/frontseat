package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// A plugin owns its commands: you do not write how Go is compiled. For
// what no plugin does, an entity can have actions of its own, written in
// its catalog file, beside what it sets of its plugins:
//
//	spec:
//	  frontseat:
//	    actions:
//	      licenses:
//	        description: Check every dependency's licence
//	        command: [license-check, --report, report.json]
//	        tools: [license-check]
//	        inputs: [go.mod, go.sum, licenses.toml]
//	        needs: [":go.compile"]
//	        outputs: [report.json]
//
// It is an action like any other. It is named custom.<name>, after the
// entity's directory: //platform/gateway:custom.licenses. It sees only
// the files it lists, runs only tools the repository pins, has no
// network, and is answered from what the grid kept.
//
// This is the one place a person writes a command. Nobody writes what an
// action waits for: it reads the patterns of its inputs and what other
// actions left that its needs name, and needs_test.go describes how a
// need names what another action left.
//
// The specs use chain, an entity of the repository lab with three actions
// of its own: write, read and report.
// An entity's own actions / An entity's own action is built beside its plugins' actions.
func TestAnEntitySOwnActionIsBuiltBesideItsPluginsActions(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("chain").build()

	spec.Expect(build.actions()).To(ContainElements("//lab/chain:custom.write", "//lab/chain:go.compile"))
}

// An entity's own actions / An entity's own action is pointed at as it is printed.
func TestAnEntitySOwnActionIsPointedAtAsItIsPrinted(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("chain").build(of("//lab/chain:custom.write"))

	spec.Expect(build.outcomeOf("//lab/chain:custom.write")).To(Equal("passed"))
}

// An entity's own actions / A key an action does not take is refused by name.
func TestAKeyAnActionDoesNotTakeIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.chainWithActions(`      write:
        comand: [go, version]
`)

	spec.Expect(build.says()).To(ContainSubstring("an action takes no key comand"))
}

// An entity's own actions / An action's inputs are patterns, and what it needs of other actions is said apart.
func TestAnActionSInputsArePatternsAndWhatItNeedsOfOtherActionsIsSaidApart(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	refused := func(key, entry, says string) {
		spec.Helper()
		build := spec.chainWithActions("      report:\n        command: [go, version]\n        tools: [go]\n        " + key + ": [\"" + entry + "\"]\n")

		spec.Expect(build.says()).To(ContainSubstring(says), entry)
	}
	refused("inputs", "../go.mod", `the action report: its input 1 is not a pattern of files beneath the project`)
	refused("inputs", ":go.compile", `the action report: its input 1 names an action: what another action left is written under needs`)
	refused("inputs", "//lab/chain:made/", `the action report: its input 1 names an action: what another action left is written under needs`)
	refused("needs", "go.mod", `the action report: its need 1 names no action: a need is :<action> or //<path>:<action>`)
	refused("needs", ":", `the action report: the input ":" names nothing after the colon`)
	refused("needs", "//lab/chain", `the action report: the input "//lab/chain" names a project and nothing of it`)
	// The control: chain's report, which needs what the Go plugin's
	// compile left.
	build := spec.workspaceOf("chain").build(of("//lab/chain:custom.report"))
	spec.Expect(build.outcomeOf("//lab/chain:custom.report")).To(Equal("passed"))
}

// An entity's own actions / An action's outputs are beneath the entity, or in the action's home.
func TestAnActionSOutputsAreBeneathTheEntityOrInTheActionSHome(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	refused := func(output string) {
		spec.Helper()
		build := spec.chainWithActions("      report:\n        command: [go, version]\n        tools: [go]\n        outputs: [\"" + output + "\"]\n")

		spec.Expect(build.says()).To(ContainSubstring(`the action report writes "`+output+`", which is not a plain path beneath the project or the action's home`), output)
	}
	// Out of the entity, out of the home, the whole of either, and a
	// place under a second spelling.
	for _, output := range []string{"../made/", "/made/", "~/../made/", "", ".", "./", "~/", "~/.", "./made/", "made//", "made/../made/", ".frontseat/home/made/"} {
		refused(output)
	}
	// The control: made/, which chain's write leaves and its read reads.
	build := spec.workspaceOf("chain").build(of("//lab/chain:custom.read"))
	spec.Expect(build.outcomeOf("//lab/chain:custom.read")).To(Equal("passed"))
}

// An entity's own actions / An action that says no command is refused.
func TestAnActionThatSaysNoCommandIsRefused(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.chainWithActions(`      write:
        tools: [go]
`)

	spec.Expect(build.says()).To(ContainSubstring("the action write says no command"))
}
