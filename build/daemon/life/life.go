// Package life is a build daemon's life, as the command line starts one
// for a directory: the state directory it takes for itself, the port of
// the loopback it serves on to its token alone, what it says of itself
// there, what it is made of, and when it ends itself. It knows a directory and a file of
// settings, and no control plane, sandbox or workspace's name.
package life

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/core/httpserver"
)

// infoFile and lockFile are, in a build daemon's state directory, what
// it says of where it serves and the lock it holds for as long as it
// lives.
const infoFile, lockFile = "daemon.json", "daemon.lock"

// Info is what a build daemon says of itself, to the command that would
// start one: where it serves, on the loopback, the token it is asked
// with, and what it was started as. A command whose own version or
// settings differ, or that stands in another directory, starts another.
type Info struct {
	// Address is where it serves the build API, host:port.
	Address string `json:"address"`
	// Token is what every call presents, as a bearer token.
	Token string `json:"token"`
	// Version is the program it is, Settings its settings as they were
	// when it read them, and Directory the directory it builds.
	Version   string `json:"version"`
	Settings  string `json:"settings"`
	Directory string `json:"directory"`
	// Environment is what the variables a publish may be given were
	// where it was started (Environment): it keeps them for as long as
	// it lives, so a command where they are otherwise starts another.
	Environment string `json:"environment"`
	// PID is its process. With its address and token it is what one
	// daemon says and no other: whoever asked a daemon to stop knows it
	// is gone once the directory's daemon says anything else.
	PID int `json:"pid"`
}

// minted is a token as a daemon mints one: thirty-two bytes, in
// sixteens.
var minted = regexp.MustCompile(`^[0-9a-f]{64}$`)

// errUnsaid is a file that holds what no daemon writes. It says nothing
// of what the file holds: a token may be in it.
var errUnsaid = errors.New("what the build daemon said of itself is not what a build daemon writes: its address is an address of the loopback and a port, and its token one it minted")

// Told returns what the build daemon of a state directory says of
// itself, held to what a daemon writes: its address an address of the
// loopback, written as a number, and a port, with nothing else, no name,
// path or user, and its token one a daemon mints. Anything else is an
// error that repeats nothing of the file. One that has ended says
// nothing: there is then no file. What it says is not proof that it
// lives: a daemon that died left its file, and whoever reads it asks
// whether the directory is held (Living), and then the daemon.
func Told(state string) (Info, error) {
	raw, err := os.ReadFile(filepath.Join(state, infoFile))
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return Info{}, errUnsaid
	}
	at, err := netip.ParseAddrPort(info.Address)
	if err != nil || !at.Addr().IsLoopback() || at.Addr().Zone() != "" || at.Port() == 0 || at.String() != info.Address || !minted.MatchString(info.Token) {
		return Info{}, errUnsaid
	}
	return info, nil
}

// Living says whether a daemon holds a state directory, and what that
// daemon says of itself. What a file there says is believed only while
// the directory's lock is held, which a daemon lets go of by ending,
// however it ends: held by none, the file is what a daemon that died
// left, and whatever has its port since is no build daemon. It is then
// removed. It is this user's own to delete: it is in the state directory
// of the build daemons this user starts, which is theirs alone, no
// daemon lives there, and it is removed while this holds the directory's
// lock, so that it is never the file of a daemon starting beside this.
// Held, an error is a daemon that says nothing to believe: one that is
// starting and has not said yet, or is ending and has taken it back.
func Living(state string) (Info, bool, error) {
	if _, err := os.Stat(state); err != nil {
		return Info{}, false, nil
	}
	lock, held, err := taken(state)
	if err != nil {
		return Info{}, false, nil
	}
	if !held {
		err := os.Remove(filepath.Join(state, infoFile))
		_ = lock.Close()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Info{}, false, fmt.Errorf("what a build daemon that is gone left: %w", err)
		}
		return Info{}, false, nil
	}
	said, err := Told(state)
	return said, true, err
}

// Life is a build daemon's life: the state directory that is its own,
// what it serves, and when it ends.
type Life struct {
	// State is its state directory.
	State string
	// Said is what it says of itself, but its address, token and
	// process, which are made here.
	Said Info
	// Handler is what it serves, to whoever presents its token.
	Handler http.Handler
	// Busy says whether it has anything in hand that no call is waiting
	// on: a build, a run, a release.
	Busy func() bool
	// Idle is how long it has nothing to do before it ends itself.
	Idle time.Duration
	// Ended is what ends the rest of the daemon once it serves no more,
	// what it built with and what that keeps in the state directory; it
	// may be nil. It is run once, before the directory is let go of.
	Ended func()
}

// HelloPath is what a daemon answers, to its token, with 204 and
// nothing: its own answer to its own call, which nothing else that has a
// port of the loopback gives. A daemon that is ending does not give it.
const HelloPath = "/frontseat.daemon/hello"

// StopPath is what a daemon is asked, with its token, to end itself: by
// the command that finds it is not the daemon it would start, of another
// version, other settings or another directory. One with nothing in
// hand answers 204 and ends; one with a build, a run or a release in
// hand, or a call under way, answers 409 and stays: what it has in hand
// is someone's, and is not ended for another command.
const StopPath = "/frontseat.daemon/stop"

// Taken is a state directory a daemon took for itself: no other serves
// it until it is released.
type Taken struct {
	state string
	lock  *os.File
}

// probed is how long a daemon waits for a directory's lock where it is
// held: whoever asks whether a directory is held (Held, Living) holds
// its lock for an instant, which is no daemon.
const probed = 100 * time.Millisecond

