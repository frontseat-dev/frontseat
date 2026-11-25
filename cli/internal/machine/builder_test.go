package machine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
)

// started stands in for starting a build daemon: each is a daemon's life
// in this process, over the state directory asked, saying what it was
// started as. It counts them, and they end with the test. Each takes a
// while to end once it serves no more, as a daemon that closes its build
// does, holding its directory until then: one started in its place
// before it has ended is refused the directory, and is counted.
type started struct {
	t  *testing.T
	mu sync.Mutex
	n  int
	// busy is whether the daemons have something in hand.
	busy atomic.Bool
}

// closing is how long a stand-in daemon takes to end.
const closing = 50 * time.Millisecond

func (s *started) start(o life.Options) (*begun, error) {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	over, failed := make(chan struct{}), new(atomic.Pointer[error])
	child := &begun{ended: over, failed: func() bool { return failed.Load() != nil }, printed: func() string {
		if err := failed.Load(); err != nil {
			return (*err).Error()
		}
		return ""
	}}
	read, err := life.Read(o.Settings)
	if err != nil {
		failed.Store(&err)
		close(over)
		return child, nil
	}
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		defer close(over)
		if err := life.Keep(ctx, life.Life{State: o.State, Said: life.Info{Version: o.Version, Settings: read.Made.Settings, Directory: o.Directory, Environment: life.Environment(read.Settings.Given)},
			Handler: http.NotFoundHandler(), Busy: s.busy.Load, Idle: time.Hour, Ended: func() { time.Sleep(closing) }}); err != nil {
			failed.Store(&err)
		}
	}()
	s.t.Cleanup(func() { stop(); <-over })
	return child, nil
}

func (s *started) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// program has every command from now on be of a program, by a name that
// stands in for its content.
func program(name string) {
	identified = func(settings string) (life.Loaded, error) {
		read, err := life.Read(settings)
		read.Made.Program = name
		return read, err
	}
}

// settled lays out a machine's configuration with the settings a build
// daemon is told, and stands in for starting one. It returns what
// counts the daemons started and the settings' file.
func settled(t *testing.T) (*started, string) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	plugins := filepath.Join(config, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins, "go.wasm"), []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(config, "frontseat", "build.yaml")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("version: 1\nplugins: "+plugins+"\ngrid:\n  address: localhost:8980\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &started{t: t}
	before, was := spawn, identified
	spawn = s.start
	program("one")
	t.Cleanup(func() { spawn, identified = before, was })
	return s, file
}

