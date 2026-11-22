package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	credentialv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1/credentialv1alpha1connect"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// messages reads what a command printed with --json: one message of
// the API to a line, each read as the type made for its line.
func messages[T proto.Message](t *testing.T, printed string, made func() T) []T {
	t.Helper()
	var out []T
	for _, line := range strings.Split(strings.TrimSuffix(printed, "\n"), "\n") {
		message := made()
		if err := protojson.Unmarshal([]byte(line), message); err != nil {
			t.Fatalf("%q is no line of JSON of the API's message: %v", line, err)
		}
		out = append(out, message)
	}
	return out
}

// With --json, what a command prints is the API's own message, one to a
// line, by proto field names: a list, a resource described, a build's
// events (and no log inlined beneath a failed action), a plan whose etag
// is what ship --etag takes. The control is each without it, which
// prints lines for a person.
func TestJSONIsTheAPIsOwnMessagesOneToALine(t *testing.T) {
	f := &failedAction{lines: 3}
	at := serving(t, func(api *connect.Server) { buildv1alpha1connect.RegisterBuildServiceHandler(api, f) })
	printed, _, err := runAt(t, context.Background(), at, "build", "events", "build_7", "--json")
	if err != nil {
		t.Fatal(err)
	}
	events := messages(t, printed, func() *buildv1.BuildEvent { return &buildv1.BuildEvent{} })
	if len(events) != 3 || events[0].GetActionFinished().GetResult().GetOutcome() != buildv1.Outcome_OUTCOME_FAILED || strings.Contains(printed, "line 0") || !strings.Contains(printed, `"exit_code"`) {
		t.Errorf("build events --json printed %q", printed)
	}
	if printed, _, err := runAt(t, context.Background(), at, "build", "describe", "build_7", "--json"); err != nil || messages(t, printed, func() *buildv1.Build { return &buildv1.Build{} })[0].GetFailure() != "1 action failed" || !strings.Contains(printed, `"STATE_FINISHED"`) {
		t.Errorf("build describe --json printed %q (%v)", printed, err)
	}
	if printed, _, err := runAt(t, context.Background(), at, "build", "actions", "build_7", "--json"); err != nil || len(messages(t, printed, func() *buildv1.ActionResult { return &buildv1.ActionResult{} })) != 2 {
		t.Errorf("build actions --json printed %q (%v)", printed, err)
	}
	if printed, _, err := runAt(t, context.Background(), at, "build", "describe", "build_7"); err != nil || strings.HasPrefix(printed, "{") {
		t.Errorf("the control: build describe printed %q (%v)", printed, err)
	}

	d := &daemonOf{}
	printed, _, err = run(t, d, "ship", "--dry-run", "greet", "--json")
	if plan := messages(t, printed, func() *buildv1.ReleasePlan { return &buildv1.ReleasePlan{} }); err != nil || len(plan) != 1 || plan[0].GetEtag() != "etag-of-the-plan" {
		t.Errorf("ship --dry-run --json printed %q (%v)", printed, err)
	}

	plane := serving(t, func(api *connect.Server) {
		workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, keeping{daemon: func() string { return "" }})
		credentialv1alpha1connect.RegisterCredentialServiceHandler(api, &keeper{})
	})
	serverAt(t, plane)
	if printed, _, err := runAt(t, context.Background(), "", "workspace", "list", "--json"); err != nil || messages(t, printed, func() *workspacev1.Workspace { return &workspacev1.Workspace{} })[0].GetName() != "named" {
		t.Errorf("workspace list --json printed %q (%v)", printed, err)
	}
	if printed, _, err := runAt(t, context.Background(), "", "credential", "list", "--json"); err != nil || len(messages(t, printed, func() *credentialv1.Credential { return &credentialv1.Credential{} })) != 2 || strings.Contains(printed, "secret") {
		t.Errorf("credential list --json printed %q (%v)", printed, err)
	}
}

// With --json, a failure is one line of JSON on standard error: its
// code, reason, domain, message and exit code. The control is the same
// failure without it, said for a person.
func TestWithJSONAFailureIsOneLineOfJSON(t *testing.T) {
	refused := refusal(t, connect.CodeNotFound, "WORKSPACE_NOT_FOUND", "there is no workspace \"x\"")
	asJSON = false
	if _, said := Failed(context.Background(), refused); !strings.HasPrefix(said, "frontseat: there is no workspace") {
		t.Errorf("the control: without --json it says %q", said)
	}
	asJSON = true
	t.Cleanup(func() { asJSON = false })
	code, said := Failed(context.Background(), refused)
	var line struct {
		Code, Reason, Domain, Message string
		Exit                          int
	}
	if err := json.Unmarshal([]byte(said), &line); err != nil || strings.Contains(said, "\n") {
		t.Fatalf("with --json it says %q: %v", said, err)
	}
	if line.Code != "not_found" || line.Reason != "WORKSPACE_NOT_FOUND" || line.Domain != "workspace.frontseat.dev" || !strings.Contains(line.Message, "there is no workspace") || line.Exit != code || code != ExitRefused {
		t.Errorf("with --json it says %+v, exiting %d", line, code)
	}
}