// Take takes a state directory for a daemon, making it, and refuses
// where another daemon holds it. It is taken before anything else of the
// daemon is made, and released after everything else of it has ended:
// a state directory whose lock is free has no daemon left in it, and
// nothing of one.
func Take(state string) (*Taken, error) {
	if err := os.MkdirAll(state, 0o700); err != nil {
		return nil, fmt.Errorf("the build daemon's state: %w", err)
	}
	for begun := time.Now(); ; time.Sleep(2 * time.Millisecond) {
		lock, held, err := taken(state)
		if err != nil {
			return nil, err
		}
		if !held {
			return &Taken{state: state, lock: lock}, nil
		}
		if time.Since(begun) > probed {
			return nil, fmt.Errorf("another build daemon serves the directory %s is the state of: a directory has one at a time", state)
		}
	}
}

// Release lets go of the directory.
func (t *Taken) Release() { _ = t.lock.Close() }

// Keep is a build daemon's life. It takes the state directory for
// itself, refusing where another daemon holds it; serves on a port of
// the loopback, to its token alone; says where in the directory; and
// ends when the context does, or once it has had nothing to do for long
// enough, having taken back what it said, ended the rest of itself
// (Life.Ended) and, last, let go of the directory.
func Keep(ctx context.Context, l Life) error {
	held, err := Take(l.State)
	if err != nil {
		if l.Ended != nil {
			l.Ended()
		}
		return err
	}
	defer held.Release()
	return held.Keep(ctx, l)
}

// Keep is a daemon's life in a directory it took, which stays taken: it
// serves, says where, and ends as Keep does, having run Life.Ended
// however it ended.
func (t *Taken) Keep(ctx context.Context, l Life) error {
	// Run after what the daemon said is taken back, and before whoever
	// took the directory lets go of it.
	defer func() {
		if l.Ended != nil {
			l.Ended()
		}
	}()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	said := l.Said
	said.Address, said.Token, said.PID = listener.Addr().String(), hex.EncodeToString(token), os.Getpid()
	file := filepath.Join(t.state, infoFile)
	// The file is this daemon's own: it holds the directory's lock, and
	// wrote it. What a daemon that died left is replaced.
	defer os.Remove(file)
	if err := say(file, said); err != nil {
		_ = listener.Close()
		return err
	}
	serving, stop := context.WithCancel(ctx)
	defer stop()
	calls := &admitted{handler: l.Handler, token: []byte(said.Token), busy: l.Busy, last: time.Now(), end: stop}
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		calls.idle(serving, l.Idle, stop)
	}()
	err = httpserver.Serve(serving, listener, calls)
	stop()
	<-watched
	return err
}

// taken takes a state directory's lock for a daemon, and says whether
// another holds it; held, no lock is returned.
func taken(state string) (lock *os.File, held bool, err error) {
	lock, err = os.OpenFile(filepath.Join(state, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("the build daemon's state: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, true, nil
	}
	return lock, false, nil
}

// Held says whether a daemon holds a state directory: one that is
// starting, serving or still ending. A daemon has ended, and another may
// be started there, only once this says no: it stops answering, and
// closes what it built with, before it lets go of the directory. A
// directory that is not there, or cannot be asked, is held by none.
func Held(state string) bool {
	if _, err := os.Stat(filepath.Join(state, lockFile)); err != nil {
		return false
	}
	lock, held, err := taken(state)
	if lock != nil {
		_ = lock.Close()
	}
	return err == nil && held
}

// say writes what a daemon says of itself, whole or not at all, for its
// user alone.
func say(file string, info Info) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	beside := file + ".new"
	if err := os.WriteFile(beside, raw, 0o600); err != nil {
		return fmt.Errorf("the build daemon's state: %w", err)
	}
	if err := os.Rename(beside, file); err != nil {
		return fmt.Errorf("the build daemon's state: %w", err)
	}
	return nil
}

// admitted serves a daemon's calls to whoever presents its token, and
// knows when the last of them ended.
type admitted struct {
	handler http.Handler
	token   []byte
	// busy says whether the daemon has anything in hand no call waits on.
	busy func() bool

	mu sync.Mutex
	// under is how many calls are under way, and last when the last of
	// them ended, or the daemon began.
	under int
	last  time.Time
	// end ends the daemon.
	end func()
}

func (a *admitted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const scheme = "Bearer "
	presented := r.Header.Get("Authorization")
	if len(presented) < len(scheme) || presented[:len(scheme)] != scheme || subtle.ConstantTimeCompare([]byte(presented[len(scheme):]), a.token) != 1 {
		http.Error(w, "the build daemon is asked with its token", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == HelloPath {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == StopPath {
		a.mu.Lock()
		inHand := a.under > 0 || a.busy()
		a.mu.Unlock()
		if inHand {
			http.Error(w, "the build daemon has something in hand, and is not stopped for another command", http.StatusConflict)
			return
		}
		// Answered, and the answer sent, before the daemon ends: ending
		// closes every connection, this one with it.
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusNoContent)
		if sent, can := w.(http.Flusher); can {
			sent.Flush()
		}
		a.end()
		return
	}
	a.mu.Lock()
	a.under++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.under--
		a.last = time.Now()
		a.mu.Unlock()
	}()
	a.handler.ServeHTTP(w, r)
}

// idle ends the daemon, by ending, once it has had nothing to do for
// long enough: no call under way, and nothing in hand. It returns when
// the context ends.
func (a *admitted) idle(ctx context.Context, long time.Duration, end func()) {
	if long <= 0 {
		<-ctx.Done()
		return
	}
	tick := time.NewTicker(max(long/4, time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			a.mu.Lock()
			if a.under > 0 || a.busy() {
				a.last = now
			}
			quiet := now.Sub(a.last)
			a.mu.Unlock()
			if quiet >= long {
				end()
				return
			}
		}
	}
}