// The command line asks the build daemon of the directory it builds,
// and starts one where there is none: once, however often it asks. The
// next command finds the one that serves, by what it said of itself and
// by asking it with its token. A directory beside it has a daemon of its
// own, and each is said with where what it printed is kept. The control
// is the first ask, which starts one.
func TestTheBuildDaemonOfADirectoryIsStartedOnceAndFoundAgain(t *testing.T) {
	s, _ := settled(t)
	here, beside := t.TempDir(), t.TempDir()
	first, err := BuildDaemon(t.Context(), here, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 || first.Address == "" || first.Token == "" {
		t.Fatalf("the control: the first ask started %d daemons and was told %+v", s.count(), first)
	}
	again, err := BuildDaemon(t.Context(), here, false)
	if err != nil || again != first || s.count() != 1 {
		t.Errorf("asked again: %+v, %v, after %d daemons; want the one that serves, %+v", again, err, s.count(), first)
	}
	other, err := BuildDaemon(t.Context(), beside, false)
	if err != nil || other.Address == first.Address || s.count() != 2 {
		t.Errorf("a directory beside it: %+v, %v, after %d daemons; want a daemon of its own", other, err, s.count())
	}
	// Each says where all its daemon printed is kept: in that daemon's
	// own state directory, for whoever is told it did not answer.
	where, err := keeps(false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Log != filepath.Join(where.of(here), "daemon.log") || again.Log != first.Log || other.Log != filepath.Join(where.of(beside), "daemon.log") {
		t.Errorf("what the daemons printed is said to be in %q, %q and %q; want each in its own state directory, beneath %s", first.Log, again.Log, other.Log, where.state)
	}
}

// A daemon that is not the one the command would start is asked to stop
// and another is started: one of another version of the program, or one
// told other settings.
// The control is TestTheBuildDaemonOfADirectoryIsStartedOnceAndFoundAgain,
// where nothing differs and none is started.
func TestABuildDaemonOfAnotherVersionOrOtherSettingsIsReplaced(t *testing.T) {
	s, file := settled(t)
	here := t.TempDir()
	first, err := BuildDaemon(t.Context(), here, false)
	if err != nil {
		t.Fatal(err)
	}

	program("two")
	second, err := BuildDaemon(t.Context(), here, false)
	if err != nil || second.Address == first.Address || s.count() != 2 {
		t.Fatalf("a command of another version: %+v, %v, after %d daemons; want another daemon", second, err, s.count())
	}

	told, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(told, "  instance: other\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := BuildDaemon(t.Context(), here, false)
	if err != nil || third.Address == second.Address || s.count() != 3 {
		t.Fatalf("other settings: %+v, %v, after %d daemons; want another daemon", third, err, s.count())
	}

}

// stale writes, into the state directory of a directory's daemon, what a
// daemon that died there left: the daemon this command would start, by
// all it says, at an address, with a token of a daemon's form. No daemon
// holds the directory.
func stale(t *testing.T, directory, address string) {
	t.Helper()
	where, err := keeps(false)
	if err != nil {
		t.Fatal(err)
	}
	read, err := identified(where.settings)
	if err != nil {
		t.Fatal(err)
	}
	state := where.of(directory)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	left, err := json.Marshal(life.Info{Address: address, Token: strings.Repeat("ab", 32), Version: read.Made.Program, Settings: read.Made.Settings, Directory: directory, Environment: life.Environment(read.Settings.Given), PID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "daemon.json"), left, 0o600); err != nil {
		t.Fatal(err)
	}
}

// What a daemon that died left names no daemon: its file says where it
// served and with which token, and whatever has that port of the
// loopback since, answering anything, is asked nothing and told no
// token. No daemon holds the directory, so one is started. The control
// is TestTheBuildDaemonOfADirectoryIsStartedOnceAndFoundAgain, where a
// daemon that holds its directory is found again and none is started.
func TestWhatADeadDaemonLeftIsNotTakenForADaemon(t *testing.T) {
	for name, status := range map[string]int{"something that answers 200": http.StatusOK, "something that answers 404": http.StatusNotFound, "something that answers as a daemon does": http.StatusNoContent} {
		t.Run(name, func(t *testing.T) {
			s, _ := settled(t)
			var asked atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked.Add(1)
				w.WriteHeader(status)
			}))
			t.Cleanup(other.Close)
			here := t.TempDir()
			stale(t, here, other.Listener.Addr().String())
			soon, done := context.WithTimeout(t.Context(), 10*time.Second)
			defer done()
			found, err := BuildDaemon(soon, here, false)
			if err != nil {
				t.Fatalf("beside what a dead daemon left: %v, after %d daemons, with what has its port asked %d times; want a daemon started", err, s.count(), asked.Load())
			}
			if s.count() != 1 || found.Address == other.Listener.Addr().String() {
				t.Errorf("beside what a dead daemon left, %d daemons were started and the one found is at %s; want one started, and not whatever is at %s", s.count(), found.Address, other.Listener.Addr())
			}
			if asked.Load() != 0 {
				t.Errorf("what has a dead daemon's port was asked %d times, with its token; want it asked nothing", asked.Load())
			}
		})
	}
	// What is in the file and no daemon writes is as little a daemon:
	// a name, or an address beyond this machine.
	s, _ := settled(t)
	for _, address := range []string{"localhost:80", "192.0.2.1:80", "127.0.0.1:80/x", "user@127.0.0.1:80"} {
		here := t.TempDir()
		stale(t, here, address)
		if found, err := BuildDaemon(t.Context(), here, false); err != nil || found.Address == address {
			t.Errorf("beside a file that says %s: %+v, %v; want a daemon started", address, found, err)
		}
	}
	if s.count() != 4 {
		t.Errorf("%d daemons were started; want one for each directory", s.count())
	}
}

