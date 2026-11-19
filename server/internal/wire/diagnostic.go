package wire

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"path"
	"strings"
)

// SourceMistake retains a host rule's structured reason, source and remediation
// on the same canonical status boundary used by plugins and saved runs.
func SourceMistake(repository, file, rule, message, help string) error {
	_, reason, _ := strings.Cut(rule, "/")
	mistake := &model.Mistake{File: path.Join(repository, file), Message: message}
	var source *typev1.SourceLocation
	if file != "" {
		source = &typev1.SourceLocation{Repository: repository, Path: file}
	}
	failure, err := status.New(codes.FailedPrecondition, mistake.Error()).WithDetails(
		&errdetails.ErrorInfo{Reason: strings.ToUpper(strings.ReplaceAll(reason, "-", "_")), Domain: "build.frontseat.dev"},
		&typev1.Diagnostic{RuleId: rule, Message: message, Help: help, SourceLocation: source},
	)
	if err != nil {
		return err
	}
	mistake.Cause = failure.Err()
	return mistake
}
