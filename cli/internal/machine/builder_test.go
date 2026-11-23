package machine

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
)

// started stands in for starting a build daemon: each is a daemon's life
// in this process, over the state directory asked, saying what it was
// started as. It counts them, and they end with the test.
type started struct {
	t  *testing.T
	mu sync.Mutex
	n  int
}

func (s *started) start(o life.Options) error {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	read, digest, err := life.Read(o.Settings)
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(context.Background())
	over := make(chan struct{})
	go func() {
		defer close(over)
		_ = life.Keep(ctx, life.Life{State: o.State, Said: life.Info{Version: o.Version, Settings: digest, Directory: o.Directory, Environment: life.Environment(read.Given)},
			Handler: http.NotFoundHandler(), Busy: func() bool { return false }, Idle: time.Hour})
	}()
	s.t.Cleanup(func() { stop(); <-over })
	return nil
}

func (s *started) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
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
	before, was := spawn, version
	spawn, version = s.start, func() (string, error) { return "one", nil }
	t.Cleanup(func() { spawn, version = before, was })
	return s, file
}

// The command line asks the build daemon of the directory it builds,
// and starts one where there is none: once, however often it asks. The
// next command finds the one that serves, by what it said of itself and
// by asking it with its token. A directory beside it has a daemon of its
// own. The control is the first ask, which starts one.
func TestTheBuildDaemonOfADirectoryIsStartedOnceAndFoundAgain(t *testing.T) {
	s, _ := settled(t)
	here, beside := t.TempDir(), t.TempDir()
	first, err := BuildDaemon(t.Context(), here)
	if err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 || first.Address == "" || first.Token == "" {
		t.Fatalf("the control: the first ask started %d daemons and was told %+v", s.count(), first)
	}
	again, err := BuildDaemon(t.Context(), here)
	if err != nil || again != first || s.count() != 1 {
		t.Errorf("asked again: %+v, %v, after %d daemons; want the one that serves, %+v", again, err, s.count(), first)
	}
	other, err := BuildDaemon(t.Context(), beside)
	if err != nil || other.Address == first.Address || s.count() != 2 {
		t.Errorf("a directory beside it: %+v, %v, after %d daemons; want a daemon of its own", other, err, s.count())
	}
}

// A daemon that is not the one the command would start is asked to stop
// and another is started: one of another version of the program, or one
// told other settings. So is one that said where it serves and is gone.
// The control is TestTheBuildDaemonOfADirectoryIsStartedOnceAndFoundAgain,
// where nothing differs and none is started.
func TestABuildDaemonOfAnotherVersionOrOtherSettingsIsReplaced(t *testing.T) {
	s, file := settled(t)
	here := t.TempDir()
	first, err := BuildDaemon(t.Context(), here)
	if err != nil {
		t.Fatal(err)
	}

	version = func() (string, error) { return "two", nil }
	second, err := BuildDaemon(t.Context(), here)
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
	third, err := BuildDaemon(t.Context(), here)
	if err != nil || third.Address == second.Address || s.count() != 3 {
		t.Fatalf("other settings: %+v, %v, after %d daemons; want another daemon", third, err, s.count())
	}

	// One that said where it serves and is gone: asked, nothing answers.
	gone := t.TempDir()
	state, err := stateOf(gone)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "daemon.json"), []byte(`{"address":"127.0.0.1:1","token":"0000","version":"two","directory":"`+gone+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := BuildDaemon(t.Context(), gone)
	if err != nil || fresh.Address == "127.0.0.1:1" || s.count() != 4 {
		t.Errorf("beside what a dead daemon said: %+v, %v, after %d daemons; want one started", fresh, err, s.count())
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
	if _, err := BuildDaemon(t.Context(), here); !errors.Is(err, ErrNoSettings) || s.count() != 0 {
		t.Fatalf("with no settings: %v, after %d daemons; want %v and none started", err, s.count(), ErrNoSettings)
	}
	if err := os.WriteFile(file, told, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDaemon(t.Context(), here); err != nil || s.count() != 1 {
		t.Errorf("the control: with its settings: %v, after %d daemons", err, s.count())
	}
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
	first, err := BuildDaemon(t.Context(), here)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANOTHER_VARIABLE_OF_THE_TEST", "said")
	if again, err := BuildDaemon(t.Context(), here); err != nil || again != first || s.count() != 1 {
		t.Fatalf("the control: with nothing a publish is given changed: %+v, %v, after %d daemons", again, err, s.count())
	}
	t.Setenv("FORGE_TOKEN_OF_THE_TEST", "one")
	second, err := BuildDaemon(t.Context(), here)
	if err != nil || second.Address == first.Address || s.count() != 2 {
		t.Fatalf("with the variable set: %+v, %v, after %d daemons; want another daemon", second, err, s.count())
	}
	t.Setenv("FORGE_TOKEN_OF_THE_TEST", "two")
	third, err := BuildDaemon(t.Context(), here)
	if err != nil || third.Address == second.Address || s.count() != 3 {
		t.Fatalf("with another value: %+v, %v, after %d daemons; want another daemon", third, err, s.count())
	}
	if again, err := BuildDaemon(t.Context(), here); err != nil || again != third || s.count() != 3 {
		t.Errorf("asked again there: %+v, %v, after %d daemons; want the one just started", again, err, s.count())
	}
}