// A daemon is taken for serving by its own answer to its own call, and
// by no other: where the directory is held and what is said there leads
// to something that answers otherwise, as a daemon that is stopping
// does, or anything that is none, the command goes on waiting and takes
// nothing. The control is TestTheBuildDaemonOfADirectoryIsStartedOnceAndFoundAgain,
// whose daemon gives that answer and is taken.
func TestOnlyADaemonsOwnAnswerIsTakenForServing(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusServiceUnavailable} {
		s, _ := settled(t)
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		t.Cleanup(other.Close)
		here := t.TempDir()
		stale(t, here, other.Listener.Addr().String())
		where, err := keeps(false)
		if err != nil {
			t.Fatal(err)
		}
		held, err := life.Take(where.of(here))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(held.Release)
		ctx, done := context.WithTimeout(t.Context(), 300*time.Millisecond)
		got, err := BuildDaemon(ctx, here, false)
		done()
		if err == nil || s.count() != 0 {
			t.Errorf("where what holds the directory answers %d: %+v, %v, after %d daemons; want it not taken, and none started beside it", status, got, err, s.count())
		}
	}
}

// A daemon that has something in hand is not stopped for a command that
// would start another: the command ends at once, says that the daemon
// has a build in hand and in what it differs, never a value, and starts
// nothing, and the daemon goes on serving whoever started it. The
// control is the same daemon with nothing in hand, which is replaced.
func TestABusyBuildDaemonIsNotStoppedForACommandThatWouldStartAnother(t *testing.T) {
	s, _ := settled(t)
	here := t.TempDir()
	first, err := BuildDaemon(t.Context(), here, false)
	if err != nil {
		t.Fatal(err)
	}
	s.busy.Store(true)
	program("two")
	since := time.Now()
	got, err := BuildDaemon(t.Context(), here, false)
	if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "the program") || strings.Contains(err.Error(), "two") || s.count() != 1 {
		t.Fatalf("a command of another program, beside a busy daemon: %+v, %v, after %d daemons; want %v, saying the program differs, and none started", got, err, s.count(), ErrBusy)
	}
	if took := time.Since(since); took > 5*time.Second {
		t.Errorf("it said so after %s; want it said at once", took)
	}
	program("one")
	if still, err := BuildDaemon(t.Context(), here, false); err != nil || still != first || s.count() != 1 {
		t.Errorf("the busy daemon, asked by its own command afterwards: %+v, %v, after %d daemons; want it still serving, %+v", still, err, s.count(), first)
	}
	s.busy.Store(false)
	program("two")
	if replaced, err := BuildDaemon(t.Context(), here, false); err != nil || replaced.Address == first.Address || s.count() != 2 {
		t.Errorf("the control: with nothing in hand: %+v, %v, after %d daemons; want it replaced", replaced, err, s.count())
	}
}

// Two commands that would start different daemons of one directory, at
// once, each end soon: with its daemon, which the other may replace
// after, or told the other's is busy. Neither waits out the time a
// daemon is given to start on a daemon it will never be given.
func TestTwoCommandsThatWouldStartDifferentDaemonsBothEndSoon(t *testing.T) {
	settled(t)
	var commands atomic.Int32
	identified = func(settings string) (life.Loaded, error) {
		read, err := life.Read(settings)
		read.Made.Program = []string{"one", "two"}[commands.Add(1)%2]
		return read, err
	}
	here := t.TempDir()
	type ended struct {
		err  error
		took time.Duration
	}
	each := make(chan ended, 2)
	for range 2 {
		go func() {
			since := time.Now()
			_, err := BuildDaemon(t.Context(), here, false)
			each <- ended{err, time.Since(since)}
		}()
	}
	for range 2 {
		if got := <-each; (got.err != nil && !errors.Is(got.err, ErrBusy)) || got.took > 20*time.Second {
			t.Errorf("a command ended with %v after %s; want its daemon, or %v, soon", got.err, got.took, ErrBusy)
		}
	}
}

