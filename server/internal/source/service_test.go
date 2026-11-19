package source_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
	"github.com/frontseat-dev/frontseat/server/internal/source"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
)

// stalled is the tapes system, whose clones wait until the test lets
// them go on: a daemon is bringing for as long as the test says.
type stalled struct {
	*tapes
	gate chan struct{}
}

func (s stalled) Clone(ctx context.Context, directory string, from vcs.Source) (string, error) {
	<-s.gate
	return s.tapes.Clone(ctx, directory, from)
}

// sourceOver serves the source API of a daemon over a system, and
// returns a client of it.
func sourceOver(t *testing.T, mounted bool, system vcs.System) sourcev1alpha1connect.SourceServiceClient {
	t.Helper()
	held, err := source.Open(t.TempDir(), mounted, system)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(source.Serve(held))
	t.Cleanup(srv.Close)
	return sourcev1alpha1connect.NewSourceServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
}

// reasonOf holds that a refusal is of a code and a reason of the source
// API's domain.
func reasonOf(t *testing.T, err error, code connect.Code, want string) {
	t.Helper()
	reason, domain := frontseat.Reason(err)
	if connect.CodeOf(err) != code || reason != want || domain != refusal.Source {
		t.Errorf("refused %v with the reason %q of %q, want %v with %s of %s", err, reason, domain, code, want, refusal.Source)
	}
}

// What a daemon refuses to hold is refused by a reason of its own:
// another source while it brings one, SOURCE_POPULATING; nothing to
// bring, SOURCE_MISSING; a repository it cannot hold, SOURCE_UNFIT; and
// anything, where it was given a checkout, SOURCE_MOUNTED; one told at
// no generation, GENERATION_INVALID. The control
// is the same source told once bringing is done, which is taken, at the
// generation told.
func TestWhatADaemonRefusesToHoldIsRefusedByAReason(t *testing.T) {
	ctx := context.Background()
	system := stalled{tapes: &tapes{}, gate: make(chan struct{})}
	api := sourceOver(t, false, system)
	asked := &sourcev1.SourceSpec{Generation: 1, Repositories: []*sourcev1.Checkout{{Remote: "vault/reels", Name: "reels"}}}
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: asked}); err != nil {
		t.Fatal(err)
	}
	_, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: 2, Repositories: asked.GetRepositories()}})
	reasonOf(t, err, connect.CodeFailedPrecondition, "SOURCE_POPULATING")
	// Waited after the generation it brings, it is not answered until
	// that is done; after another, at once.
	other, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 7})
	if err != nil || other.GetSource().GetState() != sourcev1.Source_STATE_POPULATING || other.GetSource().GetGeneration() != 1 {
		t.Errorf("waited after another generation: %v (%v), want it at once, bringing the first", other.GetSource(), err)
	}
	// Waited after the generation it brings, with a deadline, it is
	// answered before the deadline, still bringing it: a deadline the
	// caller reaches is a daemon that does not answer.
	waiting, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	started := time.Now()
	still, err := api.WaitSource(waiting, &sourcev1.WaitSourceRequest{Generation: 1})
	cancel()
	if err != nil || still.GetSource().GetState() != sourcev1.Source_STATE_POPULATING || time.Since(started) < 200*time.Millisecond {
		t.Errorf("waited after the generation it brings: %v (%v) after %v, want it waited for, and answered bringing before the deadline", still.GetSource(), err, time.Since(started))
	}
	close(system.gate)
	done, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 1})
	if err != nil || done.GetSource().GetState() != sourcev1.Source_STATE_READY || len(done.GetSource().GetRepositories()) != 1 {
		t.Fatalf("once brought it is %v (%v)", done.GetSource(), err)
	}
	// The control: told again once it is done, it takes the generation.
	again, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: 2, Repositories: asked.GetRepositories()}})
	if err != nil || again.GetSource().GetGeneration() != 2 {
		t.Errorf("told again once done: %v (%v), want it taken at the generation 2", again.GetSource(), err)
	}
	if _, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 2}); err != nil {
		t.Fatal(err)
	}

	_, err = api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: 3}})
	reasonOf(t, err, connect.CodeInvalidArgument, "SOURCE_MISSING")
	_, err = api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Repositories: asked.GetRepositories()}})
	reasonOf(t, err, connect.CodeInvalidArgument, "GENERATION_INVALID")
	_, err = api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: 3, Repositories: []*sourcev1.Checkout{{Remote: "vault/reels", Name: "../elsewhere"}}}})
	reasonOf(t, err, connect.CodeInvalidArgument, "SOURCE_UNFIT")
	_, err = sourceOver(t, true, nil).SetSource(ctx, &sourcev1.SetSourceRequest{Spec: asked})
	reasonOf(t, err, connect.CodeFailedPrecondition, "SOURCE_MOUNTED")
}

// A daemon keeps no spec: started again over what it brought, it holds
// that and was told no generation, so that whoever tells it knows to
// tell it again. The control is the daemon before, which holds the
// generation it was told.
func TestADaemonStartedAgainHoldsWhatItBroughtAndNoGeneration(t *testing.T) {
	ctx := context.Background()
	root, system := t.TempDir(), &tapes{}
	first, err := source.Open(root, false, system)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(source.Serve(first))
	defer srv.Close()
	api := sourcev1alpha1connect.NewSourceServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: 5, Repositories: []*sourcev1.Checkout{{Remote: "vault/reels", Name: "reels"}}}}); err != nil {
		t.Fatal(err)
	}
	before, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 5})
	if err != nil || before.GetSource().GetGeneration() != 5 || len(before.GetSource().GetRepositories()) != 1 {
		t.Fatalf("before it was started again it is %v (%v)", before.GetSource(), err)
	}
	again, err := source.Open(root, false, system)
	if err != nil {
		t.Fatal(err)
	}
	if now, _ := again.Now(); now.Generation != 0 || len(now.Repositories) != 1 || now.State != source.Ready {
		t.Errorf("started again it holds %v at the generation %d, want what it brought and none", now.Repositories, now.Generation)
	}
}
