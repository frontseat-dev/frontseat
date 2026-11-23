package plan

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/record"
)

// Pointed returns, of a workspace's actions, those the targets point at,
// in the order they were given, and of those only the ones named in
// only. No target is every action; no name is every name. A target that
// points at nothing, or a name no action has, is a model.Mistake that
// says what is nearest.
//
// A run is told what to build by pointing at things, in the words
// whoever asks already has for them:
//
//	a name      an entity: gateway, component:default/gateway
//	a path      everything at or under it: platform/, platform/gateway,
//	            or a file, which is the project it belongs to
//	an action   exactly as it is printed: //platform/gateway:go.lint
//	            or, for tests in pieces, what every piece's begins
//	            with: //platform/gateway:go.test is each of them, and
//	            a piece cut into shards is each of its shards
//
// Several targets are all of them together.
func Pointed(actions []Action, entities []record.Located, targets, only []string) ([]Action, error) {
	chosen := map[int]bool{}
	for _, target := range targets {
		at, err := pointed(actions, entities, target)
		if err != nil {
			return nil, err
		}
		for _, i := range at {
			// A program is run, and is no part of a build: pointed
			// at by itself, it is said so, and never built in passing.
			if actions[i].IsProgram() && strings.HasPrefix(target, "//") {
				return nil, &model.Mistake{Message: fmt.Sprintf("%s is a program, which no build runs", target), Help: "frontseat run " + target}
			}
			// Nor is what publishes: a release runs it, once its tags
			// are pushed.
			if actions[i].IsPublish() && strings.HasPrefix(target, "//") {
				return nil, &model.Mistake{Message: fmt.Sprintf("%s publishes, which no build runs: a release of what it publishes runs it", target)}
			}
			chosen[i] = true
		}
	}
	names := map[string]bool{}
	for _, name := range only {
		known := map[string]bool{}
		// A program is no action of a build, nor what publishes: its
		// name is none to keep.
		for _, action := range actions {
			if action.Built() {
				known[action.Name()], known[kindOf(action.Name())] = true, true
			}
		}
		if !known[name] {
			return nil, &model.Mistake{Message: "no action is named " + name, Help: nearest(name, slices.Collect(maps.Keys(known)))}
		}
		names[name] = true
	}
	var out []Action
	for i, action := range actions {
		if !action.Built() {
			continue
		}
		if len(targets) > 0 && !chosen[i] {
			continue
		}
		if len(only) > 0 && !asked(names, action) {
			continue
		}
		out = append(out, action)
	}
	return out, nil
}

// asked reports whether a run asked for these names is asked for an
// action: by its own name, or by its kind, the part of its name before
// the first slash, so that whoever asks for "test" is given every piece
// of every project's tests, and whoever asks for "generate" every
// "generate/<what>", whichever plugin declared it. Nothing else of the
// name is read.
func asked(names map[string]bool, action Action) bool {
	return names[action.Name()] || names[kindOf(action.Name())]
}

// kindOf is the kind an action's name says: the part before its first
// slash, or the whole of a name of none.
func kindOf(name string) string {
	kind, _, _ := strings.Cut(name, "/")
	return kind
}

// pointed returns which actions one target points at.
func pointed(actions []Action, entities []record.Located, target string) ([]int, error) {
	switch {
	case strings.HasPrefix(target, "//"):
		var labels []string
		// The pieces of a project's tests are pointed at together by
		// what their labels begin with, the label one test of it would
		// have.
		var pieces []int
		// And the shards of one piece by the label the piece would have:
		// a shard is named its piece, "@" and which shard it is.
		whole, err := model.ParseLabel(target)
		// Every piece of one kind of a project is pointed at together
		// the same way, by the label of the kind: //app:go.generate is
		// each generate/<what>, as //app:go.test is each test/<piece>.
		together := err == nil && !strings.Contains(whole.Name, "/")
		shards := ""
		if err == nil && strings.HasPrefix(whole.Name, "test/") && !strings.Contains(whole.Name, "@") {
			shards = whole.Name + "@"
		}
		for i, action := range actions {
			if action.Label.String() == target {
				return []int{i}, nil
			}
			if action.Label.Path == whole.Path && action.Label.Plugin == whole.Plugin && action.Built() &&
				(together && kindOf(action.Label.Name) == whole.Name && action.Label.Name != whole.Name || shards != "" && strings.HasPrefix(action.Label.Name, shards)) {
				pieces = append(pieces, i)
			}
			labels = append(labels, action.Label.String())
		}
		if len(pieces) > 0 {
			return pieces, nil
		}
		return nil, &model.Mistake{Message: "there is no action " + target, Help: nearest(target, labels)}

	case target == "." || strings.Contains(target, "/") && !strings.Contains(target, ":"):
		at := strings.Trim(path.Clean(target), "/")
		if at == "." {
			at = ""
		}
		if under := beneath(actions, at); len(under) > 0 {
			return under, nil
		}
		// A file is the project it belongs to: the deepest one above it.
		deepest := ""
		for _, action := range actions {
			if action.IsShared() {
				continue
			}
			if strings.HasPrefix(at, action.Label.Path+"/") && len(action.Label.Path) > len(deepest) {
				deepest = action.Label.Path
			}
		}
		if deepest != "" {
			return beneath(actions, deepest), nil
		}
		return nil, &model.Mistake{Message: fmt.Sprintf("nothing is built at %s", at)}
	}

	// What reads as no reference names no entity, and is answered as a
	// name nothing has.
	// An entity is everything built in every directory of its own: one
	// that has several is all of them, not the first.
	// A name finds its entity by what it says: a kind left out is a
	// component, and a namespace left out is any, so a name a catalog
	// put in a namespace of its own is found as it is typed; one that
	// names several is refused.
	typed, err := entity.ParsePattern(target)
	var ref entity.Ref
	if err == nil {
		held := make([]entity.Ref, 0, len(entities))
		for _, each := range entities {
			held = append(held, each.Ref)
		}
		var found bool
		if ref, found, err = typed.Find(held); err != nil {
			return nil, &model.Mistake{Message: err.Error()}
		} else if !found {
			err = fmt.Errorf("no entity %s", target)
		}
	}
	var names, paths []string
	var under []int
	seen := map[int]bool{}
	for _, entity := range entities {
		if err == nil && entity.Ref == ref {
			paths = append(paths, entity.Path())
			for _, i := range beneath(actions, entity.Path()) {
				if !seen[i] {
					seen[i] = true
					under = append(under, i)
				}
			}
			continue
		}
		names = append(names, entity.Ref.Name)
	}
	switch {
	case len(under) > 0:
		sort.Ints(under)
		return under, nil
	case len(paths) > 0:
		return nil, &model.Mistake{Message: fmt.Sprintf("nothing is built for %s, at %s", target, strings.Join(paths, " and "))}
	}
	return nil, &model.Mistake{Message: fmt.Sprintf("there is no entity %s in the workspace", target), Help: nearest(target, names)}
}

