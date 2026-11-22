package cmd

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

// The codes the command line exits with, as frontseat --help lists
// them. A build's own codes (3 where a test failed, 4 where there was
// none, 8 where it was interrupted) are the contract's, and are passed
// through by build, test and build events; run passes its program's.
const (
	// ExitDone is a command that did what it was asked.
	ExitDone = 0
	// ExitRefused is a command the server refused by name, or whose
	// work did not succeed: a release that failed or is unpublished, a
	// query --why that found no path, a program that was not run.
	ExitRefused = 1
	// ExitUsage is a command line written wrongly (an unknown flag, an
	// argument missing, flags that do not go together), or what this
	// machine keeps that cannot be read.
	ExitUsage = 2
	// ExitUnauthorized is a server that refused who asked: no token, or
	// one it does not take (UNAUTHENTICATED, PERMISSION_DENIED).
	ExitUnauthorized = 36
	// ExitUnanswered is a server that did not answer (UNAVAILABLE,
	// DEADLINE_EXCEEDED).
	ExitUnanswered = 37
	// ExitInterrupted is a command interrupted (Ctrl-C, SIGTERM), which
	// says nothing more.
	ExitInterrupted = 130
)

// exitCodes is the table of exit codes, as frontseat --help says it.
const exitCodes = `Exit codes:
  0    done
  1    the server refused it by name, or what was asked for did not
       succeed (a release that failed or is unpublished, query --why
       with no path, a program that was not run)
  2    the command line was written wrongly, or what this machine keeps
       cannot be read
  3 4 8 ...
       a build's own exit code (build, test, build events): 3 a test
       failed, 4 no tests were found, 8 interrupted
  36   the server refused who asked: no token, or a token it does not take
  37   the server did not answer
  130  interrupted
  255  workspace ssh: the SSH client could not get in
  run exits with its program's own exit code, which may be any of these.`

// Failed is how a command that failed ends: the code it exits with,
// and what is said of it, for a person, empty where nothing is
// (interrupted). What a server refused is said by its message, without
// the code word Connect puts before it, and with what to do where the
// command line knows.
func Failed(ctx context.Context, err error) (int, string) {
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || connect.CodeOf(err) == connect.CodeCanceled) {
		return ExitInterrupted, ""
	}
	if errors.Is(err, errUnfinished) {
		return ExitUnanswered, err.Error()
	}
	var raw *connect.Error
	if !errors.As(err, &raw) {
		var own *frontseat.Error
		if errors.As(err, &own) {
			return ExitRefused, err.Error()
		}
		return ExitUsage, err.Error()
	}
	said := strings.Replace(err.Error(), raw.Error(), raw.Message(), 1)
	reason, _ := frontseat.Reason(err)
	switch code := raw.Code(); {
	case code == connect.CodeUnauthenticated:
		return ExitUnauthorized, said + "\nthe control plane refused your token: it is ~/.config/frontseat/local/token, beside the server it is for"
	case code == connect.CodePermissionDenied:
		return ExitUnauthorized, withReason(said, reason)
	case code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded:
		return ExitUnanswered, said + "\n" + unanswered()
	case reason == frontseat.ReasonWorkspaceNotFound:
		return ExitRefused, withReason(said, reason) + "\n`frontseat workspace list` lists the workspaces there are"
	case code == connect.CodeUnimplemented && strings.HasSuffix(reason, "_NOT_SERVED"):
		return ExitRefused, said + "\nthis control plane does not serve it [" + reason + "]"
	}
	return ExitRefused, withReason(said, reason)
}

// withReason is what is said, with the reason the server gave, by which
// a person finds it in the contract.
func withReason(said, reason string) string {
	if reason == "" {
		return said
	}
	return said + " [" + reason + "]"
}
