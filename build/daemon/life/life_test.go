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

	"github.com/frontseat-dev/frontseat/build/daemon/life/lifetest"
)

// kept starts a daemon's life over a state directory of the test's,
// serving a handler, and returns its info once it serves, and what it
// ended with.
func kept(t *testing.T, state string, handler http.Handler, busy func() bool, idle time.Duration) (Info, <-chan error) {
	t.Helper()
	return keptAs(t, state, Life{Handler: handler, Busy: busy, Idle: idle})
}

// keptAs is kept, of a life as the test says it.
func keptAs(t *testing.T, state string, l Life) (Info, <-chan error) {
	t.Helper()
	l.State, l.Said = state, Info{Version: "v1", Settings: "s1", Directory: "/work"}
	ctx, stop := context.WithCancel(context.Background())
	// ended is given what the daemon ended with, and over closed then,
	// so that the test and its cleanup may each wait.
	ended, over := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(over)
		ended <- Keep(ctx, l)
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

// reached asks a daemon at its address with a token, or with none
// where it is empty, and returns the status it answered with.
func reached(t *testing.T, info Info, token string) int {
	t.Helper()
	return lifetest.Asked(t, info.Address, http.MethodGet, "/", token)
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

// A daemon asked to stop, with its token, ends itself where it has
// nothing in hand: the command that finds a daemon of another version,
// other settings or another directory asks it so, and starts its own.
// One that has a build in hand, or a call under way, refuses and stays:
// what it has in hand is someone's. Asked without the token it goes on.
// The control is the same daemon with nothing in hand, which ends.
func TestABuildDaemonAskedToStopEndsItselfUnlessItHasSomethingInHand(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	var busy atomic.Bool
	held, let := make(chan struct{}, 1), make(chan struct{})
	info, ended := kept(t, state, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/held" {
			held <- struct{}{}
			<-let
		}
	}), busy.Load, time.Hour)
	if status := lifetest.Asked(t, info.Address, http.MethodPost, StopPath, "not its token"); status != http.StatusUnauthorized {
		t.Fatalf("asked to stop without its token it answered %d", status)
	}
	busy.Store(true)
	if status := lifetest.Asked(t, info.Address, http.MethodPost, StopPath, info.Token); status != http.StatusConflict {
		t.Fatalf("asked to stop with a build in hand it answered %d; want %d", status, http.StatusConflict)
	}
	busy.Store(false)
	answered := make(chan int, 1)
	go func() { answered <- lifetest.Asked(t, info.Address, http.MethodGet, "/held", info.Token) }()
	<-held
	if status := lifetest.Asked(t, info.Address, http.MethodPost, StopPath, info.Token); status != http.StatusConflict {
		t.Fatalf("asked to stop with a call under way it answered %d; want %d", status, http.StatusConflict)
	}
	close(let)
	<-answered
	if status := reached(t, info, info.Token); status != http.StatusOK {
		t.Fatalf("after it refused to stop it answered %d; want it still serving", status)
	}
	if status := lifetest.Asked(t, info.Address, http.MethodPost, StopPath, info.Token); status != http.StatusNoContent {
		t.Fatalf("the control: asked to stop with nothing in hand it answered %d", status)
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("it ended with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("asked to stop, the daemon never ended")
	}
	if Held(state) {
		t.Error("a daemon that has ended still holds its state directory")
	}
}

// A daemon gives its own answer to its own call, to its token: 204 and
// nothing, which is how the command that finds it knows it for a build
// daemon, and not for whatever answers on a port. It is no call of what
// the daemon serves, and without the token it is refused as any is. The
// control is a call of what it serves, which is answered otherwise.
func TestABuildDaemonGivesItsOwnAnswerToItsOwnCall(t *testing.T) {
	info, _ := kept(t, filepath.Join(t.TempDir(), "state"), answering, never, time.Hour)
	if status := lifetest.Asked(t, info.Address, http.MethodGet, HelloPath, info.Token); status != http.StatusNoContent {
		t.Errorf("asked its own call with its token it answered %d; want %d", status, http.StatusNoContent)
	}
	if status := lifetest.Asked(t, info.Address, http.MethodGet, HelloPath, "not its token"); status != http.StatusUnauthorized {
		t.Errorf("asked its own call without its token it answered %d", status)
	}
	if status := reached(t, info, info.Token); status != http.StatusOK {
		t.Errorf("the control: a call of what it serves answered %d", status)
	}
}