// beneath returns the actions of the projects at or under a directory of
// the workspace; the empty directory is the workspace.
func beneath(actions []Action, directory string) []int {
	var out []int
	for i, action := range actions {
		// A shared action is brought by what needs it, never as a
		// directory's own.
		if action.IsShared() {
			continue
		}
		if directory == "" || action.Label.Path == directory || strings.HasPrefix(action.Label.Path, directory+"/") {
			out = append(out, i)
		}
	}
	return out
}

// nearest says which of the names there are is nearest a name there is
// not, for whoever mistyped it; it says nothing where none is near.
func nearest(name string, known []string) string {
	sort.Strings(known)
	best, least := "", len(name)/2+1
	for _, candidate := range known {
		if d := distance(name, candidate); d < least {
			best, least = candidate, d
		}
	}
	if best == "" {
		return ""
	}
	return "did you mean " + best + "?"
}

// helped is what helps, each clause of it that says anything, in turn.
func helped(clauses ...string) string {
	return strings.Join(slices.DeleteFunc(clauses, func(clause string) bool { return clause == "" }), "; ")
}

// distance is how many letters must be changed, added or dropped to turn
// one word into another, a swap of two neighbours counting once.
func distance(a, b string) int {
	previous2, previous := make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				current[j] = min(current[j], previous2[j-2]+1)
			}
		}
		previous2, previous = previous, current
	}
	return previous[len(b)]
}

// Program returns what running a program does: the action a target
// names, exactly as it is printed, which must be a program, and what
// runs for it, the program last, each after all it needs.
//
// A target that names no action, or one that is no program, is a
// model.Mistake that says what can be run.
func Program(actions []Action, target string) (runs []Action, program Action, err error) {
	if err := Check(actions); err != nil {
		return nil, Action{}, err
	}
	var programs []string
	found := -1
	for i, action := range actions {
		if action.IsProgram() {
			programs = append(programs, action.Label.String())
		}
		if action.Label.String() == target {
			found = i
		}
	}
	switch {
	case found < 0:
		return nil, Action{}, &model.Mistake{Message: "there is no program " + target, Help: helped(nearest(target, programs), offered(programs))}
	case !actions[found].IsProgram():
		return nil, Action{}, &model.Mistake{Message: target + " is a step of a build, and no program", Help: helped("frontseat build "+target, offered(programs))}
	}
	if actions[found].HasSources() || len(actions[found].Declared.GetTools()) > 0 {
		return nil, Action{}, &model.Mistake{Message: fmt.Sprintf("%s needs prepared source or tools: this program is supported for test services, not for frontseat run", target)}
	}
	return With(actions, []Action{actions[found]}), actions[found], nil
}

// offeredAtMost is how many programs a mistake names.
const offeredAtMost = 20

// offered says what can be run, for whoever asked for what cannot:
// the workspace's programs, the first of them where there are many.
func offered(programs []string) string {
	if len(programs) == 0 {
		return "nothing in the workspace is a program"
	}
	sort.Strings(programs)
	if len(programs) > offeredAtMost {
		return fmt.Sprintf("what can be run: %s and %d more", strings.Join(programs[:offeredAtMost], ", "), len(programs)-offeredAtMost)
	}
	return "what can be run: " + strings.Join(programs, ", ")
}
