package pluginhost

import (
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func errorDetail(t *testing.T, message proto.Message) *anypb.Any {
	t.Helper()
	detail, err := anypb.New(message)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestPluginFailuresAreValidatedBeforeAttribution(t *testing.T) {
	valid := func() *statuspb.Status {
		return &statuspb.Status{Code: int32(codes.FailedPrecondition), Message: "manifest: repair this", Details: []*anypb.Any{
			errorDetail(t, &errdetails.ErrorInfo{Reason: "INVALID_MANIFEST", Domain: "go.plugins.frontseat.dev"}),
			errorDetail(t, &typev1.Diagnostic{RuleId: "go/invalid-manifest", Message: "repair this", SourceLocation: &typev1.SourceLocation{Path: "manifest", Line: 1}, Help: "Use a valid manifest."}),
		}}
	}
	changeFinding := func(change func(*typev1.Diagnostic)) func(*statuspb.Status) {
		return func(s *statuspb.Status) {
			d := new(typev1.Diagnostic)
			if err := s.Details[1].UnmarshalTo(d); err != nil {
				t.Fatal(err)
			}
			change(d)
			s.Details[1] = errorDetail(t, d)
		}
	}
	cases := map[string]func(*statuspb.Status){
		"success":              func(s *statuspb.Status) { s.Code = 0 },
		"negative status":      func(s *statuspb.Status) { s.Code = -1 },
		"unknown status":       func(s *statuspb.Status) { s.Code = 17 },
		"empty message":        func(s *statuspb.Status) { s.Message = "" },
		"missing reason":       func(s *statuspb.Status) { s.Details = s.Details[1:] },
		"missing diagnostic":   func(s *statuspb.Status) { s.Details = s.Details[:1] },
		"duplicate reason":     func(s *statuspb.Status) { s.Details = append(s.Details, s.Details[0]) },
		"duplicate diagnostic": func(s *statuspb.Status) { s.Details = append(s.Details, s.Details[1]) },
		"malformed detail":     func(s *statuspb.Status) { s.Details[1].Value = []byte{0xff} },
		"nil detail":           func(s *statuspb.Status) { s.Details = append(s.Details, nil) },
		"empty detail":         func(s *statuspb.Status) { s.Details = append(s.Details, &anypb.Any{}) },
		"empty type suffix":    func(s *statuspb.Status) { s.Details = append(s.Details, &anypb.Any{TypeUrl: "type.googleapis.com/"}) },
		"malformed help": func(s *statuspb.Status) {
			s.Details = append(s.Details, &anypb.Any{TypeUrl: "type.googleapis.com/google.rpc.Help", Value: []byte{0xff}})
		},
		"unknown type": func(s *statuspb.Status) {
			s.Details = append(s.Details, &anypb.Any{TypeUrl: "type.googleapis.com/extension.Explanation", Value: []byte{0x0a, 0x01, 'x'}})
		},
		"nested status": func(s *statuspb.Status) {
			s.Details = append(s.Details, errorDetail(t, &statuspb.Status{Code: 9, Message: "another status"}))
		},
		"foreign reason": func(s *statuspb.Status) {
			s.Details[0] = errorDetail(t, &errdetails.ErrorInfo{Reason: "INVALID_MANIFEST", Domain: "buf.plugins.frontseat.dev"})
		},
		"lowercase reason": func(s *statuspb.Status) {
			s.Details[0] = errorDetail(t, &errdetails.ErrorInfo{Reason: "invalid-manifest", Domain: "go.plugins.frontseat.dev"})
		},
		"foreign rule":       changeFinding(func(d *typev1.Diagnostic) { d.RuleId = "buf/invalid-manifest" }),
		"empty rule":         changeFinding(func(d *typev1.Diagnostic) { d.RuleId = "go/" }),
		"invalid rule":       changeFinding(func(d *typev1.Diagnostic) { d.RuleId = "go/../invalid" }),
		"different reason":   changeFinding(func(d *typev1.Diagnostic) { d.RuleId = "go/something-else" }),
		"empty diagnostic":   changeFinding(func(d *typev1.Diagnostic) { d.Message = "" }),
		"foreign repository": changeFinding(func(d *typev1.Diagnostic) { d.SourceLocation.Repository = "other" }),
		"empty path":         changeFinding(func(d *typev1.Diagnostic) { d.SourceLocation.Path = "" }),
		"absolute path":      changeFinding(func(d *typev1.Diagnostic) { d.SourceLocation.Path = "/etc/passwd" }),
		"escaping path":      changeFinding(func(d *typev1.Diagnostic) { d.SourceLocation.Path = "../outside" }),
		"negative line":      changeFinding(func(d *typev1.Diagnostic) { d.SourceLocation.Line = -1 }),
	}
	for label, change := range cases {
		t.Run(label, func(t *testing.T) {
			if _, err := readError("go", valid()); err != nil {
				t.Fatalf("valid control: %v", err)
			}
			bad := valid()
			change(bad)
			if _, err := readError("go", bad); err == nil {
				t.Fatal("malformed failure accepted")
			}
		})
	}
	// A source-free diagnostic still carries remediation; other typed details
	// are preserved for clients that understand them.
	withoutPath := valid()
	changeFinding(func(d *typev1.Diagnostic) { d.SourceLocation = nil })(withoutPath)
	withoutPath.Details = append(withoutPath.Details, errorDetail(t, &errdetails.Help{Links: []*errdetails.Help_Link{{Url: "https://example.com/help"}}}))
	got, err := readError("go", withoutPath)
	if err != nil || got.Detail.GetHelp() == "" || !proto.Equal(got.Status, withoutPath) {
		t.Fatalf("source-free detail lost: %v, %v", got, err)
	}
	if _, err := protojson.Marshal(withoutPath); err != nil {
		t.Fatalf("valid status cannot reach JSON clients: %v", err)
	}
	if _, err := readError("go", nil); err == nil {
		t.Fatal("nil status accepted")
	}
}

func TestPluginErrorBindingDoesNotMutateSharedDetails(t *testing.T) {
	s := &statuspb.Status{Code: int32(codes.FailedPrecondition), Message: "manifest: repair this", Details: []*anypb.Any{
		errorDetail(t, &errdetails.ErrorInfo{Reason: "INVALID_MANIFEST", Domain: "go.plugins.frontseat.dev"}),
		errorDetail(t, &typev1.Diagnostic{RuleId: "go/invalid-manifest", Message: "repair this", SourceLocation: &typev1.SourceLocation{Path: "manifest"}}),
	}}
	e, err := readError("go", s)
	if err != nil {
		t.Fatal(err)
	}
	before := proto.Clone(s)
	for _, repository := range []string{"", "first", "second"} {
		bound, err := e.InRepository(repository)
		if err != nil {
			t.Fatal(err)
		}
		wantMessage := s.GetMessage()
		if repository != "" {
			wantMessage = repository + "/" + wantMessage
		}
		if bound.Detail.GetSourceLocation().GetRepository() != repository || bound.Error() != wantMessage {
			t.Fatalf("wrong attribution: %v", bound)
		}
		if !proto.Equal(s, before) {
			t.Fatal("binding mutated the original status")
		}
	}
}
