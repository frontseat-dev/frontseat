package life

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// kept starts a daemon's life over a state directory of the test's,
// serving a handler, and returns its info once it serves, and what it
// ended with.
func kept(t *testing.T, state string, handler http.Handler, busy func() bool, idle time.Duration) (Info, <-chan error) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	// ended is given what the daemon ended with, and over closed then,
	// so that the test and its cleanup may each wait.
	ended, over := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(over)
		ended <- Keep(ctx, Life{State: state, Said: Info{Version: "v1", Settings: "s1", Directory: "/work"}, Handler: handler, Busy: busy, Idle: idle})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-over:
		case <-time.After(30 * time.Second):
			t.Error("the daemon had not ended thirty seconds after it was stopped")
		}
	})
	for begun := time.Now(); time.Since(begun) < 30*time.Second; time.Sleep(5 * time.Millisecond) {
		if info, err := Told(state); err == nil {
			return info, ended
		}
		select {
		case <-over:
			return Info{}, ended
		default:
		}
	}
	t.Fatal("the daemon never said where it serves")
	return Info{}, nil
}

// answering is a handler that says it was reached.
var answering = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "reached") })

// reached asks a daemon at its address with a token, and returns the
// status it answered with.
func reached(t *testing.T, info Info, token string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://"+info.Address+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	answer, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = answer.Body.Close()
	return answer.StatusCode
}

// never is a daemon that always has something in hand.
func never() bool { return true }

// A build daemon says where it serves in a file of its state directory
// that is its user's alone: its address, on the loopback, the token it
// is asked with, and what it was started as, so that the command that
// finds it knows whether it is the daemon it would start. It is asked
// with the token, and whoever asks without it, or with another, is
// refused and not served. When it ends the file is gone. The control is
// the same call with the token, which is served.
func TestABuildDaemonSaysWhereItServesAndIsAskedWithItsToken(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	info, _ := kept(t, state, answering, never, time.Hour)
	if !strings.HasPrefix(info.Address, "127.0.0.1:") || len(info.Token) < 32 || info.Version != "v1" || info.Settings != "s1" || info.Directory != "/work" || info.PID != os.Getpid() {
		t.Fatalf("it says %+v; want a loopback address, a token, and what it was started as", info)
	}
	said, err := os.Stat(filepath.Join(state, infoFile))
	if err != nil {
		t.Fatal(err)
	}
	if said.Mode().Perm() != 0o600 {
		t.Errorf("what it says is kept %s; want its user's alone", said.Mode().Perm())
	}
	if raw, _ := os.ReadFile(filepath.Join(state, infoFile)); !json.Valid(raw) {
		t.Errorf("what it says is not JSON: %q", raw)
	}
	if status := reached(t, info, info.Token); status != http.StatusOK {
		t.Fatalf("the control: asked with its token it answered %d", status)
	}
	for name, token := range map[string]string{"no token": "", "another token": strings.Repeat("0", len(info.Token)), "its token and more": info.Token + "0"} {
		if status := reached(t, info, token); status != http.StatusUnauthorized {
			t.Errorf("asked with %s it answered %d; want it refused", name, status)
		}
	}
}

