package testforge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// GeneratedGo writes a Buf workspace, its generated Go module's handwritten
// metadata, and a downstream consumer. No generated file is present. Both
// build and sandbox specs use this same real ecosystem fixture.
func GeneratedGo(t testing.TB, directory string) {
	t.Helper()
	mod, sum := generatedModule(t, directory)
	for name, content := range map[string]string{
		"frontseat.yaml":           "plugins: {go: {}, buf: {}}\n",
		"api/buf.yaml":             "version: v2\nmodules: [{path: .}]\n",
		"api/buf.gen.yaml":         "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen/go\n    opt: paths=source_relative\ninputs:\n  - directory: .\n",
		"api/hello/v1/hello.proto": "syntax = \"proto3\";\npackage hello.v1;\noption go_package = \"example.com/generated/hello/v1\";\nmessage Hello { string message = 1; }\n",
		"api/gen/go/go.mod":        strings.Replace(mod, generatedPath, "example.com/generated", 1),
		"api/gen/go/go.sum":        sum,
		"api/gen/go/keep.go":       "package generated\nconst Kept = true\n",
		"app/go.mod":               strings.Replace(mod, generatedPath, "example.com/application", 1) + "\nrequire example.com/generated v0.0.0\n",
		"app/go.sum":               sum,
		"app/main.go":              "package main\nimport hello \"example.com/generated/hello/v1\"\nfunc main() { _ = hello.Hello{Message: \"generated\"} }\n",
	} {
		write(t, directory, name, content)
	}
}

// GeneratedElsewhere writes a Buf workspace in the Component api whose
// template buf.gen.lib.yaml generates into lib/gen, a directory of the Go
// module of the Component lib, and a consumer app of that module. lib's
// catalog file says that api generates lib/gen. No generated file is
// present. The catalog plugin is listed beside Go and Buf.
func GeneratedElsewhere(t testing.TB, directory string) {
	t.Helper()
	mod, sum := generatedModule(t, directory)
	for name, content := range map[string]string{
		"frontseat.yaml":           "plugins: {go: {}, buf: {}, backstage: {}}\n",
		"api/catalog-info.yaml":    ConsentingComponent("api", ""),
		"api/buf.yaml":             "version: v2\nmodules: [{path: .}]\n",
		"api/buf.gen.lib.yaml":     "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: ../lib/gen\n    opt: paths=source_relative\ninputs:\n  - directory: .\n",
		"api/hello/v1/hello.proto": "syntax = \"proto3\";\npackage hello.v1;\noption go_package = \"example.com/lib/gen/hello/v1\";\nmessage Hello { string message = 1; }\n",
		"lib/catalog-info.yaml":    ConsentingComponent("lib", "    generated:\n      - directory: gen\n        by: component:api\n"),
		"lib/go.mod":               strings.Replace(mod, generatedPath, "example.com/lib", 1),
		"lib/go.sum":               sum,
		"lib/lib.go":               "// Package lib is written by hand.\npackage lib\n",
		"app/go.mod":               strings.Replace(mod, generatedPath, "example.com/application", 1) + "\nrequire example.com/lib v0.0.0\n",
		"app/go.sum":               sum,
		"app/main.go":              "package main\nimport hello \"example.com/lib/gen/hello/v1\"\nfunc main() { _ = hello.Hello{Message: \"generated\"} }\n",
	} {
		write(t, directory, name, content)
	}
}

// ConsentingComponent is the catalog document of a Component of a name,
// with what is given after spec.frontseat: written beneath it, two
// spaces in; nothing gives it no spec.frontseat.
func ConsentingComponent(name, frontseat string) string {
	document := "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: " + name + "}\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n"
	if frontseat != "" {
		document += "  frontseat:\n" + frontseat
	}
	return document
}

// generatedPath is the module path of the Go SDK, where this repository's
// generated Go is, whose go.mod and go.sum the fixtures take and rename:
// each module requires what the SDK requires.
const generatedPath = "github.com/frontseat-dev/frontseat/sdk/go"

// generatedModule pins the directory as this repository pins its tools,
// and returns the Go SDK's go.mod and go.sum.
func generatedModule(t testing.TB, directory string) (mod, sum string) {
	t.Helper()
	testpins.Own(t, directory)
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	readMod, err := os.ReadFile(filepath.Join(root, "sdk/go/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	readSum, err := os.ReadFile(filepath.Join(root, "sdk/go/go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	return string(readMod), string(readSum)
}
