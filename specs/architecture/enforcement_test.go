package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
)

// A nested go.mod is a boundary to inspect, not a way out of inspection.
// Fixtures remain excluded, because they are input repositories, not this one.
func TestArchitectureScanIncludesNestedGoModules(t *testing.T) {
	root := t.TempDir()
	testfiles.Tree(t, root, map[string]string{
		"go.work":                 "go 1.27.3\nuse (\n .\n ./client\n)\n",
		"go.mod":                  "module example.com/root\n",
		"root.go":                 "package root\n",
		"cli/go.mod":              "module example.com/root/client\n",
		"client/build/client.go":  "package client\nimport _ \"github.com/frontseat-dev/frontseat/server/internal/build\"\n",
		"testdata/fixture/go.mod": "module example.com/fixture\n",
		"testdata/fixture/app.go": "package fixture\n",
	})
	got := importsUnder(t, root, root)
	if _, found := got["root.go"]; !found {
		t.Fatal("the root package control was not inspected")
	}
	if imports, found := got["client/build/client.go"]; !found || len(imports) != 1 || imports[0] != module+"/server/internal/build" {
		t.Fatalf("nested module was not inspected: %v", got)
	}
	if _, found := got["testdata/fixture/app.go"]; found {
		t.Fatal("an input fixture was treated as repository source")
	}
}

