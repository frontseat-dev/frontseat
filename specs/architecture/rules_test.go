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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/specs/internal/gatecommand"
	"github.com/frontseat-dev/frontseat/specs/internal/generated"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
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
	// The command line starts the build daemon of the directory it builds
	// of itself: it knows the daemon's life, and where a workspace's
	// manifest is.
	{"cli/internal/machine", []string{"sdk/go/api/**", "sdk/go", "sdk/go/bearer", "cli/internal/vcs", "cli/internal/vcs/git", "stack/local", "core/ports", "build/daemon/life", "common/record/manifest"}},
	{"cli/internal/vcs", []string{}},
	{"cli/internal/vcs/git", []string{"cli/internal/vcs", "common/git"}},
	{"cli/internal/ui/**", []string{"sdk/go/api/**", "sdk/go/entity", "sdk/go/enum", "cli/internal/ui/**"}},
	// The binary is a thin main over the commands, which are the command
	// line's own (cli/internal/cmd).
	{"cli/cmd/frontseat", []string{"cli/internal/cmd", "core/telemetry"}},
	// The commands are flags and printing over the SDK's one client, which
	// they hand the command's telemetry to instrument what they ask. What
	// a person writes that the control plane reads too (a port) is read
	// by the reader both have (core/plain).
	{"cli/internal/cmd", []string{"sdk/go/api/**", "sdk/go/entity", "sdk/go/enum", "sdk/go", "cli/internal/machine", "cli/internal/ui/**", "core/telemetry", "core/plain", "build/daemon/life"}},
	{"core/telemetry", []string{}},
	// Git as frontseat runs it, hardened, and how it writes a remote: one
	// implementation for the client's adapter and the daemon's.
	{"common/git", []string{"common/confine"}},
	// The test support at the top is beneath every module but the SDK, and
	// reads nothing of what is above it: what the local stack is, and its
	// ports, are read by the test support beside the stack.
	{"testsupport/**", []string{"testsupport/**"}},
	// Test support of the local stack's own: the box, the gateway, the
	// grid and the owner's token a test is given, beside what they start.
	// It reads what the local stack is from its files (stack/local),
	// never through the stack's control plane (frontseat-server), which
	// links what the test support tests, and never builds the box, which
	// is built from the source of other packages: the gate builds it
	// before the tests (fromSource).
	{"stack/testsupport/**", []string{"stack/local", "stack/testsupport/**", "core/ports"}},
	// The stack's one container is started for a test as the stack starts
	// it: by its own flags and profile.
	{"stack/testsupport/testbox", []string{"testsupport/**", "stack/local", "stack"}},
	// The grid's runner runs only a confined command: what another tool
	// sends it begins as the daemon's do, which the confiner says.
	{"stack/testsupport/testgrid", []string{"testsupport/**", "stack/local", "stack/testsupport/**", "core/ports", "common/confine", "core/poll"}},
	// A test logs in to a workspace's shell with its own key, through the
	// SDK's tunnel, presenting the token its control plane knows it by.
	{"testsupport/testidentity", []string{"sdk/go"}},
	// The owner's token is a person's own: a test presents it as the
	// SDK does, to its origin alone and following no redirect.
	{"stack/testsupport/testowner", []string{"stack/local", "core/ports", "sdk/go/bearer"}},
	// The sandbox API is no part of the SDK: a test asks it, and opens a
	// tunnel to a sandbox's port, through this.
	{"sandbox/testsupport/testsandbox", []string{"sdk/go/api/**", "sandbox/api/**", "testsupport/testwire"}},
	// What the local stack is, read from its files: one reader, for the
	// stack's control plane, the box and the test support, which links
	// nothing of the server.
	{"stack/local", []string{"core/ports", "stack/image"}},
	// The stack's image, beside what it is built of: the files of it a
	// program carries.
	{"stack/image", []string{}},
	// The ports frontseat's programs are reached on, each said once: the
	// daemon's defaults, the control plane, the command line, the stack's
	// own control plane and the test support all read them here.
	{"core/ports", []string{}},
	// The box's one build, from what the stack says it is laid on; and
	// the stack brought up and down, running the box it builds, its one
	// container publishing the ports where they are said.
	{"stack/box", []string{"stack/local", "stack/internal/poll"}},
	{"stack", []string{"stack/local", "stack/box", "core/ports", "stack/internal/poll"}},
	// Asking again until the stack's container comes to something: one
	// loop, for the box's build and the stack's program.
	{"stack/internal/poll", []string{}},
	{"core/httpserver", []string{}},
	{"core/image", []string{}},
	{"common/confine", []string{}},
	// Asking again until what is asked is done: one loop, for the
	// daemon's dial, the control plane, the sandbox service and its
	// gateway's driver, the runner and the test support that waits so.
	{"core/poll", []string{}},
	// Whether what leads out of a sandbox is written plainly: one reader
	// of a host, an origin, a path and a program, for the control plane
	// and the gateway's driver.
	{"core/plain", []string{}},
	// A directory of records, each written whole and read strictly: one
	// store, for the workspaces, the sandboxes, the credentials and a
	// workspace's manifest.
	{"common/record/records", []string{}},
	// The file a build daemon is told its settings in, as it is written:
	// its shape alone, for whoever writes it and whoever reads it.
	{"common/record/buildfile", []string{"common/record/records"}},
	// A secret kept in a file that is the server's alone: the owner's
	// token, and the token presented to Backstage.
	{"core/secretfile", []string{}},
	// The Backstage catalog the control plane resolves entities through,
	// asked by one client held to its origin; its tests ask a fake.
	{"workspace/server/backstage", []string{"sdk/go/entity", "sdk/go/bearer", "core/plain", "core/secretfile", "common/record", "testsupport/backstagetest"}},
	// What a test service is held to: one check, for the daemon and the grid's runner.
	{"common/runner/testservice", []string{"sdk/go/api/**", "sdk/go/entity", "core/image", "core/plain"}},
	// The build reads its directory (build/daemon/directory) and
	// nothing of what fills it, its tests neither: they fill one with the
	// version control system and the manifest
	// (TestTheBuildReadsItsDirectoryAndNothingOfWhatFillsIt).
	{"build/daemon/build", []string{"sdk/go/api/**", "sdk/go/entity", "sdk/go/enum", "core/poll", "build/engine/pluginapi/**", "build/engine/exec/publish", "build/engine/exec/tools", "core/telemetry", "build/daemon/build", "build/engine/engine/evaluate", "build/engine/engine/graph", "build/engine/engine/plan", "build/engine/engine/release", "build/engine/engine/source", "build/engine/exec", "build/engine/exec/access", "build/engine/exec/download", "build/engine/exec/program", "build/engine/exec/run", "build/engine/history", "build/engine/model", "build/engine/wire", "build/engine/pluginhost", "common/record", "common/vcs", "build/daemon/directory", "common/record/manifest", "core/page", "core/refusal"}},
	// A directory as a build reads it: the workspace's manifest in it,
	// and its repositories' history through the version control port.
	{"build/daemon/directory", []string{"build/daemon/directory", "common/record/manifest", "common/record", "common/vcs"}},
	// The workspace's manifest, which what fills a directory writes and
	// a build reads.
	{"common/record/manifest", []string{"common/record/manifest", "common/record/records", "common/record", "sdk/go/entity", "testsupport/testfiles"}},
	{"build/engine/history", []string{"sdk/go/api/**"}},
	// The plugin contract's Go, generated for the daemon alone.
	{"build/engine/pluginapi/**", []string{"sdk/go/api/**"}},
	// What a worker's runner is told to start for a test, generated
	// from the contracts (api/buf.gen.runner.yaml): no API, the server's
	// own.
	{"common/runner/runnerapi/**", []string{"sdk/go/api/**"}},
	// The daemon's internal API, generated from the contracts
	// (api/buf.gen.source.yaml): the daemon serves it and the control
	// plane alone asks it, through the one translation between it and
	// the workspace API (common/told). Nothing a client
	// links names it.
	{"common/told/sourceapi/**", []string{"sdk/go/api/**", "common/told/sourceapi/**"}},
	// How a List is cut into pages, and how a service refuses by name:
	// shared by the services, and importing nothing of this repository.
	{"core/page", nil},
	{"core/refusal", nil},
	{"build/engine/model", []string{}},
	{"build/engine/wire", []string{"sdk/go/api/**", "build/engine/pluginapi/**", "build/engine/model"}},
	{"build/engine/pattern", []string{}},
	{"build/engine/pins", []string{}},
	{"build/engine/pluginhost", []string{"sdk/go/api/**", "sdk/go/entity", "build/engine/pluginapi/**", "core/telemetry", "build/engine/pattern", "core/plain", "common/runner/testservice"}},
	{"build/engine/pluginhost/conformance", []string{"sdk/go/api/**", "build/engine/pluginapi/**", "build/engine/pluginhost"}},
	{"build/engine/engine/evaluate", []string{"sdk/go/api/**", "sdk/go/entity", "build/engine/pluginapi/**", "core/telemetry", "build/engine/engine/catalog", "build/engine/engine/config", "build/engine/engine/evaluate", "build/engine/engine/graph", "build/engine/engine/internal/yamlnode", "build/engine/engine/plan", "build/engine/engine/source", "build/engine/model", "build/engine/wire", "build/engine/pins", "build/engine/pluginhost", "build/engine/pattern", "common/record", "common/runner/testservice", "common/runner/runnerapi/**"}},
	{"build/engine/engine/catalog", []string{"sdk/go/api/**", "sdk/go/entity", "build/engine/pluginapi/**"}},
	{"build/engine/engine/release", []string{"sdk/go/api/**", "sdk/go/entity", "build/engine/pluginapi/**", "build/engine/engine/release", "build/engine/engine/config", "build/engine/model", "common/vcs"}},
	{"build/engine/engine/graph", []string{"build/engine/pattern", "sdk/go/entity", "build/engine/engine/graph", "build/engine/engine/config", "build/engine/model", "common/record"}},
	{"build/engine/engine/plan", []string{"build/engine/pattern", "sdk/go/api/**", "sdk/go/entity", "build/engine/pluginapi/**", "build/engine/engine/plan", "build/engine/engine/graph", "build/engine/model", "common/record"}},
	{"build/engine/engine/config", []string{"sdk/go/api/**", "sdk/go/entity", "build/engine/pluginapi/**", "build/engine/model", "build/engine/engine/catalog", "build/engine/engine/internal/yamlnode", "common/runner/testservice"}},
	{"build/engine/engine/source", []string{"build/engine/engine/source", "build/engine/pattern"}},
	{"build/engine/engine/internal/yamlnode", []string{}},
	{"build/engine/exec", []string{}},
	// How a grid that asks who a daemon is, is asked: one answer, for the
	// grid's dial and its asset service's, and for nothing else.
	{"build/engine/exec/access", []string{}},
	{"build/engine/exec/reapi", []string{"core/telemetry", "build/engine/exec", "build/engine/exec/access", "common/confine"}},
	{"build/engine/exec/download", []string{"core/telemetry", "build/engine/exec", "build/engine/exec/access"}},
	{"build/engine/exec/tools", []string{"core/telemetry", "build/engine/exec", "build/engine/pins", "common/runner/testservice", "common/confine"}},
	{"build/engine/exec/tools/toolstest", []string{"build/engine/exec"}},
	{"build/engine/pluginhost/pluginhosttest", []string{"sdk/go/api/**", "build/engine/pluginapi/**", "build/engine/pattern"}},
	{"build/engine/engine/catalog/catalogtest", []string{}},
	{"build/engine/engine/source/sourcetest", []string{"build/engine/engine/source"}},
	{"build/engine/exec/reapi/reapitest", []string{"build/engine/exec/reapi"}},
	{"build/engine/engine/plan/plantest", []string{"build/engine/engine/plan", "build/engine/model"}},
	{"build/engine/exec/stage", []string{"core/telemetry", "build/engine/exec", "build/engine/exec/download", "build/engine/exec/tools", "build/engine/engine/plan", "build/engine/engine/source", "sdk/go/api/**", "build/engine/pluginapi/**", "common/confine", "core/image", "common/runner/testservice", "common/runner/runnerapi/**"}},
	{"build/engine/exec/schedule", []string{"core/telemetry", "build/engine/exec", "build/engine/engine/plan", "build/engine/model"}},
	{"build/engine/exec/here", []string{"build/engine/exec", "build/engine/exec/tools", "build/engine/engine/source", "build/engine/pluginapi/**"}},
	{"build/engine/exec/publish", []string{"common/confine", "build/engine/exec", "build/engine/exec/here", "build/engine/exec/tools", "build/engine/engine/plan", "build/engine/engine/source", "core/plain", "build/engine/pluginhost"}},
	{"build/engine/exec/fix", []string{"core/telemetry", "build/engine/exec", "build/engine/exec/here", "build/engine/exec/tools", "build/engine/engine/plan", "build/engine/engine/source", "build/engine/model", "build/engine/pattern", "build/engine/pins"}},
	{"build/engine/exec/program", []string{"sdk/go/api/**", "build/engine/pluginapi/**", "build/engine/exec", "build/engine/exec/tools", "build/engine/engine/plan", "build/engine/model", "common/confine"}},
	{"build/engine/exec/run", []string{"sdk/go/api/**", "sdk/go/entity", "build/engine/pluginapi/**", "core/telemetry", "build/engine/exec", "build/engine/exec/download", "build/engine/exec/tools", "build/engine/exec/fix", "build/engine/exec/stage", "build/engine/exec/schedule", "build/engine/engine/plan", "build/engine/engine/source", "build/engine/model", "common/runner/runnerapi/**"}},
	{"workspace/box/source/**", []string{"sdk/go/api/**", "sdk/go/entity", "workspace/box/source/**", "core/refusal", "common/told/sourceapi/**", "common/confine", "common/record/records", "common/record/manifest", "common/record", "common/vcs", "common/vcs/chosen", "common/told"}},
	{"common/record", []string{"sdk/go/entity", "common/record"}},
	// A daemon told, for a test, what the control plane would tell it:
	// its entities resolved through a fake Backstage.
	{"workspace/server/toldtest", []string{"sdk/go/api/**", "sdk/go/entity", "workspace/server/backstage", "common/told", "common/told/sourceapi/**", "testsupport/backstagetest"}},
	// What a box's daemon is told of what a workspace is asked for, and
	// what it says back: the control plane's and the daemon's alike.
	{"common/told", []string{"sdk/go/api/**", "sdk/go/entity", "common/record", "common/told/sourceapi/**"}},
	{"common/vcs", []string{"common/vcs"}},
	{"common/vcs/git", []string{"common/vcs", "common/git"}},
	// The one package that names the version control system the server's
	// programs use, which each program that composes one is given by.
	{"common/vcs/chosen", []string{"common/vcs/git"}},
	// A sandbox is the isolation and knows nothing of what lives in one.
	// A driver's tests, and the service's, are held to the conformance
	// and run over the driver held in memory (drivertest).
	{"sandbox/**", []string{"sdk/go/api/**", "sandbox/**", "core/image", "sandbox/testsupport/drivertest", "core/page", "core/refusal", "core/plain", "common/record/records", "core/poll"}},
	{"sandbox/driver/openshell", []string{"sdk/go/api/**", "sandbox/**", "sandbox/testsupport/drivertest", "core/plain", "core/poll", "core/ports"}},
	// A driver of the container runtime, for tests alone: what an image
	// starts is asked through the image package, as the gateway's way
	// asks it. Its tests start a box and ask its daemon.
	// Test support of the sandbox's own, beside it: each piece says what
	// it imports, and one that says nothing imports nothing.
	{"sandbox/testsupport/**", []string{}},
	{"sandbox/testsupport/testcontainer", []string{"sandbox/testsupport/**", "sandbox/driver", "core/image", "testsupport/**", "core/ports", "sdk/go/api/**", "common/told", "common/told/sourceapi/**"}},
	// The control plane keeps workspaces over the sandboxes they live in,
	// and asks the version control system through its port (vcs.Fetching)
	// what reaching a repository takes. It tells a workspace's daemon
	// what to hold through the daemon's source API, and reads back what
	// it holds.
	// Its tests hold it over the real sandbox service, over the driver
	// that holds sandboxes in memory (drivertest.Memory): that driver is
	// for tests, and is refused to what is none. It opens a box's ports
	// (core/ports), and is told nothing of the stack.
	// It resolves entities through the Backstage its operator gives it
	// (workspace/server/backstage), held over a fake one in its tests.
	{"workspace/server/controlplane/**", []string{"sdk/go/api/**", "sandbox/api/**", "sdk/go/entity", "workspace/server/controlplane/**", "sandbox", "workspace/server/backstage", "core/secretfile", "common/record", "common/vcs", "common/told", "common/told/sourceapi/**", "sandbox/testsupport/drivertest", "testsupport/backstagetest", "core/plain", "core/ports", "sdk/go", "core/page", "core/refusal", "common/record/records", "core/poll"}},
	// A test starts a daemon as frontseatd does, and a control plane as
	// frontseat-server does, each with the test support beside it.
	{"specs/buildtest", []string{"build/daemon/build", "core/telemetry", "testsupport/**", "sdk/go/api/**", "workspace/box/source", "common/told", "workspace/server/toldtest", "common/told/sourceapi/**", "common/vcs/chosen"}},
	// A fake Backstage, of entities written as catalog files write them.
	{"testsupport/backstagetest", []string{"sdk/go/entity"}},
	// What any sandbox driver is held to, and the driver held in memory.
	{"sandbox/testsupport/drivertest", []string{"sandbox/driver", "sandbox/testsupport/drivertest", "core/poll"}},
	// It serves the sandbox service beneath the plane to the test alone,
	// and says how the plane's owner is known as the SDK's clients take it.
	{"workspace/server/controlplanetest", []string{"sandbox/api/**", "workspace/server/controlplane", "core/page", "workspace/server/backstage", "sandbox", "testsupport/**", "sdk/go/api/**", "sdk/go", "common/vcs/chosen"}},
	// The daemon: a thin main over the package that is it, which reads
	// its flags and serves the build API over what it is told.
	{"cmd/frontseatd", []string{"build/daemon/daemon"}},
	// A box's supervisor: a thin main over what holds the sandbox's lock,
	// fills the workspace as the control plane tells it and serves its
	// shell, and knows no build.
	{"cmd/frontseat-box", []string{"workspace/box/supervisor"}},
	{"workspace/box/supervisor", []string{"workspace/box/source", "core/httpserver", "core/telemetry", "core/ports", "common/told/sourceapi/**", "common/record/buildfile"}},
	// What a build daemon is told, read from one file: the build's own
	// types, and nothing of a workspace, a sandbox or a control plane.
	{"build/daemon/settings", []string{"build/daemon/build", "common/record/buildfile"}},
	// A build daemon's life, for a directory: the build it serves, what
	// it is told, and where a workspace's manifest is. Nothing of a box,
	// a sandbox or a control plane, so that the command line may link it.
	{"build/daemon/life", []string{"build/daemon/build", "build/daemon/settings", "common/record/manifest", "common/vcs/chosen", "core/httpserver", "core/telemetry"}},
	{"build/daemon/daemon", []string{"build/daemon/build", "build/daemon/settings", "build/daemon/life", "workspace/box/source", "workspace/box/supervisor", "core/httpserver", "core/telemetry", "core/ports", "common/vcs/chosen"}},
	// The local stack's control plane: the control plane, told what the
	// stack is by the stack's own package.
	{"cmd/frontseat-server", []string{"workspace/server/controlplane", "workspace/server/backstage", "core/httpserver", "core/secretfile", "core/telemetry", "stack/local", "core/ports", "common/vcs/chosen"}},
	// What brings the local stack up and down from a checkout, and builds
	// the box and the stack's image: the stack's own packages alone.
	{"cmd/frontseat-stack", []string{"stack", "stack/box", "stack/local"}},
	{"cmd/frontseat-confine", []string{"common/confine"}},
	// The grid's runner: a thin main over the runner, which serves the
	// sandbox API over the container runtime to itself.
	{"cmd/frontseat-runner", []string{"grid/runner"}},
	// The runner keeps the grid's pool of runner sandboxes with the
	// OpenShell driver itself: their bound directories and private policy
	// are the driver's to say, and no client's of the sandbox API.
	{"grid/runner", []string{"sandbox/api/**", "core/page", "sandbox", "sandbox/driver", "sandbox/driver/openshell", "core/httpserver", "core/image", "sdk/go/api/**", "sdk/go/entity", "common/runner/testservice", "common/confine", "sdk/go", "common/runner/runnerapi/**", "core/poll"}},
	// The box's one build, asked by the spec that holds a box to what it
	// was built with; no other spec builds one. That spec asks the
	// stack's Podman as the stack does (local.Podman).
	// The command line's specs read what a build daemon the command line
	// started said of itself, and stop it.
	{"specs/cli", []string{"sdk/go", "sdk/go/api/**", "sandbox/api/**", "sdk/go/entity", "testsupport/**", "core/ports", "build/daemon/life"}},
	{"specs/local", []string{"testsupport/**", "stack/local", "core/ports", "stack/box"}},
	{"specs/compare", []string{"sdk/go", "sdk/go/api/**", "sandbox/api/**", "testsupport/**"}},
	{"specs/**", []string{"sdk/go", "sdk/go/api/**", "sandbox/api/**", "sdk/go/entity", "testsupport/**", "core/telemetry", "specs/internal/**", "core/ports"}},
	// The gate programs: what runs the specs and every other test, and
	// builds the box before the tests that need it; what reads a gate's
	// command, and what the contracts' generators are held to.
	{"specs/cmd/**", []string{"specs/internal/**", "testsupport/**", "stack/local"}},
	// The gate builds the box by its one build, which no test does, and
	// asks that the stack answers where core/ports says, before the
	// tests that need it.
	{"specs/cmd/gate", []string{"specs/internal/**", "testsupport/**", "stack/local", "stack/box", "core/ports"}},
	{"specs/internal/**", []string{"specs/internal/**"}},
}

