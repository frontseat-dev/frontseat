package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
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
	kept := serverAt(t, "http://127.0.0.1:1")
	// Of a command that asked the control plane, as one that asked no
	// build daemon did.
	reached = nil
	for name, c := range map[string]struct {
		err  error
		code int
		says []string
	}{
		"refused by name":   {refusal(t, connect.CodeFailedPrecondition, frontseat.ReasonSpecFixed, "the image is said once"), ExitRefused, []string{"the image is said once [SPEC_FIXED]"}},
		"no such workspace": {fmt.Errorf("opening: %w", refusal(t, connect.CodeNotFound, frontseat.ReasonWorkspaceNotFound, "there is no workspace \"x\"")), ExitRefused, []string{"opening: there is no workspace \"x\" [WORKSPACE_NOT_FOUND]", "frontseat workspace list"}},
		"not served":        {refusal(t, connect.CodeUnimplemented, frontseat.ReasonEntitiesNotServed, "no catalog"), ExitRefused, []string{"no catalog", "this control plane does not serve it [ENTITIES_NOT_SERVED]"}},
		"no token":          {refusal(t, connect.CodeUnauthenticated, frontseat.ReasonIdentityMissing, "who are you"), ExitUnauthorized, []string{"who are you", "the control plane refused your token: it is " + filepath.Join(kept, "token") + ", beside the server it is for"}},
		"not yours":         {refusal(t, connect.CodePermissionDenied, frontseat.ReasonNotOwner, "another's"), ExitUnauthorized, []string{"another's [NOT_OWNER]"}},
		"no answer":         {refusal(t, connect.CodeUnavailable, "", "connection refused"), ExitUnanswered, []string{"connection refused", "the control plane " + filepath.Join(kept, "server") + " names does not answer"}},
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
	// What a build daemon does not serve is said of it, and never of a
	// control plane the command did not ask.
	reached = &daemonAsked{directory: "/a/directory", log: "/its/daemon.log"}
	t.Cleanup(func() { reached = nil })
	unserved := refusal(t, connect.CodeUnimplemented, frontseat.ReasonEntitiesNotServed, "no catalog")
	if _, said := Failed(context.Background(), unserved); !strings.Contains(said, "no catalog\nthe build daemon of /a/directory does not serve it [ENTITIES_NOT_SERVED]") || strings.Contains(said, "control plane") {
		t.Errorf("not served by a build daemon: says %q", said)
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

// A server that did not answer, or refused who asked, is said as the
// one the command asked: a command that builds this machine's directory
// asked that directory's build daemon and no control plane, and is told
// so, with where all that daemon printed is kept; inside a workspace's
// sandbox it is the workspace's; and of a token refused, that it is the
// daemon's own, never the person's. No daemon's token is said. The
// control is a workspace command, which asked the control plane and is
// told of it, by its files as they are named on this machine.
func TestAServerThatFailedIsSaidAsTheOneThatWasAsked(t *testing.T) {
	// A control plane, and a build daemon, that refuse whoever asks.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "who are you", http.StatusUnauthorized)
	}))
	t.Cleanup(refusing.Close)
	kept := serverAt(t, "http://127.0.0.1:1")
	noDaemon(t)
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if here, err = filepath.EvalSymlinks(here); err != nil {
		t.Fatal(err)
	}
	const log = "/state/of/the/daemon/daemon.log"
	found := func(address string) {
		builder = func(context.Context, string, bool) (machine.Builder, error) {
			return machine.Builder{Address: strings.TrimPrefix(address, "http://"), Token: "the-daemons-token", Log: log}, nil
		}
	}
	of := "the build daemon of " + here
	for name, c := range map[string]struct {
		daemon string
		inside bool
		args   []string
		code   int
		says   []string
	}{
		"the directory's daemon, gone":      {"http://127.0.0.1:1", false, []string{"build", "list"}, ExitUnanswered, []string{"it was " + of + " that was asked, which the command line started on this machine, and no control plane: all it said is in " + log + ", and one that is gone is started again by the next command"}},
		"the directory's daemon, refusing":  {refusing.URL, false, []string{"build", "list"}, ExitUnauthorized, []string{"it was " + of + " that refused, of the token it wrote itself beside " + log}},
		"the workspace's daemon, gone":      {"http://127.0.0.1:1", true, []string{"build", "list"}, ExitUnanswered, []string{"it was the build daemon of this workspace that was asked, which the command line started here for " + besideDirectory + ": all it said is in " + log}},
		"the control: the control plane":    {"", false, []string{"workspace", "list"}, ExitUnanswered, []string{"the control plane " + filepath.Join(kept, "server") + " names does not answer"}},
		"the control: after a build, again": {"", false, []string{"workspace", "describe", "named"}, ExitUnanswered, []string{"the control plane " + filepath.Join(kept, "server") + " names does not answer"}},
	} {
		found(c.daemon)
		if c.inside {
			inside = func() bool { return true }
			directory = func(bool) (string, error) { return besideDirectory, nil }
		}
		_, code, err := runAt(t, context.Background(), "", c.args...)
		inside, directory = outside, machine.Directory
		_, said := Failed(context.Background(), err)
		if code != c.code {
			t.Errorf("%s: ended %d (%v), want %d", name, code, err, c.code)
		}
		for _, want := range c.says {
			if !strings.Contains(said, want) {
				t.Errorf("%s: says %q, want %q in it", name, said, want)
			}
		}
		if asksDaemon := c.daemon != ""; asksDaemon == strings.Contains(said, "control plane "+kept) || asksDaemon == !strings.Contains(said, "build daemon") || strings.Contains(said, "local stack") || strings.Contains(said, "your token") {
			t.Errorf("%s: says %q, which is said of another server than the one asked", name, said)
		}
		if strings.Contains(said, "the-daemons-token") {
			t.Errorf("%s: says the daemon's token: %q", name, said)
		}
	}
	// The control: a control plane that refuses who asked is said to
	// have refused the person's token, by its file as it is named here.
	if err := os.WriteFile(filepath.Join(kept, "server"), []byte(refusing.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, code, err := runAt(t, context.Background(), "", "workspace", "list")
	if _, said := Failed(context.Background(), err); code != ExitUnauthorized || !strings.Contains(said, "the control plane refused your token: it is "+filepath.Join(kept, "token")+", beside the server it is for") || strings.Contains(said, "~/.config") || strings.Contains(said, "build daemon") {
		t.Errorf("the control: a control plane that refused who asked: %d, %q", code, said)
	}
}
