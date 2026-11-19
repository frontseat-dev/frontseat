package wire

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"path"
)

// SourceMistake retains a host rule, the reason it is refused by, its
// source and remediation on the same canonical status boundary used by
// plugins and saved runs.
func SourceMistake(repository, file, rule, reason, message, help string) error {
	mistake := &model.Mistake{File: path.Join(repository, file), Message: message}
	var source *typev1.SourceLocation
	if file != "" {
		source = &typev1.SourceLocation{Repository: repository, Path: file}
	}
	failure, err := status.New(codes.FailedPrecondition, mistake.Error()).WithDetails(
		&errdetails.ErrorInfo{Reason: reason, Domain: "build.frontseat.dev"},
		&typev1.Diagnostic{RuleId: rule, Message: message, Help: help, SourceLocation: source},
	)
	if err != nil {
		return err
	}
	mistake.Cause = failure.Err()
	return mistake
}
