// Package enum says a value of one of the API's enums as a program and
// a person read it: by its own name, without its enum's (Name), and in
// words (Word). It is the one way the command line, the SDK and the
// server say one.
package enum

import (
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Name is a value of one of the API's enums by its own name, without its
// enum's: ExitCode's EXIT_CODE_BUILD_FAILURE is BUILD_FAILURE, Bazel's
// name for it. A value the API does not name is its number.
func Name(value protoreflect.Enum) string {
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
	return strings.TrimPrefix(string(described.Name()), prefix.String())
}

// Word is a value of one of the API's enums as a person reads it: its
// Name in lower case, its words apart, so that Workspace.State's
// STATE_READY is "ready" and ReleaseReason.Kind's KIND_OWN_CHANGE is
// "own change". A value the API does not name is its number.
func Word(value protoreflect.Enum) string {
	return strings.ToLower(strings.ReplaceAll(Name(value), "_", " "))
}
