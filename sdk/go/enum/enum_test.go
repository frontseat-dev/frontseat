package enum_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/enum"
)

// A value of an enum is said in words, without its enum's name; one the
// contract does not name is said by its number.
func TestAnEnumsValueIsSaidInWords(t *testing.T) {
	for want, value := range map[string]protoreflect.Enum{
		"ready":      workspacev1.WorkspaceStatus_STATE_READY,
		"own change": buildv1.ReleaseReason_KIND_OWN_CHANGE,
		"grid cache": buildv1.Provenance_PROVENANCE_GRID_CACHE,
		"unpushed":   buildv1.Release_STATE_UNPUSHED,
		"99":         buildv1.Outcome(99),
	} {
		if got := enum.Word(value); got != want {
			t.Errorf("%v is said %q, want %q", value, got, want)
		}
	}
}

// A value of an enum is named without its enum's name, as it is
// written otherwise: how a build ended is Bazel's name for it. One the
// contract does not name is its number.
func TestAnEnumsValueIsNamedWithoutItsEnumsName(t *testing.T) {
	for want, value := range map[string]protoreflect.Enum{
		"BUILD_FAILURE":  buildv1.ExitCode_EXIT_CODE_BUILD_FAILURE,
		"NO_TESTS_FOUND": buildv1.ExitCode_EXIT_CODE_NO_TESTS_FOUND,
		"READY":          workspacev1.WorkspaceStatus_STATE_READY,
		"42":             buildv1.ExitCode(42),
	} {
		if got := enum.Name(value); got != want {
			t.Errorf("%v is named %q, want %q", value, got, want)
		}
	}
}
