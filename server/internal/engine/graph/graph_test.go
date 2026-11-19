package graph

import (
	"errors"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// module is a project at a directory that provides a module and requires
// others.
func module(at, name string, requires ...string) Project {
	p := Project{Repository: "r", Root: at, Plugin: "go", Provides: []Provide{{Coordinate: model.Coordinate{Ecosystem: "go", Name: name}, Sources: []string{"**/*.go"}}}}
	for _, required := range requires {
		p.Requires = append(p.Requires, Requirement{Coordinate: model.Coordinate{Ecosystem: "go", Name: required}})
	}
	return p
}

func TestAProjectIsHandedWhatItRequiresAndWhatThatRequires(t *testing.T) {
	projects := []Project{module("a", "a", "b", "elsewhere"), module("b", "b", "c"), module("c", "c"), module("d", "d")}
	g, err := Resolve(projects)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, handed := range g.Handed(projects[0]) {
		got = append(got, handed.From.Root)
	}
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("a is handed %v, want b and then c", got)
	}
}

func TestProjectsThatRequireEachOtherAreEachHandedTheOtherOnce(t *testing.T) {
	projects := []Project{module("a", "a", "b"), module("b", "b", "a")}
	g, err := Resolve(projects)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Handed(projects[0]); len(got) != 1 || got[0].From.Root != "b" {
		t.Fatalf("a is handed %v, want b alone", got)
	}
}

func TestWhatNothingProvidesIsLeftToComeFromOutside(t *testing.T) {
	g, err := Resolve([]Project{module("a", "a", "elsewhere")})
	if err != nil {
		t.Fatal(err)
	}
	if _, held := g.ProvidedBy(model.Coordinate{Ecosystem: "go", Name: "elsewhere"}); held {
		t.Fatal("go:elsewhere is provided, want nothing to provide it")
	}
}

func TestTwoProjectsThatProvideOneCoordinateAreNamed(t *testing.T) {
	_, err := Resolve([]Project{module("one", "twin"), module("two", "twin")})
	var mistake *model.Mistake
	if !errors.As(err, &mistake) || mistake.Message != "go:twin is provided by r/one (go) and r/two (go)" {
		t.Fatalf("Resolve = %v", err)
	}
}
