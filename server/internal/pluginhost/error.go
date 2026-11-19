package pluginhost

import (
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
}

func (e *Error) Error() string { return e.Status.GetMessage() }

// GRPCStatus lets standard status adapters retain typed details when wrapping.
func (e *Error) GRPCStatus() *status.Status { return status.FromProto(e.Status) }

func (e *Error) Is(target error) bool {
	return target == ErrUnsupported && e.Status.GetCode() == int32(codes.Unimplemented)
}

var errorReason = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// readError validates untrusted error data before it reaches source attribution
// or persistence. The SDK builds these details; the host still checks them.
func readError(plugin string, failure *statuspb.Status) (*Error, error) {
	if failure.GetCode() < int32(codes.Canceled) || failure.GetCode() > int32(codes.Unauthenticated) || failure.GetMessage() == "" {
		return nil, fmt.Errorf("plugin %s returned an invalid failure status", plugin)
	}
	out := &Error{Plugin: plugin, Status: failure}
	var domain string
	for _, detail := range failure.GetDetails() {
		if !strings.Contains(detail.GetTypeUrl(), "/") {
			return nil, fmt.Errorf("plugin %s returned a detail without a type URL", plugin)
		}
		decoded, err := detail.UnmarshalNew()
		if err != nil {
			return nil, fmt.Errorf("plugin %s returned an invalid failure detail: %w", plugin, err)
		}
		// The ABI extends reasons, not protobuf schemas. Every detail must be
		// readable over JSON as well as binary RPCs and durable run records.
		switch decoded.(type) {
		case *typev1.Diagnostic, *errdetails.ErrorInfo, *errdetails.RetryInfo,
			*errdetails.DebugInfo, *errdetails.QuotaFailure, *errdetails.PreconditionFailure,
			*errdetails.BadRequest, *errdetails.RequestInfo, *errdetails.ResourceInfo,
			*errdetails.Help, *errdetails.LocalizedMessage:
		default:
			return nil, fmt.Errorf("plugin %s returned unsupported failure detail %s", plugin, detail.MessageName())
		}
		if _, err := protojson.Marshal(decoded); err != nil {
			return nil, fmt.Errorf("plugin %s returned a detail JSON clients cannot read: %w", plugin, err)
		}
		switch info := decoded.(type) {
		case *errdetails.ErrorInfo:
			if out.Reason != "" || !errorReason.MatchString(info.GetReason()) || (plugin != "" && info.GetDomain() != plugin+".plugins.frontseat.dev") {
				return nil, fmt.Errorf("plugin %s returned an invalid error reason", plugin)
			}
			out.Reason = info.GetReason()
			domain = info.GetDomain()
		case *typev1.Diagnostic:
			if out.Detail != nil {
				return nil, fmt.Errorf("plugin %s returned duplicate diagnostics", plugin)
			}
			out.Detail = info
			location := out.Detail.GetSourceLocation()
			if location != nil && (location.GetRepository() != "" || !pattern.Local(location.GetPath()) || location.GetLine() < 0) {
				return nil, fmt.Errorf("plugin %s returned an invalid diagnostic location", plugin)
			}
			if plugin != "" && !strings.HasPrefix(out.Detail.GetRuleId(), plugin+"/") {
				return nil, fmt.Errorf("plugin %s returned another plugin's diagnostic", plugin)
			}
		}
	}
	if out.Reason == "" || out.Detail == nil {
		return nil, fmt.Errorf("plugin %s returned a failure without its reason and diagnostic", plugin)
	}
	owner, rule, found := strings.Cut(out.Detail.GetRuleId(), "/")
	if !found || !name.MatchString(owner) || !name.MatchString(rule) ||
		strings.ToUpper(strings.ReplaceAll(rule, "-", "_")) != out.Reason ||
		domain != owner+".plugins.frontseat.dev" || out.Detail.GetMessage() == "" {
		return nil, fmt.Errorf("plugin %s returned an invalid diagnostic identity or message", plugin)
	}
	return out, nil
}

// InRepository binds the host's repository identity without losing any detail.
// It returns a copy so cached plugin answers cannot retain another context.
func (e *Error) InRepository(repository string) (*Error, error) {
	out := &Error{Plugin: e.Plugin, Status: proto.Clone(e.Status).(*statuspb.Status), Reason: e.Reason, Detail: proto.Clone(e.Detail).(*typev1.Diagnostic)}
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
