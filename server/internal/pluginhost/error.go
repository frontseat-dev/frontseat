package pluginhost

import (
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"regexp"
	"strings"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// Error preserves the canonical failure reported by a plugin.
type Error struct {
	Plugin string
	Status *statuspb.Status
	Reason string
	Detail *typev1.Diagnostic

	// method is the method the plugin was asked, as Call spelled it.
	method string
}

func (e *Error) Error() string { return e.Status.GetMessage() }

// GRPCStatus lets standard status adapters retain typed details when wrapping.
func (e *Error) GRPCStatus() *status.Status { return status.FromProto(e.Status) }

// unknownMethod is the reason the SDK gives for a method it does not
// know.
const unknownMethod = "UNKNOWN_METHOD"

// Is reports a failure as ErrUnsupported where it says the plugin does
// not serve the method it was asked: UNIMPLEMENTED for the method's own
// reason. UNIMPLEMENTED for any other reason is of something the method
// was asked about, and is no word on the method.
func (e *Error) Is(target error) bool {
	if target != ErrUnsupported || e.Status.GetCode() != int32(codes.Unimplemented) {
		return false
	}
	return e.Reason == unknownMethod || e.method != "" && e.Reason == strings.ToUpper(e.method)+"_NOT_SERVED"
}

var errorReason = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// readError validates untrusted error data before it reaches source attribution
// or persistence. The SDK builds these details; the host still checks them.
// Each rule has a refusal of its own, which says the rule and nothing of
// what was refused; the SDK holds a failure to the same rules before it
// encodes one (sdk/plugin-test/testdata/failures.json is the table both
// run).
func readError(plugin string, failure *statuspb.Status) (*Error, error) {
	switch {
	case failure.GetCode() < int32(codes.Canceled) || failure.GetCode() > int32(codes.Unauthenticated):
		return nil, errors.New("its failure's code is no canonical code of a failure")
	case failure.GetMessage() == "":
		return nil, errors.New("its failure has no message")
	}
	out := &Error{Plugin: plugin, Status: failure}
	var domain string
	for _, detail := range failure.GetDetails() {
		if !strings.Contains(detail.GetTypeUrl(), "/") {
			return nil, errors.New("its failure has a detail without a type URL")
		}
		decoded, err := detail.UnmarshalNew()
		if err != nil {
			return nil, fmt.Errorf("its failure has a detail that cannot be read: %w", err)
		}
		// The ABI extends reasons, not protobuf schemas. Every detail must be
		// readable over JSON as well as binary RPCs and durable run records.
		switch decoded.(type) {
		case *typev1.Diagnostic, *errdetails.ErrorInfo, *errdetails.RetryInfo,
			*errdetails.DebugInfo, *errdetails.QuotaFailure, *errdetails.PreconditionFailure,
			*errdetails.BadRequest, *errdetails.RequestInfo, *errdetails.ResourceInfo,
			*errdetails.Help, *errdetails.LocalizedMessage:
		default:
			return nil, fmt.Errorf("its failure has a detail of the type %s, which is none a failure carries", detail.MessageName())
		}
		if _, err := protojson.Marshal(decoded); err != nil {
			return nil, fmt.Errorf("its failure has a detail JSON clients cannot read: %w", err)
		}
		switch info := decoded.(type) {
		case *errdetails.ErrorInfo:
			switch {
			case out.Reason != "":
				return nil, errors.New("its failure has two ErrorInfo details")
			case !errorReason.MatchString(info.GetReason()):
				return nil, errors.New("its failure's ErrorInfo reason is not UPPER_SNAKE_CASE, begun with a letter")
			case plugin != "" && info.GetDomain() != plugin+".plugins.frontseat.dev":
				return nil, fmt.Errorf("its failure's ErrorInfo domain is not %s.plugins.frontseat.dev", plugin)
			}
			out.Reason = info.GetReason()
			domain = info.GetDomain()
		case *typev1.Diagnostic:
			if out.Detail != nil {
				return nil, errors.New("its failure has two diagnostics")
			}
			out.Detail = info
			if location := out.Detail.GetSourceLocation(); location != nil {
				switch {
				case location.GetRepository() != "":
					return nil, errors.New("its failure's diagnostic names a repository, which is the host's to say")
				case !pattern.Local(location.GetPath()):
					return nil, errors.New("its failure's diagnostic is about a path that is not a file of the repository, relative to its root")
				case location.GetLine() < 0:
					return nil, errors.New("its failure's diagnostic is about a line below zero")
				}
			}
			if plugin != "" && !strings.HasPrefix(out.Detail.GetRuleId(), plugin+"/") {
				return nil, fmt.Errorf("its failure's diagnostic has a rule that is not %s/<reason>", plugin)
			}
		}
	}
	switch {
	case out.Reason == "":
		return nil, errors.New("its failure has no ErrorInfo detail, which says its reason")
	case out.Detail == nil:
		return nil, errors.New("its failure has no diagnostic")
	}
	owner, rule, found := strings.Cut(out.Detail.GetRuleId(), "/")
	switch {
	case !found || !name.MatchString(owner) || !name.MatchString(rule):
		return nil, errors.New("its failure's diagnostic has a rule that is not <plugin>/<reason>, each lower-case words of [a-z0-9] joined by single hyphens")
	case strings.ToUpper(strings.ReplaceAll(rule, "-", "_")) != out.Reason:
		return nil, errors.New("its failure's diagnostic has a rule that is not its ErrorInfo reason in lower case with hyphens")
	case domain != owner+".plugins.frontseat.dev":
		return nil, errors.New("its failure's ErrorInfo domain is not the plugin its diagnostic's rule names")
	case out.Detail.GetMessage() == "":
		return nil, errors.New("its failure's diagnostic has no message")
	}
	return out, nil
}

// InRepository binds the host's repository identity without losing any detail.
// It returns a copy so cached plugin answers cannot retain another context.
func (e *Error) InRepository(repository string) (*Error, error) {
	out := &Error{Plugin: e.Plugin, Status: proto.Clone(e.Status).(*statuspb.Status), Reason: e.Reason, Detail: proto.Clone(e.Detail).(*typev1.Diagnostic), method: e.method}
	if location := out.Detail.GetSourceLocation(); location != nil {
		location.Repository = repository
		if repository != "" {
			out.Status.Message = repository + "/" + out.Status.GetMessage()
		}
	}
	for i, detail := range out.Status.GetDetails() {
		if detail.MessageIs(&typev1.Diagnostic{}) {
			encoded, err := anypb.New(out.Detail)
			if err != nil {
				return nil, fmt.Errorf("bind plugin %s diagnostic: %w", e.Plugin, err)
			}
			out.Status.Details[i] = encoded
		}
	}
	return out, nil
}
