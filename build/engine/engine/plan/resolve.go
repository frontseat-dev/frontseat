package plan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/build/engine/model"
)

// Reference is an input of an action that names what another action
// left, as evaluation parsed it, with what resolves it among the
// workspace's actions.
type Reference struct {
	model.Reference
	// Plugin, for a reference a plugin wrote, is that plugin: it names
	// only its own actions, by their bare name. Empty for one a catalog
	// file wrote, where an action is named "plugin.name".
	Plugin string
	// Shared, for a reference a plugin wrote, is the path its shared
	// actions are labelled with: the repository's, which is empty for a
	// lone repository. Unused for a catalog's.
	Shared string
	// Repository and File are the catalog file the reference was
	// written in, File relative to Repository, for the mistake that
	// names it; empty for a plugin's.
	Repository, File string
}

// Read is what an action reads of what another left: all of it, where
// the action named the other; or one output, spelled as the other
// declares it, where the action named that.
type Read struct {
	// From is the action that leaves it.
	From model.Label
	// Repository and ProjectRoot are From's, where its outputs sit.
	Repository, ProjectRoot string
	// Output is the one output read, relative to ProjectRoot or, written
	// "~/…", in the action's home, with no slash at its end; empty for
	// all of them.
	Output string
	// Optional permits an absent output in a provided-artifact binding.
	// It is inherited from the producing action, never chosen by a consumer.
	Optional bool
}

// All reports whether the read is of everything the action left.
func (r Read) All() bool { return r.Output == "" }

// Resolve turns what each action's inputs name of other actions into
// what it reads and what it needs: each reference into a Read, and each
// action read into a need. What a reference names is looked up among
// the workspace's actions, at the project it names: an action, by name,
// or one output an action there declares. What is named is one thing: a
// reference that names nothing, an action and an output both, an output
// more than one action declares, or an action that declares no output,
// is a model.Mistake that names the reader and the reference, and the
// catalog file where one wrote it. The same thing named twice is read
// once; an output named beside its action is read with the action.
//
// Nothing orders an action but what it reads: this is the one place a
// need comes from, apart from the generated source an input selects
// (BindGenerated).
//
// Every build resolves every reference, and a module names each entry
// of its lock, so a reference is looked up, never searched for: the
// cost is of the references, whatever the workspace holds beside them.
func Resolve(actions []Action) error {
	referenced := false
	for _, action := range actions {
		if len(action.References) > 0 {
			referenced = true
			break
		}
	}
	if !referenced {
		return nil
	}
	named := make(map[name]int, len(actions))
	leaves := make(map[output]leavers, len(actions))
	for i, action := range actions {
		label := action.Label
		named[name{label.Path, label.Plugin, label.Name}] = i
		for _, declared := range action.Declared.GetOutputs() {
			// By where it sits, so that a file and a directory of one
			// name are one place. By whichever plugin, for a catalog
			// file; and by its own plugin, shared or not, for a plugin,
			// which names no other's.
			place := strings.TrimSuffix(declared.GetPath(), "/")
			for _, key := range [2]output{
				{path: label.Path, output: place},
				{path: label.Path, output: place, plugin: label.Plugin, own: true, shared: action.IsShared()},
			} {
				had := leaves[key]
				had.add(i)
				leaves[key] = had
			}
		}
	}
	var sofar reading
	for i := range actions {
		reader := &actions[i]
		if len(reader.References) == 0 {
			continue
		}
		sofar.begin(reader)
		// Every reference is walked again, so what its plugin named one
		// by one is said again from them, never added to what was said.
		reader.IdleWithout = nil
		for _, ref := range reader.References {
			// A slash at the end changes nothing of what is named: it is
			// how a directory is written, and an action or an output is
			// looked up without it.
			action, left := -1, leavers{}
			place := strings.TrimSuffix(ref.Rest, "/")
			if ref.Plugin == "" {
				if plugin, rest, is := strings.Cut(place, "."); is {
					if j, held := named[name{ref.Path, plugin, rest}]; held {
						action = j
					}
				}
				left = leaves[output{path: ref.Path, output: place}]
			} else {
				// An action of the plugin's at the project; or a shared
				// one, which is the repository's whichever project
				// declared it.
				if j, held := named[name{ref.Path, ref.Plugin, place}]; held {
					action = j
				} else if j, held := named[name{ref.Shared, ref.Plugin, place}]; held && actions[j].IsShared() {
					action = j
				}
				left.merge(leaves[output{path: ref.Path, output: place, plugin: ref.Plugin, own: true}])
				left.merge(leaves[output{path: ref.Path, output: place, plugin: ref.Plugin, own: true, shared: true}])
				if ref.Shared != ref.Path {
					left.merge(leaves[output{path: ref.Shared, output: place, plugin: ref.Plugin, own: true, shared: true}])
				}
			}
			switch {
			case action >= 0 && left.count > 0:
				return mistake(ref, "%s reads %s, which names both an action and what an action leaves", reader.Label, ref.Written)
			case action < 0 && left.count == 0:
				return mistake(ref, "%s reads %s, which names no action and nothing an action leaves", reader.Label, ref.Written)
			case left.count > 1:
				return mistake(ref, "%s reads %s, which more than one action leaves: %s and %s. Name the action whose it is", reader.Label, ref.Written, actions[left.first].Label, actions[left.second].Label)
			case action >= 0 && len(actions[action].Declared.GetOutputs()) == 0:
				return mistake(ref, "%s reads %s, which leaves nothing: an action reads what another left, and waits for nothing else", reader.Label, ref.Written)
			}
			read := Read{}
			if action < 0 {
				action, read.Output = left.first, place
			}
			producer := actions[action]
			read.From, read.Repository, read.ProjectRoot = producer.Label, producer.Repository, producer.ProjectRoot
			sofar.add(reader, read)
			// What its own plugin named one by one is what it has nothing
			// to do without, whatever else is read beside it or over it.
			if ref.Plugin != "" && !read.All() && reader.Declared.GetTest().GetIdleWithoutInputs() && !slices.Contains(reader.IdleWithout, read) {
				reader.IdleWithout = append(reader.IdleWithout, read)
			}
		}
		sofar.end(reader)
	}
	return nil
}