// What a daemon says of itself is believed only as a daemon writes it:
// an address of the loopback, written as a number, and a port, and a
// token a daemon mints. A name, an address beyond this machine, a path,
// a user or another token is refused, and the refusal repeats nothing of
// the file, which may hold a token. The control is what a daemon wrote.
func TestWhatADaemonSaysOfItselfIsHeldToWhatADaemonWrites(t *testing.T) {
	state := t.TempDir()
	token := strings.Repeat("0a", 32)
	write := func(address, token string) {
		t.Helper()
		raw, err := json.Marshal(Info{Address: address, Token: token, Version: "secret-version"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, infoFile), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"127.0.0.1:7600", "[::1]:7600"} {
		write(address, token)
		if said, err := Told(state); err != nil || said.Address != address || said.Token != token {
			t.Fatalf("the control: what a daemon wrote at %s is read as %+v, %v", address, said, err)
		}
	}
	for name, said := range map[string][2]string{
		"a name":                     {"localhost:7600", token},
		"an address of another host": {"192.0.2.7:7600", token},
		"every address":              {"0.0.0.0:7600", token},
		"a path":                     {"127.0.0.1:7600/secret-path", token},
		"a user":                     {"secret-user@127.0.0.1:7600", token},
		"no port":                    {"127.0.0.1", token},
		"the port that is none":      {"127.0.0.1:0", token},
		"a port written otherwise":   {"127.0.0.1:07600", token},
		"a short token":              {"127.0.0.1:7600", "secret-token"},
		"a token and a line":         {"127.0.0.1:7600", token + "\r\nsecret-header: 1"},
		"no token":                   {"127.0.0.1:7600", ""},
	} {
		write(said[0], said[1])
		got, err := Told(state)
		if err == nil {
			t.Errorf("%s was read: %+v", name, got)
		} else if strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the refusal repeats what the file holds: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(state, infoFile), []byte(`{"address": secret-word`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Told(state); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("what is no JSON: %v; want it refused, repeating nothing", err)
	}
}

// What a file in a state directory says is believed only while a daemon
// holds the directory: held by none, the file is what a daemon that died
// left, it names no daemon, and it is removed. The control is a daemon
// that lives there, whose file is read and stays.
func TestWhatADaemonLeftIsBelievedOnlyWhileTheDirectoryIsHeld(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if said, held, err := Living(state); held || err != nil || said != (Info{}) {
		t.Fatalf("a directory that is not there: %+v, held %t, %v", said, held, err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	left, err := json.Marshal(Info{Address: "127.0.0.1:7600", Token: strings.Repeat("0a", 32), PID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, infoFile), left, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Told(state); err != nil {
		t.Fatalf("what a dead daemon left is not what a daemon writes: %v", err)
	}
	if said, held, err := Living(state); held || err != nil || said != (Info{}) {
		t.Errorf("beside what a dead daemon left: %+v, held %t, %v; want no daemon", said, held, err)
	}
	if _, err := os.Stat(filepath.Join(state, infoFile)); err == nil {
		t.Error("what a dead daemon left is still there")
	}
	info, _ := kept(t, state, answering, never, time.Hour)
	if said, held, err := Living(state); !held || err != nil || said != info {
		t.Errorf("the control: a directory a daemon lives in: %+v, held %t, %v; want %+v", said, held, err, info)
	}
	if _, err := os.Stat(filepath.Join(state, infoFile)); err != nil {
		t.Errorf("the control: what a living daemon says was removed: %v", err)
	}
}

// A daemon lets go of its state directory last: after it has taken back
// what it said, and after the rest of it has ended, its build closed and
// what that keeps in the directory released, however long that takes. A
// daemon started where the lock is free finds nothing of the one before.
// The control is the directory once that has ended, which none holds.
func TestADaemonHoldsItsDirectoryUntilEverythingOfItHasEnded(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	closing, closed := make(chan struct{}), make(chan struct{})
	ctx, stop := context.WithCancel(t.Context())
	ended := make(chan error, 1)
	go func() {
		ended <- Keep(ctx, Life{State: state, Handler: answering, Busy: never, Idle: time.Hour, Ended: func() {
			close(closing)
			<-closed
		}})
	}()
	stop()
	select {
	case <-closing:
	case <-time.After(30 * time.Second):
		t.Fatal("a daemon that was stopped never ended the rest of itself")
	}
	if _, err := Told(state); err == nil {
		t.Error("a daemon that is ending the rest of itself still says where it serves")
	}
	for range 20 {
		if !Held(state) {
			t.Fatal("a daemon let go of its directory while the rest of it was still ending")
		}
		time.Sleep(time.Millisecond)
	}
	if _, held, _ := Living(state); !held {
		t.Error("a daemon that is still ending is held by none, asked as the command line asks")
	}
	select {
	case err := <-ended:
		t.Fatalf("the daemon's life returned (%v) while the rest of it was still ending", err)
	default:
	}
	close(closed)
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
	if Held(state) {
		t.Error("the control: a directory whose daemon has ended altogether is still held")
	}
	// One refused the directory ends the rest of itself too.
	first, _ := kept(t, state, answering, never, time.Hour)
	var ran atomic.Bool
	if err := Keep(t.Context(), Life{State: state, Handler: answering, Busy: never, Idle: time.Hour, Ended: func() { ran.Store(true) }}); err == nil || !ran.Load() {
		t.Errorf("a daemon refused the directory of %s: %v, the rest of it ended: %t; want it refused, and ended", first.Address, err, ran.Load())
	}
}

// A state directory is held for as long as a daemon lives in it, and by
// none before one started or after it ended: what would start another
// there asks, since a daemon stops answering before it has let go. The
// control is the directory before any daemon.
func TestAStateDirectoryIsHeldWhileADaemonLivesInIt(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if Held(state) {
		t.Fatal("the control: a directory no daemon was started in is held")
	}
	ctx, stop := context.WithCancel(t.Context())
	ended := make(chan error, 1)
	go func() {
		ended <- Keep(ctx, Life{State: state, Handler: answering, Busy: never, Idle: time.Hour})
	}()
	for begun := time.Now(); ; time.Sleep(5 * time.Millisecond) {
		if _, err := Told(state); err == nil {
			break
		}
		if time.Since(begun) > 30*time.Second {
			t.Fatal("the daemon never said where it serves")
		}
	}
	if !Held(state) {
		t.Error("a directory a daemon serves is held by none")
	}
	stop()
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
	if Held(state) {
		t.Error("a directory whose daemon ended is still held")
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

// A call that yields, a wait for what a build read to change, does not
// keep a daemon from being stopped by the command that would start
// another: asked to stop while it is under way, the daemon answers 204,
// the call is ended with the daemon, and the daemon ends. It keeps the
// daemon from ending itself for having nothing to do all the same. The
// control is TestABuildDaemonAskedToStopEndsItselfUnlessItHasSomethingInHand:
// a call that does not yield, under way, keeps the daemon.
func TestACallThatYieldsGivesWayToAStop(t *testing.T) {
	const idle = 100 * time.Millisecond
	waiting, waited := make(chan struct{}, 1), make(chan error, 1)
	info, ended := keptAs(t, filepath.Join(t.TempDir(), "state"), Life{
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			waiting <- struct{}{}
			<-r.Context().Done()
			waited <- r.Context().Err()
		}),
		Busy:   func() bool { return false },
		Yields: func(r *http.Request) bool { return r.URL.Path == "/waiting" },
		Idle:   idle,
	})
	go func() {
		request, err := http.NewRequest(http.MethodGet, "http://"+info.Address+"/waiting", nil)
		if err != nil {
			return
		}
		request.Header.Set("Authorization", "Bearer "+info.Token)
		if answer, err := http.DefaultClient.Do(request); err == nil {
			_ = answer.Body.Close()
		}
	}()
	<-waiting
	select {
	case err := <-ended:
		t.Fatalf("a daemon with a call that yields under way ended itself (%v)", err)
	case <-time.After(5 * idle):
	}
	if status := lifetest.Asked(t, info.Address, http.MethodPost, StopPath, info.Token); status != http.StatusNoContent {
		t.Fatalf("asked to stop with a call that yields under way it answered %d; want %d", status, http.StatusNoContent)
	}
	select {
	case err := <-waited:
		if err == nil {
			t.Error("the call that yielded was not ended with the daemon")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the call that yielded went on after the daemon was stopped")
	}
	select {
	case <-ended:
	case <-time.After(30 * time.Second):
		t.Fatal("asked to stop, the daemon never ended")
	}
}
