package build_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/build/daemon/build"
	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/build/daemon/life/lifetest"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// A build daemon's life over the build daemon itself, as the daemon's
// program composes them (life.Serve): its handler, what it says of a
// call (Daemon.Yields) and of what it has in hand (Daemon.Busy). A wait
// for what a build read to change, under way, gives way to a stop: the
// daemon answers 204 and ends, and the wait with it. Controls: a stop
// while a build is in hand is answered 409, and so is one while a build
// is in hand and the wait is under way beside it.
func TestAWaitForWhatABuildReadToChangeGivesWayToAStopAndABuildInHandDoesNot(t *testing.T) {
	daemon, hold := build.Holding(t)
	state := filepath.Join(t.TempDir(), "state")
	waiting := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == buildv1alpha1connect.BuildServiceWaitSourceChangedProcedure {
			// Admitted, and counted as under way.
			waiting <- struct{}{}
		}
		daemon.Handler.ServeHTTP(w, r)
	})
	ctx, stop := context.WithCancel(t.Context())
	ended := make(chan error, 1)
	go func() {
		ended <- life.Keep(ctx, life.Life{State: state, Said: life.Info{Version: "v1", Settings: "s1", Directory: "/work"}, Handler: handler, Busy: daemon.Busy, Yields: daemon.Yields, Idle: time.Hour})
	}()
	t.Cleanup(stop)
	var info life.Info
	for began := time.Now(); ; time.Sleep(5 * time.Millisecond) {
		var err error
		if info, err = life.Told(state); err == nil {
			break
		}
		if time.Since(began) > 30*time.Second {
			t.Fatal("the daemon never said where it serves")
		}
	}
	client := buildv1alpha1connect.NewBuildServiceClient(connect.NewClient(connecthttp.NewTransport(lifetest.Client(info.Address, info.Token), "http://"+info.Address)))
	stopped := func(what string, want int) {
		t.Helper()
		if status := lifetest.Asked(t, info.Address, http.MethodPost, life.StopPath, info.Token); status != want {
			t.Fatalf("%s: asked to stop, the daemon answered %d; want %d", what, status, want)
		}
	}
	created := func(only ...string) string {
		t.Helper()
		made, err := client.CreateBuild(t.Context(), &buildv1.CreateBuildRequest{Only: only})
		if err != nil {
			t.Fatal(err)
		}
		return made.GetBuild().GetId()
	}
	ended1, err := client.WaitBuild(t.Context(), &buildv1.WaitBuildRequest{BuildId: created("compile")})
	if err != nil {
		t.Fatal(err)
	}
	hold.Store(true)
	inHand := created("lint")
	stopped("the control, a build in hand", http.StatusConflict)

	waited := make(chan error, 1)
	go func() {
		_, err := client.WaitSourceChanged(context.Background(), &buildv1.WaitSourceChangedRequest{BuildId: ended1.GetBuild().GetId()})
		waited <- err
	}()
	<-waiting
	stopped("the control, a build in hand and a wait under way", http.StatusConflict)
	if _, err := client.CancelBuild(t.Context(), &buildv1.CancelBuildRequest{BuildId: inHand}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WaitBuild(t.Context(), &buildv1.WaitBuildRequest{BuildId: inHand}); err != nil {
		t.Fatal(err)
	}
	stopped("a wait under way, and nothing in hand", http.StatusNoContent)
	select {
	case err := <-waited:
		if err == nil {
			t.Error("the wait was answered, not ended with the daemon")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the wait went on after the daemon was stopped")
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("the daemon ended with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("asked to stop, the daemon never ended")
	}
}
