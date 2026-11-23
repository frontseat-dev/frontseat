package graph

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/model"
)

func TestColocatedPluginsKeepTheirOwnDependencyIdentity(t *testing.T) {
	consumer, provider := module("pkg[1]", "consumer", "provider"), module("pkg[1]", "provider", "consumer")
	consumer.Plugin, provider.Plugin = "consumer", "provider"
	for _, projects := range [][]Project{{consumer, provider}, {provider, consumer}} {
		g, err := Resolve(projects)
		if err != nil {
			t.Fatal(err)
		}
		handed := g.Handed(consumer)
		if len(handed) != 1 || handed[0].From.Plugin != "provider" {
			t.Fatalf("handed %#v; want the other plugin's source once, never the consumer's own", handed)
		}
	}
}

func TestDuplicateProvidersCannotHideBehindADirectory(t *testing.T) {
	one, two := module("same", "duplicate"), module("same", "duplicate")
	one.Plugin, two.Plugin = "one", "two"
	for _, projects := range [][]Project{{one, two}, {two, one}} {
		_, err := Resolve(projects)
		if err == nil || !strings.Contains(err.Error(), "one") || !strings.Contains(err.Error(), "two") {
			t.Fatalf("Resolve = %v; want both competing plugin identities", err)
		}
	}

	// Different coordinates are allowed in that same directory.
	two.Provides[0].Coordinate.Name = "different"
	if _, err := Resolve([]Project{one, two}); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateDeclarationsDoNotProduceTwoHandovers(t *testing.T) {
	provider := module("lib", "library")
	provider.Provides = append(provider.Provides, provider.Provides[0])
	if _, err := Resolve([]Project{provider}); err == nil {
		t.Fatal("duplicate declarations were accepted")
	}
}

func TestRepositoryAndRootAreSeparatePartsOfProjectIdentity(t *testing.T) {
	one, two := module("b", "one", "two"), module("", "two")
	one.Repository, two.Repository = "a", "a/b"
	one.Plugin, two.Plugin = "same", "same"
	g, err := Resolve([]Project{one, two})
	if err != nil {
		t.Fatal(err)
	}
	if handed := g.Handed(one); len(handed) != 1 || handed[0].From.Repository != "a/b" {
		t.Fatalf("handed %#v; repository and directory were collapsed into one string", handed)
	}
}

func TestARootProjectNeverReceivesItsOwnSourceThroughAnAlias(t *testing.T) {
	provider := module(".", "self", "self")
	provider.Plugin = "go"
	g, err := Resolve([]Project{provider})
	if err != nil {
		t.Fatal(err)
	}
	request := provider
	request.Root = ""
	if handed := g.Handed(request); len(handed) != 0 {
		t.Fatalf("handed itself: %#v", handed)
	}
	if _, found := g.ProvidedBy(model.Coordinate{Ecosystem: "go", Name: "self"}); !found {
		t.Fatal("self provider was lost")
	}
}

func TestCanonicalProjectIdentityIsUniqueAcrossAnalysisResults(t *testing.T) {
	for _, roots := range [][2]string{{"", "."}, {"pkg", "sub/../pkg"}, {"pkg", "pkg"}, {"pkg", "other"}} {
		first, second := module(roots[0], "one"), module(roots[1], "two")
		_, err := Resolve([]Project{first, second})
		if roots[1] == "other" {
			if err != nil {
				t.Fatalf("distinct directory control: %v", err)
			}
		} else if err == nil {
			t.Errorf("accepted duplicate identity with distinct coordinates: %q", roots)
		}
	}
}
