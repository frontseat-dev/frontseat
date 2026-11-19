package entity

import (
	"fmt"
	"slices"
	"strings"
)

// Typed reads a reference as a person types one to find an entity:
// "daemon", "component:daemon", "frontseat/daemon" or
// "component:frontseat/daemon". A kind left out is a component, as
// Parse reads it; a namespace left out is left empty, and stands for
// any, so that a name finds its entity in whatever namespace its catalog
// put it (Find).
func Typed(written string) (Ref, error) {
	kind, rest, qualified := strings.Cut(written, ":")
	if !qualified {
		kind, rest = "", written
	}
	namespace, name, placed := strings.Cut(rest, "/")
	if !placed {
		namespace, name = "", rest
	}
	if qualified && kind == "" || placed && namespace == "" || name == "" || strings.ContainsAny(name, ":/") {
		return Ref{}, fmt.Errorf("%q names no entity: a reference is a name, kind:name or kind:namespace/name", written)
	}
	if kind == "" {
		kind = KindComponent
	}
	ref := Ref{Kind: lowerASCII(kind), Namespace: lowerASCII(namespace), Name: lowerASCII(name)}
	if err := ref.ValidateTyped(); err != nil {
		return Ref{}, fmt.Errorf("%q names no entity: %w", written, err)
	}
	return ref, nil
}

// ValidateTyped checks a typed reference as Validate checks one in full:
// its kind and its name are there, and each part it says is a canonical
// part; its namespace may be left out.
func (r Ref) ValidateTyped() error {
	if r.Kind == "" || r.Name == "" {
		return fmt.Errorf("a reference has a kind and a name")
	}
	for _, part := range []string{r.Kind, r.Namespace, r.Name} {
		if part != "" && (len(part) > 255 || !identityPartPattern.MatchString(part)) {
			return fmt.Errorf("%q is no part of a catalog identity", part)
		}
	}
	return nil
}

// Whole is whether a reference names its kind, its namespace and its
// name, and so one entity, found nowhere but by itself.
func (r Ref) Whole() bool { return r.Kind != "" && r.Namespace != "" && r.Name != "" }

// Written is a reference as it was typed: what it leaves out is left out.
func (r Ref) Written() string {
	out := r.Name
	if r.Namespace != "" {
		out = r.Namespace + "/" + out
	}
	if r.Kind != "" {
		out = r.Kind + ":" + out
	}
	return out
}

// Names is whether a typed reference names an entity: by its kind and
// its name, and by its namespace where it says one.
func (r Ref) Names(held Ref) bool {
	return r.Name == held.Name && r.Kind == held.Kind && (r.Namespace == "" || r.Namespace == held.Namespace)
}

// Find returns the one entity of those held that a typed reference names.
// Where it names none, found is false; where it names several, it is
// refused, naming each, since which was meant is the person's to say.
func Find(typed Ref, held []Ref) (one Ref, found bool, err error) {
	var named []Ref
	for _, each := range held {
		if typed.Names(each) && !slices.Contains(named, each) {
			named = append(named, each)
		}
	}
	switch len(named) {
	case 0:
		return Ref{}, false, nil
	case 1:
		return named[0], true, nil
	}
	written := make([]string, 0, len(named))
	for _, each := range named {
		written = append(written, each.String())
	}
	slices.Sort(written)
	return Ref{}, false, fmt.Errorf("%s names %d entities, %s: name one in full", typed.Written(), len(named), strings.Join(written, ", "))
}