var forTests = []string{"testsupport", "stack/testsupport", "sandbox/testsupport", "workspace/server/controlplanetest", "specs", "workspace/server/toldtest", "build/engine/pluginhost/conformance", "build/engine/exec/tools/toolstest", "build/engine/engine/source/sourcetest", "build/engine/exec/reapi/reapitest", "build/engine/engine/plan/plantest", "build/engine/pluginhost/pluginhosttest", "build/engine/engine/catalog/catalogtest"}

func beneath(path, root string) bool { return path == root || strings.HasPrefix(path, root+"/") }

// theBuild are the daemon's packages: the build service, its engine,
// its execution and its plugin host, and buildtest, and not its binary.
var theBuild = []string{"build/daemon/build", "build/engine/engine", "build/engine/exec", "build/engine/history", "build/engine/model", "build/engine/pattern", "build/engine/pins", "build/engine/pluginapi", "build/engine/pluginhost", "build/engine/wire", "specs/buildtest"}

// ofTheBuild is whether a directory is of the daemon's build.
func ofTheBuild(dir string) bool {
	return slices.ContainsFunc(theBuild, func(root string) bool { return beneath(dir, root) })
}

// starters are the test support that starts an implementation: a daemon,
// a control plane, a sandbox held in memory.
var starters = []string{"specs/buildtest", "workspace/server/controlplanetest", "sandbox/testsupport/drivertest"}

