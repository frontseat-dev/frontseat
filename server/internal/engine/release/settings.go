package release

import (
	"fmt"
	"sort"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// The settings an entity has where no level says one.
const (
	DefaultTag        = "{unit}/v{version}"
	DefaultInitial    = "0.1.0"
	DefaultVersioning = "release:conventional-commits"
	DefaultBranching  = "release:release-flow"
	// DefaultPrerelease names a prerelease by its branch and by when its
	// commit was made.
	DefaultPrerelease = "{branch}-{timestamp}"
)

// Entity is an entity as releasing needs it: where it is, and what it
// is part of.
type Entity struct {
	Ref entity.Ref
	// Repository is the workspace's name for the repository it is in,
	// and Roots its directories there; a system or a domain has none.
	Repository string
	Roots      []string
	// Manifest is the file that describes it, relative to its repository.
	Manifest string
	// PartOf are the entities the catalog says it is part of, whether or
	// not the workspace holds them.
	PartOf []entity.Ref
}

// Workspace is what a workspace holds, as releasing reads it.
type Workspace struct {
	// Entities are its entities.
	Entities []Entity
	// Said is what each says of how it is released.
	Said []config.Release
	// Listed are the plugins each repository lists.
	Listed map[string][]string
	// DependsOn is what each entity depends on, as the source says.
	DependsOn map[entity.Ref][]entity.Ref
}

// Settings is how an entity is released, as its settings resolve.
type Settings struct {
	// Unit is what it is released as: its synced group, or itself. The
	// zero reference for a system or a domain that is no synced group.
	Unit entity.Ref
	// Group is the group it versions with; itself where it is in none.
	Group entity.Ref
	// Synced is whether its group versions its members together.
	Synced bool
	// Tag, Initial, Versioning and Branching are its settings.
	Tag, Initial, Versioning, Branching string
	// Prerelease is how a prerelease of it is spelled after its
	// version's dash, and PrereleaseTag whether one is tagged.
	Prerelease    string
	PrereleaseTag bool
	// SetBy says, for each setting a level says, which entity says it.
	SetBy map[string]entity.Ref
}

// Resolved is a workspace's entities with how each is released.
type Resolved struct {
	entities map[entity.Ref]Entity
	settings map[entity.Ref]Settings
	members  map[entity.Ref][]entity.Ref
	order    []entity.Ref
	// systems are each domain's systems.
	systems map[entity.Ref][]entity.Ref
	// unserved are the entities whose release is not served yet, each
	// with why: what a domain within a domain holds, and the domains.
	unserved map[entity.Ref]string
	// unheld are the entities part of a system or a domain the workspace
	// does not hold, each with which: what they release with is not known.
	unheld map[entity.Ref]string
}

// refused says why releasing an entity is refused for what contains it:
// a domain within a domain, or a group the workspace does not hold.
func (r *Resolved) refused(ref entity.Ref) error {
	if why, unserved := r.unserved[ref]; unserved {
		return &Refusal{Unserved: true, Message: why}
	}
	if why, unheld := r.unheld[ref]; unheld {
		return &model.Mistake{Message: why}
	}
	return nil
}

// Of returns how an entity is released, and whether it is released as
// anything: an entity with directories, a system or a domain is.
func (r *Resolved) Of(ref entity.Ref) (Settings, bool) {
	s, ok := r.settings[ref]
	return s, ok
}

// Members returns the entities that take a unit's version.
func (r *Resolved) Members(unit entity.Ref) []entity.Ref { return r.members[unit] }

const (
	kindSystem = "system"
	kindDomain = "domain"
)

// Resolve says how each entity of a workspace is released. A setting is
// inherited from the system an entity is part of, and from that
// system's domain, the nearest level that says it winning; grouping is
// each level's own. A system is a synced group unless it says
// independent; a domain is one only where it says synced, and then its
// systems and their members version with it. A member leaves its synced
// group by saying independent itself: a system that leaves its domain
// versions its members on their own.
func Resolve(w Workspace) (*Resolved, error) {
	out := &Resolved{entities: map[entity.Ref]Entity{}, settings: map[entity.Ref]Settings{}, members: map[entity.Ref][]entity.Ref{}, systems: map[entity.Ref][]entity.Ref{}, unserved: map[entity.Ref]string{}, unheld: map[entity.Ref]string{}}
	for _, entity := range w.Entities {
		out.entities[entity.Ref] = entity
		out.order = append(out.order, entity.Ref)
	}
	sort.Slice(out.order, func(i, j int) bool { return out.order[i].String() < out.order[j].String() })
	said := map[entity.Ref]config.Release{}
	for _, each := range w.Said {
		if other, twice := said[each.Entity]; twice {
			return nil, &model.Mistake{File: each.File, Message: fmt.Sprintf("%s says how it is released twice: here and in %s", each.Entity, other.File)}
		}
		said[each.Entity] = each
	}
	// above is the one entity of a kind an entity is part of.
	above := func(ref entity.Ref, kind string) (entity.Ref, bool, error) {
		var found []entity.Ref
		for _, parent := range out.entities[ref].PartOf {
			if _, held := out.entities[parent]; held && parent.Kind == kind {
				found = append(found, parent)
			}
		}
		switch len(found) {
		case 0:
			return entity.Ref{}, false, nil
		case 1:
			return found[0], true, nil
		}
		return entity.Ref{}, false, &model.Mistake{File: said[ref].File, Message: fmt.Sprintf("%s is part of %s and %s; it is released with one %s", ref, found[0], found[1], kind)}
	}
	// inherited resolves the settings every level passes down.
	inherited := func(levels ...entity.Ref) Settings {
		s := Settings{Tag: DefaultTag, Initial: DefaultInitial, Versioning: DefaultVersioning, Branching: DefaultBranching, Prerelease: DefaultPrerelease, SetBy: map[string]entity.Ref{}}
		for i := len(levels) - 1; i >= 0; i-- {
			for key, value := range said[levels[i]].Settings {
				switch key {
				case config.ReleaseTag:
					s.Tag = value
				case config.ReleaseInitial:
					s.Initial = value
				case config.ReleaseVersioning:
					s.Versioning = value
				case config.ReleaseBranching:
					s.Branching = value
				case config.ReleasePrerelease:
					s.Prerelease = value
				case config.ReleasePrereleaseTag:
					s.PrereleaseTag = value == "true"
				default:
					continue
				}
				s.SetBy[key] = levels[i]
			}
		}
		return s
	}
	grouping := func(ref entity.Ref) (string, bool) {
		value, ok := said[ref].Settings[config.ReleaseGrouping]
		return value, ok
	}
	// A member of a synced group is released as its group is: it says
	// nothing of its own but its grouping.
	member := func(ref, unit entity.Ref) error {
		for key := range said[ref].Settings {
			if key != config.ReleaseGrouping {
				return &model.Mistake{File: said[ref].File, Message: fmt.Sprintf("%s versions with %s, which says how it is released: spec.frontseat.release.%s is the group's to say", ref.Name, unit.Name, key)}
			}
		}
		return nil
	}
	for _, ref := range out.order {
		if ref.Kind != kindDomain {
			continue
		}
		s := inherited(ref)
		s.Group = ref
		if value, ok := grouping(ref); ok {
			s.Synced = value == config.GroupingSynced
			s.SetBy[config.ReleaseGrouping] = ref
		}
		if s.Synced {
			s.Unit = ref
		}
		out.settings[ref] = s
	}
	// A domain within a domain is not served yet: which of them its
	// systems version with, and what each inherits of the other, is not
	// designed. Each domain of such a pair is remembered, a domain that is
	// its own subdomain among them, and releasing anything either holds
	// is refused as that, whatever either says of its grouping.
	nested := map[entity.Ref]string{}
	for _, ref := range out.order {
		if ref.Kind != kindDomain {
			continue
		}
		parent, in, err := above(ref, kindDomain)
		if err != nil {
			return nil, err
		}
		if in {
			why := fmt.Sprintf("%s is a subdomain of %s, and releasing a domain within a domain is not served yet", ref, parent)
			nested[ref], nested[parent] = why, why
		}
	}
	for _, ref := range out.order {
		if ref.Kind != kindSystem {
			continue
		}
		domain, in, err := above(ref, kindDomain)
		if err != nil {
			return nil, err
		}
		own, says := grouping(ref)
		if in {
			out.systems[domain] = append(out.systems[domain], ref)
			if out.settings[domain].Synced && says && own == config.GroupingSynced {
				// Synced here would say two things: that its members version
				// together, and that it versions with its domain.
				return nil, &model.Mistake{File: said[ref].File, Message: fmt.Sprintf("%s is part of the synced domain %s, which groups it: say independent to release it on its own, or nothing", ref.Name, domain.Name)}
			}
			if out.settings[domain].Synced && !says {
				if err := member(ref, domain); err != nil {
					return nil, err
				}
				s := out.settings[domain]
				s.SetBy = copied(s.SetBy)
				out.settings[ref] = s
				continue
			}
		}
		levels := []entity.Ref{ref}
		if in {
			levels = append(levels, domain)
		}
		s := inherited(levels...)
		s.Group, s.Synced = ref, true
		if says {
			s.Synced = own == config.GroupingSynced
			s.SetBy[config.ReleaseGrouping] = ref
		}
		if s.Synced {
			s.Unit = ref
		}
		out.settings[ref] = s
	}
	for _, ref := range out.order {
		described := out.entities[ref]
		if ref.Kind == kindDomain || ref.Kind == kindSystem || len(described.Roots) == 0 {
			continue
		}
		system, in, err := above(ref, kindSystem)
		if err != nil {
			return nil, err
		}
		own, says := grouping(ref)
		left := says && own == config.GroupingIndependent
		if in && out.settings[system].Synced && !left {
			unit := out.settings[system].Unit
			if err := member(ref, unit); err != nil {
				return nil, err
			}
			s := out.settings[system]
			s.SetBy = copied(s.SetBy)
			if says {
				s.SetBy[config.ReleaseGrouping] = ref
			}
			out.settings[ref] = s
			out.members[unit] = append(out.members[unit], ref)
			continue
		}
		if says && own == config.GroupingSynced {
			if !in {
				return nil, &model.Mistake{File: said[ref].File, Message: fmt.Sprintf("%s says it versions with its group, and is part of no system", ref.Name)}
			}
			return nil, &model.Mistake{File: said[ref].File, Message: fmt.Sprintf("%s says it versions with %s, which says its members version on their own", ref.Name, system.Name)}
		}
		levels := []entity.Ref{ref}
		group := ref
		if in {
			group = system
			levels = append(levels, system)
			if domain, has, err := above(system, kindDomain); err != nil {
				return nil, err
			} else if has {
				levels = append(levels, domain)
			}
		}
		s := inherited(levels...)
		s.Unit, s.Group = ref, group
		if says {
			s.SetBy[config.ReleaseGrouping] = ref
		} else if by, ok := out.settings[system].SetBy[config.ReleaseGrouping]; in && ok {
			s.SetBy[config.ReleaseGrouping] = by
		}
		out.settings[ref] = s
		out.members[ref] = []entity.Ref{ref}
	}
	out.contained(nested)
	return out, out.tagsApart()
}

// contained remembers, of each entity, what in the systems and domains
// it is part of refuses releasing it: a domain within a domain, or a
// system or a domain the workspace does not hold, whose grouping would
// change what it is released as.
func (r *Resolved) contained(nested map[entity.Ref]string) {
	for _, ref := range r.order {
		seen := map[entity.Ref]bool{ref: true}
		for queue := []entity.Ref{ref}; len(queue) > 0; queue = queue[1:] {
			at := queue[0]
			if why, in := nested[at]; in {
				r.unserved[ref] = why
				break
			}
			for _, parent := range r.entities[at].PartOf {
				if parent.Kind != kindSystem && parent.Kind != kindDomain {
					continue
				}
				if _, held := r.entities[parent]; !held {
					r.unheld[ref] = fmt.Sprintf("%s is part of %s, which this workspace does not hold: open the workspace with its catalog", at, parent)
					break
				}
				if !seen[parent] {
					seen[parent] = true
					queue = append(queue, parent)
				}
			}
			if _, unheld := r.unheld[ref]; unheld {
				break
			}
		}
	}
}

// tagsApart refuses two units of one repository whose tags would be
// spelled alike: one tag would then name two releases.
func (r *Resolved) tagsApart() error {
	type spelled struct{ repository, tag string }
	seen := map[spelled]entity.Ref{}
	for _, ref := range r.order {
		s := r.settings[ref]
		if s.Unit != ref || len(r.members[ref]) == 0 {
			continue
		}
		at := spelled{r.entities[r.members[ref][0]].Repository, strings.ReplaceAll(s.Tag, "{unit}", ref.Name)}
		if other, alike := seen[at]; alike {
			return &model.Mistake{Message: fmt.Sprintf("%s and %s spell their tags alike in %s: %s", other, ref, at.repository, at.tag)}
		}
		seen[at] = ref
	}
	return nil
}

func copied(m map[string]entity.Ref) map[string]entity.Ref {
	out := make(map[string]entity.Ref, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
