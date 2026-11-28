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
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
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

// ErrBusy is a directory whose build daemon is not the one this command
// would start and has something in hand, a build, a run or a release:
// it is not stopped for this command, and no other is started beside it.
// Whoever asked waits for what it has in hand, or cancels it from where
// it was started.
var ErrBusy = errors.New("the build daemon of this directory has a build in hand and is not the one this command would start")

// startsWithin is how long a command waits for the daemon of its
// directory, whatever it must wait for: one that is ending, and the one
// it starts, which reaches its grid first.
const startsWithin = 2 * time.Minute

// round is how long a command waits before it looks at the directory
// again, where nothing told it sooner.
const round = 20 * time.Millisecond

// begun is a build daemon this command started.
type begun struct {
	// ended is closed once its process has ended.
	ended <-chan struct{}
	// failed says, once it has ended, whether it ended in failure: one
	// that served and was asked to stop did not.
	failed func() bool
	// printed is what it printed, and nothing a daemon before it did.
	printed func() string
}

// spawn starts a build daemon, which goes on after the command that
// started it has ended; identified reads the settings and says what a
// daemon of them is made of. A test stands in for both.
var (
	spawn      = detached
	identified = life.Read
)

// BuildDaemon returns the build daemon of a directory, starting one
// where none serves: each directory has its own, in a state directory
// that is that directory's alone, beneath the command line's outside a
// sandbox and beneath the sandbox's inside one, where its settings are
// those the sandbox's supervisor wrote (keeps). One that serves is taken
// where it is the daemon this command would start: made of this program,
// of the settings as they are now and of what they load (life.Made), for
// this directory, with what a publish may be given as it is here. Any
// other is asked to stop, and another is started; one that has something
// in hand is not stopped, and the command ends (ErrBusy).
//
// A daemon is known by three things, and by nothing it left behind: the
// lock of its state directory is held, which a daemon lets go of by
// ending, however it ends; what it says there is an address of the
// loopback and a token a daemon mints; and it gives its own answer to
// its own call there (life.HelloPath). So a file a daemon that died
// left names no daemon, and whatever has its port since is told no
// token.
//
// The command looks at the directory again each time round, until the
// daemon it wants serves, the context ends or two minutes have passed:
// it takes a daemon another command started meanwhile, and says at once
// what a daemon it started printed where that one ended without serving.
func BuildDaemon(ctx context.Context, directory string, inside bool) (Builder, error) {
	where, err := keeps(inside)
	if err != nil {
		return Builder{}, err
	}
	settings := where.settings
	found, err := os.Lstat(settings)
	if errors.Is(err, fs.ErrNotExist) {
		return Builder{}, ErrNoSettings
	}
	if err != nil {
		return Builder{}, err
	}
	// It says which grid a token is sent to and which plugins run, and
	// so is its person's alone to write, as where the control plane is
	// (Server). In a sandbox it is the supervisor's, the same user.
	if file, dir := open(settings, found); file {
		return Builder{}, fmt.Errorf("%s can be written by others (%s), or is no file: it says what a build runs and where its grid's token is sent, so it is yours alone (chmod 600)", settings, found.Mode().Perm())
	} else if dir {
		return Builder{}, fmt.Errorf("%s can be written by others: it holds what a build runs and where its grid's token is sent, so it is yours alone (chmod 700)", filepath.Dir(settings))
	}
	read, err := identified(settings)
	if err != nil {
		return Builder{}, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return Builder{}, err
	}
	state := where.of(directory)
	want := life.Info{Version: read.Made.Program, Settings: read.Made.Settings, Directory: directory, Environment: life.Environment(read.Settings.Given)}
	waited, done := context.WithTimeout(ctx, startsWithin)
	defer done()
	var (
		// child is the daemon this command started and still waits for,
		// and asked the daemon it asked to stop, by what it said of itself.
		child *begun
		asked life.Info
	)
	for {
		// Whether the child has ended is asked before who holds the
		// directory: one that lost it to another daemon ended after that
		// one took it.
		over := child != nil && ended(child)
		said, held, err := life.Living(state)
		if held && over {
			// It lost the directory to another daemon, which is no
			// failure: that one is looked at as any is.
			child = nil
		}
		switch {
		case held && err != nil:
			// A daemon that is starting, or ending: it says nothing yet,
			// or any more.
		case held && said == asked:
			// The daemon asked to stop, which has not ended yet.
		case held && differs(said, want) == "":
			if ask(waited, http.MethodGet, said, life.HelloPath) == http.StatusNoContent {
				return Builder{Address: said.Address, Token: said.Token, Log: filepath.Join(state, logFile)}, nil
			}
		case held:
			switch ask(waited, http.MethodPost, said, life.StopPath) {
			case http.StatusNoContent:
				asked = said
			case http.StatusConflict:
				return Builder{}, fmt.Errorf("%w: it differs in %s. Wait for what it has in hand, or cancel it from where it was started", ErrBusy, differs(said, want))
			}
		case err != nil:
			return Builder{}, err
		case child != nil && !over:
			// Started, and not yet holding the directory.
		case child != nil && !child.failed():
			// It served, and ended as another command asked it to before
			// this one found it serving: no failure, and looked at again.
			child = nil
		case child != nil:
			return Builder{}, fmt.Errorf("the build daemon ended as it was started: %s (all it said is in %s)", child.printed(), filepath.Join(state, logFile))
		default:
			if child, err = spawn(life.Options{Directory: directory, State: state, Settings: settings, Compiled: where.compiled(), Version: want.Version}); err != nil {
				return Builder{}, fmt.Errorf("starting the build daemon: %w", err)
			}
		}
		var childEnded <-chan struct{}
		if child != nil && !over {
			childEnded = child.ended
		}
		select {
		case <-waited.Done():
			if err := ctx.Err(); err != nil {
				return Builder{}, err
			}
			printed := ""
			if child != nil {
				printed = ": " + child.printed()
			}
			return Builder{}, fmt.Errorf("the build daemon did not serve within %s%s (all it said is in %s)", startsWithin, printed, filepath.Join(state, logFile))
		case <-childEnded:
		case <-time.After(round):
		}
	}
}

