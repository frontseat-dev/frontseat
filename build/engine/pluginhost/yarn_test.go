package pluginhost_test

import (
	"errors"
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/pluginhosttest"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

const yarnRoot = `{"name":"portal","private":true,"workspaces":["packages/*"]}`
const yarnLock = "__metadata:\n  version: 8\n  cacheKey: 10c0\n\n" +
	"\"is-number@npm:7.0.0\":\n  version: 7.0.0\n  resolution: \"is-number@npm:7.0.0\"\n  checksum: 10c0/b4686d0d3053146095ccd45346461bc8e53b80aeb7671cc52a4de02dbbf7dc0d1d2a986e2fe4ae206984b4d34ef37e8b795ebc4f4295c978373e6575e295d811\n  languageName: node\n  linkType: hard\n\n" +
	"\"portal@workspace:.\":\n  version: 0.0.0-use.local\n  resolution: \"portal@workspace:.\"\n  languageName: unknown\n  linkType: soft\n"

// yarnLaunch is how the install starts Yarn, telling node-gyp the pinned
// Node's directory: the one above the program that runs.
const yarnLaunch = "const {spawnSync} = require(\"node:child_process\"); const {dirname} = require(\"node:path\"); const run = spawnSync(\"yarn\", process.argv.slice(1), {stdio: \"inherit\", env: {...process.env, npm_config_nodedir: dirname(dirname(process.execPath))}}); if (run.error) { console.error(`yarn could not be started: ${run.error.message}`); process.exit(127); } if (run.signal) { process.kill(process.pid, run.signal); } process.exit(run.status);"

func yarnContext() *pluginv1.Context {
	return &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT, Tools: map[string]string{"yarn": "4.13.0"}}
}

func yarnSource() files {
	return files{
		"app/package.json":                       yarnRoot,
		"app/yarn.lock":                          yarnLock,
		"app/.yarnrc.yml":                        "nodeLinker: node-modules\n",
		"app/packages/web/package.json":          `{"name":"web","dependencies":{"is-number":"7.0.0"}}`,
		"app/packages/web/jest.config.cjs":       "throw new Error('analysis must never execute JavaScript');",
		"app/packages/backend/package.json":      `{"name":"backend"}`,
		"app/packages/web/testdata/package.json": `{"name":"fixture"}`,
	}
}

func yarnFixture() conformance.Fixture {
	context := yarnContext()
	yarn := func(arguments ...string) *pluginv1.Command {
		environment := map[string]string{
			"YARN_IGNORE_PATH": "1", "YARN_ENABLE_GLOBAL_CACHE": "false", "YARN_CACHE_FOLDER": ".yarn/cache",
			"YARN_NPM_REGISTRY_SERVER": "https://registry.npmjs.org", "YARN_ENABLE_TELEMETRY": "false",
			"YARN_CHECKSUM_BEHAVIOR": "throw", "YARN_ENABLE_STRICT_SSL": "true",
		}
		if arguments[len(arguments)-1] != "--immutable-cache" {
			return &pluginv1.Command{Arguments: append([]string{"yarn"}, arguments...), Environment: environment}
		}
		// Offline, Yarn is started by the pinned Node, which tells
		// node-gyp where its own headers are.
		environment["YARN_ENABLE_NETWORK"] = "false"
		environment["npm_config_build_from_source"] = "true"
		return &pluginv1.Command{Arguments: append([]string{"node", "-e", yarnLaunch}, arguments...), Environment: environment}
	}
	read := []*pluginv1.Input{
		pluginhost.SourceInput("package.json"), pluginhost.SourceInput("yarn.lock"), pluginhost.SourceInput(".yarn/patches/**"),
		pluginhost.SourceInput(".yarnrc.yml"), pluginhost.SourceInput("packages/*/package.json"),
	}
	malformed := yarnSource()
	malformed["app/yarn.lock"] = strings.Replace(yarnLock, "  checksum: 10c0/", "  shasum: 10c0/", 1)
	return arrangingNothing(context, conformance.Fixture{
		Name: "yarn",
		ABI:  25, Description: &pluginv1.DescribeResponse{Name: "yarn", Interests: []string{"**/package.json", "!**/node_modules/**", "!**/testdata/**"}},
		Source: yarnSource(),
		Absent: files{"package.json": `{"name":"root","workspaces":["app"]}`, "yarn.lock": yarnLock, "app/package.json": `{"name":"web"}`}, Malformed: malformed, ErrorCode: "INVALID_LOCK",
		Analyze: &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}},
		Projects: []*pluginv1.Project{{Root: "app", Name: "portal", Kind: "yarn-install", Manifest: "app/package.json", Provides: []*pluginv1.Provision{{Coordinate: &pluginv1.Coordinate{Ecosystem: "javascript-install", Name: "portal"}, Artifacts: []*pluginv1.OutputRef{{Action: "install", Output: "node_modules/"}, {Action: "install", Output: "packages/backend/node_modules/"}, {Action: "install", Output: "packages/web/node_modules/"}}}, {Coordinate: &pluginv1.Coordinate{Ecosystem: "yarn-cache", Name: "portal"}, Artifacts: []*pluginv1.OutputRef{{Action: "fetch", Output: ".yarn/cache/"}}}}, Actions: []*pluginv1.Action{
			{Name: "fetch", Command: yarn("install", "--immutable", "--mode=skip-build"), Inputs: read, Outputs: []*pluginv1.Output{{Path: ".yarn/cache/", Optional: true}}, Tools: []string{"node", "yarn"}, Kind: &pluginv1.Action_Fetch{Fetch: &pluginv1.Fetch{}}},
			{Name: "install", Command: yarn("install", "--immutable", "--immutable-cache"), Inputs: append(append([]*pluginv1.Input{}, read...), pluginhosttest.OutputInput("fetch")), Outputs: []*pluginv1.Output{{Path: "node_modules/", Optional: true}, {Path: "packages/backend/node_modules/", Optional: true}, {Path: "packages/web/node_modules/", Optional: true}}, Tools: []string{"node", "yarn"}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
		}}},
		Reads: []conformance.Observation{
			{Import: "read", Argument: "app/yarn.lock", Found: true},
			{Import: "read", Argument: "app/package.json", Found: true},
			{Import: "read", Argument: "app/.yarnrc.yml", Found: true},
			{Import: "glob", Argument: "packages/*/package.json\x00!**/node_modules/**", Directory: "app", Found: true},
			{Import: "read", Argument: "app/packages/backend/package.json", Found: true},
			{Import: "read", Argument: "app/packages/web/package.json", Found: true},
		},
	})
}

