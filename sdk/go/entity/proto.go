package entity

import typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

// Proto is an entity's reference as the contract carries it.
func Proto(ref Ref) *typev1.EntityRef {
	return &typev1.EntityRef{Kind: ref.Kind, Namespace: ref.Namespace, Name: ref.Name}
}

// Protos are references as the contract carries them.
func Protos(refs []Ref) []*typev1.EntityRef {
	out := make([]*typev1.EntityRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, Proto(ref))
	}
	return out
}

// FromProto is a reference the contract carried, as the host names
// an entity. A kind or a namespace the message leaves empty is a
// component, and the default namespace.
func FromProto(ref *typev1.EntityRef) Ref {
	return New(ref.GetKind(), ref.GetNamespace(), ref.GetName())
}

// FromTypedProto is a reference a request carried as a person typed
// it (Typed): a kind it leaves empty is a component, and a namespace it
// leaves empty stands for any. Its parts are checked by ValidateTyped.
func FromTypedProto(ref *typev1.EntityRef) Ref {
	kind := ref.GetKind()
	if kind == "" {
		kind = KindComponent
	}
	return Ref{Kind: lowerASCII(kind), Namespace: lowerASCII(ref.GetNamespace()), Name: lowerASCII(ref.GetName())}
}
