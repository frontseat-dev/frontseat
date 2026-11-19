package source

import (
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/internal/vcs/git"
	"github.com/frontseat-dev/frontseat/server/internal/source/told"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
)

// The daemon resolves nothing: it is told repositories by name and where
// entities are by whole reference. A repository without a name, and an
// entity located by less than its whole reference, are refused
// SOURCE_UNFIT before anything is brought; so is a spec of entities
// alone. The control is the same, told whole.
func TestWhatTheDaemonIsToldIsWholeOrRefused(t *testing.T) {
	held, err := Open(t.TempDir(), false, git.System{})
	if err != nil {
		t.Fatal(err)
	}
	named := []*sourcev1.Checkout{{Name: "services", Remote: "file:///nowhere/services"}}
	for what, c := range map[string]struct {
		spec   *sourcev1.SourceSpec
		reason string
	}{
		"a repository without a name": {&sourcev1.SourceSpec{Generation: 1, Repositories: []*sourcev1.Checkout{{Remote: "file:///nowhere/services"}}}, told.Unfit},
		"an entity of no namespace":   {&sourcev1.SourceSpec{Generation: 1, Repositories: named, Located: []*sourcev1.LocatedEntity{{Ref: &typev1.EntityRef{Kind: "component", Name: "hello"}, Repository: "services"}}}, told.Unfit},
		"an entity of no kind":        {&sourcev1.SourceSpec{Generation: 1, Repositories: named, Located: []*sourcev1.LocatedEntity{{Ref: &typev1.EntityRef{Namespace: "default", Name: "hello"}, Repository: "services"}}}, told.Unfit},
		"entities alone":              {&sourcev1.SourceSpec{Generation: 1, Located: []*sourcev1.LocatedEntity{{Ref: &typev1.EntityRef{Kind: "component", Namespace: "default", Name: "hello"}, Repository: "services"}}}, told.Missing},
	} {
		if reason, _ := frontseat.Reason(brought(held, c.spec)); reason != c.reason {
			t.Errorf("%s: refused %q, want %s", what, reason, c.reason)
		}
	}
	whole := &sourcev1.SourceSpec{Generation: 1, Repositories: named, Located: []*sourcev1.LocatedEntity{{Ref: &typev1.EntityRef{Kind: "component", Namespace: "default", Name: "hello"}, Repository: "services", Root: "hello"}}}
	if err := brought(held, whole); err != nil {
		t.Errorf("the control, told whole: %v", err)
	}
	// What it was told is brought, or fails to be, before the test ends.
	for {
		now, changed := held.Now()
		if now.State != Populating {
			break
		}
		<-changed
	}
}
