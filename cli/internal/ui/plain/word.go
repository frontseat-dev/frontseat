package plain

import (
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Word is a value of one of the contract's enums as a person reads it:
// its name without its enum's, in lower case, its words apart, so that
// Workspace.State's STATE_READY is "ready" and ReasonKind's
// REASON_KIND_OWN_CHANGE is "own change". A value the contract does not
// name is its number.
func Word(value protoreflect.Enum) string {
	described := value.Descriptor().Values().ByNumber(value.Number())
	if described == nil {
		return strconv.Itoa(int(value.Number()))
	}
	var prefix strings.Builder
	for i, r := range string(value.Descriptor().Name()) {
		if i > 0 && unicode.IsUpper(r) {
			prefix.WriteByte('_')
		}
		prefix.WriteRune(unicode.ToUpper(r))
	}
	prefix.WriteByte('_')
	name := strings.TrimPrefix(string(described.Name()), prefix.String())
	return strings.ToLower(strings.ReplaceAll(name, "_", " "))
}

// Shown is text a server gave, as a line may hold it (shown).
func Shown(text string) string { return shown(text) }