func TestFeaturePoliciesHaveAllowedAndForbiddenControls(t *testing.T) {
	for _, c := range []struct {
		file, dependency string
		allowed          bool
	}{
		// The Go SDK is built on the generated API and its own transport,
		// and on nothing else of this repository: not a server, not the
		// command line, not the shared private libraries.
		{"sdk/go/build.go", "sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect", true},
		{"sdk/go/frontseat.go", "sdk/go/internal/transport", true},
		{"sdk/go/build.go", "server/internal/build", false},
		{"sdk/go/build.go", "cli/internal/machine", false},
		{"sdk/go/internal/transport/transport.go", "server/telemetry", false},
		{"sdk/go/workspace.go", "cli/internal/vcs", false},
		{"sdk/go/entity/proto.go", "sdk/go/api/frontseat/build/v1alpha1", true},
		{"sdk/go/entity/proto.go", "sdk/go", false},
		// The transport is the client's; it knows nothing of the client.
		{"sdk/go/internal/transport/transport.go", "sdk/go", false},
		// The command line asks the control plane for credentials through
		// the SDK, and asks the sandbox API nothing: it is served to no
		// one, and is no part of the SDK.
		{"cli/internal/cmd/credential.go", "sdk/go", true},
		{"cli/internal/cmd/credential.go", "sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect", true},
		// The history of runs reads the API, not the plugin contract.
		{"server/internal/history/history.go", "sdk/go/api/frontseat/build/v1alpha1", true},
		{"server/internal/history/history.go", "server/internal/pluginapi/frontseat/plugin/v1alpha1", false},
		// A test logs in to a workspace through the SDK's tunnel, from its
		// identity alone.
		{"server/testsupport/testidentity/shell.go", "sdk/go", true},
		{"server/testsupport/testgrid/testgrid.go", "sdk/go", false},
		// The command line hands the SDK its telemetry; the SDK takes it
		// as an option.
		{"cli/internal/cmd/root.go", "server/telemetry", true},
		{"cli/internal/cmd/root.go", "sdk/go", true},
		{"cli/internal/cmd/root.go", "server/internal/build", false},
		// The binary is a thin main over the commands.
		{"cli/cmd/frontseat/main.go", "cli/internal/cmd", true},
		{"cli/cmd/frontseat/main.go", "sdk/go", false},
		// The plugin contract's Go is the daemon's: it is generated into
		// the build's internal packages, and the SDK has none of it.
		{"server/internal/pluginhost/host.go", "server/internal/pluginapi/frontseat/plugin/v1alpha1", true},
		{"server/internal/pluginapi/frontseat/plugin/v1alpha1/plugin.pb.go", "sdk/go/api/frontseat/build/v1alpha1", true},
		{"server/internal/source/workspace.go", "server/internal/pluginapi/frontseat/plugin/v1alpha1", false},
		{"sdk/go/entity/proto.go", "server/internal/pluginapi/frontseat/plugin/v1alpha1", false},
		// The daemon's internal API is the daemon's to serve and the
		// control plane's to ask, and no client's: the SDK and the
		// command line link none of it.
		{"server/internal/source/service.go", "server/internal/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect", true},
		{"server/internal/controlplane/controlplane.go", "server/internal/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect", true},
		{"cli/internal/cmd/root.go", "server/internal/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect", false},
		{"sdk/go/workspace.go", "server/internal/sourceapi/frontseat/source/v1alpha1", false},
		{"server/internal/engine/evaluate/evaluate.go", "server/internal/sourceapi/frontseat/source/v1alpha1", false},
		// Git is run by one package, for both version control packages,
		// and by nothing else; it knows nothing of either's port.
		{"cli/internal/vcs/git/git.go", "server/git", true},
		{"server/internal/source/internal/vcs/git/git.go", "server/git", true},
		{"cli/internal/machine/open.go", "server/git", false},
		{"server/internal/source/workspace.go", "server/git", false},
		{"server/git/git.go", "cli/internal/vcs", false},
		// What leads out of a sandbox is held to being written plainly by
		// one check, which the control plane and the gateway's driver use
		// and the container runtime's does not need.
		{"server/internal/controlplane/rules.go", "server/internal/plain", true},
		// What a forge asks is the control plane's to write as rules, and
		// its composers' to tell it of: the control plane, the program
		// that serves it and the test support that starts one. A forge
		// knows nothing of them, nor of a sandbox, and no daemon knows a
		// forge: what it may reach is its sandbox's to hold.
		{"server/internal/controlplane/rules.go", "server/internal/forge", true},
		{"server/cmd/frontseat-local/main.go", "server/internal/forge", true},
		// What the local control plane is known to its gateway by is a
		// secret in files of its own, held as its other secrets are; a
		// daemon keeps none so.
		{"server/cmd/frontseat-local/main.go", "server/internal/secretfile", true},
		{"server/cmd/frontseatd/main.go", "server/internal/secretfile", false},
		{"server/testsupport/controlplanetest/controlplanetest.go", "server/internal/forge", true},
		{"server/internal/forge/forge.go", "server/internal/plain", true},
		{"server/internal/exec/fix/fix.go", "server/internal/exec/here", true},
		{"server/internal/exec/publish/publish.go", "server/internal/exec/here", true},
		{"server/internal/exec/publish/publish.go", "server/internal/pluginhost", true},
		{"server/internal/exec/here/here.go", "server/internal/exec/fix", false},
		{"server/internal/exec/here/here.go", "server/internal/engine/plan", false},
		{"server/internal/exec/publish/publish.go", "server/internal/exec/fix", false},
		{"server/internal/exec/publish/publish.go", "server/internal/build", false},
		{"server/internal/exec/run/run.go", "server/internal/exec/publish", false},
		{"server/internal/forge/forge.go", "server/internal/controlplane", false},
		{"server/internal/forge/forge.go", "server/sandbox", false},
		{"server/cmd/frontseatd/main.go", "server/internal/forge", false},
		{"server/internal/build/releases.go", "server/internal/forge", false},
		{"server/sandbox/driver/openshell/fetch.go", "server/internal/forge", false},
		{"server/sandbox/driver/openshell/fetch.go", "server/internal/plain", true},
		{"server/testsupport/testcontainer/container.go", "server/internal/plain", false},
		{"server/internal/plain/plain.go", "server/sandbox", false},
		// What a server keeps to find again is kept by one store: the
		// control plane's workspaces and credentials, the sandbox
		// service's sandboxes and a workspace's manifest; the store knows
		// none of them.
		{"server/internal/controlplane/credentials.go", "server/internal/records", true},
		{"server/sandbox/kept.go", "server/internal/records", true},
		{"server/internal/source/internal/manifest/manifest.go", "server/internal/records", true},
		{"server/internal/records/records.go", "server/internal/plain", false},
		{"server/internal/records/records.go", "server/sandbox", false},
		{"cli/internal/cmd/root.go", "server/internal/records", false},
		// A process group is ended by one implementation, which a
		// sandbox's refusal of a signal to a group does not stop: the
		// confiner's, which git, a program and a workspace's shell use.
		{"server/git/stop_unix.go", "server/internal/confine", true},
		{"server/internal/exec/program/program.go", "server/internal/confine", true},
		{"server/internal/source/internal/shell/shell.go", "server/internal/confine", true},
		{"server/git/git.go", "server/internal/exec", false},
		{"server/internal/backstage/backstage.go", "server/internal/confine", false},
		// Who a sandbox runs as and what of its image it writes are held by
		// one check, the sandbox service's and a test's services' alike.
		{"server/sandbox/service.go", "server/internal/plain", true},
		{"server/internal/testservice/testservice.go", "server/internal/plain", true},
		// A server's test calls the API as anyone does, through the
		// generated clients, and reads a stream to its end with the SDK's
		// one reader; the server's code links no client of frontseat's.
		{"server/internal/build/logs_test.go", "sdk/go", true},
		{"server/internal/build/runs.go", "sdk/go", false},
		{"server/internal/source/service_test.go", "sdk/go", true},
		// What the command line prints names an entity as the SDK's one
		// reader and writer of references does, and asks no server.
		{"cli/internal/ui/plain/release.go", "sdk/go/entity", true},
		{"cli/internal/ui/plain/release.go", "sdk/go", false},
		{"server/internal/source/service.go", "sdk/go", false},
		{"server/internal/build/logs_test.go", "sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect", true},
		// The specs are what nothing depends on: they use the rest, and
		// no component's code or test imports them back.
		{"server/internal/build/logs_test.go", "specs/build", false},
		{"server/testsupport/testgrid/grid.go", "specs/cli", false},
		// A spec starts what it needs in its setup file alone, and acts
		// through the API everywhere else.
		{"specs/cli/support_test.go", "server/testsupport/controlplanetest", true},
		{"specs/cli/local_test.go", "server/internal/controlplane", false},
		{"specs/cli/local_test.go", "server/internal/source", false},
		{"specs/cli/local_test.go", "local", false},
		{"specs/compare/support_test.go", "server/testsupport/controlplanetest", true},
		{"specs/compare/support_test.go", "server/internal/controlplane", false},
		// The control plane is started beside it, by its own test support;
		// the sandbox service knows nothing of what lives in a sandbox.
		{"server/testsupport/controlplanetest/controlplanetest.go", "server/internal/controlplane", true},
		{"server/testsupport/controlplanetest/controlplanetest.go", "server/internal/source", true},
		{"server/sandbox/sandbox.go", "server/internal/controlplane", false},
		{"server/sandbox/sandbox_test.go", "server/internal/source", false},
		{"server/sandbox/sandbox_test.go", "server/testsupport/controlplanetest", false},
		{"specs/compare/compare_test.go", "server/internal/source", false},
		{"specs/compare/compare_test.go", "local", false},
		// Test support uses nothing it is there to test.
		{"server/testsupport/testgrid/grid.go", "server/sandbox", false},
		{"server/testsupport/testgrid/grid.go", "local", true},
		{"server/testsupport/testgrid/grid.go", "server/internal/controlplane", false},
		{"server/testsupport/testgrid/grid.go", "server/testsupport/controlplanetest", false},
		{"server/testsupport/testgrid/grid.go", "server/testsupport/buildtest", false},
		{"server/testsupport/buildtest/buildtest.go", "server/testsupport/controlplanetest", false},
		// What starts a control plane for a test ends what one it stopped
		// left in the gateway, through the sandbox service; other test
		// support makes no sandboxes there.
		{"server/testsupport/controlplanetest/left.go", "server/sandbox", true},
		// What a test is told of its repository is said by the host and
		// read by the test support, and a test of the host holds the two
		// to one name; the host itself imports no test support.
		{"server/internal/exec/stage/source_test.go", "server/testsupport/testmodule", true},
		{"server/internal/exec/stage/stage.go", "server/testsupport/testmodule", false},
		// A program a person starts at a checkout's root finds it as the
		// stack's program does (local.Checkout), and nothing of the local
		// stack beyond it is the gate's to reach for.
		{"specs/cmd/prepare-modules/main.go", "local", true},
		{"specs/cmd/prepare-modules/main.go", "local/stack", false},
		{"server/testsupport/testgrid/testgrid.go", "server/sandbox", false},
		{"server/testsupport/drivertest/drivertest_test.go", "server/testsupport/drivertest", true},
		// The container driver is the sandbox service's to name, and its
		// own tests' and the gateway's driver's tests'; nothing else's.
		{"server/sandbox/sandbox.go", "server/internal/image", true},
		{"server/sandbox/sandbox.go", "server/testsupport/testcontainer", false},
		{"server/sandbox/driver/openshell/openshell.go", "server/internal/image", false},
		{"server/sandbox/driver/openshell/openshell_test.go", "server/internal/image", true},
		{"server/internal/runner/runner.go", "server/testsupport/testcontainer", false},
		{"server/internal/runner/attempt_test.go", "server/testsupport/testcontainer", true},
		{"server/internal/runner/pool.go", "server/sandbox/driver/openshell", true},
		{"server/internal/runner/pool.go", "server/internal/controlplane", false},
		{"server/testsupport/testcontainer/container.go", "server/sandbox/driver", true},
		{"server/testsupport/testcontainer/container.go", "server/sandbox", false},
		// The gate programs read their own packages and the test support.
		{"specs/cmd/gate/main.go", "specs/internal/gatecommand", true},
		{"specs/cmd/gate/main.go", "server/internal/build", false},
		{"specs/sandbox/sandbox_test.go", "server/testsupport/drivertest", false},
		{"specs/sandbox/support_test.go", "server/testsupport/drivertest", true},
		{"server/testsupport/testbox/testbox.go", "server/testsupport/testrequired", true},
		{"server/testsupport/testbox/testbox.go", "local/box", false},
		{"specs/cmd/gate/main.go", "local/box", true},
		{"specs/cli/box_test.go", "local/box", true},
		{"specs/sandbox/support_test.go", "local/box", false},
		{"specs/cli/box_test.go", "local/stack", false},
		{"server/testsupport/testbox/testbox.go", "server/cmd/frontseat-local", false},
		{"server/testsupport/testbox/stack.go", "local/stack", true},
		{"server/testsupport/testgrid/testgrid.go", "local/stack", false},
		{"server/testsupport/testgrid/testgrid.go", "server/internal/confine", true},
		{"server/testsupport/testgrid/testgrid.go", "server/internal/exec/tools", false},
		// The box is built from what the stack says it is laid on, and
		// from nothing of what it holds.
		{"local/box/box.go", "local", true},
		{"local/box/box.go", "server/sandbox", false},
		{"local/box/box.go", "server/internal/build", false},
		{"local/stack/stack.go", "local/box", true},
		{"local/stack/stack.go", "server/internal/controlplane", false},
		{"local/stack/container.go", "local/ports", true},
		{"local/stack/container.go", "server/internal/httpserver", false},
		// The local stack's program is the control plane told what the
		// stack is; the control plane is told nothing of the stack.
		{"server/cmd/frontseat-local/main.go", "server/internal/controlplane", true},
		{"server/cmd/frontseat-local/main.go", "local", true},
		{"server/cmd/frontseat-local/local.go", "local/stack", true},
		{"server/cmd/frontseat-local/main.go", "server/sandbox", false},
		{"server/internal/controlplane/controlplane.go", "local", false},
		{"local/local.go", "server/internal/controlplane", false},
		// The ports are said once, and read by what opens, serves or asks
		// them, which reads nothing else of the stack for them.
		{"server/internal/controlplane/rules.go", "local/ports", true},
		{"server/cmd/frontseatd/main.go", "local/ports", true},
		{"server/cmd/frontseatd/main.go", "local", false},
		{"cli/internal/machine/token.go", "local/ports", true},
		{"cli/internal/machine/token.go", "local", false},
		{"server/testsupport/testcontainer/container_test.go", "local/ports", true},
		{"server/testsupport/testcontainer/container_test.go", "local/stack", false},
		{"local/stack.go", "local/ports", true},
		{"local/ports/ports.go", "local", false},
		{"local/stack.go", "server/telemetry", false},
		// The grid's runner is a thin main over the runner, which alone
		// serves the sandbox API over the container runtime.
		{"server/cmd/frontseat-runner/main.go", "server/internal/runner", true},
		{"server/cmd/frontseat-runner/main.go", "server/sandbox", false},
		{"server/internal/runner/runner.go", "server/sandbox", true},
		{"server/internal/runner/runner.go", "server/internal/controlplane", false},
		// The confiner is confine alone.
		{"server/cmd/frontseat-action/main.go", "server/internal/confine", true},
		{"server/cmd/frontseat-action/main.go", "server/internal/runner", false},
		// The shared libraries moved from the server are each one thing.
		{"server/internal/testservice/testservice.go", "server/internal/image", true},
		{"server/internal/testservice/testservice.go", "server/internal/model", false},
		{"server/internal/plain/plain.go", "server/internal/testservice", false},
		{"sdk/go/build.go", "sdk/go/testdata/bridge", false},
		{"server/internal/engine/catalog/catalog.go", "server/internal/pluginhost", false},
		{"server/internal/engine/plan/generated.go", "server/internal/pattern", true},
		{"server/internal/engine/graph/project.go", "server/internal/pattern", true},
		{"server/internal/engine/plan/generated.go", "server/internal/pluginhost", false},
		{"server/internal/exec/executor.go", "server/internal/engine/plan", false},
		{"server/internal/build/runs.go", "server/internal/exec/schedule", false},
		{"sdk/go/build_test.go", "server/testsupport/buildtest", false},
		{"server/telemetry/trace.go", "server/internal/build", false},
		{"server/testsupport/testgrid/grid.go", "server/internal/build", false},
		{"server/internal/build/service.go", "server/internal/source", true},
		{"server/internal/build/graph_service.go", "server/internal/wire", true},
		{"sdk/go/build.go", "server/internal/wire", false},
		{"server/internal/source/service.go", "server/internal/wire", false},
		{"server/internal/build/service.go", "server/internal/source/internal/populate", false},
		{"server/internal/source/service.go", "server/internal/build", false},
		{"server/sandbox/service.go", "server/internal/build", false},
		{"server/internal/engine/graph/graph.go", "server/internal/engine/plan", false},
		{"server/internal/engine/graph/graph_test.go", "server/internal/engine/plan", false},
		{"server/internal/engine/evaluate/evaluate.go", "server/internal/exec", false},
		{"server/internal/engine/release/plan.go", "server/internal/source/vcs", true},
		{"server/internal/engine/release/plan.go", "server/internal/pluginhost", false},
		{"server/internal/engine/release/plan.go", "server/internal/source", false},
		{"server/internal/engine/evaluate/evaluate.go", "server/internal/pluginhost", true},
		{"server/internal/exec/run/run.go", "server/internal/build", false},
		{"specs/build/builds_test.go", "sdk/go", true},
		{"specs/build/builds_test.go", "server/testsupport/buildtest", false},
		{"specs/build/support_test.go", "server/testsupport/buildtest", true},
		{"server/cmd/frontseatd/main.go", "server/internal/build", true},
		{"server/cmd/frontseatd/main.go", "server/internal/pluginhost", false},
		{"server/internal/build/service.go", "server/internal/pluginhost/conformance", false},
		{"server/internal/pluginhost/host_test.go", "server/internal/pluginhost/conformance", true},
		{"server/internal/engine/evaluate/catalog_rewrite_test.go", "server/internal/pluginhost/conformance", true},
		{"server/internal/engine/evaluate/evaluate.go", "server/internal/pluginhost/conformance", false},
		{"server/internal/engine/config/config_test.go", "server/internal/pluginhost/conformance", false},
		{"server/internal/engine/config/config_test.go", "server/internal/pluginhost/pluginhosttest", true},
		{"server/internal/pluginhost/host_test.go", "server/internal/pluginhost/pluginhosttest", true},
		{"server/internal/pluginhost/host.go", "server/internal/pluginhost/pluginhosttest", false},
		{"server/internal/engine/config/config.go", "server/internal/pluginhost/pluginhosttest", false},
		{"server/internal/source/service_test.go", "server/internal/pluginhost/pluginhosttest", false},
		// A driver's test is given a running sandbox as drivertest makes
		// one; a driver is not.
		{"server/sandbox/driver/openshell/openshell_test.go", "server/testsupport/drivertest", true},
		{"server/testsupport/testcontainer/container_test.go", "server/testsupport/drivertest", true},
		{"server/sandbox/driver/openshell/openshell.go", "server/testsupport/drivertest", false},
		{"server/internal/source/service_test.go", "server/testsupport/drivertest", false},
		// A test of the control plane or of the build's service reads a
		// stream with the SDK's one public reader, not its transport,
		// which is the SDK's alone.
		{"server/internal/controlplane/controlplane_test.go", "sdk/go/internal/transport", false},
		{"server/internal/controlplane/controlplane_test.go", "server/internal/source", true},
		{"server/internal/controlplane/rules.go", "server/internal/source", false},
		{"server/internal/build/history_test.go", "sdk/go/internal/transport", false},
		{"server/internal/controlplane/controlplane_test.go", "sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect", true},
		// The architecture test is a spec: it reads the catalog's
		// references with the one parser of them, and starts nothing.
		{"specs/architecture/catalog_test.go", "sdk/go/entity", true},
		{"specs/architecture/catalog_test.go", "server/internal/model", false},
		{"server/testsupport/testgrid/testgrid.go", "sdk/go/entity", false},
		{"server/internal/build/retained_session_test.go", "server/internal/engine/catalog/catalogtest", true},
		{"server/internal/engine/evaluate/retained_projection_test.go", "server/internal/engine/catalog/catalogtest", true},
		{"server/internal/engine/catalog/catalog.go", "server/internal/engine/catalog/catalogtest", false},
		{"server/internal/build/graph_service.go", "server/internal/engine/catalog/catalogtest", false},
		{"server/internal/source/service_test.go", "server/internal/engine/catalog/catalogtest", false},
		{"server/internal/build/run_test.go", "server/internal/exec/tools/toolstest", true},
		{"server/internal/exec/tools/budget_test.go", "server/internal/exec/tools/toolstest", true},
		{"server/internal/exec/tools/tools.go", "server/internal/exec/tools/toolstest", false},
		{"server/internal/build/runs.go", "server/internal/exec/tools/toolstest", false},
		{"server/internal/source/service_test.go", "server/internal/exec/tools/toolstest", false},
		{"server/internal/exec/schedule/schedule_test.go", "server/internal/engine/plan/plantest", true},
		{"server/internal/engine/plan/needs_test.go", "server/internal/engine/plan/plantest", true},
		{"server/internal/exec/schedule/schedule.go", "server/internal/engine/plan/plantest", false},
		{"server/internal/source/service_test.go", "server/internal/engine/plan/plantest", false},
		{"server/internal/build/service.go", "server/testsupport/testgrid", false},
		{"server/internal/controlplane/ending_test.go", "server/testsupport/drivertest", true},
		{"server/internal/controlplane/controlplane.go", "server/testsupport/drivertest", false},
		{"server/internal/controlplane/ending_test.go", "server/sandbox/driver", false},
		{"server/internal/controlplane/ending_test.go", "server/sandbox/driver/openshell", false},
		{"server/sandbox/service_test.go", "server/testsupport/drivertest", true},
		// How a grid is asked is said once, to the daemon's two dials, by
		// the daemon that was told; nothing that stages or runs an action
		// knows of it.
		{"server/internal/exec/reapi/reapi.go", "server/internal/exec/access", true},
		{"server/internal/exec/download/download.go", "server/internal/exec/access", true},
		{"server/internal/build/build.go", "server/internal/exec/access", true},
		{"server/internal/exec/stage/stage.go", "server/internal/exec/access", false},
		{"server/internal/exec/tools/tools.go", "server/internal/exec/access", false},
		{"server/internal/exec/access/access.go", "server/internal/exec", false},
		{"server/cmd/frontseatd/main.go", "server/internal/exec/access", false},
		// Evaluation writes the source groups of the actions it plans.
		{"server/internal/engine/evaluate/evaluate.go", "server/internal/pattern", true},
		{"server/internal/exec/schedule/schedule.go", "server/internal/pattern", false},
		{"server/internal/build/runs.go", "server/internal/pattern", false},
	} {
		if got := permits(c.file, module+"/"+c.dependency); got != c.allowed {
			t.Errorf("%s -> %s: allowed=%v, want %v", c.file, c.dependency, got, c.allowed)
		}
	}
}

