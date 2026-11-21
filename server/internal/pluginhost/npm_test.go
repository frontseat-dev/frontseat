package pluginhost_test

import (
	"errors"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"strings"
	"testing"
	"time"
)

const npmPackage = `{"name":"web","scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.2"}}`
const npmIntegrity = "sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng=="
const npmLock = `{"lockfileVersion":3,"packages":{"":{},"node_modules/jest":{"resolved":"https://registry.npmjs.org/jest/-/jest-30.5.2.tgz","integrity":"` + npmIntegrity + `"}}}`

func npmContext() *pluginv1.Context {
	return &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}
}

func npmFixture() conformance.Fixture {
	context := npmContext()
	return arrangingNothing(context, conformance.Fixture{
		Name: "npm",
		Description: &pluginv1.DescribeResponse{Name: "npm", Abi: 24,
			Interests: []string{"**/package.json", "!**/node_modules/**", "!**/testdata/**"}},
		Source: files{"app/package.json": npmPackage, "app/package-lock.json": npmLock},
		Absent: files{}, Malformed: files{"app/package.json": `{"name":"web"}`}, ErrorCode: "INVALID_MANIFEST",
		Analyze: &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}},
		Projects: []*pluginv1.Project{{Root: "app", Name: "web", Kind: "npm-package", Manifest: "app/package.json", Provides: []*pluginv1.Provide{{Coordinate: &pluginv1.Coordinate{Ecosystem: "javascript-install", Name: "web"}, Artifacts: []*pluginv1.Produced{{Action: "install", Output: "node_modules/"}}}}, Actions: []*pluginv1.Action{
			{Name: "fetch", Command: &pluginv1.Command{Arguments: []string{"npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"}}, Inputs: []*pluginv1.Input{pluginhost.SourceInput("package.json"), pluginhost.SourceInput("package-lock.json")}, Outputs: []*pluginv1.Output{{Path: "~/.npm/_cacache/", Optional: true}}, Tools: []string{"node"}, Fetch: true},
			{Name: "install", Command: &pluginv1.Command{Arguments: []string{"npm", "ci", "--offline", "--no-audit", "--no-fund"}}, Inputs: []*pluginv1.Input{pluginhost.SourceInput("package.json"), pluginhost.SourceInput("package-lock.json"), pluginhosttest.OutputInput("fetch")}, Outputs: []*pluginv1.Output{{Path: "node_modules/", Optional: true}}, Tools: []string{"node"}},
		}}},
		Reads: []conformance.Observation{
			{Import: "read", Argument: "app/package.json", Found: true},
			{Import: "read", Argument: "app/npm-shrinkwrap.json"},
			{Import: "read", Argument: "app/yarn.lock"},
			{Import: "read", Argument: "app/pnpm-lock.yaml"},
			{Import: "read", Argument: "app/.npmrc"},
			{Import: "read", Argument: "app/binding.gyp"},
			{Import: "read", Argument: "app/package-lock.json", Found: true},
		},
	})
}

func TestNpmRefusesPreparationSemanticsItDoesNotServe(t *testing.T) {
	plugin := conformance.Plugin(t, "npm")
	cases := map[string]func(files, *pluginv1.Context){
		"workspace": func(s files, _ *pluginv1.Context) {
			s["app/package.json"] = strings.Replace(npmPackage, `"name":"web"`, `"name":"web","workspaces":["packages/*"]`, 1)
		},
		"package manager": func(s files, _ *pluginv1.Context) {
			s["app/package.json"] = strings.Replace(npmPackage, `"name":"web"`, `"name":"web","packageManager":"pnpm@11"`, 1)
		},
		"install hook": func(s files, _ *pluginv1.Context) {
			s["app/package.json"] = strings.Replace(npmPackage, `"test":"jest"`, `"test":"jest","prepare":"node build.js"`, 1)
		},
		"shrinkwrap":          func(s files, _ *pluginv1.Context) { s["app/npm-shrinkwrap.json"] = npmLock },
		"alternate lock":      func(s files, _ *pluginv1.Context) { s["app/pnpm-lock.yaml"] = "lockfileVersion: 9.0" },
		"config":              func(s files, _ *pluginv1.Context) { s["app/.npmrc"] = "ignore-scripts=true" },
		"implicit root build": func(s files, _ *pluginv1.Context) { s["app/binding.gyp"] = "{}" },
		"missing lock":        func(s files, _ *pluginv1.Context) { delete(s, "app/package-lock.json") },
		"old lock": func(s files, _ *pluginv1.Context) {
			s["app/package-lock.json"] = strings.Replace(npmLock, `"lockfileVersion":3`, `"lockfileVersion":2`, 1)
		},
		"no integrity": func(s files, _ *pluginv1.Context) {
			s["app/package-lock.json"] = strings.Replace(npmLock, `,"integrity":"`+npmIntegrity+`"`, "", 1)
		},
		"git": func(s files, _ *pluginv1.Context) {
			s["app/package-lock.json"] = strings.Replace(npmLock, "https://registry.npmjs.org/jest/-/jest-30.5.2.tgz", "git+https://example.com/jest.git", 1)
		},
		"link": func(s files, _ *pluginv1.Context) {
			s["app/package-lock.json"] = strings.Replace(npmLock, `"resolved":`, `"link":true,"resolved":`, 1)
		},
	}
	invoke := func(s files, c *pluginv1.Context) error {
		_, err := plugin.Call(t.Context(), s, "Analyze", &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: c}, new(pluginv1.AnalyzeResponse))
		return err
	}
	valid := func() files {
		return files{"app/package.json": npmPackage, "app/package-lock.json": npmLock, "app/jest.config.cjs": "throw new Error('analysis must never execute JavaScript');"}
	}
	if err := invoke(valid(), npmContext()); err != nil {
		t.Fatal(err)
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			source, context := valid(), npmContext()
			change(source, context)
			var failure *pluginhost.Error
			// What npm reads and the plugin does not build yet is refused
			// as not served, each by its own reason; what is wrong, as
			// invalid.
			want := map[string]string{
				"workspace": "WORKSPACES_NOT_SERVED", "package manager": "PACKAGE_MANAGER_NOT_SERVED",
				"install hook": "INSTALL_HOOKS_NOT_SERVED", "shrinkwrap": "SHRINKWRAP_NOT_SERVED",
				"alternate lock": "PNPM_NOT_SERVED", "config": "NPMRC_NOT_SERVED",
				"implicit root build": "NATIVE_ADDONS_NOT_SERVED",
			}[name]
			if want == "" {
				want = "INVALID_MANIFEST"
			}
			if err := invoke(source, context); !errors.As(err, &failure) || failure.Reason != want {
				t.Fatalf("unsupported npm preparation not refused by name: %v", err)
			}
		})
	}
	if err := invoke(valid(), npmContext()); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkNpmAnalyze(b *testing.B) {
	plugin := conformance.Plugin(b, "npm")
	source := files{"app/package.json": npmPackage, "app/package-lock.json": npmLock}
	request := &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: npmContext()}
	benchmarkAnalysis(b, plugin, "npm", source, request)
}

// `mise exec -- go test -run '^$' -bench '^BenchmarkNpmAnalyze$' -benchmem
// -count=3 ./server/internal/pluginhost` measured 137–139 us and 708
// allocations on 2026-11-14, including tracked JSON reads and host validation.
func TestBudgetOfNpmAnalysis(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 1500 * time.Microsecond, Allocs: 760}, BenchmarkNpmAnalyze)
}