// ended is whether a daemon this command started has ended.
func ended(child *begun) bool {
	select {
	case <-child.ended:
		return true
	default:
		return false
	}
}

// differs says in what a daemon is not the one a command would start,
// by what each is and never by a value; "" where it is that daemon.
func differs(said, want life.Info) string {
	var in []string
	for what, same := range map[string]bool{
		"the program":                    said.Version == want.Version,
		"the settings or what they load": said.Settings == want.Settings,
		"what a publish may be given":    said.Environment == want.Environment,
		"the directory":                  said.Directory == want.Directory,
	} {
		if !same {
			in = append(in, what)
		}
	}
	slices.Sort(in)
	return strings.Join(in, ", ")
}

// logFile is, in a daemon's state directory, what it printed, and
// logLimit how much of it is kept when a daemon is started: over that,
// it is begun anew.
const (
	logFile  = "daemon.log"
	logLimit = 4 << 20
)

// compiled is the directory every build daemon kept here keeps compiled
// plugins in, beside their state directories, which are named by hex
// digits and so never it.
func (k kept) compiled() string { return filepath.Join(k.state, "compiled") }

// of is the state directory of a directory's build daemon: beneath the
// one place they are kept, named by the directory.
func (k kept) of(directory string) string {
	named := sha256.Sum256([]byte(directory))
	return filepath.Join(k.state, hex.EncodeToString(named[:8]))
}

// asking is what a daemon is asked with: it follows no redirect, which
// would carry the token to wherever an answer named, and takes no proxy
// to the loopback from the environment.
var asking = &http.Client{CheckRedirect: bearer.Unfollowed, Transport: &http.Transport{DisableKeepAlives: true}}

// ask asks a daemon one thing with its token, and returns the status it
// answered with, or 0 where it did not answer: one that is gone, or
// ending, which may close the connection it was asked on.
func ask(ctx context.Context, method string, said life.Info, path string) int {
	ctx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	request, err := http.NewRequestWithContext(ctx, method, "http://"+said.Address+path, nil)
	if err != nil {
		return 0
	}
	bearer.Present(request.Header, said.Token)
	answer, err := asking.Do(request)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(answer.Body, 1<<16))
	_ = answer.Body.Close()
	return answer.StatusCode
}

// detached starts this program as a build daemon (frontseat daemon), in
// a session of its own, so that it goes on when the command that
// started it and its terminal are gone. What it prints is kept in its
// state directory, after what the daemons before it there printed, or,
// where that is more than logLimit, in their place: the file is the
// daemons' own, in the state directory this command keeps for them. The
// command waits for it only for as long as it runs itself.
func detached(o life.Options) (*begun, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(o.State, 0o700); err != nil {
		return nil, err
	}
	log := filepath.Join(o.State, logFile)
	said, from, err := appended(log)
	if err != nil {
		return nil, err
	}
	defer said.Close()
	command := exec.Command(self, "daemon", "--directory", o.Directory, "--state", o.State, "--settings", o.Settings, "--compiled", o.Compiled, "--version", o.Version)
	command.Dir = o.Directory
	command.Stdout, command.Stderr = said, said
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	// It outlives this command, which only notes that it ended where it
	// ends first.
	over := make(chan struct{})
	var failure error
	go func() {
		defer close(over)
		failure = command.Wait()
	}()
	return &begun{ended: over, failed: func() bool { <-over; return failure != nil }, printed: func() string { return printedFrom(log, from) }}, nil
}

// appended opens a daemon's log to be added to, begun anew where it is
// over its limit, and says how long it is: what is printed after that is
// the daemon's that is started now.
func appended(log string) (*os.File, int64, error) {
	if kept, err := os.Stat(log); err == nil && kept.Size() > logLimit {
		if err := os.Truncate(log, 0); err != nil {
			return nil, 0, err
		}
	}
	said, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, 0, err
	}
	now, err := said.Stat()
	if err != nil {
		_ = said.Close()
		return nil, 0, err
	}
	return said, now.Size(), nil
}

// printedFrom is what a log holds from where a daemon began to print,
// the end of it where it is long, for whoever is told that daemon did
// not serve.
func printedFrom(log string, from int64) string {
	const most = 4 << 10
	file, err := os.Open(log)
	if err != nil {
		return ""
	}
	defer file.Close()
	if now, err := file.Stat(); err == nil && now.Size()-from > most {
		from = now.Size() - most
	}
	if _, err := file.Seek(from, io.SeekStart); err != nil {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(file, most))
	return strings.TrimSpace(string(raw))
}
