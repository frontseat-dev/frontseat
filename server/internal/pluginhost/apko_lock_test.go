package pluginhost_test

import (
	"path"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
)

// reviewApkoLock is a lock of one package and, where one is given, one
// key, written as apko writes one.
func reviewApkoLock(config, key, apk string) string {
	keyring := ""
	if key != "" {
		keyring = `{"name": "k", "url": "` + key + `", "content": "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"}`
	}
	return `{"version": "v1", "config": {"name": "` + config + `", "checksum": "sha256-x"}, "contents": {"keyring": [` + keyring + `], "packages": [
		{"name": "a", "url": "` + apk + `", "version": "1-r0", "architecture": "aarch64",
		 "signature": {"range": "", "checksum": ""}, "control": {"range": "bytes=0-2671", "checksum": "sha1-rRT/lrbBVxEZdhyr2qqt3S8ghjc="},
		 "data": {"range": "bytes=2672-14621", "checksum": "sha256-5RP/3NK4uU2QTgSOj9J8QxZHFkBTX6Rna6y2uCrw36o="}, "checksum": "Q1rRT/lrbBVxEZdhyr2qqt3S8ghjc="}]}}`
}

func reviewApkoActions(t *testing.T, source files) ([]*pluginv1.Action, error) {
	t.Helper()
	module, err := testmodule.Plugin("apko")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := analyzeApko(t, conformance.Load(t, module), source)
	if err != nil {
		return nil, err
	}
	return answer.GetAnalyses()[0].GetProjects()[0].GetActions(), nil
}

// Review (2026-11-10). The keys apko discovers for an Alpine repository
// and writes into the lock are named with an "@"
// (apko 26953696, pkg/apk/apk/releases.go KeysFor: "%20" becomes "@";
// internal/cli/lock.go discoverKeysForLock writes each URL to the lock).
// lock.rs place() takes only letters, digits and "._+-" in a step, so
// every lock apko writes of an Alpine image is refused, and the README's
// list of what is refused ("another scheme, a port, a query, an escape")
// does not say so. The control is the same lock with Wolfi's key.
func TestReviewApkoTakesTheLockApkoWritesOfAnAlpineImage(t *testing.T) {
	const apk = "https://dl-cdn.alpinelinux.org/alpine/v3.22/main/aarch64/musl-1.2.5-r10.apk"
	config := "contents:\n  repositories:\n    - https://dl-cdn.alpinelinux.org/alpine/v3.22/main\n  packages: [musl]\n"
	with := func(key string) files {
		return files{"app/apko.yaml": config, "app/apko.lock.json": reviewApkoLock("apko.yaml", key, apk)}
	}
	if _, err := reviewApkoActions(t, with("https://packages.wolfi.dev/os/wolfi-signing.rsa.pub")); err != nil {
		t.Fatalf("control refused: %v", err)
	}
	if _, err := reviewApkoActions(t, with("https://alpinelinux.org/keys/alpine-devel@lists.alpinelinux.org-616ae350.rsa.pub")); err != nil {
		t.Errorf("the lock of an Alpine image, with the key apko discovers for it, is refused: %v", err)
	}
}

// Review. apko keeps a package in the directory its file is named for
// with ".apk" taken off once (cache.go cacheDirForPackage:
// strings.TrimSuffix(p, ".apk")); lock.rs takes it off as often as it
// stands (trim_end_matches). For x.apk.apk apko reads .../x.apk/ and the
// fetch leaves .../x/, so the offline build finds nothing; and x.apk and
// x.apk.apk get one fetch name.
func TestReviewApkoKeepsAPackageWhereApkoLooksWhateverItsName(t *testing.T) {
	actions, err := reviewApkoActions(t, files{
		"app/apko.yaml":      "contents:\n  packages: [a]\n",
		"app/apko.lock.json": reviewApkoLock("apko.yaml", "", "https://packages.wolfi.dev/os/aarch64/a-1-r0.apk.apk"),
	})
	if err != nil {
		t.Logf("refused, which is sound too: %v", err)
		return
	}
	const want = "~/.cache/dev.chainguard.go-apk/https%3A%2F%2Fpackages.wolfi.dev%2Fos/aarch64/a-1-r0.apk/"
	if got := actions[0].GetOutputs()[0].GetPath(); got != want {
		t.Errorf("the fetch leaves %s; apko's cacheDirForPackage reads %s", got, want)
	}
}

// Review. server/AGENTS.md, "A name read as a pattern": what comes from the
// disk is a name and is never joined into a pattern. The lock's
// config.name is a file's name, checked only for "/", "\\" and a leading
// "-", and actions.rs puts it into the image action's inputs, which are
// patterns ("!" excludes, "[1]" is a class, ":" a reference). A
// configuration named apko[1].yaml is read by config.rs by its name, and
// the build is then given its neighbour apko1.yaml and not itself.
func TestReviewApkoHandsTheConfigurationByItsNameNotAsAPattern(t *testing.T) {
	config := "contents:\n  packages: [a]\n"
	for name, neighbour := range map[string]string{"apko[1].yaml": "apko1.yaml", "apko?.yaml": "apko2.yaml", "a*.yaml": "another.yaml"} {
		t.Run(name, func(t *testing.T) {
			actions, err := reviewApkoActions(t, files{
				"app/" + name:        config,
				"app/" + neighbour:   "contents:\n  packages: [not-this-one]\n",
				"app/apko.lock.json": reviewApkoLock(name, "", "https://packages.wolfi.dev/os/aarch64/a-1-r0.apk"),
			})
			if err != nil {
				t.Logf("refused, which is sound: %v", err)
				return
			}
			image := actions[len(actions)-1]
			for _, input := range image.GetInputs() {
				if input.GetOutput() != "" {
					continue
				}
				if matched, _ := path.Match(input.GetSource(), neighbour); matched {
					t.Errorf("%s reads %q, which as a pattern names the neighbour %s", image.GetName(), input, neighbour)
				}
			}
		})
	}
	// A configuration whose name excludes itself: the build is given no
	// configuration at all.
	actions, err := reviewApkoActions(t, files{
		"app/!apko.yaml":     config,
		"app/apko.lock.json": reviewApkoLock("!apko.yaml", "", "https://packages.wolfi.dev/os/aarch64/a-1-r0.apk"),
	})
	if err == nil {
		t.Errorf("a configuration named !apko.yaml is taken, and the build's inputs %q exclude it", actions[len(actions)-1].GetInputs())
	}
}
