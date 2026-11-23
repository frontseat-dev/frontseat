package graph

import (
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"testing"
)

func TestRepositoryRootOwnershipIncludesChildrenAndYieldsToNestedOwners(t *testing.T) {
	root := record.Located{Ref: entity.New("component", "", "root")}
	nested := record.Located{Ref: entity.New("component", "", "nested"), Root: "a"}
	for _, entities := range [][]record.Located{{root, nested}, {nested, root}} {
		for at, want := range map[string]record.Located{"": root, ".": root, "b": root, "a": nested, "a/deep": nested} {
			got, found := Owner(entities, at)
			if !found || got != want {
				t.Errorf("Owner(%q) = %v,%v; want %v", at, got, found, want)
			}
		}
		for _, at := range []string{"../outside", "/outside"} {
			if _, found := Owner(entities, at); found {
				t.Errorf("root owns non-workspace path %q", at)
			}
		}
	}
}