func testSupport(path string) bool {
	for _, root := range forTests {
		if beneath(path, root) {
			return true
		}
	}
	return false
}

// forATest is whether a Go file is a test's and no program's: a test
// file, or a file of the test support, wherever it is (forTests).
func forATest(file string) bool {
	file = filepath.ToSlash(file)
	return strings.HasSuffix(file, "_test.go") || testSupport(filepath.ToSlash(filepath.Dir(file)))
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
	if test && (path == dir || path == "core/telemetry") {
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
		driven := dir == containerDriver && path == "sandbox/testsupport/drivertest"
		if starts && !driven && (beneath(dir, "testsupport") || besideItsModule(dir)) && !slices.ContainsFunc(starters, func(root string) bool { return beneath(path, root) && beneath(dir, root) }) {
			return false
		}
		// Acceptance specs can start implementations only in their setup file.
		if starts && beneath(dir, "specs") {
			return filepath.Base(file) == "support_test.go"
		}
		if (beneath(path, "testsupport") || besideItsModule(path)) && !starts {
			return true
		}
	}
	// Internal unit tests can inspect their feature's collaborators, but cannot
	// bypass the feature boundary or make a client link a server. They read
	// a stream to its end as any client of the API does, with the SDK's
	// reader (sdk/go).
	if test && ofTheBuild(dir) && (beneath(path, "sdk/go/api") || path == "sdk/go/entity" || path == "sdk/go" || beneath(path, "build/engine/pluginapi") || path == "build/engine/engine/source" || path == "build/engine/exec/reapi") {
		return true
	}
	// A server's test reads what it was refused by its reason as any
	// client of the API does, with the SDK's one reader of it
	// (frontseat.Reason).
	if test && path == "sdk/go" && slices.ContainsFunc(serverParts, func(part string) bool { return beneath(dir, part) }) {
		return true
	}
	// Evaluation's tests load a real plugin as the host's own tests do.
	if test && (dir == "build/engine/pluginhost" || dir == "build/engine/engine/evaluate") && path == "build/engine/pluginhost/conformance" {
		return true
	}
	// A test is given a repository held in memory as pluginhosttest holds
	// it, and the catalog files catalogtest writes.
	if test && ofTheBuild(dir) && (path == "build/engine/pluginhost/pluginhosttest" || path == "build/engine/engine/catalog/catalogtest") {
		return true
	}
	// A test whose grid runs nothing answers what the tools package asks
	// of a grid as toolstest does.
	if test && ofTheBuild(dir) && path == "build/engine/exec/tools/toolstest" {
		return true
	}
	// A test is given a repository of the files it names, as a run sees
	// it, as sourcetest gives it, and the test grid as reapitest dials it.
	if test && ofTheBuild(dir) && (path == "build/engine/engine/source/sourcetest" || path == "build/engine/exec/reapi/reapitest") {
		return true
	}
	// A test of the plan, and of what schedules it, is given the actions
	// it is about as plantest writes them.
	if test && ofTheBuild(dir) && path == "build/engine/engine/plan/plantest" {
		return true
	}
	if test && ofTheBuild(dir) && (path == "build/engine/pattern" || path == "build/engine/pins") {
		return true
	}
	// The control plane's tests, and the build's, are given the version
	// control system the programs choose (chosen.System), so that what
	// they prove is proven of that system; the control plane and the
	// build ask it through its port alone, and are given it by the
	// program that composes them.
	if test && (beneath(dir, "workspace/server/controlplane") || dir == "build/daemon/build") && path == "common/vcs/chosen" {
		return true
	}
	// The tests of what fills a workspace plant what the version control
	// system would find, with that system itself; what fills a workspace
	// is given it by the one package that chooses it.
	if test && beneath(dir, "workspace/box/source") && path == "common/vcs/git" {
		return true
	}
	// The gateway's driver's tests ask the container runtime what an
	// image starts, as the sandbox service does; the driver does not.
	if test && dir == "sandbox/driver/openshell" && path == "core/image" {
		return true
	}
	if file == "build/daemon/build/build.go" && path == "build/engine/exec/reapi" {
		return true
	}
	for _, allowed := range p.imports {
		if root, all := strings.CutSuffix(allowed, "/**"); all && beneath(path, root) || path == allowed {
			return true
		}
		// The test support is one thing to whoever may use it, wherever
		// each piece of it is: beside the module it starts, or at the top.
		if allowed == "testsupport/**" && besideItsModule(path) {
			return true
		}
	}
	return false
}

