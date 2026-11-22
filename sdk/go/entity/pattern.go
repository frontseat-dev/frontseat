package entity

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Pattern is a reference as a person types one to find an entity:
// "daemon", "component:daemon", "frontseat/daemon" or
// "component:frontseat/daemon". Its kind is always said (a component
// where it is left out); its namespace may be left empty, and then
// stands for any, so that a name finds its entity in whatever namespace
// its catalog put it (Find). The API takes entities as patterns, and
// answers with references in full (Ref).
type Pattern struct {
	// Kind is its kind, in lower case: "component", "system", "domain".
	Kind string
	// Namespace is its namespace; empty for any.
	Namespace string
	// Name is its name.
	Name string
}

// ParsePattern reads a pattern as a person types one. A kind left out
// is a component, as Parse reads it; a namespace left out is left
// empty, and stands for any.
func ParsePattern(written string) (Pattern, error) {
	kind, rest, qualified := strings.Cut(written, ":")
	if !qualified {
		kind, rest = "", written
	}
	namespace, name, placed := strings.Cut(rest, "/")
	if !placed {
		namespace, name = "", rest
	}
	if qualified && kind == "" || placed && namespace == "" || name == "" || strings.ContainsAny(name, ":/") {
		return Pattern{}, fmt.Errorf("%q names no entity: a reference is a name, kind:name or kind:namespace/name", written)
	}
	if kind == "" {
		kind = KindComponent
	}
	p := Pattern{Kind: lowerASCII(kind), Namespace: lowerASCII(namespace), Name: lowerASCII(name)}
	if err := p.Validate(); err != nil {
		return Pattern{}, fmt.Errorf("%q names no entity: %w", written, err)
	}
	return p, nil
}

// Validate checks a pattern as Ref.Validate checks a reference: its
// kind and its name are there, and each part it says is a canonical
// part; its namespace may be left out.
func (p Pattern) Validate() error {
	if p.Kind == "" || p.Name == "" {
		return errors.New("a reference has a kind and a name")
	}
	for _, part := range []string{p.Kind, p.Namespace, p.Name} {
		if part != "" && (len(part) > 255 || !identityPartPattern.MatchString(part)) {
			return fmt.Errorf("%q is no part of a catalog identity", part)
		}
	}
	return nil
}

// Whole is whether a pattern names its kind, its namespace and its
// name, and so one entity, found nowhere but by itself: Ref(p) is it.
func (p Pattern) Whole() bool { return p.Kind != "" && p.Namespace != "" && p.Name != "" }

// String is a pattern as it was typed: what it leaves out is left out.
func (p Pattern) String() string {
	out := p.Name
	if p.Namespace != "" {
		out = p.Namespace + "/" + out
	}
	if p.Kind != "" {
		out = p.Kind + ":" + out
	}
	return out
}

// Matches is whether a pattern names an entity: by its kind and its
// name, and by its namespace where it says one.
func (p Pattern) Matches(ref Ref) bool {
	return p.Name == ref.Name && p.Kind == ref.Kind && (p.Namespace == "" || p.Namespace == ref.Namespace)
}

// Find returns the one entity of those held that the pattern names.
// Where it names none, found is false; where it names several, it is
// refused, naming each, since which was meant is the person's to say.
func (p Pattern) Find(held []Ref) (one Ref, found bool, err error) {
	var named []Ref
	for _, each := range held {
		if p.Matches(each) && !slices.Contains(named, each) {
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
	return Ref{}, false, fmt.Errorf("%s names %d entities, %s: name one in full", p, len(named), strings.Join(written, ", "))
}
