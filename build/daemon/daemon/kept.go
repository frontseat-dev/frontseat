package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	// PID is its process.
	PID int `json:"pid"`
}

// Told returns what the build daemon of a state directory says of
// itself. One that has ended says nothing: there is then no file. What
// it says is not proof that it lives: whoever reads it asks the daemon.
func Told(state string) (Info, error) {
	raw, err := os.ReadFile(filepath.Join(state, infoFile))
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return Info{}, fmt.Errorf("what the build daemon said of itself: %w", err)
	}
	return info, nil
}

// life is a build daemon's life: the state directory that is its own,
// what it serves, and when it ends.
type life struct {
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
}

// keep is a build daemon's life. It takes the state directory for
// itself, refusing where another daemon holds it; serves on a port of
// the loopback, to its token alone; says where in the directory; and
// ends when the context does, or once it has had nothing to do for long
// enough, having taken back what it said.
func keep(ctx context.Context, l life) error {
	if err := os.MkdirAll(l.State, 0o700); err != nil {
		return fmt.Errorf("the build daemon's state: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(l.State, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("the build daemon's state: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another build daemon serves the directory %s is the state of: a directory has one at a time", l.State)
	}
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
	file := filepath.Join(l.State, infoFile)
	// The file is this daemon's own: it holds the directory's lock, and
	// wrote it. What a daemon that died left is replaced.
	defer os.Remove(file)
	if err := say(file, said); err != nil {
		_ = listener.Close()
		return err
	}
	serving, stop := context.WithCancel(ctx)
	defer stop()
	calls := &admitted{handler: l.Handler, token: []byte(said.Token), last: time.Now()}
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		calls.idle(serving, l.Busy, l.Idle, stop)
	}()
	err = httpserver.Serve(serving, listener, calls)
	stop()
	<-watched
	return err
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

	mu sync.Mutex
	// under is how many calls are under way, and last when the last of
	// them ended, or the daemon began.
	under int
	last  time.Time
}

func (a *admitted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const scheme = "Bearer "
	presented := r.Header.Get("Authorization")
	if len(presented) < len(scheme) || presented[:len(scheme)] != scheme || subtle.ConstantTimeCompare([]byte(presented[len(scheme):]), a.token) != 1 {
		http.Error(w, "the build daemon is asked with its token", http.StatusUnauthorized)
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
func (a *admitted) idle(ctx context.Context, busy func() bool, long time.Duration, end func()) {
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
			if a.under > 0 || busy() {
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
