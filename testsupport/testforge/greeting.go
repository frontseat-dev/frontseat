package testforge

import (
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// Greeting writes, in a directory that is to be a repository, what a
// build of more than one project and more than one ecosystem is of: the
// Go library greet, which says a word and has a test; the Go program
// hello, which calls it; the Go module aside, with a test, which reads
// neither and is read by neither; and the npm package web, which has
// nothing to install. Go and Node are pinned as this repository pins
// them. Nothing is committed: whoever clones the directory commits it
// (Committed).
//
// The word is what greet says (Greets). Given one no run before gave,
// every action of greet and of hello, which reads greet, is one no grid
// has kept; those of aside and web are the same from run to run.
func Greeting(t testing.TB, directory, word string) {
	t.Helper()
	testpins.Go(t, directory)
	testpins.Node(t, directory)
	for name, content := range map[string]string{
		"frontseat.yaml":        "plugins:\n  go: {}\n  npm: {}\n  backstage: {}\n",
		"greet/go.mod":          "module example.com/greet\n\ngo 1.27\n",
		"greet/greet.go":        Greets(word),
		"greet/greet_test.go":   "package greet\n\nimport \"testing\"\n\nfunc TestSaysSomething(t *testing.T) {\n\tif Hello() == \"\" {\n\t\tt.Fatal(\"greet says nothing\")\n\t}\n}\n",
		"hello/go.mod":          "module example.com/hello\n\ngo 1.27\n\nrequire example.com/greet v0.0.0\n",
		"hello/main.go":         "package main\n\nimport \"example.com/greet\"\n\nfunc main() { println(greet.Hello()) }\n",
		"aside/go.mod":          "module example.com/aside\n\ngo 1.27\n",
		"aside/aside.go":        "// Package aside is read by nothing else.\npackage aside\n\n// Kept is what it keeps.\nconst Kept = true\n",
		"aside/aside_test.go":   "package aside\n\nimport \"testing\"\n\nfunc TestKeeps(t *testing.T) {\n\tif !Kept {\n\t\tt.Fatal(\"aside keeps nothing\")\n\t}\n}\n",
		"web/package.json":      `{"name":"web","version":"1.0.0"}`,
		"web/package-lock.json": `{"name":"web","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"web","version":"1.0.0"}}}`,
	} {
		write(t, directory, name, content)
	}
}

// Greets is greet's one source file, saying a word: what a spec writes
// over it to change the library, and with it what reads the library.
func Greets(word string) string {
	return "package greet\n\n// Hello is a greeting.\nfunc Hello() string { return \"" + word + "\" }\n"
}

// GreetingActions are the actions a build of Greeting is made of, each
// as it is printed for a repository that is the whole directory built
// (//greet:go.lint); a workspace that holds the repository under a name
// prints that name first (//code/greet:go.lint). Changed are those a
// change to what greet says reaches: greet's own, and hello's, which
// reads greet. Unchanged are the rest.
var GreetingActions = struct{ Changed, Unchanged []string }{
	Changed: []string{
		"//greet:go.compile", "//greet:go.compile-tests", "//greet:go.format", "//greet:go.lint", "//greet:go.test/.",
		"//hello:go.compile", "//hello:go.format", "//hello:go.lint",
	},
	Unchanged: []string{
		"//aside:go.compile", "//aside:go.compile-tests", "//aside:go.format", "//aside:go.lint", "//aside:go.test/.",
		"//web:npm.fetch", "//web:npm.install",
	},
}
