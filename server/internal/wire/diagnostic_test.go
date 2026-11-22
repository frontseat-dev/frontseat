package wire

import (
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// details are a failure's details, read back as what they are.
func details(t *testing.T, failure *statuspb.Status) []proto.Message {
	t.Helper()
	var out []proto.Message
	for _, detail := range failure.GetDetails() {
		read, err := detail.UnmarshalNew()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, read)
	}
	return out
}

// A mistake is sent with each of its parts in a field of its own: where
// it is, the rule that found it as the reason it is refused by, what is
// wrong, and what to do; the failure's message says it in words, as
// whatever wraps it does.
func TestAMistakeIsSentWithEachPartInAFieldOfItsOwn(t *testing.T) {
	mistake := &model.Mistake{Repository: "lab", File: "app/catalog-info.yaml", Line: 7, Rule: "catalog/duplicate-entity", Reason: "DUPLICATE_ENTITY", Message: "component:app is described twice", Help: "give it one descriptor"}
	said := "evaluating: " + mistake.Error()
	failure := Failure(mistake, codes.FailedPrecondition, said)
	if failure.GetCode() != int32(codes.FailedPrecondition) || failure.GetMessage() != said {
		t.Fatalf("sent as %v", failure)
	}
	want := []proto.Message{
		&errdetails.ErrorInfo{Reason: "DUPLICATE_ENTITY", Domain: "build.frontseat.dev"},
		&typev1.Diagnostic{RuleId: "catalog/duplicate-entity", Message: "component:app is described twice", Help: "give it one descriptor", SourceLocation: &typev1.SourceLocation{Repository: "lab", Path: "app/catalog-info.yaml", Line: 7}},
	}
	got := details(t, failure)
	if len(got) != len(want) {
		t.Fatalf("sent %v, want %v", got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("sent %v, want %v", got[i], want[i])
		}
	}
	if mistake.Error() != "lab/app/catalog-info.yaml:7: component:app is described twice; give it one descriptor" {
		t.Errorf("said %q", mistake.Error())
	}
}

// A mistake of a repository and no file of it is sent with no place in
// it; one that says nothing but its message is sent as its message, its
// reason left to whoever refuses it.
func TestAMistakeSaysOnlyWhatItKnows(t *testing.T) {
	got := details(t, Failure(&model.Mistake{Repository: "lab", Rule: "catalog/generated-without-consent", Reason: "GENERATED_WITHOUT_CONSENT", Message: "api generates gen"}, codes.FailedPrecondition, "lab: api generates gen"))
	if len(got) != 2 || got[1].(*typev1.Diagnostic).GetSourceLocation() != nil || got[0].(*errdetails.ErrorInfo).GetReason() != "GENERATED_WITHOUT_CONSENT" {
		t.Errorf("a mistake of no file was sent with %v", got)
	}
	plain := Failure(&model.Mistake{Message: "no action is named tset"}, codes.FailedPrecondition, "no action is named tset")
	if len(plain.GetDetails()) != 0 || plain.GetMessage() != "no action is named tset" {
		t.Errorf("a mistake of its message alone was sent as %v", plain)
	}
	// The control: a mistake of a file and no rule names its place, and
	// no reason.
	got = details(t, Failure(&model.Mistake{File: "frontseat.yaml", Line: 3, Message: "it takes no key jobs"}, codes.FailedPrecondition, "frontseat.yaml:3: it takes no key jobs"))
	if len(got) != 1 || got[0].(*typev1.Diagnostic).GetSourceLocation().GetLine() != 3 || got[0].(*typev1.Diagnostic).GetRuleId() != "" {
		t.Errorf("a mistake of a file was sent with %v", got)
	}
}

// A mistake that wraps a plugin's failure is sent as the plugin's, with
// the plugin's own details, saying the message given.
func TestAMistakeThatWrapsAFailureSendsItsDetails(t *testing.T) {
	finding := &typev1.Diagnostic{RuleId: "go/invalid-manifest", Message: "no module", SourceLocation: &typev1.SourceLocation{Repository: "lab", Path: "go.mod", Line: 1}}
	plugin, err := status.New(codes.InvalidArgument, "go.mod: no module").WithDetails(finding)
	if err != nil {
		t.Fatal(err)
	}
	failure := Failure(&model.Mistake{Message: "lab/go.mod: no module", Cause: plugin.Err()}, codes.FailedPrecondition, "evaluating: lab/go.mod: no module")
	got := details(t, failure)
	if failure.GetCode() != int32(codes.InvalidArgument) || failure.GetMessage() != "evaluating: lab/go.mod: no module" || len(got) != 1 || !proto.Equal(got[0], finding) {
		t.Errorf("sent as %v with %v", failure, got)
	}
}
