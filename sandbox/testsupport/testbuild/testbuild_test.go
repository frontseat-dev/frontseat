package testbuild

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/ports"
)

// token is what the test's build daemon is asked with.
const token = "the-daemons-own"

// boxed is a box that holds no sandbox: its lines are answered from what
// the test says its build daemons keep, and its port leads to a server
// of the test's, which answers as a build daemon does to its token.
type boxed struct {
	daemon *httptest.Server
	// states is what each state directory beneath ports.Builds holds as
	// daemon.json.
	states map[string]string
	// noise is what every line complains of, whatever it prints.
	noise string

	mu        sync.Mutex
	presented []string
	dialled   []int
	// streamed is closed when the first stream is being answered, and
	// ended when its connection has ended.
	streamed, ended chan struct{}
	first           sync.Once
}

// aBox is a box whose one build daemon, of the workspace's directory,
// says where the test's server serves and the token it knows.
func aBox(t *testing.T) *boxed {
	t.Helper()
	b := &boxed{noise: "{\"address\": \"warning: no locale\"}\n", streamed: make(chan struct{}), ended: make(chan struct{})}
	b.daemon = httptest.NewServer(http.HandlerFunc(b.answer))
	t.Cleanup(b.daemon.Close)
	b.states = map[string]string{ports.Builds + "/0a1b": b.says(token, workspace)}
	return b
}

// says is what a build daemon at the test's server says of itself, with
// a token and the directory it builds.
func (b *boxed) says(token, directory string) string {
	return fmt.Sprintf(`{"address": %q, "token": %q, "version": "1", "settings": "", "directory": %q, "environment": "", "pid": 7}`+"\n", b.daemon.Listener.Addr().String(), token, directory)
}

// answer answers as a build daemon: nothing without its token, its
// builds as none, and a stream that lasts until its connection ends.
func (b *boxed) answer(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.presented = append(b.presented, r.Header.Get("Authorization"))
	b.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, "the build daemon is asked with its token", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/frontseat.build.v1alpha1.BuildService/ListBuilds":
		// No build: an empty message, in what it was asked in.
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
	case "/a/stream":
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		b.first.Do(func() {
			close(b.streamed)
			<-r.Context().Done()
			close(b.ended)
		})
	default:
		http.NotFound(w, r)
	}
}

func (b *boxed) box() Box {
	return Box{
		Run: func(_ context.Context, line string) (string, string, error) {
			switch {
			case line == starts:
				return "", b.noise, nil
			case strings.HasPrefix(line, "ls -d "):
				var listed []string
				for state := range b.states {
					listed = append(listed, state+"/")
				}
				if len(listed) == 0 {
					return "", "ls: nothing there\n", errors.New("exit status 2")
				}
				slices.Sort(listed)
				return strings.Join(listed, "\n") + "\n", b.noise, nil
			case strings.HasPrefix(line, "cat "):
				return b.states[path.Dir(strings.TrimPrefix(line, "cat "))], b.noise, nil
			}
			return "", "", fmt.Errorf("the box was not asked to run %q by what reaches its build daemon", line)
		},
		Dial: func(ctx context.Context, port int) (net.Conn, error) {
			b.mu.Lock()
			b.dialled = append(b.dialled, port)
			b.mu.Unlock()
			return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
		},
	}
}

// was is what the daemon was presented, and the ports dialled.
func (b *boxed) was() ([]string, []int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.presented), slices.Clone(b.dialled)
}

// The build daemon of a box is asked once, with the token it says,
// over the port it says it serves on, before a test is given it; and
// what a line of the box complained of, though it reads as what a
// daemon says, is no part of what is read.
func TestTheBuildDaemonOfABoxIsAskedWithItsTokenBeforeATestIsGivenIt(t *testing.T) {
	b := aBox(t)

	daemon, err := b.box().reach(context.Background())

	if err != nil {
		t.Fatalf("the build daemon of the box: %v", err)
	}
	defer daemon.Close()
	_, port, _ := Read(b.says(token, workspace))
	if presented, dialled := b.was(); !slices.Equal(presented, []string{"Bearer " + token}) || !slices.Equal(dialled, []int{port}) {
		t.Errorf("the daemon was presented %q over the ports %v, want its token, once, over %d", presented, dialled, port)
	}
	if daemon.Address != "http://"+b.daemon.Listener.Addr().String() {
		t.Errorf("the daemon is said to serve at %s", daemon.Address)
	}
}

