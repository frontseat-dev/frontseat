package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
)

// Builder is the build daemon of a directory, as the command line asks
// it: where it serves, on the loopback, the token it is asked with, and
// the file that holds all it printed, for whoever is told it did not
// answer.
type Builder struct {
	Address, Token string
	Log            string
}

// ErrNoSettings is a machine, or a workspace's sandbox, that was told
// nothing of how it builds: there is no file of settings, and so no
// build daemon. Whoever asked says which it is, and how it is told.
var ErrNoSettings = errors.New("nothing was told of the grid to build on: there is no file of build settings")

// startsWithin is how long a daemon is given to serve once it is
// started: it loads its plugins and reaches its grid first.
const startsWithin = 2 * time.Minute

// spawn starts a build daemon, which goes on after the command that
// started it has ended; version is what this program is. A test stands
// in for both.
var (
	spawn   = detached
	version = executable
)

// BuildDaemon returns the build daemon of a directory, starting one
// where none serves: each directory has its own, in a state directory
// that is that directory's alone, beneath the command line's outside a
// sandbox and beneath the sandbox's inside one, where its settings are
// those the sandbox's supervisor wrote (keeps). One that
// serves is taken where it is the daemon this command would start, of
// this program, told the settings as they are now, for this directory,
// with what a publish may be given as it is here;
// any other is asked to stop, and another is started. It is known to
// serve by answering to its token, never by what it left behind.
func BuildDaemon(ctx context.Context, directory string, inside bool) (Builder, error) {
	where, err := keeps(inside)
	if err != nil {
		return Builder{}, err
	}
	settings := where.settings
	if _, err := os.Stat(settings); errors.Is(err, fs.ErrNotExist) {
		return Builder{}, ErrNoSettings
	}
	read, told, err := life.Read(settings)
	if err != nil {
		return Builder{}, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return Builder{}, err
	}
	state := where.of(directory)
	is, err := version()
	if err != nil {
		return Builder{}, err
	}
	found := func(said life.Info) Builder {
		return Builder{Address: said.Address, Token: said.Token, Log: filepath.Join(state, logFile)}
	}
	want := life.Info{Version: is, Settings: told, Directory: directory, Environment: life.Environment(read.Given)}
	wanted := func(said life.Info) bool {
		return said.Version == want.Version && said.Settings == want.Settings && said.Directory == want.Directory && said.Environment == want.Environment
	}
	if said, err := life.Told(state); err == nil && serves(ctx, said) {
		if wanted(said) {
			return found(said), nil
		}
		stopped(ctx, state, said)
	}
	if err := spawn(life.Options{Directory: directory, State: state, Settings: settings, Version: is}); err != nil {
		return Builder{}, fmt.Errorf("starting the build daemon: %w", err)
	}
	for begun := time.Now(); time.Since(begun) < startsWithin; {
		if said, err := life.Told(state); err == nil && wanted(said) && serves(ctx, said) {
			return found(said), nil
		}
		select {
		case <-ctx.Done():
			return Builder{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return Builder{}, fmt.Errorf("the build daemon did not serve within %s: %s (all it said is in %s)", startsWithin, lastSaid(state), filepath.Join(state, logFile))
}

// logFile is, in a daemon's state directory, what it printed.
const logFile = "daemon.log"

// of is the state directory of a directory's build daemon: beneath the
// one place they are kept, named by the directory.
func (k kept) of(directory string) string {
	named := sha256.Sum256([]byte(directory))
	return filepath.Join(k.state, hex.EncodeToString(named[:8]))
}

// serves says whether the daemon that said where it serves answers
// there to its token: one that is gone does not answer, and whatever
// else has its port since does not know the token.
func serves(ctx context.Context, said life.Info) bool {
	answer, err := ask(ctx, http.MethodGet, said, "/")
	return err == nil && answer != http.StatusUnauthorized
}

// stopped asks a daemon to stop, and waits for it to have: until it
// holds its state directory no longer.
func stopped(ctx context.Context, state string, said life.Info) {
	// However it answered, or did not: a daemon that is ending may close
	// the connection it was asked on.
	_, _ = ask(ctx, http.MethodPost, said, life.StopPath)
	// It has stopped once it has let go of its directory, which is after
	// it stopped answering: a daemon started before then would be
	// refused the directory, and none would serve.
	for begun := time.Now(); life.Held(state) && time.Since(begun) < 30*time.Second; {
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// ask asks a daemon one thing with its token, and returns the status it
// answered with.
func ask(ctx context.Context, method string, said life.Info, path string) (int, error) {
	ctx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	request, err := http.NewRequestWithContext(ctx, method, "http://"+said.Address+path, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+said.Token)
	answer, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	_ = answer.Body.Close()
	return answer.StatusCode, nil
}

// executable is what this program is: the digest of its own file, so
// that a program built again is another, whatever version it says.
func executable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// detached starts this program as a build daemon (frontseat daemon), in
// a session of its own, so that it goes on when the command that
// started it and its terminal are gone. What it prints is kept in its
// state directory, after what the daemons before it there printed.
func detached(o life.Options) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.State, 0o700); err != nil {
		return err
	}
	said, err := os.OpenFile(filepath.Join(o.State, logFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer said.Close()
	command := exec.Command(self, "daemon", "--directory", o.Directory, "--state", o.State, "--settings", o.Settings, "--version", o.Version)
	command.Dir = o.Directory
	command.Stdout, command.Stderr = said, said
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	// It is no child to wait for: it outlives this command.
	return command.Process.Release()
}

// lastSaid is the end of what a daemon printed, for whoever is told it
// did not serve.
func lastSaid(state string) string {
	raw, err := os.ReadFile(filepath.Join(state, logFile))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	return lines[len(lines)-1]
}