func TestEscapedImportsCannotBypassOwnership(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "client")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Go permits escapes in import literals; the checker must inspect their value.
	source := "package client\nimport _ \"github.com/frontseat-dev/frontseat/server/internal/\\x62uild\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	got := importsUnder(t, root, dir)["client/bad.go"]
	if len(got) != 1 || permits("client/bad.go", got[0]) {
		t.Fatalf("escaped forbidden import accepted: %v", got)
	}
}

func TestPublicSignatureControlsFollowAliasesAndValues(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		leaks        bool
	}{
		{"direct", "type Public struct {Value model.Private}", true},
		{"hidden alias", "type hidden = model.Private; type Public struct {Value hidden}", true},
		{"alias chain return", "type first = model.Private; type second = first; func Public() second {panic(0)}", true},
		{"inferred variable", "var hidden = model.Private{}; var Public = hidden", true},
		{"inferred call", "func hidden() model.Private {panic(0)}; var Public = hidden()", true},
		{"private field", "type Public struct {hidden model.Private}", false},
		{"recursive public value", "type Public struct {Next *Public; Name string}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "entry.go", "package entry\nimport model \"github.com/frontseat-dev/frontseat/server/internal/model\"\n"+tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(publicLeaks([]*ast.File{file})) > 0; got != tc.leaks {
				t.Fatalf("leaks=%v, want %v", got, tc.leaks)
			}
		})
	}
}
