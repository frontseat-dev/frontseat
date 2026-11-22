package stage

import (
	"context"
	"reflect"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
)

// an action's patterns are relative to its project's directory,
// and are made patterns of the repository by joining the two. The
// directory's name is then read as a pattern too: a project in a
// directory whose name has a character a pattern gives meaning to is
// sent another directory's files, and none of its own.
func TestAProjectsFilesAreThoseOfItsOwnDirectory(t *testing.T) {
	for name, c := range map[string]struct{ project, other string }{
		"the control, a plain name": {"pkg", "pkg1"},
		"a name with brackets":      {"pkg[1]", "pkg1"},
		"a name with braces":        {"{a,b}", "a"},
		"a name with a star":        {"a*", "ab"},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := sourcetest.Taken(t, map[string]string{c.project + "/own.go": "package p\n", c.other + "/others.go": "package p\n"})

			files, err := snapshot.Within(context.Background(), "", c.project, []string{"*.go"})

			if want := []string{c.project + "/own.go"}; err != nil || !reflect.DeepEqual(files, want) {
				t.Errorf("the project in %q is sent %q, err %v; want %q", c.project, files, err, want)
			}
		})
	}
}