// What Yarn reads and the plugin does not build yet is refused by its
// own reason through the real module; the control is a workspace, which
// is built; and npm refuses a Yarn workspace by name.
func TestYarnRefusesWhatItDoesNotServeAndNpmRefusesItsWorkspaces(t *testing.T) {
	yarn := conformance.Plugin(t, "yarn")
	npm := conformance.Plugin(t, "npm")
	analyze := func(plugin *pluginhost.Plugin, s files, roots ...string) (*pluginv1.AnalyzeResponse, error) {
		var answer pluginv1.AnalyzeResponse
		_, err := plugin.Call(t.Context(), s, "Analyze", &pluginv1.AnalyzeRequest{Roots: roots, Context: yarnContext()}, &answer)
		return &answer, err
	}
	if _, err := analyze(yarn, yarnSource(), "app"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		file, content, reason string
	}{
		"plug'n'play":   {"app/.yarnrc.yml", "nodeLinker: pnp\n", "LINKER_NOT_SERVED"},
		"yarn 1":        {"app/yarn.lock", strings.Replace(yarnLock, "__metadata:\n  version: 8\n  cacheKey: 10c0\n", "# yarn lockfile v1\n", 1), "YARN_CLASSIC_NOT_SERVED"},
		"git":           {"app/yarn.lock", strings.Replace(yarnLock, `resolution: "is-number@npm:7.0.0"`, `resolution: "is-number@https://github.com/jonschlinkert/is-number.git#commit=98e8ff1"`, 1), "RESOLUTION_NOT_SERVED"},
		"install hook":  {"app/packages/web/package.json", `{"name":"web","scripts":{"postinstall":"node build.js"}}`, "INSTALL_HOOKS_NOT_SERVED"},
		"other manager": {"app/package.json", strings.Replace(yarnRoot, `"private":true`, `"packageManager":"pnpm@11.0.0"`, 1), "INVALID_PACKAGE_MANAGER"},
	} {
		t.Run(name, func(t *testing.T) {
			source := yarnSource()
			source[c.file] = c.content
			var failure *pluginhost.Error
			if _, err := analyze(yarn, source, "app"); !errors.As(err, &failure) || failure.Reason != c.reason {
				t.Fatalf("not refused by %s: %v", c.reason, err)
			}
		})
	}
	// npm refuses the install's root and a workspace by name: a
	// repository lists the yarn plugin in its place.
	for _, root := range []string{"app", "app/packages/web"} {
		var failure *pluginhost.Error
		if _, err := analyze(npm, yarnSource(), root); !errors.As(err, &failure) || failure.Reason != "YARN_NOT_SERVED" {
			t.Fatalf("npm did not refuse %s as Yarn's: %v", root, err)
		}
	}
}
