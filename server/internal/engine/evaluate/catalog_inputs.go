package evaluate

import (
	"fmt"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

// catalogReading is what a catalog file says is read, as the plan
// holds it: patterns of source beneath the directory they are written
// in, patterns of source from the repository's root, and what other
// actions left.
type catalogReading struct {
	source, root pattern.Group
	references   []plan.Reference
}

// groups are the groups of source it reads, each where it says any.
func (r catalogReading) groups() []pattern.Group {
	var out []pattern.Group
	for _, group := range []pattern.Group{r.source, r.root} {
		if len(group.Patterns) > 0 {
			out = append(out, group)
		}
	}
	return out
}

// ofOwn are the groups an entity's own action reads: its directory's,
// whether or not it names any, and the repository's where it does.
func (r catalogReading) ofOwn() []pattern.Group {
	if len(r.root.Patterns) == 0 {
		return []pattern.Group{r.source}
	}
	return []pattern.Group{r.source, r.root}
}

// fromRoot is a pattern written from the repository's root, "/…" or
// "!/…", as the pattern it is from there.
func fromRoot(input string) (string, bool) {
	rest, negated := strings.CutPrefix(input, "!")
	within, rooted := strings.CutPrefix(rest, "/")
	// "//path:what" names what another project left, and is no pattern.
	if !rooted || pattern.IsReference(input) {
		return "", false
	}
	if negated {
		return "!" + within, true
	}
	return within, true
}

// readCatalogInputs reads what a catalog file says an entity's own
// action, or a Component's tests, read: the catalog's shorthand, which
// a person writes and no plugin speaks. A pattern of source is held to
// what a plugin's is. ":what" and "//path:what" name what another action
// left, and are resolved among the workspace's actions (plan.Resolve);
// here each is only read. A pattern that begins "/" is written from the
// repository's root: a fixture of another Component, the contracts. A
// catalog file declares no input set, so "=name" names none. A mistake
// names the file and what was being read.
func readCatalogInputs(repository, directory, file, what string, written []string) (catalogReading, error) {
	out := catalogReading{source: pattern.Group{Directory: directory}, root: pattern.Group{Committed: true}}
	var sources, named int
	// A mistake says which input by its place in the list and never
	// repeats it: a catalog file's text is said back by no refusal.
	for i, input := range written {
		within, rooted := fromRoot(input)
		switch {
		case pattern.IsReference(input):
			named++
		case rooted && !pattern.Valid(within):
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: its input %d is not a pattern of files beneath the repository's root", what, i+1)}
		case rooted:
		case strings.HasPrefix(input, "="):
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: its input %d names an input set, which a catalog file declares none of", what, i+1)}
		case !pattern.Valid(input):
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: its input %d is not a pattern of files beneath the project", what, i+1)}
		default:
			sources++
		}
	}
	if sources > 0 {
		out.source.Patterns = make([]string, 0, sources)
	}
	if named > 0 {
		out.references = make([]plan.Reference, 0, named)
	}
	for _, input := range written {
		if within, rooted := fromRoot(input); rooted && !pattern.IsReference(input) {
			out.root.Patterns = append(out.root.Patterns, within)
			continue
		}
		if !pattern.IsReference(input) {
			out.source.Patterns = append(out.source.Patterns, input)
			continue
		}
		ref, err := model.ParseReference(repository, directory, input)
		if err != nil {
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: %s", what, strings.TrimPrefix(err.Error(), "model: "))}
		}
		out.references = append(out.references, plan.Reference{Reference: ref, File: file})
	}
	return out, nil
}
