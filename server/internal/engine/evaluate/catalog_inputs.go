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
// a person writes and no plugin speaks. Its inputs are patterns of
// source, held to what a plugin's are; a pattern that begins "/" is
// written from the repository's root: a fixture of another Component,
// the contracts. Its needs are what other actions left, ":what" and
// "//path:what", resolved among the workspace's actions (plan.Resolve);
// here each is only read. Each list holds one kind of thing: an action
// among the inputs, or a pattern among the needs, is refused. A mistake
// names the file and what was being read.
func readCatalogInputs(repository, directory, file, what string, inputs, needs []string) (catalogReading, error) {
	out := catalogReading{source: pattern.Group{Directory: directory}, root: pattern.Group{Committed: true}}
	sources := 0
	// A mistake says which entry by its place in the list and never
	// repeats it: a catalog file's text is said back by no refusal.
	for i, input := range inputs {
		within, rooted := fromRoot(input)
		switch {
		case pattern.IsReference(input):
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: its input %d names an action: what another action left is written under needs", what, i+1)}
		case rooted && !pattern.Valid(within):
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: its input %d is not a pattern of files beneath the repository's root", what, i+1)}
		case rooted:
		case !pattern.Valid(input):
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: its input %d is not a pattern of files beneath the project", what, i+1)}
		default:
			sources++
		}
	}
	for i, need := range needs {
		if !pattern.IsReference(need) {
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: its need %d names no action: a need is :<action> or //<path>:<action>", what, i+1)}
		}
	}
	if sources > 0 {
		out.source.Patterns = make([]string, 0, sources)
	}
	if len(needs) > 0 {
		out.references = make([]plan.Reference, 0, len(needs))
	}
	for _, input := range inputs {
		if within, rooted := fromRoot(input); rooted {
			out.root.Patterns = append(out.root.Patterns, within)
			continue
		}
		out.source.Patterns = append(out.source.Patterns, input)
	}
	for _, need := range needs {
		ref, err := model.ParseReference(repository, directory, need)
		if err != nil {
			return catalogReading{}, &model.Mistake{File: file, Message: fmt.Sprintf("%s: %s", what, strings.TrimPrefix(err.Error(), "model: "))}
		}
		out.references = append(out.references, plan.Reference{Reference: ref, File: file})
	}
	return out, nil
}