// besideItsModule is whether a package is test support that is beside
// the module it serves, and not at the top.
func besideItsModule(path string) bool {
	return beneath(path, "stack/testsupport") || beneath(path, "sandbox/testsupport") || beneath(path, "workspace/server/controlplanetest")
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
	"common/vcs/git":       "common/vcs/chosen",
	"cli/internal/vcs/git": "cli/internal/machine",
}

// gitItself is git as both version control packages run it and read
// its remotes: it names git, and only they import it.
const gitItself = "common/git"

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
const containerDriver = "sandbox/testsupport/testcontainer"

// driverPort is the port the sandbox service drives its drivers through,
// and gatewayDriver the one driver beneath it: the OpenShell gateway's.
const (
	driverPort    = "sandbox/driver"
	gatewayDriver = "sandbox/driver/openshell"
)

// containerRuntimeUses is what a file does that could make a sandbox
// with the container runtime where it may not: every sandbox a program
// makes is the OpenShell gateway's, and the driver of the container
// runtime is for tests alone. A test of the journey is held as a
// program is, since one that made a workspace so would prove nothing of
// what a person is given. A package beneath the drivers' port
// (sandbox/driver) that is not the gateway's driver, where there is none,
// is refused to tests too: it would be a second driver beside the
// gateway's, which the sandbox's one implementation leaves no room for.
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
		beside := beneath(path, driverPort) && path != driverPort && !beneath(path, gatewayDriver)
		if path != containerDriver && !beside {
			continue
		}
		if !test || strings.HasPrefix(file, "specs/") || beside {
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
		"a server's test":                 {"grid/runner/attempt_test.go", `package runner; import "` + driver + `"`, false},
		"the sandbox service's test":      {"sandbox/runner_test.go", `package sandbox_test; import "` + driver + `"`, false},
		"under another name, in a test":   {"sandbox/runner_test.go", `package sandbox_test; import made "` + driver + `"`, false},
		"the grid's runner":               {"grid/runner/runner.go", `package runner; import "` + driver + `"`, true},
		"the sandbox service":             {"sandbox/sandbox.go", `package sandbox; import made "` + driver + `"`, true},
		"a program":                       {"cmd/frontseat-server/main.go", `package main; import _ "` + driver + `"`, true},
		"a test of the journey":           {"specs/cli/local_test.go", `package cli; import "` + driver + `"`, true},
		"a driver beside the gateway's":   {"grid/runner/pool.go", `package runner; import "` + module + `/sandbox/driver/podman"`, true},
		"a driver beside, even in a test": {"sandbox/runner_test.go", `package sandbox_test; import "` + module + `/sandbox/driver/podman"`, true},
		"the gateway's driver":            {"grid/runner/pool.go", `package runner; import "` + module + `/sandbox/driver/openshell"`, false},
		"the drivers' port":               {"sandbox/sandbox.go", `package sandbox; import "` + module + `/sandbox/driver"`, false},
		"what an image starts, asked":     {"sandbox/sandbox.go", `package sandbox; import "` + module + `/core/image"`, false},
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
	outs := []string{"sdk/go/api", "build/engine/pluginapi"}
	held := []string{"sdk/go/api/frontseat/build/v1alpha1/run.pb.go", "sdk/go/api/x/xconnect/x.connect.go", "build/engine/pluginapi/p/p.pb.go", "build/daemon/build/build.go"}
	if stray := generatedElsewhere(held, outs); len(stray) != 0 {
		t.Errorf("generated Go where a template writes it was refused: %v", stray)
	}
	refused := []string{"build/daemon/build/hand.pb.go", "client/x.connect.go", "sdk/go/apix/y.pb.go"}
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

// clientShares is what the command line links of this repository beyond
// itself, the Go SDK and the build daemon (clientBuilds), each with why:
// nothing else of the repository is a client's.
var clientShares = map[string]string{
	"common/git":     "git, as frontseat runs it over a checkout: the command line reads a checkout of this machine with it",
	"common/confine": "what git ends a process group with, the confiner's one way past a sandbox's refusal of a signal to a group (owner's ruling, 2026-11-19)",
	"core/telemetry": "the command line's telemetry, which it hands the SDK's client",
	"core/ports":     "where the local stack's control plane is reached, said once",
	"stack/local":    "what the command line keeps on this machine is where the stack and the test support find it (local.Config)",
	"stack/image":    "the stack's files, which stack/local reads what the stack is from",
}

// clientBuilds is the build daemon, which the command line starts of
// itself for the directory it builds, as Bazel's and Buck2's clients do
// (owner, 2026-11-23): its life, what it is told, the build it serves
// and the engine beneath it, with what those are built on. It is the
// build's alone: nothing of a box, of a control plane, of a sandbox, of
// the grid's runner or of the stack's programs, and not frontseatd's
// program, which composes what fills a workspace.
var clientBuilds = []string{
	"build/daemon/life", "build/daemon/settings", "build/daemon/build", "build/daemon/directory", "build/engine",
	"common/record", "common/runner", "common/vcs",
	"core/httpserver", "core/image", "core/page", "core/plain", "core/poll", "core/refusal",
}

// notAClients says why a package the command line links is none it may
// link, or "" where it may: its own, the Go SDK's, one of clientShares,
// the build daemon's (clientBuilds), or no package of this repository.
// Test support is refused wherever it is, and so is every other
// package: a box's, a control plane's, a sandbox's, the runner's, the
// stack's programs, the specs.
func notAClients(linked string) string {
	path, local := strings.CutPrefix(linked, module+"/")
	switch {
	case !local || beneath(path, "cli") || beneath(path, "sdk/go") || clientShares[path] != "":
		return ""
	case testSupport(path):
		return "client links test support " + path
	case slices.ContainsFunc(clientBuilds, func(part string) bool { return beneath(path, part) }):
		return ""
	}
	return "client links " + path + ", which is no client's (clientShares, clientBuilds)"
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
	linked := map[string]bool{}
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
				var pkg struct{ ImportPath string }
				if err := decoder.Decode(&pkg); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				linked[strings.TrimPrefix(pkg.ImportPath, module+"/")] = true
				if why := notAClients(pkg.ImportPath); why != "" {
					t.Error(why)
				}
			}
		})
	}
	// The list holds what is linked and nothing kept for later.
	for shared := range clientShares {
		if !linked[shared] {
			t.Errorf("clientShares names %s, which the command line does not link", shared)
		}
	}
	for _, part := range clientBuilds {
		if !slices.ContainsFunc(slices.Collect(maps.Keys(linked)), func(path string) bool { return beneath(path, part) }) {
			t.Errorf("clientBuilds names %s, of which the command line links nothing", part)
		}
	}
}

