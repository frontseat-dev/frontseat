package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestPrivateMethodsReachableFromPublicConstructor(t *testing.T) {
	for _, c := range []struct {
		name, source string
		want         bool
	}{
		{"public-control", `package sandbox; import driver "github.com/frontseat-dev/frontseat/server/sandbox/driver"; type view struct{}; func New() view {return view{}}; func(view) Spec() driver.Spec {return driver.Spec{}}`, false},
		{"private-result", `package workspace; import git "github.com/frontseat-dev/frontseat/server/internal/vcs/git"; type view struct{}; func New() view {return view{}}; func(view) Repository() git.System {return git.System{}}`, true},
		{"generic-receiver", `package workspace; import git "github.com/frontseat-dev/frontseat/server/internal/vcs/git"; type View[T any] struct{}; func(*View[T]) Repository() git.System {return git.System{}}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), "view.go", c.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := publicLeaks([]*ast.File{f})
			if (len(got) > 0) != c.want {
				t.Fatalf("private public-method exposure: got %v; want refusal %v", got, c.want)
			}
		})
	}
}
