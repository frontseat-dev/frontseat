// Package entity owns provider-neutral entity references and their API representation.
package entity

import (
	"fmt"
	"regexp"
	"strings"
)

// The kind and namespace used by Frontseat's abbreviated references.
const (
	KindComponent    = "component"
	NamespaceDefault = "default"
)

// Ref names a Frontseat entity by kind, namespace and name. It is written
// "component:default/gateway". Adapters map provider identities to this key;
// provider-native identifiers and schemas are not part of its grammar.
type Ref struct {
	// Kind is its kind, in lower case: "component", "system", "group".
	Kind string
	// Namespace is its namespace.
	Namespace string
	// Name is its name.
	Name string
}

// New returns the reference of an entity of a kind, in a
// namespace, by a name. A kind left empty is a component, and a
// namespace left empty the default one.
func New(kind, namespace, name string) Ref {
	if kind == "" {
		kind = KindComponent
	}
	if namespace == "" {
		namespace = NamespaceDefault
	}
	return Ref{Kind: lowerASCII(kind), Namespace: lowerASCII(namespace), Name: lowerASCII(name)}
}

// Preserve non-ASCII input for Validate to refuse. Unicode case folding can
// turn characters such as the Kelvin sign into an otherwise valid ASCII key.
func lowerASCII(value string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'A' && c <= 'Z' {
			return c + ('a' - 'A')
		}
		return c
	}, value)
}

// Parse reads a reference as a person writes one: "gateway",
// "component:gateway" or "component:default/gateway". A kind left out is
// a component, and a namespace left out is the default one.
func Parse(written string) (Ref, error) {
	return ParseIn(written, NamespaceDefault)
}

// ParseIn reads a reference as an entity's own file writes one
// of another entity: a namespace left out is the namespace of the entity
// whose file it is.
func ParseIn(written, namespace string) (Ref, error) {
	kind, rest, qualified := strings.Cut(written, ":")
	if !qualified {
		kind, rest = "", written
	}
	within, name, placed := strings.Cut(rest, "/")
	if !placed {
		within, name = namespace, rest
	}
	if qualified && kind == "" || placed && within == "" || name == "" || strings.ContainsAny(name, ":/") {
		return Ref{}, fmt.Errorf("%q names no entity: a reference is a name, kind:name or kind:namespace/name", written)
	}
	ref := New(kind, within, name)
	return ref, ref.Validate()
}

var identityPartPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9_.]*[a-z0-9])?$`)

// Validate checks Frontseat's canonical key: each part is 1–255 ASCII bytes,
// lower case, with alphanumeric endpoints and hyphens, dots or underscores
// inside. Provider-specific field policies belong to the provider adapter.
// It supplies no defaults; a plugin's wire reference must be fully qualified.
func (r Ref) Validate() error {
	for _, part := range []string{r.Kind, r.Namespace, r.Name} {
		if len(part) > 255 || !identityPartPattern.MatchString(part) {
			return fmt.Errorf("%q is not a canonical catalog identity", r.String())
		}
	}
	return nil
}

// String writes the reference in full: "component:default/gateway".
func (r Ref) String() string {
	return r.Kind + ":" + r.Namespace + "/" + r.Name
}

// MarshalText writes the reference in full, for a file that holds one.
func (r Ref) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// UnmarshalText reads a reference from a file that holds one.
func (r *Ref) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}