// The controls: what the command line may link is allowed, and a
// package of a server, of the stack's programs or of the test support,
// wherever it is, is refused.
func TestWhatIsNoClientsIsFound(t *testing.T) {
	for linked, refused := range map[string]bool{
		"connectrpc.com/connect":                        false,
		module + "/cli/internal/cmd":                    false,
		module + "/sdk/go/api/frontseat/build/v1alpha1": false,
		module + "/common/git":                          false,
		module + "/stack/local":                         false,
		module + "/stack":                               true,
		module + "/stack/box":                           true,
		module + "/build/daemon/build":                  false,
		module + "/build/daemon/life":                   false,
		module + "/build/engine/model":                  false,
		module + "/common/vcs/chosen":                   false,
		module + "/build/daemon/daemon":                 true,
		module + "/workspace/box/supervisor":            true,
		module + "/build/engine/exec/tools/toolstest":   true,
		module + "/grid/runner":                         true,
		module + "/sandbox":                             true,
		module + "/workspace/server/controlplane":       true,
		module + "/workspace/box/source":                true,
		module + "/core/secretfile":                     true,
		module + "/testsupport/testfiles":               true,
		module + "/stack/testsupport/testgrid":          true,
		module + "/sandbox/testsupport/testsandbox":     true,
		module + "/workspace/server/controlplanetest":   true,
		module + "/workspace/server/toldtest":           true,
		module + "/specs/buildtest":                     true,
	} {
		if why := notAClients(linked); (why != "") != refused {
			t.Errorf("%s: %q, want refused %v", linked, why, refused)
		}
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
	for _, entry := range []string{"sandbox", "sandbox/driver", "sandbox/driver/openshell"} {
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
					// What is of this repository, in no SDK and not on the
					// list of what frontseat-enterprise may import, is the
					// repository's own: no one outside can name it.
					if own, of := strings.CutPrefix(path, module+"/"); of && !forOutsiders(own) {
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
	modules := workspaceModules(t, repositoryRoot(t))
	edges, _ := moduleRequires(t, repositoryRoot(t), modules)
	for dir, required := range edges {
		for _, local := range required {
			if !slices.Contains(modules, local) {
				t.Errorf("%s requires unowned module %s", dir, local)
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
const whatFills = "workspace/box/source"

// buildsOfWhatFills returns each file of the build, or of the directory
// it reads, that imports what fills the directory, its tests too.
func buildsOfWhatFills(imports map[string][]string) []string {
	var out []string
	for file, imported := range imports {
		file = filepath.ToSlash(file)
		if !strings.HasPrefix(file, "build/daemon/build/") && !strings.HasPrefix(file, "build/daemon/directory/") {
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
// the directory it reads, and their tests, import nothing of what fills
// a workspace, which the daemon's program starts beside them.
func TestTheBuildReadsItsDirectoryAndNothingOfWhatFillsIt(t *testing.T) {
	root := repositoryRoot(t)
	for _, file := range buildsOfWhatFills(importsUnder(t, root, root)) {
		t.Errorf("%s imports %s: a build reads its directory, and knows nothing of what fills it", file, whatFills)
	}
	// The controls: a file of the build that imports it is found, a test
	// of the build too; the daemon's program is not.
	planted := map[string][]string{
		"build/daemon/build/runs.go":      {module + "/" + whatFills},
		"build/daemon/build/run_test.go":  {module + "/" + whatFills},
		"build/daemon/daemon/daemon.go":   {module + "/" + whatFills},
		"build/daemon/directory/read.go":  {module + "/common/record"},
		"build/daemon/directory/other.go": {module + "/" + whatFills},
	}
	if found := buildsOfWhatFills(planted); !slices.Equal(found, []string{"build/daemon/build/run_test.go", "build/daemon/build/runs.go", "build/daemon/directory/other.go"}) {
		t.Errorf("of what was planted, found %v", found)
	}
}

// moduleOrder is the Go modules of the repository from the bottom up:
// each requires only modules beneath it in this list, which is what the
// repository is built of, in the order it is built. Go holds none of
// it: under go.work a package imports any module of the workspace,
// required or not. So what a package may import of another module is
// held here, import by import
// (TestAPackageImportsOnlyWhatItsModuleRequiresBeneathIt): the module is
// beneath its own in this list, and its go.mod requires it itself; the
// rules of each package (policies) are beside that, and finer. A module
// added goes where what it requires is beneath it; one that would need a
// module below to require one above is refused, and the code is cut
// instead.
var moduleOrder = []string{
	"sdk/go",
	// What a test of any module is helped by, and what every program is
	// built on.
	"testsupport",
	"core",
	// What some parts use and others do not, each a module of its own.
	"common/confine",
	"common/git",
	"common/vcs",
	"common/record",
	"common/told",
	"common/runner",
	"stack",
	"sandbox",
	"build/engine",
	"grid",
	"workspace/box",
	"build/daemon",
	"workspace/server",
	// The programs, each a thin main over the part it runs.
	"cmd/frontseat-confine",
	"cmd/frontseat-runner",
	"cmd/frontseatd",
	"cmd/frontseat-box",
	"cmd/frontseat-server",
	"cmd/frontseat-stack",
	"cli",
	"specs",
}

// moduleOf is the module a file or a package's directory is of: the
// longest module directory it is beneath; "" for one of none.
func moduleOf(path string) string {
	path = filepath.ToSlash(path)
	found := ""
	for _, dir := range moduleOrder {
		if beneath(path, dir) && len(dir) > len(found) {
			found = dir
		}
	}
	return found
}

// above returns each requirement that is not beneath the module that
// requires it, in an order of modules, as "module requires other".
func above(order []string, requires map[string][]string) []string {
	var out []string
	for dir, required := range requires {
		at := slices.Index(order, dir)
		for _, other := range required {
			if beneath := slices.Index(order, other); at < 0 || beneath < 0 || beneath >= at {
				out = append(out, dir+" requires "+other)
			}
		}
	}
	slices.Sort(out)
	return out
}

// The modules are built from the bottom up: each requires only modules
// beneath it in moduleOrder, and every module of the workspace is in it.
// So a change to one rebuilds what is above it and nothing beside it.
func TestAModuleRequiresOnlyWhatIsBeneathIt(t *testing.T) {
	root := repositoryRoot(t)
	modules := workspaceModules(t, root)
	if listed := slices.Sorted(slices.Values(moduleOrder)); !slices.Equal(listed, modules) {
		t.Fatalf("the order names %v, and the workspace's modules are %v", listed, modules)
	}
	requires, _ := moduleRequires(t, root, modules)
	for _, each := range above(moduleOrder, requires) {
		t.Errorf("%s, which is not beneath it: a module requires only what is beneath it in moduleOrder", each)
	}
	// The controls: a requirement beneath is allowed; one above, of
	// itself, or of a module the order does not name, is found.
	order := []string{"low", "middle", "high"}
	if found := above(order, map[string][]string{"high": {"low", "middle"}, "middle": {"low"}}); len(found) != 0 {
		t.Errorf("requirements beneath were found: %v", found)
	}
	if found := above(order, map[string][]string{"low": {"high"}, "middle": {"middle", "absent"}}); !slices.Equal(found, []string{"low requires high", "middle requires absent", "middle requires middle"}) {
		t.Errorf("of the requirements above, found %v", found)
	}
}

// moduleRequires reads, of each module, the modules of this repository
// its go.mod requires, by their directories: all of them, and those it
// requires itself, for what its own packages import (not // indirect).
func moduleRequires(t *testing.T, root string, modules []string) (all, itself map[string][]string) {
	t.Helper()
	all, itself = map[string][]string{}, map[string][]string{}
	for _, dir := range modules {
		var mod struct {
			Require []struct {
				Path     string
				Indirect bool
			}
		}
		goJSON(t, filepath.Join(root, dir), &mod, "mod", "edit", "-json")
		for _, require := range mod.Require {
			local, ok := strings.CutPrefix(require.Path, module+"/")
			if !ok {
				continue
			}
			all[dir] = append(all[dir], local)
			if !require.Indirect {
				itself[dir] = append(itself[dir], local)
			}
		}
	}
	return all, itself
}

// crossings returns each import, by a file, of a package of another
// module of the repository that its own module may not reach: one that
// is not beneath it in moduleOrder, or that its go.mod does not require
// itself. A test file is held as any other: a test builds with its
// module.
func crossings(requires, imports map[string][]string) []string {
	var out []string
	for file, imported := range imports {
		file = filepath.ToSlash(file)
		from := moduleOf(file)
		for _, each := range imported {
			path, local := strings.CutPrefix(each, module+"/")
			to := moduleOf(path)
			switch {
			case !local || to == from && to != "":
			case from == "" || to == "":
				out = append(out, fmt.Sprintf("%s imports %s: one of them is of no module in moduleOrder", file, path))
			case slices.Index(moduleOrder, to) > slices.Index(moduleOrder, from):
				out = append(out, fmt.Sprintf("%s imports %s, of the module %s, which is not beneath its own, %s, in moduleOrder", file, path, to, from))
			case !slices.Contains(requires[from], to):
				out = append(out, fmt.Sprintf("%s imports %s, of the module %s, which %s/go.mod does not require itself", file, path, to, from))
			}
		}
	}
	slices.Sort(out)
	return out
}

// Under go.work a package imports any module of the workspace, whatever
// its go.mod says. So every import, a test's too, of a package of
// another module is held to the order of the modules and to what the
// importing module's go.mod requires itself.
func TestAPackageImportsOnlyWhatItsModuleRequiresBeneathIt(t *testing.T) {
	root := repositoryRoot(t)
	_, requires := moduleRequires(t, root, workspaceModules(t, root))
	for _, each := range crossings(requires, importsUnder(t, root, root)) {
		t.Error(each)
	}
	// The controls: an import within a module, of a module beneath that
	// is required, and of what is not this repository's are allowed; an
	// import of a module above, of one beneath that go.mod does not
	// require, and of what is of no module are found, in a test too.
	required := map[string][]string{"core": {"testsupport"}, "stack": {"core"}, "sandbox": {"stack", "testsupport"}}
	allowed := map[string][]string{
		"stack/box/box.go":         {module + "/stack/local", module + "/core/ports", "os"},
		"core/poll/poll_test.go":   {module + "/testsupport/testrun"},
		"sandbox/service_test.go":  {module + "/stack/testsupport/testgrid", module + "/sandbox/testsupport/drivertest", "connectrpc.com/connect"},
		"sandbox/driver/driver.go": {module + "/sandbox"},
	}
	if found := crossings(required, allowed); len(found) != 0 {
		t.Errorf("imports beneath and required were found: %v", found)
	}
	refused := map[string][]string{
		"testsupport/testfiles/tree.go": {module + "/stack/local"},
		"core/poll/poll_test.go":        {module + "/sandbox/testsupport/testsandbox"},
		"sandbox/service.go":            {module + "/core/poll"},
		"stack/stack.go":                {module + "/server/internal/build"},
	}
	want := []string{
		"core/poll/poll_test.go imports sandbox/testsupport/testsandbox, of the module sandbox, which is not beneath its own, core, in moduleOrder",
		"sandbox/service.go imports core/poll, of the module core, which sandbox/go.mod does not require itself",
		"stack/stack.go imports server/internal/build: one of them is of no module in moduleOrder",
		"testsupport/testfiles/tree.go imports stack/local, of the module stack, which is not beneath its own, testsupport, in moduleOrder",
	}
	if found := crossings(required, maps.Clone(refused)); !slices.Equal(found, want) {
		t.Errorf("of the imports planted, found %v, want %v", found, want)
	}
}
