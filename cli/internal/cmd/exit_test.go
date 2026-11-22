package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

// refusal is a server's refusal of a code, with a reason.
func refusal(t *testing.T, code connect.Code, reason, message string) error {
	t.Helper()
	refused := connect.NewError(code, message)
	if reason != "" {
		detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason, Domain: "workspace.frontseat.dev"})
		if err != nil {
			t.Fatal(err)
		}
		refused = refused.WithDetail(detail)
	}
	return refused
}

// Each way a command fails ends with its own code, and says what went
// wrong in a person's words: a server's message without the code word
// before it, its reason, and what to do where the command line knows.
// An interrupted command says nothing.
func TestEachWayACommandFailsHasItsOwnCodeAndSaysWhatToDo(t *testing.T) {
	serverAt(t, "http://127.0.0.1:1")
	for name, c := range map[string]struct {
		err  error
		code int
		says []string
	}{
		"refused by name":   {refusal(t, connect.CodeFailedPrecondition, frontseat.ReasonSpecFixed, "the image is said once"), ExitRefused, []string{"the image is said once [SPEC_FIXED]"}},
		"no such workspace": {fmt.Errorf("opening: %w", refusal(t, connect.CodeNotFound, frontseat.ReasonWorkspaceNotFound, "there is no workspace \"x\"")), ExitRefused, []string{"opening: there is no workspace \"x\" [WORKSPACE_NOT_FOUND]", "frontseat workspace list"}},
		"not served":        {refusal(t, connect.CodeUnimplemented, frontseat.ReasonEntitiesNotServed, "no catalog"), ExitRefused, []string{"no catalog", "this control plane does not serve it [ENTITIES_NOT_SERVED]"}},
		"no token":          {refusal(t, connect.CodeUnauthenticated, frontseat.ReasonIdentityMissing, "who are you"), ExitUnauthorized, []string{"who are you", "refused your token", "local/token"}},
		"not yours":         {refusal(t, connect.CodePermissionDenied, frontseat.ReasonNotOwner, "another's"), ExitUnauthorized, []string{"another's [NOT_OWNER]"}},
		"no answer":         {refusal(t, connect.CodeUnavailable, "", "connection refused"), ExitUnanswered, []string{"connection refused", "does not answer"}},
		"too slow":          {refusal(t, connect.CodeDeadlineExceeded, "", "too slow"), ExitUnanswered, []string{"too slow"}},
		"the SDK's own":     {fmt.Errorf("opening: %w", frontseat.ErrWorkspaceNotReady), ExitRefused, nil},
		"written wrongly":   {errors.New("unknown flag: --nope"), ExitUsage, []string{"unknown flag: --nope"}},
	} {
		code, said := Failed(context.Background(), c.err)
		if code != c.code {
			t.Errorf("%s: exits %d, want %d", name, code, c.code)
		}
		for _, want := range c.says {
			if !strings.Contains(said, want) {
				t.Errorf("%s: says %q, want %q in it", name, said, want)
			}
		}
		if strings.HasPrefix(said, connect.CodeOf(c.err).String()+":") {
			t.Errorf("%s: says the code word first: %q", name, said)
		}
	}
	interrupted, cancel := context.WithCancel(context.Background())
	cancel()
	if code, said := Failed(interrupted, context.Canceled); code != ExitInterrupted || said != "" {
		t.Errorf("interrupted: exits %d, saying %q", code, said)
	}
	if code, _ := Failed(context.Background(), context.Canceled); code == ExitInterrupted {
		t.Error("the control: a cancellation of the command's own is taken for an interrupt")
	}
	// A command line written wrongly exits 2, from the command line as a
	// whole; the help lists every code.
	if code, err := Execute(context.Background(), []string{"build", "--nope"}); code != ExitUsage || err == nil {
		t.Errorf("an unknown flag exits %d, %v", code, err)
	}
	for _, code := range []string{"\n  0 ", "\n  1 ", "\n  2 ", "\n  36 ", "\n  37 ", "\n  130 ", "\n  255 "} {
		if !strings.Contains(rootCmd.Long, code) {
			t.Errorf("frontseat --help does not list %q", strings.TrimSpace(code))
		}
	}
}