// A state directory is one daemon's at a time: a second started over it
// is refused at once, and serves nothing, while the first goes on. The
// control is a daemon over another directory, which starts.
func TestAStateDirectoryIsOneBuildDaemonsAtATime(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	first, _ := kept(t, state, answering, never, time.Hour)
	err := Keep(t.Context(), Life{State: state, Handler: answering, Busy: never, Idle: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "another build daemon") {
		t.Fatalf("a second daemon over the directory: %v; want it refused", err)
	}
	if again, err := Told(state); err != nil || again != first {
		t.Errorf("after the second was refused the directory says %+v (%v); want the first's, %+v", again, err, first)
	}
	if other, _ := kept(t, filepath.Join(t.TempDir(), "other"), answering, never, time.Hour); other.Address == "" || other.Address == first.Address {
		t.Errorf("the control: a daemon over another directory says %+v", other)
	}
}

// A build daemon ends itself once it has had nothing to do for a while:
// no call under way, and no build, run or release in hand. While it has
// either it stays, however long. When it has ended, what it said of
// where it serves is gone. The control is the daemon while busy, which
// is still there after the same while.
func TestABuildDaemonWithNothingToDoEndsItself(t *testing.T) {
	const idle = 200 * time.Millisecond
	var busy atomic.Bool
	busy.Store(true)
	state := filepath.Join(t.TempDir(), "state")
	_, ended := kept(t, state, answering, busy.Load, idle)
	select {
	case err := <-ended:
		t.Fatalf("the control: a daemon with a build in hand ended (%v)", err)
	case <-time.After(4 * idle):
	}
	// A call under way keeps it, with nothing else in hand.
	held, let := make(chan struct{}), make(chan struct{})
	state2 := filepath.Join(t.TempDir(), "state")
	calling, ended2 := kept(t, state2, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(held); <-let }), func() bool { return false }, idle)
	answered := make(chan int, 1)
	go func() { answered <- reached(t, calling, calling.Token) }()
	<-held
	select {
	case err := <-ended2:
		t.Fatalf("a daemon with a call under way ended (%v)", err)
	case <-time.After(4 * idle):
	}
	close(let)
	<-answered

	busy.Store(false)
	for name, each := range map[string]<-chan error{"once its build ended": ended, "once its call ended": ended2} {
		select {
		case err := <-each:
			if err != nil {
				t.Errorf("%s it ended with %v", name, err)
			}
		case <-time.After(50 * idle):
			t.Fatalf("%s the daemon never ended itself", name)
		}
	}
	if _, err := Told(state); err == nil {
		t.Error("an ended daemon still says where it serves")
	}
}

// A directory with no manifest is one repository the build was given, as
// a job's checkout is; one whose manifest whatever fills it wrote is a
// workspace of the repositories the manifest records. Nothing else says
// which: a build daemon is told a directory.
func TestADirectoryWithNoManifestIsACheckoutAndOneWithItAWorkspace(t *testing.T) {
	checkout := t.TempDir()
	if alone, err := Alone(checkout); err != nil || !alone {
		t.Errorf("a directory with no manifest: alone %v, %v; want a checkout", alone, err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".frontseat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".frontseat", "workspace.yaml"), []byte("version: 1\nrepositories: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if alone, err := Alone(workspace); err != nil || alone {
		t.Errorf("the control: a directory with a manifest: alone %v, %v; want a workspace", alone, err)
	}
}

// A daemon asked to stop, with its token, ends itself: the command that
// finds a daemon of another version, other settings or another directory
// asks it so, and starts its own. Asked without the token it goes on.
func TestABuildDaemonAskedToStopWithItsTokenEndsItself(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	info, ended := kept(t, state, answering, never, time.Hour)
	stop := func(token string) int {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, "http://"+info.Address+StopPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		answer, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = answer.Body.Close()
		return answer.StatusCode
	}
	if status := stop("not its token"); status != http.StatusUnauthorized {
		t.Fatalf("asked to stop without its token it answered %d", status)
	}
	if status := reached(t, info, info.Token); status != http.StatusOK {
		t.Fatalf("the control: after that it answered %d; want it still serving", status)
	}
	if status := stop(info.Token); status != http.StatusNoContent {
		t.Fatalf("asked to stop with its token it answered %d", status)
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("it ended with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("asked to stop, the daemon never ended")
	}
}

// What a daemon serves is part of no trace of the command that started
// it: the daemon outlives that command, and every build it made after
// would be one trace with it. The control is the context it was started
// in, which is part of that command's trace.
func TestWhatADaemonServesIsNoPartOfTheTraceItWasStartedIn(t *testing.T) {
	started := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	}))
	if !trace.SpanContextFromContext(started).IsValid() {
		t.Fatal("the control: the context a daemon is started in is part of no trace")
	}
	if within := trace.SpanContextFromContext(apart(started)); within.IsValid() {
		t.Errorf("what the daemon serves is part of the trace %s it was started in", within.TraceID())
	}
}