// A daemon this command started that lost the directory to the daemon
// another command started is no failure: the one that holds the
// directory is looked at as any is, asked to stop where it is not the
// one wanted, and another is started in its place. The control is
// TestADaemonThatEndsAsItIsStartedIsSaidAtOnceWithWhatItPrinted, where
// none holds the directory and the daemon's end is the command's.
func TestADaemonThatLostTheDirectoryToAnotherIsNoFailure(t *testing.T) {
	s, _ := settled(t)
	real := spawn
	var once atomic.Bool
	lost := make(chan struct{})
	close(lost)
	spawn = func(o life.Options) (*begun, error) {
		if !once.CompareAndSwap(false, true) {
			return real(o)
		}
		// Another command's daemon, of another program, takes the
		// directory first; this command's is refused it, and ends.
		rival := o
		rival.Version = "another"
		if _, err := real(rival); err != nil {
			return nil, err
		}
		for !life.Held(o.State) {
			time.Sleep(time.Millisecond)
		}
		return &begun{ended: lost, failed: func() bool { return true }, printed: func() string { return "another build daemon serves the directory" }}, nil
	}
	found, err := BuildDaemon(t.Context(), t.TempDir(), false)
	if err != nil || found.Address == "" || s.count() != 2 {
		t.Errorf("after its daemon lost the directory to another: %+v, %v, after %d daemons; want that one replaced by its own, and no failure", found, err, s.count())
	}
}

