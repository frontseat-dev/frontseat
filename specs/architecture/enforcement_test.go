package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
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
		"client/build/client.go":  "package client\nimport _ \"github.com/frontseat-dev/frontseat/build/daemon/build\"\n",
		"testdata/fixture/go.mod": "module example.com/fixture\n",
		"testdata/fixture/app.go": "package fixture\n",
	})
	got := importsUnder(t, root, root)
	if _, found := got["root.go"]; !found {
		t.Fatal("the root package control was not inspected")
	}
	if imports, found := got["client/build/client.go"]; !found || len(imports) != 1 || imports[0] != module+"/build/daemon/build" {
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
		// The client refuses a token in clear as bearer says it, and the
		// bearer knows nothing of the client.
		{"sdk/go/frontseat.go", "sdk/go/bearer", true},
		{"sdk/go/bearer/bearer.go", "sdk/go", false},
		// The sandbox API's Go is the server's: the runner, the control
		// plane and frontseat-enterprise speak it, and neither the Go SDK
		// nor the command line does.
		{"grid/runner/runner.go", "sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect", true},
		{"sdk/go/workspace.go", "sandbox/api/frontseat/sandbox/v1alpha1", false},
		{"cli/internal/cmd/root.go", "sandbox/api/frontseat/sandbox/v1alpha1", false},
		{"sdk/go/build.go", "build/daemon/build", false},
		{"sdk/go/build.go", "cli/internal/machine", false},
		{"sdk/go/internal/transport/transport.go", "core/telemetry", false},
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
		{"build/engine/history/history.go", "sdk/go/api/frontseat/build/v1alpha1", true},
		{"build/engine/history/history.go", "build/engine/pluginapi/frontseat/plugin/v1alpha1", false},
		// A test logs in to a workspace through the SDK's tunnel, from its
		// identity alone.
		{"testsupport/testidentity/shell.go", "sdk/go", true},
		// The owner's token is presented as the SDK presents one; the
		// SDK's client is no test support's to reach through it.
		{"stack/testsupport/testowner/testowner.go", "sdk/go/bearer", true},
		{"stack/testsupport/testowner/testowner.go", "sdk/go", false},
		// What the command line keeps is where it, the stack and the test
		// support find it alike (local.Config).
		{"stack/testsupport/testowner/testowner.go", "stack/local", true},
		{"cli/internal/machine/context.go", "stack/local", true},
		{"stack/testsupport/testgrid/testgrid.go", "sdk/go/bearer", false},
		{"stack/testsupport/testgrid/testgrid.go", "sdk/go", false},
		// The command line hands the SDK its telemetry; the SDK takes it
		// as an option.
		{"cli/internal/cmd/root.go", "core/telemetry", true},
		{"cli/internal/cmd/root.go", "sdk/go", true},
		{"cli/internal/cmd/root.go", "build/daemon/build", false},
		// The binary is a thin main over the commands.
		{"cli/cmd/frontseat/main.go", "cli/internal/cmd", true},
		{"cli/cmd/frontseat/main.go", "sdk/go", false},
		// The plugin contract's Go is the daemon's: it is generated into
		// the build's internal packages, and the SDK has none of it.
		{"build/engine/pluginhost/host.go", "build/engine/pluginapi/frontseat/plugin/v1alpha1", true},
		{"build/engine/pluginapi/frontseat/plugin/v1alpha1/plugin.pb.go", "sdk/go/api/frontseat/build/v1alpha1", true},
		{"workspace/box/source/workspace.go", "build/engine/pluginapi/frontseat/plugin/v1alpha1", false},
		{"sdk/go/entity/proto.go", "build/engine/pluginapi/frontseat/plugin/v1alpha1", false},
		// The daemon's internal API is the daemon's to serve and the
		// control plane's to ask, and no client's: the SDK and the
		// command line link none of it.
		{"workspace/box/source/service.go", "common/told/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect", true},
		{"workspace/server/controlplane/controlplane.go", "common/told/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect", true},
		{"cli/internal/cmd/root.go", "common/told/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect", false},
		{"sdk/go/workspace.go", "common/told/sourceapi/frontseat/source/v1alpha1", false},
		{"build/engine/engine/evaluate/evaluate.go", "common/told/sourceapi/frontseat/source/v1alpha1", false},
		// Git is run by one package, for both version control packages,
		// and by nothing else; it knows nothing of either's port.
		{"cli/internal/vcs/git/git.go", "common/git", true},
		{"common/vcs/git/git.go", "common/git", true},
		{"cli/internal/machine/open.go", "common/git", false},
		{"workspace/box/source/workspace.go", "common/git", false},
		{"common/git/git.go", "cli/internal/vcs", false},
		// What leads out of a sandbox is held to being written plainly by
		// one check, which the control plane and the gateway's driver use
		// and the container runtime's does not need.
		{"workspace/server/controlplane/rules.go", "core/plain", true},
		// What the local control plane is known to its gateway by is a
		// secret in files of its own, held as its other secrets are; a
		// daemon keeps none so.
		{"cmd/frontseat-server/main.go", "core/secretfile", true},
		{"cmd/frontseatd/main.go", "core/secretfile", false},
		{"build/engine/exec/fix/fix.go", "build/engine/exec/here", true},
		{"build/engine/exec/publish/publish.go", "build/engine/exec/here", true},
		{"build/engine/exec/publish/publish.go", "build/engine/pluginhost", true},
		{"build/engine/exec/here/here.go", "build/engine/exec/fix", false},
		{"build/engine/exec/here/here.go", "build/engine/engine/plan", false},
		{"build/engine/exec/publish/publish.go", "build/engine/exec/fix", false},
		{"build/engine/exec/publish/publish.go", "build/daemon/build", false},
		{"build/engine/exec/run/run.go", "build/engine/exec/publish", false},
		{"sandbox/driver/openshell/fetch.go", "core/plain", true},
		{"sandbox/testsupport/testcontainer/container.go", "core/plain", false},
		{"core/plain/plain.go", "sandbox", false},
		// What a server keeps to find again is kept by one store: the
		// control plane's workspaces and credentials, the sandbox
		// service's sandboxes and a workspace's manifest; the store knows
		// none of them.
		{"workspace/server/controlplane/credentials.go", "common/record/records", true},
		{"sandbox/kept.go", "common/record/records", true},
		{"workspace/box/source/internal/manifest/manifest.go", "common/record/records", true},
		{"common/record/records/records.go", "core/plain", false},
		{"common/record/records/records.go", "sandbox", false},
		{"cli/internal/cmd/root.go", "common/record/records", false},
		// A process group is ended by one implementation, which a
		// sandbox's refusal of a signal to a group does not stop: the
		// confiner's, which git, a program and a workspace's shell use.
		{"common/git/stop_unix.go", "common/confine", true},
		{"build/engine/exec/program/program.go", "common/confine", true},
		{"workspace/box/source/internal/shell/shell.go", "common/confine", true},
		{"common/git/git.go", "build/engine/exec", false},
		{"workspace/server/backstage/backstage.go", "common/confine", false},
		// Who a sandbox runs as and what of its image it writes are held by
		// one check, the sandbox service's and a test's services' alike.
		{"sandbox/service.go", "core/plain", true},
		{"common/runner/testservice/testservice.go", "core/plain", true},
		// The plugin host shows what a module printed as plain says
		// text from outside is shown; it reads no secret of the server's.
		{"build/engine/pluginhost/host.go", "core/plain", true},
		{"build/engine/pluginhost/host.go", "core/secretfile", false},
		// A server's test calls the API as anyone does, through the
		// generated clients, and reads a stream to its end with the SDK's
		// one reader; the server's code links no client of frontseat's.
		{"build/daemon/build/logs_test.go", "sdk/go", true},
		{"build/daemon/build/runs.go", "sdk/go", false},
		{"workspace/box/source/service_test.go", "sdk/go", true},
		// What the command line prints names an entity as the SDK's one
		// reader and writer of references does, and asks no server.
		{"cli/internal/ui/plain/release.go", "sdk/go/entity", true},
		{"cli/internal/ui/plain/release.go", "sdk/go", false},
		{"workspace/box/source/service.go", "sdk/go", false},
		{"build/daemon/build/logs_test.go", "sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect", true},
		// The specs are what nothing depends on: they use the rest, and
		// no component's code or test imports them back.
		{"build/daemon/build/logs_test.go", "specs/build", false},
		{"stack/testsupport/testgrid/grid.go", "specs/cli", false},
		// A spec starts what it needs in its setup file alone, and acts
		// through the API everywhere else.
		{"specs/cli/support_test.go", "workspace/server/controlplanetest", true},
		{"specs/cli/local_test.go", "workspace/server/controlplane", false},
		{"specs/cli/local_test.go", "workspace/box/source", false},
		{"specs/cli/local_test.go", "stack/local", false},
		// The journey names entities as the SDK takes them.
		{"specs/cli/local_test.go", "sdk/go/entity", true},
		{"specs/cli/local_test.go", "sdk/go/internal/transport", false},
		{"specs/compare/support_test.go", "workspace/server/controlplanetest", true},
		{"specs/compare/support_test.go", "workspace/server/controlplane", false},
		// The control plane is started beside it, by its own test support;
		// the sandbox service knows nothing of what lives in a sandbox.
		{"workspace/server/controlplanetest/controlplanetest.go", "workspace/server/controlplane", true},
		{"workspace/server/controlplanetest/controlplanetest.go", "common/vcs/chosen", true},
		// The version control system is chosen by the program that
		// composes a part, and by what fills a workspace where it is
		// opened with the default; the control plane and the build are
		// given it, and only their tests choose it themselves. What
		// fills a workspace names the system in its tests alone.
		{"cmd/frontseat-server/main.go", "common/vcs/chosen", true},
		{"build/daemon/daemon/daemon.go", "common/vcs/chosen", true},
		{"workspace/box/source/workspace.go", "common/vcs/chosen", true},
		{"workspace/server/controlplane/controlplane_test.go", "common/vcs/chosen", true},
		{"workspace/server/controlplane/controlplane.go", "common/vcs/chosen", false},
		{"build/daemon/build/run_test.go", "common/vcs/chosen", true},
		{"build/daemon/build/runs.go", "common/vcs/chosen", false},
		{"workspace/box/source/workspace_test.go", "common/vcs/git", true},
		{"workspace/box/source/workspace.go", "common/vcs/git", false},
		// The test support at the top reads nothing of the stack, which is
		// above it; the stack's own does.
		{"testsupport/testfiles/tree.go", "stack/local", false},
		{"testsupport/testfiles/tree.go", "core/ports", false},
		{"stack/testsupport/testbox/testbox.go", "stack/local", true},
		{"stack/testsupport/testbox/testbox.go", "core/ports", false},
		{"sandbox/testsupport/testcontainer/container.go", "stack/local", false},
		{"sandbox/sandbox.go", "workspace/server/controlplane", false},
		{"sandbox/sandbox_test.go", "workspace/box/source", false},
		{"sandbox/sandbox_test.go", "workspace/server/controlplanetest", false},
		{"specs/compare/compare_test.go", "workspace/box/source", false},
		{"specs/compare/compare_test.go", "stack/local", false},
		// Test support uses nothing it is there to test.
		{"stack/testsupport/testgrid/grid.go", "sandbox", false},
		{"stack/testsupport/testgrid/grid.go", "stack/local", true},
		{"stack/testsupport/testgrid/grid.go", "workspace/server/controlplane", false},
		{"stack/testsupport/testgrid/grid.go", "workspace/server/controlplanetest", false},
		{"stack/testsupport/testgrid/grid.go", "specs/buildtest", false},
		{"specs/buildtest/buildtest.go", "workspace/server/controlplanetest", false},
		// What starts a control plane for a test ends what one it stopped
		// left in the gateway, through the sandbox service; other test
		// support makes no sandboxes there.
		{"workspace/server/controlplanetest/left.go", "sandbox", true},
		// What gathers every page of another's List is the server's, for
		// the runner and a control plane's test support alike; a plugin has no
		// List to read.
		{"workspace/server/controlplanetest/left.go", "core/page", true},
		{"grid/runner/runner.go", "core/page", true},
		{"build/engine/pluginhost/host.go", "core/page", false},
		// What a test is told of its repository is said by the host and
		// read by the test support, and a test of the host holds the two
		// to one name; the host itself imports no test support.
		{"build/engine/exec/stage/source_test.go", "testsupport/testmodule", true},
		{"build/engine/exec/stage/stage.go", "testsupport/testmodule", false},
		// A program a person starts at a checkout's root finds it as the
		// stack's program does (local.Checkout), and nothing of the local
		// stack beyond it is the gate's to reach for.
		{"specs/cmd/prepare-modules/main.go", "stack/local", true},
		{"specs/cmd/prepare-modules/main.go", "stack", false},
		// The gate asks the stack where its ports are said, before the
		// tests that need it; what prepares the plugins asks it nothing.
		{"specs/cmd/gate/main.go", "core/ports", true},
		{"specs/cmd/prepare-modules/main.go", "core/ports", false},
		{"stack/testsupport/testgrid/testgrid.go", "sandbox", false},
		{"sandbox/testsupport/drivertest/drivertest_test.go", "sandbox/testsupport/drivertest", true},
		// The container driver is the sandbox service's to name, and its
		// own tests' and the gateway's driver's tests'; nothing else's.
		{"sandbox/sandbox.go", "core/image", true},
		{"sandbox/sandbox.go", "sandbox/testsupport/testcontainer", false},
		{"sandbox/driver/openshell/openshell.go", "core/image", false},
		{"sandbox/driver/openshell/openshell_test.go", "core/image", true},
		{"grid/runner/runner.go", "sandbox/testsupport/testcontainer", false},
		{"grid/runner/attempt_test.go", "sandbox/testsupport/testcontainer", true},
		{"grid/runner/pool.go", "sandbox/driver/openshell", true},
		{"grid/runner/pool.go", "workspace/server/controlplane", false},
		{"sandbox/testsupport/testcontainer/container.go", "sandbox/driver", true},
		{"sandbox/testsupport/testcontainer/container.go", "sandbox", false},
		// The gate programs read their own packages and the test support.
		{"specs/cmd/gate/main.go", "specs/internal/gatecommand", true},
		{"specs/cmd/gate/main.go", "build/daemon/build", false},
		{"specs/sandbox/sandbox_test.go", "sandbox/testsupport/drivertest", false},
		{"specs/sandbox/support_test.go", "sandbox/testsupport/drivertest", true},
		{"stack/testsupport/testbox/testbox.go", "testsupport/testrequired", true},
		{"stack/testsupport/testbox/testbox.go", "stack/box", false},
		{"specs/cmd/gate/main.go", "stack/box", true},
		{"specs/local/box_test.go", "stack/box", true},
		{"specs/cli/local_test.go", "stack/box", false},
		{"specs/sandbox/support_test.go", "stack/box", false},
		{"specs/local/box_test.go", "stack", false},
		{"stack/testsupport/testbox/testbox.go", "cmd/frontseat-server", false},
		{"stack/testsupport/testbox/stack.go", "stack", true},
		{"stack/testsupport/testgrid/testgrid.go", "stack", false},
		{"stack/testsupport/testgrid/testgrid.go", "common/confine", true},
		{"stack/testsupport/testgrid/testgrid.go", "build/engine/exec/tools", false},
		// The box is built from what the stack says it is laid on, and
		// from nothing of what it holds.
		{"stack/box/box.go", "stack/local", true},
		// The stack's container is asked again until it comes to
		// something by one loop of the local module's, which nothing of
		// the server's reaches.
		{"stack/box/box.go", "stack/internal/poll", true},
		{"stack/stack.go", "stack/internal/poll", true},
		{"stack/internal/poll/poll.go", "stack/local", false},
		// The stack's Podman is asked one way (local.Podman), by the
		// stack, the test support and the spec of the stack's container.
		{"specs/local/container_test.go", "stack/local", true},
		{"specs/local/container_test.go", "stack", false},
		// The server asks again until what it asks is done by one loop;
		// a package that waits on nothing does not import it.
		{"build/daemon/build/build.go", "core/poll", true},
		{"workspace/server/controlplane/controlplane.go", "core/poll", true},
		{"grid/runner/runner.go", "core/poll", true},
		{"sandbox/service.go", "core/poll", true},
		{"sandbox/driver/openshell/openshell.go", "core/poll", true},
		{"stack/testsupport/testgrid/door.go", "core/poll", true},
		{"sandbox/testsupport/drivertest/conformance.go", "core/poll", true},
		{"build/engine/pluginhost/host.go", "core/poll", false},
		{"stack/testsupport/testbox/testbox.go", "core/poll", false},
		{"core/poll/poll.go", "build/daemon/build", false},
		// A value of the API's enums is said by the SDK's one function,
		// which the command line and the server import without the
		// SDK's client; it imports nothing of the SDK's client.
		{"build/daemon/build/runs.go", "sdk/go/enum", true},
		{"cli/internal/ui/plain/plain.go", "sdk/go/enum", true},
		{"cli/internal/cmd/list.go", "sdk/go/enum", true},
		{"sdk/go/workspace.go", "sdk/go/enum", true},
		{"sdk/go/enum/enum.go", "sdk/go", false},
		{"workspace/box/source/service.go", "sdk/go/enum", false},
		{"stack/box/box.go", "sandbox", false},
		{"stack/box/box.go", "build/daemon/build", false},
		{"stack/stack.go", "stack/box", true},
		{"stack/stack.go", "workspace/server/controlplane", false},
		{"stack/container.go", "core/ports", true},
		{"stack/container.go", "core/httpserver", false},
		// The local stack's program is the control plane told what the
		// stack is; the control plane is told nothing of the stack.
		{"cmd/frontseat-server/main.go", "workspace/server/controlplane", true},
		{"cmd/frontseat-server/main.go", "stack/local", true},
		{"cmd/frontseat-stack/main.go", "stack", true},
		{"cmd/frontseat-server/main.go", "sandbox", false},
		{"workspace/server/controlplane/controlplane.go", "stack/local", false},
		{"stack/local/local.go", "workspace/server/controlplane", false},
		// The ports are said once, and read by what opens, serves or asks
		// them, which reads nothing else of the stack for them.
		{"workspace/server/controlplane/rules.go", "core/ports", true},
		{"build/daemon/daemon/daemon.go", "core/ports", true},
		{"build/daemon/daemon/daemon.go", "stack/local", false},
		{"cli/internal/machine/token.go", "core/ports", true},
		{"cli/internal/machine/token.go", "stack", false},
		{"cli/internal/machine/token.go", "sdk/go/bearer", true},
		{"cli/internal/machine/token.go", "sdk/go/internal/transport", false},
		{"sandbox/testsupport/testcontainer/container_test.go", "core/ports", true},
		{"sandbox/testsupport/testcontainer/container_test.go", "stack", false},
		{"stack/local/stack.go", "core/ports", true},
		{"core/ports/ports.go", "stack/local", false},
		{"stack/local/stack.go", "core/telemetry", false},
		// The grid's runner is a thin main over the runner, which alone
		// serves the sandbox API over the container runtime.
		{"cmd/frontseat-runner/main.go", "grid/runner", true},
		{"cmd/frontseat-runner/main.go", "sandbox", false},
		{"grid/runner/runner.go", "sandbox", true},
		{"grid/runner/runner.go", "workspace/server/controlplane", false},
		// The confiner is confine alone.
		{"cmd/frontseat-confine/main.go", "common/confine", true},
		{"cmd/frontseat-confine/main.go", "grid/runner", false},
		// The shared libraries moved from the server are each one thing.
		{"common/runner/testservice/testservice.go", "core/image", true},
		{"common/runner/testservice/testservice.go", "build/engine/model", false},
		{"core/plain/plain.go", "common/runner/testservice", false},
		{"sdk/go/build.go", "sdk/go/testdata/bridge", false},
		{"build/engine/engine/catalog/catalog.go", "build/engine/pluginhost", false},
		{"build/engine/engine/plan/generated.go", "build/engine/pattern", true},
		{"build/engine/engine/graph/project.go", "build/engine/pattern", true},
		{"build/engine/engine/plan/generated.go", "build/engine/pluginhost", false},
		{"build/engine/exec/executor.go", "build/engine/engine/plan", false},
		{"build/daemon/build/runs.go", "build/engine/exec/schedule", false},
		{"sdk/go/build_test.go", "specs/buildtest", false},
		{"core/telemetry/trace.go", "build/daemon/build", false},
		{"stack/testsupport/testgrid/grid.go", "build/daemon/build", false},
		{"build/daemon/build/service.go", "workspace/box/source", false},
		{"build/daemon/build/graph_service.go", "build/engine/wire", true},
		{"sdk/go/build.go", "build/engine/wire", false},
		{"workspace/box/source/service.go", "build/engine/wire", false},
		{"build/daemon/build/service.go", "workspace/box/source/internal/populate", false},
		{"workspace/box/source/service.go", "build/daemon/build", false},
		{"sandbox/service.go", "build/daemon/build", false},
		{"build/engine/engine/graph/graph.go", "build/engine/engine/plan", false},
		{"build/engine/engine/graph/graph_test.go", "build/engine/engine/plan", false},
		{"build/engine/engine/evaluate/evaluate.go", "build/engine/exec", false},
		{"build/engine/engine/release/plan.go", "common/vcs", true},
		{"build/engine/engine/release/plan.go", "build/engine/pluginhost", false},
		{"build/engine/engine/release/plan.go", "workspace/box/source", false},
		{"build/engine/engine/evaluate/evaluate.go", "build/engine/pluginhost", true},
		{"build/engine/exec/run/run.go", "build/daemon/build", false},
		{"specs/build/builds_test.go", "sdk/go", true},
		{"specs/build/builds_test.go", "specs/buildtest", false},
		{"specs/build/support_test.go", "specs/buildtest", true},
		{"build/daemon/daemon/daemon.go", "build/daemon/build", true},
		{"build/daemon/daemon/daemon.go", "build/engine/pluginhost", false},
		// The daemon's program is a thin main over the daemon.
		{"cmd/frontseatd/main.go", "build/daemon/daemon", true},
		{"cmd/frontseatd/main.go", "build/daemon/build", false},
		{"build/daemon/build/service.go", "build/engine/pluginhost/conformance", false},
		{"build/engine/pluginhost/host_test.go", "build/engine/pluginhost/conformance", true},
		{"build/engine/engine/evaluate/catalog_rewrite_test.go", "build/engine/pluginhost/conformance", true},
		{"build/engine/engine/evaluate/evaluate.go", "build/engine/pluginhost/conformance", false},
		{"build/engine/engine/config/config_test.go", "build/engine/pluginhost/conformance", false},
		{"build/engine/engine/config/config_test.go", "build/engine/pluginhost/pluginhosttest", true},
		{"build/engine/pluginhost/host_test.go", "build/engine/pluginhost/pluginhosttest", true},
		{"build/engine/pluginhost/host.go", "build/engine/pluginhost/pluginhosttest", false},
		{"build/engine/engine/config/config.go", "build/engine/pluginhost/pluginhosttest", false},
		// What reads the catalog files finds them by the name the catalog
		// says once, and knows nothing of how a graph is evaluated.
		{"build/engine/engine/config/config.go", "build/engine/engine/catalog", true},
		{"build/engine/engine/config/config.go", "build/engine/engine/evaluate", false},
		{"workspace/box/source/service_test.go", "build/engine/pluginhost/pluginhosttest", false},
		// A driver's test is given a running sandbox as drivertest makes
		// one; a driver is not.
		{"sandbox/driver/openshell/openshell_test.go", "sandbox/testsupport/drivertest", true},
		{"sandbox/testsupport/testcontainer/container_test.go", "sandbox/testsupport/drivertest", true},
		{"sandbox/driver/openshell/openshell.go", "sandbox/testsupport/drivertest", false},
		{"workspace/box/source/service_test.go", "sandbox/testsupport/drivertest", false},
		// A test of the control plane or of the build's service reads a
		// stream with the SDK's one public reader, not its transport,
		// which is the SDK's alone.
		{"workspace/server/controlplane/controlplane_test.go", "sdk/go/internal/transport", false},
		// What fills a workspace is the daemon's in a box, beneath no part
		// of the control plane, its tests neither.
		{"workspace/server/controlplane/controlplane_test.go", "workspace/box/source", false},
		{"workspace/server/controlplane/rules.go", "workspace/box/source", false},
		{"build/daemon/build/history_test.go", "sdk/go/internal/transport", false},
		{"workspace/server/controlplane/controlplane_test.go", "sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect", true},
		// The architecture test is a spec: it reads the catalog's
		// references with the one parser of them, and starts nothing.
		{"specs/architecture/catalog_test.go", "sdk/go/entity", true},
		{"specs/architecture/catalog_test.go", "build/engine/model", false},
		{"stack/testsupport/testgrid/testgrid.go", "sdk/go/entity", false},
		{"build/daemon/build/retained_session_test.go", "build/engine/engine/catalog/catalogtest", true},
		{"build/engine/engine/evaluate/retained_projection_test.go", "build/engine/engine/catalog/catalogtest", true},
		{"build/engine/engine/catalog/catalog.go", "build/engine/engine/catalog/catalogtest", false},
		{"build/daemon/build/graph_service.go", "build/engine/engine/catalog/catalogtest", false},
		{"workspace/box/source/service_test.go", "build/engine/engine/catalog/catalogtest", false},
		{"build/daemon/build/run_test.go", "build/engine/exec/tools/toolstest", true},
		{"build/engine/exec/tools/budget_test.go", "build/engine/exec/tools/toolstest", true},
		{"build/engine/exec/tools/tools.go", "build/engine/exec/tools/toolstest", false},
		{"build/daemon/build/runs.go", "build/engine/exec/tools/toolstest", false},
		{"workspace/box/source/service_test.go", "build/engine/exec/tools/toolstest", false},
		{"build/engine/exec/schedule/schedule_test.go", "build/engine/engine/plan/plantest", true},
		{"build/engine/engine/plan/needs_test.go", "build/engine/engine/plan/plantest", true},
		{"build/engine/exec/schedule/schedule.go", "build/engine/engine/plan/plantest", false},
		{"workspace/box/source/service_test.go", "build/engine/engine/plan/plantest", false},
		{"build/daemon/build/service.go", "stack/testsupport/testgrid", false},
		{"workspace/server/controlplane/ending_test.go", "sandbox/testsupport/drivertest", true},
		{"workspace/server/controlplane/controlplane.go", "sandbox/testsupport/drivertest", false},
		{"workspace/server/controlplane/ending_test.go", "sandbox/driver", false},
		{"workspace/server/controlplane/ending_test.go", "sandbox/driver/openshell", false},
		{"sandbox/service_test.go", "sandbox/testsupport/drivertest", true},
		// How a grid is asked is said once, to the daemon's two dials, by
		// the daemon that was told; nothing that stages or runs an action
		// knows of it.
		{"build/engine/exec/reapi/reapi.go", "build/engine/exec/access", true},
		{"build/engine/exec/download/download.go", "build/engine/exec/access", true},
		{"build/daemon/build/build.go", "build/engine/exec/access", true},
		{"build/engine/exec/stage/stage.go", "build/engine/exec/access", false},
		{"build/engine/exec/tools/tools.go", "build/engine/exec/access", false},
		{"build/engine/exec/access/access.go", "build/engine/exec", false},
		{"build/daemon/daemon/daemon.go", "build/engine/exec/access", false},
		// Evaluation writes the source groups of the actions it plans.
		{"build/engine/engine/evaluate/evaluate.go", "build/engine/pattern", true},
		{"build/engine/exec/schedule/schedule.go", "build/engine/pattern", false},
		{"build/daemon/build/runs.go", "build/engine/pattern", false},
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
			file, err := parser.ParseFile(token.NewFileSet(), "entry.go", "package entry\nimport model \"github.com/frontseat-dev/frontseat/build/engine/model\"\n"+tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(publicLeaks([]*ast.File{file})) > 0; got != tc.leaks {
				t.Fatalf("leaks=%v, want %v", got, tc.leaks)
			}
		})
	}
}
