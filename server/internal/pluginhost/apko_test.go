package pluginhost_test

import (
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"slices"
	"strings"
	"testing"
	"time"
)

// wolfiKey is Wolfi's signing key, as the fixture's lock holds it.
const wolfiKey = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEA5qQUHmzqX9oa/RS3OSgL
YTYB1mzvVmnWC0JXsAafdgRkavJ9xRhsXMKXWPKMghZw6UnkxuxQCZlB9UhaFqed
X7SWfQ8qRgECFmdGUjoeV7P3VUkg17RU0mUKsXZZF/2mU+jCGyk5eweKcLWrk6bn
lNNF1vBJ9EXbHdfvbQsA5GfLku5tJhqRJ4FVQAGCw+1JoSKu+o3q+6xP7z6kMfR9
cp54FSj48rTJUibbAv+cPtZ3AnbpjdyarV7SYMfxp14Q/KR0CJYDihG3CFoU+TaM
dr7LUrgXaYw7m7g8hA2PY1jF8aqDqVFjVu/csPKnKVQSqNHfm4C4gljwT6TDVhPe
6xKSGKfAUNvHx2RIqSsZJCh68Af+VnfuimbHMYzGhzV4/efihpJlYsDnOXJj/PeY
SbEIYG2yoI3AYvDFyFX2OAOtQ9d4TPs4zS4aDg4R3UseXLib46FX3ZTfIi1/W0IO
eC3AFU5mK+02jq+7swcGbgzem30dFA/B3n8NQExJjF2WlGNJxFw2WnJQqKIkIh0s
km5jaxJ28xYgik+bEgxy1LsilKTowdkbEPRKv36JGdap4dOEGR+LwZsM+ontF1AO
wY6tvwtZKJWhgj7ES3BdtiC1GA2htFnP90lA7KnKbSMOxwWtSCH8spitdPdzgI8G
SxrOb0SZs6ZFAxYYe3GlvS0CAwEAAQ==
-----END PUBLIC KEY-----
`

// Every action of the fixture's image, spelled out independently of the
// Rust implementation: a fetch of the key the lock holds, a fetch of each
// package of each architecture, cut into its control and data sections
// by the lock's ranges and kept under apko's cache by each checksum in
// hex, and an offline build for each architecture.
func expectedApkoActions() []*pluginv1.Action {
	const cache = "~/.cache/dev.chainguard.go-apk/"
	const key = "fetch/packages.wolfi.dev/os/wolfi-signing.rsa.pub"
	fetch := func(architecture, name string, control, data uint64, sha1, sha256 string) *pluginv1.Action {
		directory := cache + "https%3A%2F%2Fpackages.wolfi.dev%2Fos/" + architecture + "/" + name + "/"
		return &pluginv1.Action{
			Name: "fetch/packages.wolfi.dev/os/" + architecture + "/" + name + ".apk", Shared: true, Fetch: true,
			Downloads: []*pluginv1.Download{{Url: "https://packages.wolfi.dev/os/" + architecture + "/" + name + ".apk", Parts: []*pluginv1.Part{
				{Offset: 0, Length: control, Sha1: sha1, Path: directory + sha1 + ".ctl.tar.gz"},
				{Offset: control, Length: data, Sha256: sha256, Path: directory + sha256 + ".dat.tar.gz"},
			}}},
			Outputs: []*pluginv1.Output{{Path: directory}},
		}
	}
	image := func(name, architecture string) *pluginv1.Action {
		return &pluginv1.Action{
			Name:    name,
			Command: &pluginv1.Command{Arguments: []string{"apko", "build", "--offline", "--lockfile", "apko.lock.json", "--vcs=false", "--sbom=false", "--arch", architecture, "--", "apko.yaml", "image:latest", "oci/" + architecture}},
			Inputs:  []*pluginv1.Input{pluginhost.SourceInput("apko.yaml"), pluginhost.SourceInput("apko.lock.json"), pluginhosttest.OutputInput(key), pluginhosttest.OutputInput("fetch/packages.wolfi.dev/os/" + architecture + "/wolfi-baselayout-20230201-r31.apk"), pluginhosttest.OutputInput("fetch/packages.wolfi.dev/os/" + architecture + "/ca-certificates-bundle-20261020-r0.apk")},
			Outputs: []*pluginv1.Output{{Path: "oci/" + architecture + "/index.json"}, {Path: "oci/" + architecture + "/oci-layout"}, {Path: "oci/" + architecture + "/blobs/"}},
			Tools:   []string{"apko"},
		}
	}
	return []*pluginv1.Action{
		{
			Name: key, Shared: true, Fetch: true,
			Files:   []*pluginv1.File{{Path: cache + "https%3A%2F%2Fpackages.wolfi.dev%2F/os/wolfi-signing.rsa.pub/locked.etag", Content: []byte(wolfiKey)}},
			Outputs: []*pluginv1.Output{{Path: cache + "https%3A%2F%2Fpackages.wolfi.dev%2F/os/wolfi-signing.rsa.pub/"}},
		},
		fetch("aarch64", "wolfi-baselayout-20230201-r31", 2672, 11950, "ad14ff96b6c1571119761cabdaaaaddd2f208637", "e513ffdcd2b8b94d904e048e8fd27c4316471640535fa4676bacb6b82af0dfaa"),
		fetch("aarch64", "ca-certificates-bundle-20261020-r0", 10741, 107832, "5317ede8161f00cabc1781a2f4ced0d97ae8994b", "a49fd4ea44bc2c8ce9e321ef0f17ee548f1176f0b274416514dfd0acfbb61576"),
		fetch("x86_64", "wolfi-baselayout-20230201-r31", 2642, 11950, "5b87dfefc8736ed0e7e89f492514706e32a6291e", "acdd17b56e9c61e17cc448ea64de0e41d4f95d576aa4d1e0334f1579a42b1d11"),
		fetch("x86_64", "ca-certificates-bundle-20261020-r0", 10706, 107835, "f4fedfcc1f78e4fb91690127ce0f676a9891cca1", "dcba34a3b1269f5f76bfc9716e97dea183bd70de5f6dd35acbaab41d3f7dc0bd"),
		image("image-aarch64", "aarch64"),
		image("image-x86-64", "x86_64"),
	}
}

func apkoFixture(t *testing.T) conformance.Fixture {
	context := &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}
	return arrangingNothing(context, conformance.Fixture{
		Name: "apko", Description: &pluginv1.DescribeResponse{Name: "apko", Abi: 22, Interests: []string{"**/apko.lock.json", "!**/testdata/**"}},
		Source: fixtureSource(t, "apko", "valid"), Absent: files{}, Malformed: fixtureSource(t, "apko", "malformed"), ErrorCode: "INVALID_LOCK",
		Analyze:  &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}},
		Projects: []*pluginv1.Project{{Root: "app", Name: "app", Kind: "apko-image", Manifest: "app/apko.lock.json", Actions: expectedApkoActions()}},
		Reads:    []conformance.Observation{{Import: "read", Argument: "app/apko.lock.json", Found: true}, {Import: "read", Argument: "app/apko.yaml", Found: true}},
	})
}

// analyzeApko asks the real module for the image in app/ of some files.
func analyzeApko(t testing.TB, plugin *pluginhost.Plugin, source files) (*pluginv1.AnalyzeResponse, error) {
	t.Helper()
	request := &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}}
	response := new(pluginv1.AnalyzeResponse)
	if _, err := plugin.Call(t.Context(), source, "Analyze", request, response); err != nil {
		return nil, err
	}
	return response, pluginhost.CheckAnalysis("apko", request.Roots, response)
}

// An image is built only from what its fetches are given and its build
// reads: a configuration that would have apko read anything else, a file
// included, a base image, a repository that is a directory or a key that
// is a file, is refused by name, each beside the same configuration
// taken without it.
func TestApkoRefusesAConfigurationThatReadsWhatNoActionIsGiven(t *testing.T) {
	plugin := conformance.Plugin(t, "apko")
	valid := fixtureSource(t, "apko", "valid")
	config := valid["app/apko.yaml"]
	with := func(config string) files {
		return files{"app/apko.lock.json": valid["app/apko.lock.json"], "app/apko.yaml": config}
	}
	for name, config := range map[string]string{
		"as it is":                          config,
		"an empty include":                  config + "include: \"\"\n",
		"a tagged https repository":         strings.Replace(config, "- https://packages.wolfi.dev/os\n", "- https://packages.wolfi.dev/os\n    - '@extra https://packages.cgr.dev/extras'\n", 1),
		"a key written in the file":         strings.Replace(config, "  packages:", "  runtime_keyring:\n    - name: mine\n      content: key\n  packages:", 1),
		"paths, accounts and an entrypoint": config + "entrypoint:\n  command: /bin/sh\naccounts:\n  run-as: 65532\npaths:\n  - path: /work\n    type: directory\n",
	} {
		if _, err := analyzeApko(t, plugin, with(config)); err != nil {
			t.Fatalf("%s was refused, so this test proves nothing: %v", name, err)
		}
	}
	for name, config := range map[string]string{
		"an include":                              config + "include: ../base/apko.yaml\n",
		"a base image":                            strings.Replace(config, "  packages:", "  baseimage:\n    image: ./base\n    apkindex: ./base/APKINDEX\n  packages:", 1),
		"a repository that is a directory":        strings.Replace(config, "- https://packages.wolfi.dev/os\n", "- https://packages.wolfi.dev/os\n    - ./packages\n", 1),
		"a build repository over http":            strings.Replace(config, "  keyring:", "  build_repositories:\n    - http://packages.wolfi.dev/os\n  keyring:", 1),
		"a runtime repository of a file":          strings.Replace(config, "  keyring:", "  runtime_repositories:\n    - /var/packages\n  keyring:", 1),
		"a tagged repository that is a directory": strings.Replace(config, "- https://packages.wolfi.dev/os\n", "- '@local /work/packages'\n", 1),
		"a key that is a file":                    strings.Replace(config, "- https://packages.wolfi.dev/os/wolfi-signing.rsa.pub", "- ./melange.rsa.pub", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := analyzeApko(t, plugin, with(config))
			if err == nil || !strings.Contains(err.Error(), "not served yet") {
				t.Errorf("err = %v; want it refused as not served yet", err)
			}
		})
	}
	t.Run("no configuration", func(t *testing.T) {
		_, err := analyzeApko(t, plugin, files{"app/apko.lock.json": valid["app/apko.lock.json"]})
		if err == nil || !strings.Contains(err.Error(), "is not there") {
			t.Errorf("err = %v; want the missing configuration named", err)
		}
	})
}

// A lock is what a fetch is cut by and a build is held to, and it is
// committed by whoever wrote the image: one that is not what apko writes
// is refused, each beside the lock apko wrote.
func TestApkoRefusesALockThatIsNotWhatApkoWrites(t *testing.T) {
	plugin := conformance.Plugin(t, "apko")
	valid := fixtureSource(t, "apko", "valid")
	lock := valid["app/apko.lock.json"]
	with := func(lock string) files {
		return files{"app/apko.lock.json": lock, "app/apko.yaml": valid["app/apko.yaml"]}
	}
	if _, err := analyzeApko(t, plugin, with(lock)); err != nil {
		t.Fatalf("the lock apko wrote was refused, so this test proves nothing: %v", err)
	}
	// apko names the configuration as it was named to `apko lock`: run
	// from the repository's root, with the directories that lead to it.
	// It is the file of that name beside the lock all the same.
	for _, named := range []string{"app/apko.yaml", "../images/app/apko.yaml", "./apko.yaml"} {
		answer, err := analyzeApko(t, plugin, with(strings.Replace(lock, `"name": "apko.yaml",`, `"name": "`+named+`",`, 1)))
		if err != nil {
			t.Fatalf("a lock written of %s was refused: %v", named, err)
		}
		actions := answer.GetAnalyses()[0].GetProjects()[0].GetActions()
		if image := actions[len(actions)-1]; image.GetInputs()[0].GetSource() != "apko.yaml" || !slices.Contains(image.GetCommand().GetArguments(), "apko.yaml") || slices.Contains(image.GetCommand().GetArguments(), named) && named != "apko.yaml" {
			t.Errorf("a lock written of %s: the build reads %v and runs %v; want the file beside the lock", named, image.GetInputs(), image.GetCommand().GetArguments())
		}
	}
	change := func(old, new string) string {
		t.Helper()
		if !strings.Contains(lock, old) {
			t.Fatalf("the fixture's lock does not hold %q", old)
		}
		return strings.Replace(lock, old, new, 1)
	}
	const apk = "https://packages.wolfi.dev/os/aarch64/wolfi-baselayout-20230201-r31.apk"
	for name, lock := range map[string]string{
		"another version":                              change(`"version": "v1"`, `"version": "v2"`),
		"no configuration named":                       change(`"name": "apko.yaml",`, `"name": "",`),
		"a configuration that is a directory":          change(`"name": "apko.yaml",`, `"name": "images/",`),
		"a configuration that is the directory above":  change(`"name": "apko.yaml",`, `"name": "images/..",`),
		"a configuration named as an option":           change(`"name": "apko.yaml",`, `"name": "--offline=false",`),
		"a configuration named as a class":             change(`"name": "apko.yaml",`, `"name": "apko[1].yaml",`),
		"a configuration named as a wildcard":          change(`"name": "apko.yaml",`, `"name": "a*.yaml",`),
		"a configuration named as an exclusion":        change(`"name": "apko.yaml",`, `"name": "!apko.yaml",`),
		"a configuration named as a reference":         change(`"name": "apko.yaml",`, `"name": ":fetch",`),
		"a configuration named as an input set":        change(`"name": "apko.yaml",`, `"name": "=sources",`),
		"a package over http":                          change(apk, "http://packages.wolfi.dev/os/aarch64/wolfi-baselayout-20230201-r31.apk"),
		"a package that is a file":                     change(apk, "/work/packages/aarch64/wolfi-baselayout-20230201-r31.apk"),
		"a package that climbs":                        change(apk, "https://packages.wolfi.dev/os/../../wolfi-baselayout-20230201-r31.apk"),
		"a package with a query":                       change(apk, apk+"?token=x"),
		"a package with an escape":                     change(apk, "https://packages.wolfi.dev/os/aarch64/wolfi%2Dbaselayout-20230201-r31.apk"),
		"a package with a port":                        change(apk, "https://packages.wolfi.dev:8443/os/aarch64/wolfi-baselayout-20230201-r31.apk"),
		"a package that is no apk":                     change(apk, "https://packages.wolfi.dev/os/aarch64/wolfi-baselayout-20230201-r31.tar.gz"),
		"a package at its host's root":                 change(apk, "https://packages.wolfi.dev/wolfi-baselayout-20230201-r31.apk"),
		"an architecture that is a path":               change(`"architecture": "aarch64",`, `"architecture": "../aarch64",`),
		"an architecture that is an option":            change(`"architecture": "aarch64",`, `"architecture": "--offline=false",`),
		"an architecture as OCI names it":              change(`"architecture": "aarch64",`, `"architecture": "arm64",`),
		"every architecture":                           change(`"architecture": "aarch64",`, `"architecture": "all",`),
		"a package named for its ending alone":         change(apk, "https://packages.wolfi.dev/os/aarch64/.apk"),
		"a control section with no range":              change(`"range": "bytes=0-2671"`, `"range": ""`),
		"a control section that ends before it starts": change(`"range": "bytes=0-2671"`, `"range": "bytes=2671-0"`),
		"a control checksum that is a sha256":          change(`"checksum": "sha1-rRT/lrbBVxEZdhyr2qqt3S8ghjc="`, `"checksum": "sha256-5RP/3NK4uU2QTgSOj9J8QxZHFkBTX6Rna6y2uCrw36o="`),
		"a data checksum that is a sha1":               change(`"checksum": "sha256-5RP/3NK4uU2QTgSOj9J8QxZHFkBTX6Rna6y2uCrw36o="`, `"checksum": "sha1-rRT/lrbBVxEZdhyr2qqt3S8ghjc="`),
		"a checksum that is not the control section's": change(`"checksum": "Q1rRT/lrbBVxEZdhyr2qqt3S8ghjc="`, `"checksum": "Q1Uxft6BYfAMq8F4Gi9M7Q2XromUs="`),
		"a checksum with no kind":                      change(`"checksum": "Q1rRT/lrbBVxEZdhyr2qqt3S8ghjc="`, `"checksum": "rRT/lrbBVxEZdhyr2qqt3S8ghjc="`),
		"a key that is not held":                       change(`"content": "-----BEGIN PUBLIC KEY-----`, `"comment": "-----BEGIN PUBLIC KEY-----`),
		"a key that is a file":                         change("https://packages.wolfi.dev/os/wolfi-signing.rsa.pub", "/etc/apk/keys/wolfi-signing.rsa.pub"),
		"a key that is not named as one":               change("https://packages.wolfi.dev/os/wolfi-signing.rsa.pub", "https://packages.wolfi.dev/os/keys.json"),
		"no package":                                   `{"version": "v1", "config": {"name": "apko.yaml", "checksum": "sha256-x"}, "contents": {"keyring": [], "packages": []}}`,
		"not JSON":                                     "version: v1\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Refused by the plugin, of the lock: not by the host of
			// what the plugin answered, nor for a file that is missing.
			_, err := analyzeApko(t, plugin, with(lock))
			if err == nil || !strings.Contains(err.Error(), "app/apko.lock.json") {
				t.Errorf("err = %v; want the lock refused, by its name", err)
			}
		})
	}
}

// A package the lock holds twice alike is fetched once; twice with
// different sections, it is refused.
func TestApkoFetchesAPackageHeldTwiceOnceAndRefusesItHeldDifferently(t *testing.T) {
	plugin := conformance.Plugin(t, "apko")
	entry := func(control string) string {
		return `{"name": "a", "url": "https://packages.wolfi.dev/os/aarch64/a-1-r0.apk", "version": "1-r0", "architecture": "aarch64",
			"signature": {"range": "", "checksum": ""}, "control": {"range": "` + control + `", "checksum": "sha1-rRT/lrbBVxEZdhyr2qqt3S8ghjc="},
			"data": {"range": "bytes=2672-14621", "checksum": "sha256-5RP/3NK4uU2QTgSOj9J8QxZHFkBTX6Rna6y2uCrw36o="}, "checksum": "Q1rRT/lrbBVxEZdhyr2qqt3S8ghjc="}`
	}
	lock := func(entries ...string) files {
		return files{
			"app/apko.yaml":      "contents:\n  packages: [a]\n",
			"app/apko.lock.json": `{"version": "v1", "config": {"name": "apko.yaml", "checksum": "sha256-x"}, "contents": {"packages": [` + strings.Join(entries, ",") + `]}}`,
		}
	}
	answer, err := analyzeApko(t, plugin, lock(entry("bytes=0-2671"), entry("bytes=0-2671")))
	if err != nil {
		t.Fatal(err)
	}
	if actions := answer.GetAnalyses()[0].GetProjects()[0].GetActions(); len(actions) != 2 || actions[0].GetName() != "fetch/packages.wolfi.dev/os/aarch64/a-1-r0.apk" || len(actions[1].GetInputs()) != 3 {
		t.Errorf("actions = %v; want one fetch and one build that reads it once", actions)
	}
	if _, err := analyzeApko(t, plugin, lock(entry("bytes=0-2671"), entry("bytes=1-2671"))); err == nil || !strings.Contains(err.Error(), "twice, differently") {
		t.Errorf("err = %v; want the package held twice differently refused", err)
	}
}

func BenchmarkApkoAnalyze(b *testing.B) {
	plugin := conformance.Plugin(b, "apko")
	source := fixtureSource(b, "apko", "valid")
	for b.Loop() {
		if _, err := analyzeApko(b, plugin, source); err != nil {
			b.Fatal(err)
		}
	}
}

// `mise exec -- go test -run '^$' -bench '^BenchmarkApkoAnalyze$' -benchmem
// -count=3 ./build/internal/pluginhost`, in server/, measured 426–472 us and
// 1250–1251 allocations on 2026-11-10, including the tracked reads of the lock
// and the configuration and host validation of the answer.
func TestBudgetOfApkoAnalysis(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 3500 * time.Microsecond, Allocs: 1315}, BenchmarkApkoAnalyze)
}