// name is an action as a reference finds it: where, whose and which.
type name struct{ path, plugin, name string }

// output is something an action leaves, as a reference finds it: where
// its project is and where in it the output sits, and, for a plugin's
// reference (own), whose and whether the action is shared.
type output struct {
	path, output, plugin string
	own, shared          bool
}

// leavers are the actions that declare one output: how many, and the
// first two, which is all a mistake names.
type leavers struct{ first, second, count int }

func (l *leavers) add(action int) {
	switch {
	case l.count == 0:
		l.first = action
	case l.first == action || l.count > 1 && l.second == action:
		// An action that declares an output twice leaves it once.
		return
	case l.count == 1:
		l.second = action
	}
	l.count++
}

func (l *leavers) merge(other leavers) {
	if other.count > 0 {
		l.add(other.first)
	}
	if other.count > 1 {
		l.add(other.second)
	}
	if other.count > 2 {
		l.count += other.count - 2
	}
}

// reading is what one action has been found to read so far, kept so
// that each reference is added in a step, however many an action names.
// It is used for one action after another.
type reading struct {
	whole   map[model.Label]bool
	parted  map[model.Label]bool
	part    map[Read]bool
	needed  map[model.Label]bool
	dropped bool
}

func (r *reading) begin(reader *Action) {
	if r.whole == nil {
		r.whole, r.parted, r.part, r.needed = map[model.Label]bool{}, map[model.Label]bool{}, map[Read]bool{}, map[model.Label]bool{}
	}
	clear(r.whole)
	clear(r.parted)
	clear(r.part)
	clear(r.needed)
	r.dropped = false
	for _, read := range reader.Reads {
		if read.All() {
			r.whole[read.From] = true
		} else {
			r.part[read], r.parted[read.From] = true, true
		}
	}
	for _, need := range reader.Needs {
		r.needed[need] = true
	}
}

// add adds what the action reads, once: an action named is all of it,
// whatever outputs of it were named beside; an output named twice is
// named once.
func (r *reading) add(reader *Action, read Read) {
	switch {
	case r.whole[read.From]:
		return
	case read.All():
		r.whole[read.From] = true
		r.dropped = r.dropped || r.parted[read.From]
	case r.part[read]:
		return
	default:
		r.part[read], r.parted[read.From] = true, true
	}
	reader.Reads = append(reader.Reads, read)
	if !r.needed[read.From] {
		r.needed[read.From] = true
		reader.Needs = append(reader.Needs, read.From)
	}
}

// end drops each output read of an action that was then read whole.
func (r *reading) end(reader *Action) {
	if !r.dropped {
		return
	}
	kept := reader.Reads[:0]
	for _, read := range reader.Reads {
		if read.All() || !r.whole[read.From] {
			kept = append(kept, read)
		}
	}
	reader.Reads = kept
}

func mistake(ref Reference, format string, args ...any) error {
	return &model.Mistake{Repository: ref.Repository, File: ref.File, Message: fmt.Sprintf(format, args...)}
}