// A box that does not hold the one build daemon of its workspace, which
// answers to its token, is said where the daemon is taken, with what
// was found: the state of two daemons, both named; a daemon that builds
// another directory, named; one that says a token it does not answer
// to; one that does not answer where it says; one that says more than
// one thing; and none. The control is the box of
// TestTheBuildDaemonOfABoxIsAskedWithItsTokenBeforeATestIsGivenIt.
func TestABoxThatDoesNotHoldItsOneBuildDaemonIsSaidWithWhatWasFound(t *testing.T) {
	gone := httptest.NewServer(nil)
	nowhere := gone.Listener.Addr().String()
	gone.Close()
	for what, c := range map[string]struct {
		change func(b *boxed)
		names  []string
	}{
		"the state of two daemons":      {func(b *boxed) { b.states[ports.Builds+"/9f8e"] = b.says(token, workspace) }, []string{ports.Builds + "/0a1b", ports.Builds + "/9f8e", "2"}},
		"a daemon of another directory": {func(b *boxed) { b.states[ports.Builds+"/0a1b"] = b.says(token, "/work/code") }, []string{"/work/code", ports.Builds + "/0a1b"}},
		"a token it does not answer to": {func(b *boxed) { b.states[ports.Builds+"/0a1b"] = b.says("another", workspace) }, []string{"did not answer there to its token"}},
		"a daemon that is gone": {func(b *boxed) {
			b.states[ports.Builds+"/0a1b"] = fmt.Sprintf(`{"address": %q, "token": %q, "directory": %q}`, nowhere, token, workspace)
		}, []string{"did not answer there to its token", nowhere}},
		"two things said": {func(b *boxed) { b.states[ports.Builds+"/0a1b"] += b.says(token, workspace) }, []string{"followed by more"}},
		"no token":        {func(b *boxed) { b.states[ports.Builds+"/0a1b"] = b.says("", workspace) }, []string{"no token"}},
		"no state":        {func(b *boxed) { b.states = nil }, []string{"no build daemon keeps its state", "nothing there"}},
	} {
		b := aBox(t)
		c.change(b)
		daemon, err := b.box().reach(context.Background())
		if err == nil {
			daemon.Close()
			t.Errorf("%s: the daemon was taken", what)
			continue
		}
		for _, name := range c.names {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("%s: %v, want it to name %q", what, err, name)
			}
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("%s: what is said holds the daemon's token: %v", what, err)
		}
	}
}

// Letting go of a daemon ends what carries a test to it: a stream that
// is still being read ends, at the daemon too, and nothing more is
// carried.
func TestLettingGoOfABuildDaemonEndsWhatWasCarriedToIt(t *testing.T) {
	b := aBox(t)
	daemon, err := b.box().reach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := daemon.HTTP.Get(daemon.Address + "/a/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	<-b.streamed

	daemon.Close()

	select {
	case <-b.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection a stream was read on outlived the daemon's letting go")
	}
	if _, err := io.ReadAll(stream.Body); err == nil {
		t.Error("the stream ended as if the daemon had ended it")
	}
	if again, err := daemon.HTTP.Get(daemon.Address + "/a/stream"); err == nil {
		again.Body.Close()
		t.Error("a daemon that was let go of was asked again")
	}
}

// told is a test as the helper is given one, which keeps what it was
// told: what to do when it ends, and why it failed.
type told struct {
	testing.TB
	after  []func()
	failed string
}

func (r *told) Helper()          {}
func (r *told) Cleanup(f func()) { r.after = append(r.after, f) }
func (r *told) Fatal(why ...any) {
	r.failed = fmt.Sprint(why...)
	runtime.Goexit()
}

// taken is what a test was told when it took the daemon of a box, and
// the daemon, where there is one.
func taken(b *boxed) (*told, *Daemon) {
	test := &told{}
	var daemon *Daemon
	done := make(chan struct{})
	go func() {
		defer close(done)
		daemon = b.box().Daemon(context.Background(), test)
	}()
	<-done
	return test, daemon
}

// A test that takes the daemon of a box lets go of it when it ends, and
// fails, itself, where the box has none: nothing panics, so that what
// the other tests clean up is cleaned up. The control of each is the
// other.
func TestATestThatTakesADaemonLetsGoOfItWhenItEndsAndFailsWhereThereIsNone(t *testing.T) {
	test, daemon := taken(aBox(t))
	if test.failed != "" || len(test.after) != 1 || daemon == nil {
		t.Fatalf("a test that took the daemon failed with %q and was told %d things to do when it ends, want none and one", test.failed, len(test.after))
	}
	if asked, err := daemon.HTTP.Get(daemon.Address + "/"); err != nil {
		t.Fatalf("the control: the daemon, before the test ended: %v", err)
	} else {
		asked.Body.Close()
	}
	test.after[0]()
	if asked, err := daemon.HTTP.Get(daemon.Address + "/"); err == nil {
		asked.Body.Close()
		t.Error("the daemon was asked once the test that took it had ended")
	}

	none := aBox(t)
	none.states = nil
	if test, _ := taken(none); !strings.Contains(test.failed, "no build daemon keeps its state") || len(test.after) != 0 {
		t.Errorf("a test that took the daemon of a box with none failed with %q and was told %d things to do when it ends, want it failed and none", test.failed, len(test.after))
	}
}