// A daemon that ends as it is started, having served nothing, is said at
// once, with what it printed and nothing a daemon before it did: the
// command does not wait out the time a daemon is given to start. One
// that ended as it was asked to, by another command, is no failure, and
// another is started. The control is every test here whose daemon
// serves.
func TestADaemonThatEndsAsItIsStartedIsSaidAtOnceWithWhatItPrinted(t *testing.T) {
	s, _ := settled(t)
	real := spawn
	dead := make(chan struct{})
	close(dead)
	spawn = func(life.Options) (*begun, error) {
		return &begun{ended: dead, failed: func() bool { return true }, printed: func() string { return "the grid is not there" }}, nil
	}
	here := t.TempDir()
	since := time.Now()
	_, err := BuildDaemon(t.Context(), here, false)
	if err == nil || !strings.Contains(err.Error(), "the grid is not there") || time.Since(since) > time.Second {
		t.Fatalf("a daemon that ended as it was started: %v, after %s; want what it printed, within a second", err, time.Since(since))
	}
	// One that ended with no failure was stopped by another command.
	var once atomic.Bool
	spawn = func(o life.Options) (*begun, error) {
		if once.CompareAndSwap(false, true) {
			return &begun{ended: dead, failed: func() bool { return false }, printed: func() string { return "" }}, nil
		}
		return real(o)
	}
	if _, err := BuildDaemon(t.Context(), here, false); err != nil || s.count() != 1 {
		t.Errorf("after a daemon that ended as it was asked: %v, after %d daemons; want another started", err, s.count())
	}

	// What a daemon printed is what its log holds from where it began.
	log := filepath.Join(t.TempDir(), logFile)
	if err := os.WriteFile(log, []byte("what a daemon before it said\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	said, from, err := appended(log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := said.WriteString("what this one said\n"); err != nil {
		t.Fatal(err)
	}
	_ = said.Close()
	if printed := printedFrom(log, from); printed != "what this one said" {
		t.Errorf("it printed %q; want what it said, and nothing a daemon before it did", printed)
	}
	// A log over its limit is begun anew when a daemon is started.
	if err := os.Truncate(log, logLimit+1); err != nil {
		t.Fatal(err)
	}
	said, from, err = appended(log)
	if err != nil {
		t.Fatal(err)
	}
	_ = said.Close()
	if kept, _ := os.Stat(log); from != 0 || kept.Size() != 0 {
		t.Errorf("a log over its limit is %d bytes when a daemon is started, read from %d; want it begun anew", kept.Size(), from)
	}
	if err := os.Truncate(log, logLimit); err != nil {
		t.Fatal(err)
	}
	if said, from, err = appended(log); err != nil || from != logLimit {
		t.Errorf("the control: a log at its limit is read from %d (%v); want it kept", from, err)
	}
	_ = said.Close()
}

// A command that is interrupted while it waits for its daemon ends at
// once: every wait is the context's. The control is the same wait with
// no interruption, which goes on.
func TestWaitingForABuildDaemonEndsWhenTheCommandIsInterrupted(t *testing.T) {
	settled(t)
	spawn = func(life.Options) (*begun, error) {
		return &begun{ended: make(chan struct{}), failed: func() bool { return false }, printed: func() string { return "" }}, nil
	}
	ctx, interrupt := context.WithCancel(t.Context())
	over := make(chan error, 1)
	here := t.TempDir()
	go func() {
		_, err := BuildDaemon(ctx, here, false)
		over <- err
	}()
	select {
	case err := <-over:
		t.Fatalf("the control: with no interruption it ended, with %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	interrupt()
	select {
	case err := <-over:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("interrupted, it ended with %v; want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted, it was still waiting five seconds on")
	}
}

// A daemon is what it loaded, by content: plugins written again in
// place, at the same size and with the time they had, make another
// daemon, and the one that loaded the ones before is replaced. The
// control is a command with nothing changed, which finds it again.
func TestABuildDaemonWhosePluginsWereWrittenAgainIsReplaced(t *testing.T) {
	s, file := settled(t)
	plugin := filepath.Join(filepath.Dir(filepath.Dir(file)), "plugins", "go.wasm")
	here := t.TempDir()
	first, err := BuildDaemon(t.Context(), here, false)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := BuildDaemon(t.Context(), here, false); err != nil || again != first || s.count() != 1 {
		t.Fatalf("the control: with nothing changed: %+v, %v, after %d daemons", again, err, s.count())
	}
	was, err := os.Stat(plugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte("og"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(plugin, was.ModTime(), was.ModTime()); err != nil {
		t.Fatal(err)
	}
	if now, err := os.Stat(plugin); err != nil || now.Size() != was.Size() || !now.ModTime().Equal(was.ModTime()) {
		t.Fatalf("the plugin written again is %v (%v); want the size and the time it had", now, err)
	}
	second, err := BuildDaemon(t.Context(), here, false)
	if err != nil || second.Address == first.Address || s.count() != 2 {
		t.Errorf("with a plugin written again in place: %+v, %v, after %d daemons; want another daemon", second, err, s.count())
	}
}

// The settings say which grid a token is sent to and which plugins run,
// so the file is its person's alone to write: one its group or others
// may write, one that is a link, and one in a directory they may write,
// is refused, and no daemon is started. The control is the same file
// as its person's alone.
func TestSettingsOthersCouldWriteStartNoBuildDaemon(t *testing.T) {
	s, file := settled(t)
	here := t.TempDir()
	told, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	refused := func(what string) {
		t.Helper()
		if _, err := BuildDaemon(t.Context(), here, false); err == nil || !strings.Contains(err.Error(), "written by others") || s.count() != 0 {
			t.Errorf("%s: %v, after %d daemons; want it refused and none started", what, err, s.count())
		}
	}
	for _, mode := range []os.FileMode{0o620, 0o602} {
		if err := os.Chmod(file, mode); err != nil {
			t.Fatal(err)
		}
		refused("settings kept " + mode.String())
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(file), 0o777); err != nil {
		t.Fatal(err)
	}
	refused("settings in a directory others write")
	if err := os.Chmod(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "build.yaml")
	if err := os.WriteFile(elsewhere, told, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, file); err != nil {
		t.Fatal(err)
	}
	refused("settings that are a link")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	startsWith(t, s, file, told, here)
}

// startsWith is the control of a machine whose settings were refused or
// not there: given its settings, its person's alone, a daemon is
// started, the first.
func startsWith(t *testing.T, s *started, file string, told []byte, here string) {
	t.Helper()
	if err := os.WriteFile(file, told, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDaemon(t.Context(), here, false); err != nil || s.count() != 1 {
		t.Errorf("the control: with its settings, its person's alone: %v, after %d daemons", err, s.count())
	}
}

// A daemon is asked with its token by a client that follows no redirect:
// an answer that names another address is the answer, and the token goes
// nowhere it names. The control is the first address, which is asked.
func TestWhatADaemonIsAskedFollowsNoRedirect(t *testing.T) {
	var followed atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	t.Cleanup(elsewhere.Close)
	var first atomic.Int32
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first.Add(1)
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)
	status := ask(t.Context(), http.MethodGet, life.Info{Address: redirecting.Listener.Addr().String(), Token: "token"}, life.HelloPath)
	if first.Load() != 1 {
		t.Fatalf("the control: the address asked was asked %d times", first.Load())
	}
	if status != http.StatusTemporaryRedirect || followed.Load() != 0 {
		t.Errorf("asked, it answered %d and the address it named was asked %d times; want the redirect itself, followed nowhere", status, followed.Load())
	}
}

// A machine that was told nothing of its grid has no build daemon: the
// command line says the settings' file is not there, and starts nothing.
// The control is the same machine with the file, which starts one.
func TestWithNoSettingsNoBuildDaemonIsStarted(t *testing.T) {
	s, file := settled(t)
	told, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	here := t.TempDir()
	if _, err := BuildDaemon(t.Context(), here, false); !errors.Is(err, ErrNoSettings) || s.count() != 0 {
		t.Fatalf("with no settings: %v, after %d daemons; want %v and none started", err, s.count(), ErrNoSettings)
	}
	startsWith(t, s, file, told, here)
}

// A daemon has the variables a publish may be given as they were where
// it was started, and a command where one of them is otherwise, another
// token for its forge or none, is not served by it: it is replaced, and
// the next command there finds the new one. A variable the settings do
// not name changes nothing. The control is the command run again with
// nothing changed, which starts none.
func TestABuildDaemonStartedWithOtherPublishVariablesIsReplaced(t *testing.T) {
	s, file := settled(t)
	told, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(told, "publish:\n  environment: [FORGE_TOKEN_OF_THE_TEST]\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	here := t.TempDir()
	first, err := BuildDaemon(t.Context(), here, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANOTHER_VARIABLE_OF_THE_TEST", "said")
	if again, err := BuildDaemon(t.Context(), here, false); err != nil || again != first || s.count() != 1 {
		t.Fatalf("the control: with nothing a publish is given changed: %+v, %v, after %d daemons", again, err, s.count())
	}
	t.Setenv("FORGE_TOKEN_OF_THE_TEST", "one")
	second, err := BuildDaemon(t.Context(), here, false)
	if err != nil || second.Address == first.Address || s.count() != 2 {
		t.Fatalf("with the variable set: %+v, %v, after %d daemons; want another daemon", second, err, s.count())
	}
	t.Setenv("FORGE_TOKEN_OF_THE_TEST", "two")
	third, err := BuildDaemon(t.Context(), here, false)
	if err != nil || third.Address == second.Address || s.count() != 3 {
		t.Fatalf("with another value: %+v, %v, after %d daemons; want another daemon", third, err, s.count())
	}
	if again, err := BuildDaemon(t.Context(), here, false); err != nil || again != third || s.count() != 3 {
		t.Errorf("asked again there: %+v, %v, after %d daemons; want the one just started", again, err, s.count())
	}
}

// inASandbox has the test's command line be inside a workspace's
// sandbox of its own, whose supervisor wrote no settings yet, until the
// test ends.
func inASandbox(t *testing.T) {
	t.Helper()
	sandbox := t.TempDir()
	before := boxed
	boxed = kept{settings: filepath.Join(sandbox, "build.yaml"), state: filepath.Join(sandbox, "build")}
	t.Cleanup(func() { boxed = before })
}

// Inside a workspace's sandbox the build daemon of a directory is told
// the settings the sandbox's supervisor wrote, and keeps its state
// beneath the sandbox's own directory, named for the directory it
// builds: nothing of it is read from, or kept beneath, the command
// line's configuration, which a sandbox has none of. A sandbox whose
// supervisor wrote no settings has no build daemon. The control is the
// same directory asked for outside, on a machine told its grid, whose
// daemon is another, kept beneath the command line's own root.
func TestInsideASandboxABuildDaemonIsToldAndKeptWhereTheSandboxSays(t *testing.T) {
	s, file := settled(t)
	inASandbox(t)
	here := t.TempDir()

	if _, err := BuildDaemon(t.Context(), here, true); !errors.Is(err, ErrNoSettings) || s.count() != 0 {
		t.Fatalf("inside a sandbox told no grid, beside a configuration that is: %v, after %d daemons; want %v and none started", err, s.count(), ErrNoSettings)
	}
	told, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boxed.settings, append(told, "  instance: of-the-sandbox\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	within, err := BuildDaemon(t.Context(), here, true)
	if err != nil || s.count() != 1 {
		t.Fatalf("inside: %v, after %d daemons; want one started", err, s.count())
	}
	said, err := life.Told(boxed.of(here))
	if wanted, _ := life.Read(boxed.settings); err != nil || said.Address != within.Address || said.Settings != wanted.Made.Settings || filepath.Dir(boxed.of(here)) != boxed.state {
		t.Errorf("the daemon inside said %+v (%v) in %s; want it there, at %s, told the sandbox's settings", said, err, boxed.of(here), within.Address)
	}
	if again, err := BuildDaemon(t.Context(), here, true); err != nil || again != within || s.count() != 1 {
		t.Errorf("asked again inside: %+v, %v, after %d daemons; want the one that serves", again, err, s.count())
	}
	// The control: outside, the same directory has a daemon of its own.
	outside, err := BuildDaemon(t.Context(), here, false)
	if err != nil || outside.Address == within.Address || s.count() != 2 {
		t.Errorf("the control: outside, %+v, %v, after %d daemons; want another, of the machine's own settings", outside, err, s.count())
	}
}

// Inside a workspace's sandbox the directory a command builds is the
// workspace's, the nearest above that holds its manifest, and nothing
// else: a repository there is part of the workspace and is not built
// alone, as a checkout is, and before the workspace was told what to
// hold there is nothing to build, which is said of the workspace. The
// control is the same directory outside a sandbox, where a repository
// is built as it stands, being the nearest above.
func TestInsideASandboxOnlyTheWorkspacesDirectoryIsBuilt(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(workspace, "payments")
	if err := os.MkdirAll(filepath.Join(repository, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, repositoryFile), []byte("plugins: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(repository, "deep"))

	if got, err := Directory(false); err != nil || got != repository {
		t.Fatalf("the control: outside, the directory built is %q (%v); want the repository, %s", got, err, repository)
	}
	if got, err := Directory(true); err == nil || !strings.Contains(err.Error(), "this workspace's") || strings.Contains(err.Error(), repositoryFile) {
		t.Errorf("inside a workspace told nothing to hold yet: %q, %v; want nothing to build, said of the workspace", got, err)
	}
	marks(t, workspace, manifest.File)
	if got, err := Directory(true); err != nil || got != workspace {
		t.Errorf("with the workspace's manifest, inside, the directory built is %q (%v); want the workspace's, %s", got, err, workspace)
	}
	// Outside, the nearest is built: the repository, which is nearer
	// than the workspace above it, and the workspace from beside it.
	if got, err := Directory(false); err != nil || got != repository {
		t.Errorf("with the workspace's manifest, outside, the directory built is %q (%v); want the nearest, the repository %s", got, err, repository)
	}
	t.Chdir(workspace)
	if got, err := Directory(false); err != nil || got != workspace {
		t.Errorf("outside, in the workspace's directory, the directory built is %q (%v); want the workspace's, %s", got, err, workspace)
	}
}
