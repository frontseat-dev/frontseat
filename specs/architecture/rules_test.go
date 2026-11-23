// Package architecture holds module and feature boundaries to an explicit policy.
package architecture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/specs/internal/gatecommand"
	"github.com/frontseat-dev/frontseat/specs/internal/generated"
)

const module = "github.com/frontseat-dev/frontseat"

// An exact package is spelled without /**. Subtrees are deliberately explicit.
type policy struct {
	owner   string
	imports []string
}

var policies = []policy{
	// The Go SDK: one client (sdk/go, package frontseat), over one
	// transport; the generated API beneath it; entity references. It
	// imports nothing of this repository but itself
	// (TestTheGoSDKRequiresNothingOfThisRepository).
	{"sdk/go/api/**", []string{"sdk/go/api/**"}},
	{"sdk/go/entity", []string{"sdk/go/api/**"}},
	{"sdk/go", []string{"sdk/go/api/**", "sdk/go/entity", "sdk/go/enum", "sdk/go/internal/transport", "sdk/go/bearer"}},
	// A value of the API's enums said by its name or in words: one way,
	// for the SDK, the command line and the server, importing nothing of
	// the SDK beyond the API its tests say values of.
	{"sdk/go/enum", []string{"sdk/go/api/**", "sdk/go/enum"}},
	// A token presented to its origin alone, with no redirect followed:
	// the SDK's transport, and the server's Backstage client, present one
	// so.
	{"sdk/go/bearer", []string{}},
	// Its tests call a generated client over it.
	{"sdk/go/internal/transport", []string{"sdk/go/api/**", "sdk/go/bearer"}},
	// What the command line keeps and reads on this machine; what it asks
	// of a version control system about a checkout, and git, which
	// answers it.
	{"cli/internal/machine", []string{"sdk/go/api/**", "sdk/go", "sdk/go/bearer", "cli/internal/vcs", "cli/internal/vcs/git", "local", "local/ports"}},
	{"cli/internal/vcs", []string{}},
	{"cli/internal/vcs/git", []string{"cli/internal/vcs", "server/git"}},
	{"cli/internal/ui/**", []string{"sdk/go/api/**", "sdk/go/entity", "sdk/go/enum", "cli/internal/ui/**"}},
	// The binary is a thin main over the commands, which are the command
	// line's own (cli/internal/cmd).
	{"cli/cmd/frontseat", []string{"cli/internal/cmd", "server/telemetry"}},
	// The commands are flags and printing over the SDK's one client, which
	// they hand the command's telemetry to instrument what they ask.
	{"cli/internal/cmd", []string{"sdk/go/api/**", "sdk/go/entity", "sdk/go/enum", "sdk/go", "cli/internal/machine", "cli/internal/ui/**", "server/telemetry"}},
	{"server/telemetry", []string{}},
	// Git as frontseat runs it, hardened, and how it writes a remote: one
	// implementation for the client's adapter and the daemon's.
	{"server/git", []string{"server/internal/confine"}},
	// Test support reads what the local stack is from its files (local),
	// never through the stack's control plane (frontseat-local), which
	// links what the test support tests, and never builds the box, which
	// is built from the source of other packages: the gate builds it
	// before the tests (fromSource).
	{"server/testsupport/**", []string{"server/testsupport/**", "local", "local/ports"}},
	// The stack's one container is started for a test as the stack starts
	// it: by its own flags and profile.
	{"server/testsupport/testbox", []string{"server/testsupport/**", "local", "local/ports", "local/stack"}},
	// The grid's runner runs only a confined command: what another tool
	// sends it begins as the daemon's do, which the confiner says.
	{"server/testsupport/testgrid", []string{"server/testsupport/**", "local", "local/ports", "server/internal/confine", "server/internal/poll"}},
	// A test logs in to a workspace's shell with its own key, through the
	// SDK's tunnel, presenting the token its control plane knows it by.
	{"server/testsupport/testidentity", []string{"sdk/go"}},
	// The owner's token is a person's own: a test presents it as the
	// SDK does, to its origin alone and following no redirect.
	{"server/testsupport/testowner", []string{"local", "local/ports", "sdk/go/bearer"}},
	// The sandbox API is no part of the SDK: a test asks it, and opens a
	// tunnel to a sandbox's port, through this.
	{"server/testsupport/testsandbox", []string{"sdk/go/api/**", "server/sandbox/api/**", "server/testsupport/testwire"}},
	// What the local stack is, read from its files: one reader, for the
	// stack's control plane, the box and the test support, which links
	// nothing of the server.
	{"local", []string{"local/ports"}},
	// The ports frontseat's programs are reached on, each said once: the
	// daemon's defaults, the control plane, the command line, the stack's
	// own control plane and the test support all read them here.
	{"local/ports", []string{}},
	// The box's one build, from what the stack says it is laid on; and
	// the stack brought up and down, running the box it builds, its one
	// container publishing the ports where they are said.
	{"local/box", []string{"local", "local/internal/poll"}},
	{"local/stack", []string{"local", "local/box", "local/ports", "local/internal/poll"}},
	// Asking again until the stack's container comes to something: one
	// loop, for the box's build and the stack's program.
	{"local/internal/poll", []string{}},
	{"server/internal/httpserver", []string{}},
	{"server/internal/image", []string{}},
	{"server/internal/confine", []string{}},
	// Asking again until what is asked is done: one loop, for the
	// daemon's dial, the control plane, the sandbox service and its
	// gateway's driver, the runner and the test support that waits so.
	{"server/internal/poll", []string{}},
	// Whether what leads out of a sandbox is written plainly: one reader
	// of a host, an origin, a path and a program, for the control plane
	// and the gateway's driver.
	{"server/internal/plain", []string{}},
	// A directory of records, each written whole and read strictly: one
	// store, for the workspaces, the sandboxes, the credentials and a
	// workspace's manifest.
	{"server/internal/records", []string{}},
	// A secret kept in a file that is the server's alone: the owner's
	// token, and the token presented to Backstage.
	{"server/internal/secretfile", []string{}},
	// The Backstage catalog the control plane resolves entities through,
	// asked by one client held to its origin; its tests ask a fake.
	{"server/internal/backstage", []string{"sdk/go/entity", "sdk/go/bearer", "server/internal/plain", "server/internal/secretfile", "server/internal/source/record", "server/testsupport/backstagetest"}},
	// What a test service is held to: one check, for the daemon and the grid's runner.
	{"server/internal/testservice", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/image", "server/internal/plain"}},
	// The build reads its directory (server/internal/directory) and
	// nothing of what fills it; its tests alone start what does, beside
	// it, as the daemon's program has it
	// (TestTheBuildReadsItsDirectoryAndNothingOfWhatFillsIt).
	{"server/internal/build", []string{"sdk/go/api/**", "sdk/go/entity", "sdk/go/enum", "server/internal/poll", "server/internal/pluginapi/**", "server/internal/exec/publish", "server/internal/exec/tools", "server/telemetry", "server/internal/build", "server/internal/source/told/toldtest", "server/internal/engine/evaluate", "server/internal/engine/graph", "server/internal/engine/plan", "server/internal/engine/release", "server/internal/engine/source", "server/internal/exec", "server/internal/exec/access", "server/internal/exec/download", "server/internal/exec/program", "server/internal/exec/run", "server/internal/history", "server/internal/model", "server/internal/wire", "server/internal/pluginhost", "server/internal/source", "server/internal/source/record", "server/internal/source/vcs", "server/internal/directory", "server/internal/manifest", "server/internal/source/told", "server/internal/sourceapi/**", "server/internal/page", "server/internal/refusal"}},
	// A directory as a build reads it: the workspace's manifest in it,
	// and its repositories' history through the version control port.
	{"server/internal/directory", []string{"server/internal/directory", "server/internal/manifest", "server/internal/source/record", "server/internal/source/vcs"}},
	// The workspace's manifest, which what fills a directory writes and
	// a build reads.
	{"server/internal/manifest", []string{"server/internal/manifest", "server/internal/records", "server/internal/source/record", "sdk/go/entity", "server/testsupport/testfiles"}},
	{"server/internal/history", []string{"sdk/go/api/**"}},
	// The plugin contract's Go, generated for the daemon alone.
	{"server/internal/pluginapi/**", []string{"sdk/go/api/**"}},
	// What a worker's runner is told to start for a test, generated
	// from the contracts (api/buf.gen.runner.yaml): no API, the server's
	// own.
	{"server/internal/runnerapi/**", []string{"sdk/go/api/**"}},
	// The daemon's internal API, generated from the contracts
	// (api/buf.gen.source.yaml): the daemon serves it and the control
	// plane alone asks it, through the one translation between it and
	// the workspace API (server/internal/source/told). Nothing a client
	// links names it.
	{"server/internal/sourceapi/**", []string{"sdk/go/api/**", "server/internal/sourceapi/**"}},
	// How a List is cut into pages, and how a service refuses by name:
	// shared by the services, and importing nothing of this repository.
	{"server/internal/page", nil},
	{"server/internal/refusal", nil},
	{"server/internal/model", []string{}},
	{"server/internal/wire", []string{"sdk/go/api/**", "server/internal/pluginapi/**", "server/internal/model"}},
	{"server/internal/pattern", []string{}},
	{"server/internal/pins", []string{}},
	{"server/internal/pluginhost", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/pluginapi/**", "server/telemetry", "server/internal/pattern", "server/internal/plain", "server/internal/testservice"}},
	{"server/internal/pluginhost/conformance", []string{"sdk/go/api/**", "server/internal/pluginapi/**", "server/internal/pluginhost"}},
	{"server/internal/engine/evaluate", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/pluginapi/**", "server/telemetry", "server/internal/engine/catalog", "server/internal/engine/config", "server/internal/engine/evaluate", "server/internal/engine/graph", "server/internal/engine/internal/yamlnode", "server/internal/engine/plan", "server/internal/engine/source", "server/internal/model", "server/internal/wire", "server/internal/pins", "server/internal/pluginhost", "server/internal/pattern", "server/internal/source/record", "server/internal/testservice", "server/internal/runnerapi/**"}},
	{"server/internal/engine/catalog", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/pluginapi/**"}},
	{"server/internal/engine/release", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/pluginapi/**", "server/internal/engine/release", "server/internal/engine/config", "server/internal/model", "server/internal/source/vcs"}},
	{"server/internal/engine/graph", []string{"server/internal/pattern", "sdk/go/entity", "server/internal/engine/graph", "server/internal/engine/config", "server/internal/model", "server/internal/source/record"}},
	{"server/internal/engine/plan", []string{"server/internal/pattern", "sdk/go/api/**", "sdk/go/entity", "server/internal/pluginapi/**", "server/internal/engine/plan", "server/internal/engine/graph", "server/internal/model", "server/internal/source/record"}},
	{"server/internal/engine/config", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/pluginapi/**", "server/internal/model", "server/internal/engine/catalog", "server/internal/engine/internal/yamlnode", "server/internal/testservice"}},
	{"server/internal/engine/source", []string{"server/internal/engine/source", "server/internal/pattern"}},
	{"server/internal/engine/internal/yamlnode", []string{}},
	{"server/internal/exec", []string{}},
	// How a grid that asks who a daemon is, is asked: one answer, for the
	// grid's dial and its asset service's, and for nothing else.
	{"server/internal/exec/access", []string{}},
	{"server/internal/exec/reapi", []string{"server/telemetry", "server/internal/exec", "server/internal/exec/access", "server/internal/confine"}},
	{"server/internal/exec/download", []string{"server/telemetry", "server/internal/exec", "server/internal/exec/access"}},
	{"server/internal/exec/tools", []string{"server/telemetry", "server/internal/exec", "server/internal/pins", "server/internal/testservice", "server/internal/confine"}},
	{"server/internal/exec/tools/toolstest", []string{"server/internal/exec"}},
	{"server/internal/pluginhost/pluginhosttest", []string{"sdk/go/api/**", "server/internal/pluginapi/**", "server/internal/pattern"}},
	{"server/internal/engine/catalog/catalogtest", []string{}},
	{"server/internal/engine/source/sourcetest", []string{"server/internal/engine/source"}},
	{"server/internal/exec/reapi/reapitest", []string{"server/internal/exec/reapi"}},
	{"server/internal/engine/plan/plantest", []string{"server/internal/engine/plan", "server/internal/model"}},
	{"server/internal/exec/stage", []string{"server/telemetry", "server/internal/exec", "server/internal/exec/download", "server/internal/exec/tools", "server/internal/engine/plan", "server/internal/engine/source", "sdk/go/api/**", "server/internal/pluginapi/**", "server/internal/confine", "server/internal/image", "server/internal/testservice", "server/internal/runnerapi/**"}},
	{"server/internal/exec/schedule", []string{"server/telemetry", "server/internal/exec", "server/internal/engine/plan", "server/internal/model"}},
	{"server/internal/exec/here", []string{"server/internal/exec", "server/internal/exec/tools", "server/internal/engine/source", "server/internal/pluginapi/**"}},
	{"server/internal/exec/publish", []string{"server/internal/confine", "server/internal/exec", "server/internal/exec/here", "server/internal/exec/tools", "server/internal/engine/plan", "server/internal/engine/source", "server/internal/plain", "server/internal/pluginhost"}},
	{"server/internal/exec/fix", []string{"server/telemetry", "server/internal/exec", "server/internal/exec/here", "server/internal/exec/tools", "server/internal/engine/plan", "server/internal/engine/source", "server/internal/model", "server/internal/pattern", "server/internal/pins"}},
	{"server/internal/exec/program", []string{"sdk/go/api/**", "server/internal/pluginapi/**", "server/internal/exec", "server/internal/exec/tools", "server/internal/engine/plan", "server/internal/model", "server/internal/confine"}},
	{"server/internal/exec/run", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/pluginapi/**", "server/telemetry", "server/internal/exec", "server/internal/exec/download", "server/internal/exec/tools", "server/internal/exec/fix", "server/internal/exec/stage", "server/internal/exec/schedule", "server/internal/engine/plan", "server/internal/engine/source", "server/internal/model", "server/internal/runnerapi/**"}},
	{"server/internal/source/**", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/source/**", "server/internal/refusal", "server/internal/sourceapi/**", "server/internal/confine", "server/internal/records", "server/internal/manifest"}},
	{"server/internal/source/record", []string{"sdk/go/entity", "server/internal/source/record"}},
	// A daemon told, for a test, what the control plane would tell it:
	// its entities resolved through a fake Backstage.
	{"server/internal/source/told/toldtest", []string{"sdk/go/api/**", "sdk/go/entity", "server/internal/backstage", "server/internal/source/told", "server/internal/sourceapi/**", "server/testsupport/backstagetest"}},
	{"server/internal/source/vcs", []string{"server/internal/source/vcs"}},
	{"server/internal/source/internal/vcs/git", []string{"server/internal/source/vcs", "server/git"}},
	// A sandbox is the isolation and knows nothing of what lives in one.
	// A driver's tests, and the service's, are held to the conformance
	// and run over the driver held in memory (drivertest).
	{"server/sandbox/**", []string{"sdk/go/api/**", "server/sandbox/**", "server/internal/image", "server/testsupport/drivertest", "server/internal/page", "server/internal/refusal", "server/internal/plain", "server/internal/records", "server/internal/poll"}},
	{"server/sandbox/driver/openshell", []string{"sdk/go/api/**", "server/sandbox/**", "server/testsupport/drivertest", "server/internal/plain", "server/internal/poll", "local/ports"}},
	// A driver of the container runtime, for tests alone: what an image
	// starts is asked through the image package, as the gateway's way
	// asks it. Its tests start a box and ask its daemon.
	{"server/testsupport/testcontainer", []string{"server/sandbox/driver", "server/internal/image", "server/testsupport/**", "local", "local/ports", "sdk/go/api/**", "server/internal/source/told", "server/internal/sourceapi/**"}},
	// The control plane keeps workspaces over the sandboxes they live in,
	// and asks the version control system through its port (vcs.Fetching)
	// what reaching a repository takes. It tells a workspace's daemon
	// what to hold through the daemon's source API, and reads back what
	// it holds.
	// Its tests hold it over the real sandbox service, over the driver
	// that holds sandboxes in memory (drivertest.Memory): that driver is
	// for tests, and is refused to what is none. It opens a box's ports
	// (local/ports), and is told nothing of the stack.
	// It resolves entities through the Backstage its operator gives it
	// (server/internal/backstage), held over a fake one in its tests.
	{"server/internal/controlplane/**", []string{"sdk/go/api/**", "server/sandbox/api/**", "sdk/go/entity", "server/internal/controlplane/**", "server/sandbox", "server/internal/backstage", "server/internal/secretfile", "server/internal/source/record", "server/internal/source/vcs", "server/internal/source/told", "server/internal/sourceapi/**", "server/testsupport/drivertest", "server/testsupport/backstagetest", "server/internal/plain", "local/ports", "sdk/go", "server/internal/page", "server/internal/refusal", "server/internal/records", "server/internal/poll"}},
	// A test starts a daemon as frontseatd does, and a control plane as
	// frontseat-local does, each with the test support beside it.
	{"server/testsupport/buildtest", []string{"server/internal/build", "server/telemetry", "server/testsupport/**", "sdk/go/api/**", "server/internal/source", "server/internal/source/told", "server/internal/source/told/toldtest", "server/internal/sourceapi/**"}},
	// A fake Backstage, of entities written as catalog files write them.
	{"server/testsupport/backstagetest", []string{"sdk/go/entity"}},
	// What any sandbox driver is held to, and the driver held in memory.
	{"server/testsupport/drivertest", []string{"server/sandbox/driver", "server/testsupport/drivertest", "server/internal/poll"}},
	// It serves the sandbox service beneath the plane to the test alone,
	// and says how the plane's owner is known as the SDK's clients take it.
	{"server/testsupport/controlplanetest", []string{"server/sandbox/api/**", "server/internal/controlplane", "server/internal/page", "server/internal/backstage", "server/internal/source", "server/sandbox", "server/testsupport/**", "sdk/go/api/**", "sdk/go"}},
	// The daemon: a thin main over the package that is it, which reads
	// its flags and serves the build API over what it is told.
	{"server/cmd/frontseatd", []string{"server/internal/daemon"}},
	{"server/internal/daemon", []string{"server/internal/build", "server/internal/source", "server/internal/httpserver", "server/telemetry", "local/ports"}},
	// The local stack's control plane: the control plane, told what the
	// stack is by the stack's own package.
	{"server/cmd/frontseat-local", []string{"server/internal/controlplane", "server/internal/backstage", "server/internal/source", "server/internal/httpserver", "server/internal/secretfile", "server/telemetry", "local", "local/ports", "local/stack", "local/box"}},
	{"server/cmd/frontseat-action", []string{"server/internal/confine"}},
	// The grid's runner: a thin main over the runner, which serves the
	// sandbox API over the container runtime to itself.
	{"server/cmd/frontseat-runner", []string{"server/internal/runner"}},
	// The runner keeps the grid's pool of runner sandboxes with the
	// OpenShell driver itself: their bound directories and private policy
	// are the driver's to say, and no client's of the sandbox API.
	{"server/internal/runner", []string{"server/sandbox/api/**", "server/internal/page", "server/sandbox", "server/sandbox/driver", "server/sandbox/driver/openshell", "server/internal/httpserver", "server/internal/image", "sdk/go/api/**", "sdk/go/entity", "server/internal/testservice", "server/internal/confine", "sdk/go", "server/internal/runnerapi/**", "server/internal/poll"}},
	// The box's one build, asked by the spec that holds a box to what it
	// was built with; no other spec builds one. That spec asks the
	// stack's Podman as the stack does (local.Podman).
	{"specs/cli", []string{"sdk/go", "sdk/go/api/**", "server/sandbox/api/**", "sdk/go/entity", "server/testsupport/**", "local/ports"}},
	{"specs/local", []string{"server/testsupport/**", "local", "local/ports", "local/box"}},
	{"specs/compare", []string{"sdk/go", "sdk/go/api/**", "server/sandbox/api/**", "server/testsupport/**"}},
	{"specs/**", []string{"sdk/go", "sdk/go/api/**", "server/sandbox/api/**", "sdk/go/entity", "server/testsupport/**", "server/telemetry", "specs/internal/**", "local/ports"}},
	// The gate programs: what runs the specs and every other test, and
	// builds the box before the tests that need it; what reads a gate's
	// command, and what the contracts' generators are held to.
	{"specs/cmd/**", []string{"specs/internal/**", "server/testsupport/**", "local"}},
	// The gate builds the box by its one build, which no test does, and
	// asks that the stack answers where local/ports says, before the
	// tests that need it.
	{"specs/cmd/gate", []string{"specs/internal/**", "server/testsupport/**", "local", "local/box", "local/ports"}},
	{"specs/internal/**", []string{"specs/internal/**"}},
}

var forTests = []string{"server/testsupport", "specs", "server/internal/source/told/toldtest", "server/internal/pluginhost/conformance", "server/internal/exec/tools/toolstest", "server/internal/engine/source/sourcetest", "server/internal/exec/reapi/reapitest", "server/internal/engine/plan/plantest", "server/internal/pluginhost/pluginhosttest", "server/internal/engine/catalog/catalogtest"}

func beneath(path, root string) bool { return path == root || strings.HasPrefix(path, root+"/") }

// theBuild are the daemon's packages: the build service, its engine,
// its execution and its plugin host, and buildtest, and not its binary.
var theBuild = []string{"server/internal/build", "server/internal/engine", "server/internal/exec", "server/internal/history", "server/internal/model", "server/internal/pattern", "server/internal/pins", "server/internal/pluginapi", "server/internal/pluginhost", "server/internal/wire", "server/testsupport/buildtest"}

// ofTheBuild is whether a directory is of the daemon's build.
func ofTheBuild(dir string) bool {
	return slices.ContainsFunc(theBuild, func(root string) bool { return beneath(dir, root) })
}

// starters are the test support that starts an implementation: a daemon,
// a control plane, a sandbox held in memory.
var starters = []string{"server/testsupport/buildtest", "server/testsupport/controlplanetest", "server/testsupport/drivertest"}

func testSupport(path string) bool {
	for _, root := range forTests {
		if beneath(path, root) {
			return true
		}
	}
	return false
}
func owner(file string) *policy {
	dir := filepath.ToSlash(filepath.Dir(file))
	var found *policy
	ambiguous := false
	for i := range policies {
		p := &policies[i]
		base, subtree := strings.CutSuffix(p.owner, "/**")
		matches := dir == p.owner || subtree && beneath(dir, base)
		if matches && (found == nil || len(base) > len(strings.TrimSuffix(found.owner, "/**"))) {
			found = p
			ambiguous = false
		} else if matches && len(base) == len(strings.TrimSuffix(found.owner, "/**")) {
			ambiguous = true
		}
	}
	if ambiguous {
		return nil
	}
	return found
}
func permits(file, imported string) bool {
	path, local := strings.CutPrefix(imported, module+"/")
	if !local {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if part == "testdata" || part == "target" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	p := owner(file)
	if p == nil {
		return false
	}
	dir := filepath.ToSlash(filepath.Dir(file))
	test := strings.HasSuffix(file, "_test.go")
	if test && (path == dir || path == "server/telemetry") {
		return true
	}
	if testSupport(path) {
		if !test && !testSupport(dir) {
			return false
		}
		starts := slices.ContainsFunc(starters, func(root string) bool { return beneath(path, root) })
		// Test support uses nothing it is there to test: what starts a
		// daemon, a control plane or a driver is no other test support's.
		// The driver of the container runtime is held to what any driver
		// is held to (drivertest.Conforms), as the gateway's is.
		driven := dir == containerDriver && path == "server/testsupport/drivertest"
		if starts && !driven && beneath(dir, "server/testsupport") && !slices.ContainsFunc(starters, func(root string) bool { return beneath(path, root) && beneath(dir, root) }) {
			return false
		}
		// Acceptance specs can start implementations only in their setup file.
		if starts && beneath(dir, "specs") {
			return filepath.Base(file) == "support_test.go"
		}
		if beneath(path, "server/testsupport") && !starts {
			return true
		}
	}
	// Internal unit tests can inspect their feature's collaborators, but cannot
	// bypass the feature boundary or make a client link a server. They read
	// a stream to its end as any client of the API does, with the SDK's
	// reader (sdk/go).
	if test && ofTheBuild(dir) && (beneath(path, "sdk/go/api") || path == "sdk/go/entity" || path == "sdk/go" || beneath(path, "server/internal/pluginapi") || path == "server/internal/engine/source" || path == "server/internal/exec/reapi") {
		return true
	}
	// A server's test reads what it was refused by its reason as any
	// client of the API does, with the SDK's one reader of it
	// (frontseat.Reason).
	if test && beneath(dir, "server") && path == "sdk/go" {
		return true
	}
	// Evaluation's tests load a real plugin as the host's own tests do.
	if test && (dir == "server/internal/pluginhost" || dir == "server/internal/engine/evaluate") && path == "server/internal/pluginhost/conformance" {
		return true
	}
	// A test is given a repository held in memory as pluginhosttest holds
	// it, and the catalog files catalogtest writes.
	if test && ofTheBuild(dir) && (path == "server/internal/pluginhost/pluginhosttest" || path == "server/internal/engine/catalog/catalogtest") {
		return true
	}
	// A test whose grid runs nothing answers what the tools package asks
	// of a grid as toolstest does.
	if test && ofTheBuild(dir) && path == "server/internal/exec/tools/toolstest" {
		return true
	}
	// A test is given a repository of the files it names, as a run sees
	// it, as sourcetest gives it, and the test grid as reapitest dials it.
	if test && ofTheBuild(dir) && (path == "server/internal/engine/source/sourcetest" || path == "server/internal/exec/reapi/reapitest") {
		return true
	}
	// A test of the plan, and of what schedules it, is given the actions
	// it is about as plantest writes them.
	if test && ofTheBuild(dir) && path == "server/internal/engine/plan/plantest" {
		return true
	}
	if test && ofTheBuild(dir) && (path == "server/internal/pattern" || path == "server/internal/pins") {
		return true
	}
	// The control plane's tests read a remote as the daemon in a sandbox
	// reads it, by the system a daemon has (source.Fetching), so that
	// what it refuses of a remote, and writes a rule for, is proven of
	// that system; the control plane asks it through its port alone.
	if test && beneath(dir, "server/internal/controlplane") && path == "server/internal/source" {
		return true
	}
	// The gateway's driver's tests ask the container runtime what an
	// image starts, as the sandbox service does; the driver does not.
	if test && dir == "server/sandbox/driver/openshell" && path == "server/internal/image" {
		return true
	}
	if file == "server/internal/build/build.go" && path == "server/internal/exec/reapi" {
		return true
	}
	for _, allowed := range p.imports {
		if root, all := strings.CutSuffix(allowed, "/**"); all && beneath(path, root) || path == allowed {
			return true
		}
	}
	return false
}

func TestEverySourceFileHasAnOwnerAndObeysItsImports(t *testing.T) {
	root := repositoryRoot(t)
	for _, p := range policies {
		if _, err := os.Stat(filepath.Join(root, strings.TrimSuffix(p.owner, "/**"))); err != nil {
			t.Errorf("missing policy owner %s: %v", p.owner, err)
		}
	}
	for file, imports := range importsUnder(t, root, root) {
		if owner(file) == nil {
			t.Errorf("%s has no ownership rule", file)
		}
		for _, imp := range imports {
			if !permits(file, imp) {
				t.Errorf("%s cannot import %s", file, imp)
			}
		}
	}
}

func unquote(t *testing.T, literal string) string {
	t.Helper()
	value, err := strconv.Unquote(literal)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// importsUnder returns the imports of every Go file under dir that
// belongs to any repository module, by path relative to root.
func importsUnder(t *testing.T, root, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, source := range goSources(t, root, dir) {
		rel := filepath.FromSlash(source.rel)
		// A file that imports nothing is a file all the same.
		out[rel] = nil
		for _, imp := range source.file.Imports {
			out[rel] = append(out[rel], unquote(t, imp.Path.Value))
		}
	}
	return out
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// versionControl are the packages that name a version control system,
// one on each side: the daemon's, which brings a repository and reads
// its history, and the client's, which reads a checkout of this machine.
// Each is chosen by the one package beside it and by nothing else.
var versionControl = map[string]string{
	"server/internal/source/internal/vcs/git": "server/internal/source",
	"cli/internal/vcs/git":                    "cli/internal/machine",
}

// gitItself is git as both version control packages run it and read
// its remotes: it names git, and only they import it.
const gitItself = "server/git"

// named is the package that names a version control system a file is
// of, and whether it is of one.
func named(file string) (string, bool) {
	for adapter := range versionControl {
		if strings.HasPrefix(filepath.ToSlash(file), adapter+"/") {
			return adapter, true
		}
	}
	if strings.HasPrefix(filepath.ToSlash(file), gitItself+"/") {
		return gitItself, true
	}
	return "", false
}

// ignoreSyntax is the matcher for the gitignore pattern syntax, allowed
// anywhere: it is the syntax of .frontseatignore and runs no git.
const ignoreSyntax = "github.com/go-git/go-git/v5/plumbing/format/gitignore"

// Frontseat knows no version control system by name: on each side one
// package runs git and reads what git writes, the package beside it
// chooses it, and whatever else asks goes through the interface it
// implements.
func TestOnlyOnePackageNamesAVersionControlSystem(t *testing.T) {
	root := repositoryRoot(t)
	apart := func(file string) bool {
		file = filepath.ToSlash(file)
		if _, is := named(file); is || strings.HasSuffix(file, "_test.go") {
			return true
		}
		for _, prefix := range forTests {
			if strings.HasPrefix(file, prefix) {
				return true
			}
		}
		return false
	}
	for file, imports := range importsUnder(t, root, root) {
		if apart(file) {
			continue
		}
		for _, imp := range imports {
			if chooser, is := versionControl[strings.TrimPrefix(imp, module+"/")]; is && filepath.ToSlash(filepath.Dir(file)) != chooser {
				t.Errorf("%s imports %s: only %s chooses that version control system", file, imp, chooser)
			}
			if imp == module+"/"+gitItself {
				t.Errorf("%s imports %s: only a version control package runs git", file, imp)
			}
			// The one thing of go-git's anything else may use is its matcher
			// for the gitignore pattern syntax, which .frontseatignore is
			// written in: a style of ignore file, not a version control
			// system (owner, 2026-11-08). Nothing else of git's is.
			if imp == ignoreSyntax {
				continue
			}
			if strings.Contains(imp, "git-urls") || strings.Contains(imp, "go-git") || strings.Contains(imp, "git2go") {
				t.Errorf("%s imports %s: what git is and how it writes a remote is a version control package's to know", file, imp)
			}
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, file), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			// What a file imports is held to above.
			if _, imported := n.(*ast.ImportSpec); imported {
				return false
			}
			literal, is := n.(*ast.BasicLit)
			if !is || literal.Kind != token.STRING {
				return true
			}
			// The program by its name, by a path to it, or as the first
			// word of a command line.
			if words := strings.Fields(strings.Trim(literal.Value, "\"`")); len(words) > 0 && filepath.Base(words[0]) == "git" {
				t.Errorf("%s names git (%s): only a version control package runs it", file, literal.Value)
			}
			return true
		})
	}
}

// containerDriver is the one driver of the container runtime: test
// support, which no program links.
const containerDriver = "server/testsupport/testcontainer"

// containerRuntimeUses is what a file does that could make a sandbox
// with the container runtime where it may not: every sandbox a program
// makes is the OpenShell gateway's, and the driver of the container
// runtime is for tests alone. A test of the journey is held as a
// program is, since one that made a workspace so would prove nothing of
// what a person is given. A package beneath server/internal/driver/,
// where there is none, is refused to tests too: it would be a second
// driver beside the gateway's, which the sandbox's one implementation
// leaves no room for.
func containerRuntimeUses(file string, source []byte) ([]string, error) {
	file = filepath.ToSlash(file)
	parsed, err := parser.ParseFile(token.NewFileSet(), file, source, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	test := strings.HasSuffix(file, "_test.go")
	var refused []string
	for _, spec := range parsed.Imports {
		path := strings.TrimPrefix(strings.Trim(spec.Path.Value, "\"`"), module+"/")
		if path != containerDriver && !strings.HasPrefix(path, "server/internal/driver/") {
			continue
		}
		if !test || strings.HasPrefix(file, "specs/") || strings.HasPrefix(path, "server/internal/driver/") {
			refused = append(refused, fmt.Sprintf("%s makes sandboxes with the container runtime (%s): a program's are the gateway's, and that driver is for a test of a server alone", file, path))
		}
	}
	return refused, nil
}

func TestNoProgramMakesSandboxesWithTheContainerRuntime(t *testing.T) {
	root := repositoryRoot(t)
	for file := range importsUnder(t, root, root) {
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		refused, err := containerRuntimeUses(file, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, why := range refused {
			t.Error(why)
		}
	}
}

// What the rule above refuses, each tried, beside what it allows.
func TestEveryWayToTheContainerRuntimeIsSeen(t *testing.T) {
	driver := module + "/" + containerDriver
	for name, c := range map[string]struct {
		file, source string
		refused      bool
	}{
		"a server's test":                 {"server/internal/runner/attempt_test.go", `package runner; import "` + driver + `"`, false},
		"the sandbox service's test":      {"server/sandbox/runner_test.go", `package sandbox_test; import "` + driver + `"`, false},
		"under another name, in a test":   {"server/sandbox/runner_test.go", `package sandbox_test; import made "` + driver + `"`, false},
		"the grid's runner":               {"server/internal/runner/runner.go", `package runner; import "` + driver + `"`, true},
		"the sandbox service":             {"server/sandbox/sandbox.go", `package sandbox; import made "` + driver + `"`, true},
		"a program":                       {"server/cmd/frontseat-local/main.go", `package main; import _ "` + driver + `"`, true},
		"a test of the journey":           {"specs/cli/local_test.go", `package cli; import "` + driver + `"`, true},
		"a driver beside the gateway's":   {"server/sandbox/sandbox.go", `package sandbox; import "` + module + `/server/internal/driver/podman"`, true},
		"a driver beside, even in a test": {"server/sandbox/runner_test.go", `package sandbox_test; import "` + module + `/server/internal/driver/podman"`, true},
		"what an image starts, asked":     {"server/sandbox/sandbox.go", `package sandbox; import "` + module + `/server/internal/image"`, false},
	} {
		t.Run(name, func(t *testing.T) {
			refused, err := containerRuntimeUses(c.file, []byte(c.source))
			if err != nil {
				t.Fatal(err)
			}
			if (len(refused) > 0) != c.refused {
				t.Fatalf("refused = %v, want refused %v", refused, c.refused)
			}
		})
	}
}

// workspaceModules are the modules go.work uses, by their directories:
// the one place they are written.
func workspaceModules(t *testing.T, root string) []string {
	t.Helper()
	var work struct {
		Use     []struct{ DiskPath string }
		Replace []struct {
			Old struct{ Path string }
			New struct{ Path string }
		}
	}
	goJSON(t, root, &work, "work", "edit", "-json")
	var used []string
	for _, use := range work.Use {
		used = append(used, filepath.ToSlash(filepath.Clean(use.DiskPath)))
	}
	slices.Sort(used)
	// What go.work replaces is each module it uses, by its own path, and
	// nothing else: the two lists of it are held to each other.
	var replaced []string
	for _, each := range work.Replace {
		dir := filepath.ToSlash(filepath.Clean(each.New.Path))
		if each.Old.Path != module+"/"+dir {
			t.Errorf("go.work replaces %s with %s, which is not that module's directory", each.Old.Path, each.New.Path)
		}
		replaced = append(replaced, dir)
	}
	slices.Sort(replaced)
	if !slices.Equal(replaced, used) {
		t.Errorf("go.work replaces %v and uses %v: each module it uses is replaced by its directory, and nothing else", replaced, used)
	}
	return used
}

// Go itself parses manifests. Every go.mod of the repository is a module
// go.work uses, and every module go.work uses has one, declaring the path
// of its directory and replacing nothing: resolving a sibling is go.work's.
func TestEveryGoModuleIsInTheWorkspaceAndValidation(t *testing.T) {
	root := repositoryRoot(t)
	want := workspaceModules(t, root)
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (d.Name() == "target" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "go.mod" {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found)
	if !slices.Equal(found, want) {
		t.Fatalf("module files %v, want those go.work uses, %v", found, want)
	}
	for _, dir := range want {
		var mod struct {
			Module  struct{ Path string }
			Replace []json.RawMessage
		}
		goJSON(t, filepath.Join(root, dir), &mod, "mod", "edit", "-json")
		if mod.Module.Path != module+"/"+dir {
			t.Errorf("%s declares %s", dir, mod.Module.Path)
		}
		if len(mod.Replace) > 0 {
			t.Errorf("%s has replacements: local resolution belongs to go.work", dir)
		}
	}
}
func goJSON(t *testing.T, dir string, out any, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v: %v: %s", args, err, raw)
	}
	if err = json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

// cargoPackage is one package of the Cargo workspace, as cargo says it.
type cargoPackage struct {
	Name         string
	ManifestPath string `json:"manifest_path"`
	// Publish is the registries it may be published to: none where it
	// says publish = false, and nil where it says nothing.
	Publish      *[]string
	Version      string
	Description  string
	License      string
	Dependencies []cargoDependency
}

// cargoDependency is one dependency of a crate, as cargo says it: Kind
// is "dev" for a dependency of its tests alone, and empty otherwise.
type cargoDependency struct {
	Name string
	Path string
	Kind string
	// Req is the version it is required at: "*" where none is named.
	Req string
	// Source is where it comes from: empty for a path, "git+…" for a
	// git repository; Registry names a registry other than crates.io.
	Source   string
	Registry string
}

// cargoPackages are the packages of the Cargo workspace: cargo's word.
func cargoPackages(t *testing.T, root string) []cargoPackage {
	t.Helper()
	cmd := exec.Command("cargo", "metadata", "--no-deps", "--format-version", "1")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var data struct{ Packages []cargoPackage }
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	return data.Packages
}

func TestRustCratesUseTheSDKAndNeverAnotherPlugin(t *testing.T) {
	root := repositoryRoot(t)
	for _, p := range cargoPackages(t, root) {
		rel, err := filepath.Rel(root, p.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		// A plugin is a crate at plugins/<name>. A crate nested in a
		// plugin's directory is that plugin's own, as plugins/release has
		// its clone of a parser: it is no plugin, and only its plugin may
		// depend on it.
		own := filepath.Dir(p.ManifestPath)
		plugin := strings.HasPrefix(filepath.ToSlash(rel), "plugins/") && strings.Count(filepath.ToSlash(rel), "/") == 2
		sdk := false
		for _, d := range p.Dependencies {
			if d.Path == "" {
				continue
			}
			if !localDependencyAllowed(root, p.Name, own, plugin, d) {
				t.Errorf("%s depends on local crate %s at %s", p.Name, d.Name, d.Path)
			}
			sdk = sdk || d.Name == "frontseat-plugin"
		}
		if plugin && !sdk {
			t.Errorf("%s does not use the shared SDK", p.Name)
		}
	}
}

// localDependencyAllowed says whether a crate may depend on a crate of
// the repository. A plugin uses the SDK and nothing else of the
// repository but a crate nested in its own directory; the SDK, whose
// contract types are committed, depends on no crate of the repository,
// and nor does what generates them. The SDK's testkit
// (frontseat-plugin-test) is the one exception, and only for tests: the
// SDK and any crate may have it as a dev-dependency, and none as more.
func localDependencyAllowed(root, name, own string, plugin bool, d cargoDependency) bool {
	at := filepath.Clean(d.Path)
	return (name != "frontseat-plugin" && at == filepath.Join(root, "sdk", "plugin")) ||
		(d.Kind == "dev" && at == filepath.Join(root, "sdk", "plugin-test")) ||
		(plugin && strings.HasPrefix(at+string(filepath.Separator), own+string(filepath.Separator)))
}

// The testkit is a dependency of tests alone: a plugin or the SDK may
// have it as a dev-dependency, and no crate as a dependency of what it
// builds; another plugin is refused either way, and the SDK is allowed
// to a plugin.
func TestTheTestkitIsADependencyOfTestsAlone(t *testing.T) {
	root := "/r"
	own := filepath.Join(root, "plugins", "go")
	for _, c := range []struct {
		name string
		d    cargoDependency
		want bool
	}{
		{"frontseat-plugin-go", cargoDependency{Path: "/r/sdk/plugin-test", Kind: "dev"}, true},
		{"frontseat-plugin", cargoDependency{Path: "/r/sdk/plugin-test", Kind: "dev"}, true},
		{"frontseat-plugin-go", cargoDependency{Path: "/r/sdk/plugin-test"}, false},
		{"frontseat-plugin-go", cargoDependency{Path: "/r/sdk/plugin"}, true},
		{"frontseat-plugin", cargoDependency{Path: "/r/sdk/plugin"}, false},
		{"frontseat-plugin-go", cargoDependency{Path: "/r/plugins/cargo", Kind: "dev"}, false},
	} {
		if got := localDependencyAllowed(root, c.name, own, c.name != "frontseat-plugin", c.d); got != c.want {
			t.Errorf("%s depending on %+v: allowed %v, want %v", c.name, c.d, got, c.want)
		}
	}
}

// The Go SDK is what a program outside this repository builds on, from a
// module proxy: it requires no module of this repository, which no proxy
// serves at v0.0.0, and links no package of it but its own.
func TestTheGoSDKRequiresNothingOfThisRepository(t *testing.T) {
	root := repositoryRoot(t)
	var mod struct {
		Require []struct{ Path string }
		Replace []json.RawMessage
	}
	goJSON(t, filepath.Join(root, "sdk/go"), &mod, "mod", "edit", "-json")
	var required []string
	for _, each := range mod.Require {
		required = append(required, each.Path)
	}
	for _, found := range ofThisRepository(required) {
		t.Errorf("the Go SDK requires %s, of this repository", found)
	}
	if len(mod.Replace) > 0 {
		t.Error("the Go SDK replaces a module")
	}
	cmd := exec.Command("go", "list", "-deps", "-test", "-f", "{{.ImportPath}}", "./sdk/go/...")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, linked := range strings.Fields(string(raw)) {
		// The SDK's own package, and its tests' (sdk/go_test, sdk/go.test).
		sdk := strings.TrimSuffix(strings.TrimSuffix(linked, ".test"), "_test")
		if strings.HasPrefix(linked, module+"/") && sdk != module+"/sdk/go" && !strings.HasPrefix(linked, module+"/sdk/go/") {
			t.Errorf("the Go SDK links %s", linked)
		}
	}
	// And it builds as a third party has it: alone, out of any workspace,
	// from nothing but the modules its go.sum locks, which are what it
	// needs and no more.
	alone := filepath.Join(t.TempDir(), "sdk")
	if err := os.CopyFS(alone, os.DirFS(filepath.Join(root, "sdk/go"))); err != nil {
		t.Fatal(err)
	}
	for _, each := range []struct {
		flags string
		args  []string
	}{
		{"-mod=readonly", []string{"build", "./..."}},
		{"-mod=readonly", []string{"vet", "./..."}},
		{"", []string{"mod", "tidy", "-diff"}},
	} {
		cmd := exec.Command("go", each.args...)
		cmd.Dir = alone
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS="+each.flags)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the Go SDK alone: go %s: %v\n%s", strings.Join(each.args, " "), err, out)
		}
	}
}

// ofThisRepository are the modules of this repository among those
// required, but the Go SDK itself.
func ofThisRepository(required []string) []string {
	var found []string
	for _, path := range required {
		if strings.HasPrefix(path, module+"/") && path != module+"/sdk/go" {
			found = append(found, path)
		}
	}
	return found
}

func TestAModuleOfThisRepositoryRequiredIsFound(t *testing.T) {
	if found := ofThisRepository([]string{"connectrpc.com/connect/v2", "google.golang.org/protobuf", "github.com/frontseat-dev/ssh"}); len(found) != 0 {
		t.Errorf("modules of elsewhere were taken for this repository's: %v", found)
	}
	refused := []string{module + "/internal/telemetry", module + "/cli", module + "/sdk/go/other"}
	if found := ofThisRepository(append([]string{"google.golang.org/protobuf"}, refused...)); !slices.Equal(found, refused) {
		t.Errorf("found %v, want %v", found, refused)
	}
}

// Generated Go is named so (*.pb.go, *.connect.go) only beneath where a
// Buf template of the contracts writes: the name is what jscpd leaves out
// (mise.toml), so a file written by hand under such a name elsewhere
// would be hidden from it.
func TestGeneratedGoIsOnlyWhereATemplateWritesIt(t *testing.T) {
	root := repositoryRoot(t)
	outs, err := generated.All(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) == 0 {
		t.Fatal("no template of the contracts writes anything")
	}
	var named []string
	err = filepath.WalkDir(root, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if at != root && (d.Name() == "target" || d.Name() == "testdata" || d.Name() == "dist" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, at)
		named = append(named, rel)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, stray := range generatedElsewhere(named, outs) {
		t.Errorf("%s is named as generated Go and is beneath no template's output", stray)
	}
}

// generatedElsewhere are the files named as generated Go that are beneath
// none of the outputs.
func generatedElsewhere(files, outs []string) []string {
	var stray []string
	for _, file := range files {
		if !strings.HasSuffix(file, ".pb.go") && !strings.HasSuffix(file, ".connect.go") {
			continue
		}
		if !slices.ContainsFunc(outs, func(out string) bool { return beneath(filepath.ToSlash(file), filepath.ToSlash(out)) }) {
			stray = append(stray, file)
		}
	}
	return stray
}

func TestGeneratedGoElsewhereIsFound(t *testing.T) {
	outs := []string{"sdk/go/api", "server/internal/pluginapi"}
	held := []string{"sdk/go/api/frontseat/build/v1alpha1/run.pb.go", "sdk/go/api/x/xconnect/x.connect.go", "server/internal/pluginapi/p/p.pb.go", "server/internal/build/build.go"}
	if stray := generatedElsewhere(held, outs); len(stray) != 0 {
		t.Errorf("generated Go where a template writes it was refused: %v", stray)
	}
	refused := []string{"server/internal/build/hand.pb.go", "client/x.connect.go", "sdk/go/apix/y.pb.go"}
	if stray := generatedElsewhere(append(slices.Clone(held), refused...), outs); !slices.Equal(stray, refused) {
		t.Errorf("found %v, want %v", stray, refused)
	}
}

// api/ holds the contracts and nothing else: the protos, Buf's
// configuration, the API entities' catalog files, and prose about them.
// What is generated from the contracts, and every library over them, is
// in the SDKs and the daemon.
func TestTheContractsDirectoryHoldsNoCode(t *testing.T) {
	root := repositoryRoot(t)
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "api"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		files = append(files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range notContracts(files) {
		t.Errorf("%s is in api/, which holds the contracts alone", file)
	}
}

// notContracts are the files of those given that are not a contract's: a
// proto, Buf's workspace or a template of its, a catalog file, or prose.
func notContracts(files []string) []string {
	var out []string
	for _, file := range files {
		base := filepath.Base(file)
		buf := base == "buf.yaml" || base == "buf.lock" || base == "buf.gen.yaml" || strings.HasPrefix(base, "buf.gen.") && strings.HasSuffix(base, ".yaml")
		if !buf && base != "catalog-info.yaml" && !strings.HasSuffix(base, ".proto") && !strings.HasSuffix(base, ".md") {
			out = append(out, file)
		}
	}
	return out
}

func TestCodeAmongTheContractsIsFound(t *testing.T) {
	held := []string{"api/buf.yaml", "api/buf.gen.sdk.yaml", "api/build-proto/catalog-info.yaml", "api/build-proto/frontseat/build/v1alpha1/run.proto", "api/plugin-proto/ABI.md"}
	if found := notContracts(held); len(found) != 0 {
		t.Errorf("contracts taken for code: %v", found)
	}
	refused := []string{"api/gen/go/frontseat/build/v1alpha1/run.pb.go", "api/entity/go.mod", "api/entity/go.sum", "api/Cargo.toml", "api/src/lib.rs", "api/build.rs", "api/buf.gen.yaml.go", "api/gen.sh"}
	if found := notContracts(append(slices.Clone(held), refused...)); !slices.Equal(found, refused) {
		t.Errorf("found %v, want %v", found, refused)
	}
}

// Ask Go what the client actually links. This also sees imported fixture or
// generated packages that a source-directory walk might otherwise exclude.
func TestClientProductionDependencyClosure(t *testing.T) {
	root := repositoryRoot(t)
	platforms := map[string][2]string{
		"native":      {runtime.GOOS, runtime.GOARCH},
		"linux-arm64": {"linux", "arm64"},
		"linux-amd64": {"linux", "amd64"},
	}
	for name, platform := range platforms {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", "-json", "./cli/...")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "GOOS="+platform[0], "GOARCH="+platform[1], "CGO_ENABLED=0")
			raw, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			for {
				var pkg struct {
					ImportPath string
					Deps       []string
				}
				if err := decoder.Decode(&pkg); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				// Of the server, the command line links the shared packages
				// alone: git, as frontseat runs it, and its telemetry; and
				// what git ends a process group with, the confiner's one way
				// past a sandbox's refusal of a signal to a group (owner's
				// ruling, 2026-11-19).
				if shared := pkg.ImportPath == module+"/server/git" || pkg.ImportPath == module+"/server/telemetry" || pkg.ImportPath == module+"/server/internal/confine"; strings.HasPrefix(pkg.ImportPath, module+"/server/") && !shared {
					t.Errorf("client links server package %s", pkg.ImportPath)
				}
				if strings.HasPrefix(pkg.ImportPath, module+"/server/testsupport/") {
					t.Errorf("client links test support %s", pkg.ImportPath)
				}
			}
		})
	}
}

// gate is how a task asks the gate program (specs/cmd/gate)
// to run a verb over every module go.work uses.
const gate = "go run ./specs/cmd/gate "

// asked reads a task's command as the gate reads it, where it runs the
// gate; ok is false for a command that runs something else, or that the
// gate would refuse, which a task's run then fails by itself.
func asked(command string) (gatecommand.Command, bool) {
	rest, is := strings.CutPrefix(command, gate)
	if !is {
		return gatecommand.Command{}, false
	}
	read, err := gatecommand.Parse(strings.Fields(rest))
	return read, err == nil
}

// stack is what a task's tests are told of the stack: a task that needs
// no stack tells them there is none, and one that needs it never does.
type stack bool

const (
	noStack   stack = false
	needStack stack = true
)

// uncovered says what a task's commands leave out of a verb of the gate
// program, or run weaker than it must: "every module" where no command
// runs the verb over all of them; each directory one command leaves to
// another (-except DIR) that no later command runs whole (-only DIR, by
// the same reading of what is beneath it); and, for test, a command that
// weaker refuses. A gate that leaves nothing out covers every module,
// which the gate program's own tests hold
// (TestEveryModuleTheWorkspaceUsesIsGated).
func uncovered(commands []string, verb string, needs stack) []string {
	var missing []string
	gated := false
	for i, command := range commands {
		read, ok := asked(command)
		if !ok || read.Verb != verb || len(read.Only) > 0 {
			continue
		}
		gated = true
		if verb != "test" {
			continue
		}
		missing = append(missing, weaker(command, read, needs)...)
		for _, dir := range read.Except {
			ran := false
			for _, later := range commands[i+1:] {
				if then, ok := asked(later); ok && then.Verb == "test" && slices.Contains(then.Only, dir) {
					ran = true
					missing = append(missing, weaker(later, then, needs)...)
				}
			}
			if !ran {
				missing = append(missing, dir)
			}
		}
	}
	if !gated {
		return []string{"every module"}
	}
	return missing
}

// weaker says how a gate's test command runs weaker than a gate must:
// what it passes to go is not only what a gate may (-count=1, -race, -v,
// -timeout and -parallel, each once at most, the race detector and
// -count=1 always),
// so that no test is left out by name, length, listing or another runner;
// and what it tells its tests of the stack is not what the task needs.
func weaker(command string, read gatecommand.Command, needs stack) []string {
	var out []string
	seen := map[string]bool{}
	for f := 0; f < len(read.Go); f++ {
		field := read.Go[f]
		name, value, valued := strings.Cut(field, "=")
		switch {
		case field == "-count=1" || field == "-race" || field == "-v":
		case (name == "-timeout" || name == "-parallel") && valued && value != "":
		case (field == "-timeout" || field == "-parallel") && f+1 < len(read.Go) && !strings.HasPrefix(read.Go[f+1], "-"):
			f++
		default:
			out = append(out, fmt.Sprintf("what %s may leave out, in %s", field, command))
			continue
		}
		if seen[name] {
			out = append(out, fmt.Sprintf("%s twice, in %s", name, command))
		}
		seen[name] = true
	}
	if !seen["-race"] {
		out = append(out, "the race detector, in "+command)
	}
	if !seen["-count"] {
		out = append(out, "-count=1, which runs every test again, in "+command)
	}
	if read.NoStack != bool(!needs) {
		if needs {
			out = append(out, "the stack its tests need, in "+command)
		} else {
			out = append(out, "the word that it runs with no stack, in "+command)
		}
	}
	return out
}

// mise owns TOML parsing. What `check` vets and tests, and what `test`
// tests, is every module, through the gate program, with the race
// detector and nothing else that could leave a test out: a new module or
// package cannot escape them. `check` tells its tests there is no stack,
// and `test`, which needs it, never does.
func TestValidationTasksCoverEveryModule(t *testing.T) {
	root := repositoryRoot(t)
	for task, verbs := range map[string][]string{"check": {"vet", "test", "fmt", "budget"}, "test": {"test"}} {
		cmd := exec.Command("mise", "tasks", "info", task, "--json")
		cmd.Dir = root
		raw, err := cmd.Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				t.Fatalf("mise tasks info %s: %v\n%s", task, err, exit.Stderr)
			}
			t.Fatal(err)
		}
		var config struct{ Run []string }
		if err = json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		for _, verb := range verbs {
			if missing := uncovered(config.Run, verb, stack(task == "test")); len(missing) > 0 {
				t.Errorf("mise %s does not run gate %s over %s", task, verb, strings.Join(missing, "; "))
			}
		}
	}
}

func TestMissingValidationModuleIsRefused(t *testing.T) {
	refused := func(commands []string, verb string, needs stack, want string) {
		t.Helper()
		if got := strings.Join(uncovered(commands, verb, needs), "; "); got != want {
			t.Errorf("%q: left out %q, want %q", commands, got, want)
		}
	}
	all := gate + "test -- -count=1 -race -v -timeout 45m -parallel 6"
	refused([]string{all}, "test", needStack, "")
	refused([]string{gate + "test -no-stack -- -count=1 -race"}, "test", noStack, "")
	refused([]string{gate + "vet"}, "test", needStack, "every module")
	refused([]string{gate + "tests -- -count=1 -race"}, "test", needStack, "every module")
	refused([]string{"go test -race ./cli/... ./server/..."}, "test", needStack, "every module")
	// A command that runs some packages alone covers no module.
	refused([]string{gate + "test -only specs/build -- -count=1 -race"}, "test", needStack, "every module")
	// What one command leaves to another is run whole by a later one, and
	// by nothing else: an earlier command, a go test of one package (which
	// misses a package added beneath it), another directory, or a word
	// after -args does not run it.
	except := gate + "test -except specs/build -- -count=1 -race"
	refused([]string{except, gate + "test -only specs/build -- -count=1 -race -parallel 6"}, "test", needStack, "")
	refused([]string{gate + "test -except=specs/build -- -count=1 -race", gate + "test --only=specs/build -- -count=1 -race"}, "test", needStack, "")
	refused([]string{gate + "test --except specs/build -- -count=1 -race"}, "test", needStack, "specs/build")
	refused([]string{except}, "test", needStack, "specs/build")
	refused([]string{gate + "test -only specs/build -- -count=1 -race", except}, "test", needStack, "specs/build")
	refused([]string{except, gate + "go-test -race ./specs/build/..."}, "test", needStack, "specs/build")
	refused([]string{except, gate + "test -only specs/builds -- -count=1 -race"}, "test", needStack, "specs/build")
	// What follows -- is go's, and leaves no directory out; but only what a
	// gate may pass is passed there.
	refused([]string{gate + "test -- -count=1 -race -except specs/build"}, "test", needStack, "what -except may leave out, in "+gate+"test -- -count=1 -race -except specs/build; what specs/build may leave out, in "+gate+"test -- -count=1 -race -except specs/build")
	refused([]string{gate + "test -- -count=1"}, "test", needStack, "the race detector, in "+gate+"test -- -count=1")
	refused([]string{gate + "test -- -race -v"}, "test", needStack, "-count=1, which runs every test again, in "+gate+"test -- -race -v")
	refused([]string{gate + "test -- -race -count=2"}, "test", needStack, "what -count=2 may leave out, in "+gate+"test -- -race -count=2; -count=1, which runs every test again, in "+gate+"test -- -race -count=2")
	refused([]string{gate + "test -- -count=1 -race -race"}, "test", needStack, "-race twice, in "+gate+"test -- -count=1 -race -race")
	for _, chosen := range []string{"-run TestA", "-run=TestA", "--run=TestA", "-test.run=TestA", "-test.short", "-skip TestA", "-short", "-list .", "-exec true", "-args -x", "-timeout", "-race=false", "-cover"} {
		command := gate + "test -- -count=1 -race " + chosen
		if got := uncovered([]string{command}, "test", needStack); len(got) == 0 {
			t.Errorf("%q was taken", command)
		}
		later := gate + "test -only specs/build -- -count=1 -race " + chosen
		if got := uncovered([]string{except, later}, "test", needStack); len(got) == 0 {
			t.Errorf("%q was taken where it runs what another left", later)
		}
	}
	// A task that needs the stack is never told there is none, with one
	// dash or two; one that runs with none always says so.
	for _, nostack := range []string{gate + "test -no-stack -- -count=1 -race", gate + "test --no-stack -- -count=1 -race", gate + "test -no-stack=true -- -count=1 -race"} {
		refused([]string{nostack}, "test", needStack, "the stack its tests need, in "+nostack)
		refused([]string{nostack}, "test", noStack, "")
	}
	refused([]string{all}, "test", noStack, "the word that it runs with no stack, in "+all)
	refused([]string{except, gate + "test -no-stack -only specs/build -- -count=1 -race"}, "test", needStack, "the stack its tests need, in "+gate+"test -no-stack -only specs/build -- -count=1 -race")
	// Verbs other than test are covered by being run, as the gate reads them.
	refused([]string{gate + "vet"}, "vet", noStack, "")
	refused([]string{gate + "vet ./..."}, "vet", noStack, "every module")
}

// A public constructor must not force callers to name its private runtime.
func TestPublicFeatureSignaturesHideImplementationTypes(t *testing.T) {
	root := repositoryRoot(t)
	for _, entry := range []string{"server/sandbox", "server/sandbox/driver", "server/sandbox/driver/openshell", "server/git", "server/telemetry"} {
		paths, err := filepath.Glob(filepath.Join(root, entry, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		var files []*ast.File
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		for _, leak := range publicLeaks(files) {
			t.Errorf("%s exposes private implementation %s", entry, leak)
		}
	}
}

// Follow package-local aliases, types and inferred exported variables as well
// as direct selectors. Each declaration keeps its own file's import aliases.
func publicLeaks(files []*ast.File) []string {
	type declaration struct {
		node    ast.Node
		imports map[string]string
	}
	local := map[string]declaration{}
	methods := map[string][]declaration{}
	var public []declaration
	for _, file := range files {
		imports := map[string]string{}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(path)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = path
		}
		add := func(name string, node ast.Node) {
			if node == nil {
				return
			}
			d := declaration{node, imports}
			local[name] = d
			if ast.IsExported(name) {
				public = append(public, d)
			}
		}
		for _, node := range file.Decls {
			switch d := node.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						add(s.Name.Name, s)
					case *ast.ValueSpec:
						for i, name := range s.Names {
							var value ast.Node = s.Type
							if value == nil && len(s.Values) > 0 {
								at := i
								if at >= len(s.Values) {
									at = 0
								}
								value = s.Values[at]
							}
							add(name.Name, value)
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					add(d.Name.Name, d.Type)
					continue
				}
				name := receiverName(d.Recv.List[0].Type)
				if name != "" && ast.IsExported(d.Name.Name) {
					method := declaration{d.Type, imports}
					methods[name] = append(methods[name], method)
					if ast.IsExported(name) {
						public = append(public, method)
					}
				}
			}
		}
	}
	var leaks []string
	seen := map[ast.Node]bool{}
	var walk func(declaration)
	walk = func(d declaration) {
		if d.node == nil || seen[d.node] {
			return
		}
		seen[d.node] = true
		ast.Inspect(d.node, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := n.X.(*ast.Ident); ok {
					path := d.imports[pkg.Name]
					// What is internal to a server Component is its own.
					if strings.HasPrefix(path, module+"/server/") && strings.Contains(path, "/internal/") {
						leaks = append(leaks, path+"."+n.Sel.Name)
					}
				}
				return false
			case *ast.Ident:
				if next, ok := local[n.Name]; ok {
					walk(next)
					for _, method := range methods[n.Name] {
						walk(method)
					}
				}
				return false
			case *ast.StructType:
				for _, field := range n.Fields.List {
					exported := len(field.Names) == 0
					for _, name := range field.Names {
						exported = exported || ast.IsExported(name.Name)
					}
					if exported {
						walk(declaration{field.Type, d.imports})
					}
				}
				return false
			case *ast.Field:
				walk(declaration{n.Type, d.imports})
				return false
			case *ast.FuncLit:
				walk(declaration{n.Type, d.imports})
				return false
			}
			return true
		})
	}
	for _, d := range public {
		walk(d)
	}
	return leaks
}

// Normalize both ordinary and instantiated pointer/value receivers.
func receiverName(receiver ast.Expr) string {
	for {
		switch r := receiver.(type) {
		case *ast.Ident:
			return r.Name
		case *ast.StarExpr:
			receiver = r.X
		case *ast.IndexExpr:
			receiver = r.X
		case *ast.IndexListExpr:
			receiver = r.X
		default:
			return ""
		}
	}
}

func cyclic(edges map[string][]string) bool {
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(at string) bool {
		if visiting[at] {
			return true
		}
		if done[at] {
			return false
		}
		visiting[at] = true
		for _, next := range edges[at] {
			if visit(next) {
				return true
			}
		}
		delete(visiting, at)
		done[at] = true
		return false
	}
	for at := range edges {
		if visit(at) {
			return true
		}
	}
	return false
}
func TestLocalModuleRequirementsAreAcyclic(t *testing.T) {
	edges := map[string][]string{}
	modules := workspaceModules(t, repositoryRoot(t))
	for _, dir := range modules {
		var mod struct{ Require []struct{ Path string } }
		goJSON(t, filepath.Join(repositoryRoot(t), dir), &mod, "mod", "edit", "-json")
		for _, require := range mod.Require {
			if local, ok := strings.CutPrefix(require.Path, module+"/"); ok {
				if !slices.Contains(modules, local) {
					t.Errorf("%s requires unowned module %s", dir, local)
				}
				edges[dir] = append(edges[dir], local)
			}
		}
	}
	if cyclic(edges) {
		t.Errorf("local module requirement cycle: %v", edges)
	}
	if cyclic(map[string][]string{"client": {"api"}}) || !cyclic(map[string][]string{"client": {"api"}, "api": {"client"}}) {
		t.Fatal("module cycle controls failed")
	}
}

func TestEquallySpecificOwnersAreRejected(t *testing.T) {
	original := policies
	defer func() { policies = original }()
	if owner("sdk/go/entity/proto.go") == nil {
		t.Fatal("the unique owner must be allowed")
	}
	for _, duplicate := range []string{"sdk/go/entity", "sdk/go/entity/**"} {
		policies = append(append([]policy(nil), original...), policy{duplicate, []string{"server/**"}})
		if owner("sdk/go/entity/proto.go") != nil {
			t.Errorf("ambiguous ownership %s accepted", duplicate)
		}
	}
}

// whatFills is what fills a workspace's directory as the control plane
// tells it: what a build is no part of.
const whatFills = "server/internal/source"

// buildsOfWhatFills returns each file of the build, or of the directory
// it reads, that imports what fills the directory; tests apart, which
// start it beside the build as the daemon's program does.
func buildsOfWhatFills(imports map[string][]string) []string {
	var out []string
	for file, imported := range imports {
		file = filepath.ToSlash(file)
		if strings.HasSuffix(file, "_test.go") || !(strings.HasPrefix(file, "server/internal/build/") || strings.HasPrefix(file, "server/internal/directory/")) {
			continue
		}
		if slices.Contains(imported, module+"/"+whatFills) {
			out = append(out, file)
		}
	}
	slices.Sort(out)
	return out
}

// A build needs a directory, and not whoever filled it: the build and
// the directory it reads import nothing of what fills a workspace, which
// the daemon's program starts beside them.
func TestTheBuildReadsItsDirectoryAndNothingOfWhatFillsIt(t *testing.T) {
	root := repositoryRoot(t)
	for _, file := range buildsOfWhatFills(importsUnder(t, root, root)) {
		t.Errorf("%s imports %s: a build reads its directory, and knows nothing of what fills it", file, whatFills)
	}
	// The controls: a file of the build that imports it is found; a test
	// of the build, and the daemon's program, are not.
	planted := map[string][]string{
		"server/internal/build/runs.go":      {module + "/" + whatFills},
		"server/internal/build/run_test.go":  {module + "/" + whatFills},
		"server/internal/daemon/daemon.go":   {module + "/" + whatFills},
		"server/internal/directory/read.go":  {module + "/server/internal/source/record"},
		"server/internal/directory/other.go": {module + "/" + whatFills},
	}
	if found := buildsOfWhatFills(planted); !slices.Equal(found, []string{"server/internal/build/runs.go", "server/internal/directory/other.go"}) {
		t.Errorf("of what was planted, found %v", found)
	}
}
