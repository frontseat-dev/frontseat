// Package program runs a program a run asked for where the daemon
// is. A program is no action of the grid: a worker has no address and
// runs what it runs to an end. What the program is made of is built on
// the grid, as any build builds it; it is then brought here by its
// digest, and started here, in the daemon's own sandbox, which is what
// confines it, with the environment its plugin gives it and nothing of
// the daemon's. It runs until it ends or is stopped, and what was
// brought for it is removed then.
package program

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	execport "github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
)

// Bringer brings programs to where the daemon is.
type Bringer struct {
	// Grid writes a directory the grid holds here.
	Grid execport.Materializing
	// Kept is the directory what is brought is kept in, each program in
	// a directory of its own, for as long as it runs.
	Kept string
	// Platform is the workers' platform: what they built runs here only
	// where the daemon is on the same.
	Platform map[string]string
}

// Brought is a program brought here, ready to start.
type Brought struct {
	// Label is the action that is the program.
	Label string
	// dir is all that was brought and all the program is given to write:
	// its root, as the action would have had it on a worker, and its
	// temporary files. It is removed with the program.
	dir string
	// program is the file to start, arguments what it is given first,
	// directory where it runs and environment its whole environment.
	program     string
	arguments   []string
	directory   string
	environment []string
}

// Bring writes what a program reads of what other actions left where
// the daemon is, each thing where it sits in the action's root, and
// finds the program among it. repository is the directory of the
// action's repository in the workspace. What is returned is removed by
// its Remove, or by Start's program ending.
func (b Bringer) Bring(ctx context.Context, repository string, action plan.Action, left map[string]execport.Held) (brought *Brought, err error) {
	label := action.Label.String()
	arguments := action.Declared.GetCommand().GetArguments()
	if !action.IsProgram() || len(arguments) == 0 {
		return nil, fmt.Errorf("%s is no program", label)
	}
	if err := tools.RunsHere(label+" was built for the workers'", b.Platform); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(b.Kept, 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(b.Kept, "program-")
	if err != nil {
		return nil, err
	}
	// Its own to delete: a directory made here, for this program, which
	// nothing else names.
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	// What is brought is named by the grid, and by what a plugin said an
	// action leaves: each place is held to being beneath the root before
	// anything is written there.
	places := make([]string, 0, len(left))
	for at := range left {
		places = append(places, at)
	}
	sort.Strings(places)
	for i, at := range places {
		held := left[at]
		// Places are sorted, so one inside another comes right after it
		// or after others inside it: a directory is written whole, and
		// what sat inside one already written would be lost.
		for _, before := range places[:i] {
			if strings.HasPrefix(at, before+"/") {
				return nil, fmt.Errorf("%s is handed something at %s and at %s inside it: one would be written over the other", label, before, at)
			}
		}
		if !filepath.IsLocal(filepath.FromSlash(at)) {
			return nil, fmt.Errorf("%s is handed something at %q, which is not beneath its root", label, at)
		}
		if !held.Directory {
			return nil, fmt.Errorf("%s reads the file %s another action left: a program is brought directories, and one file alone is not served yet", label, at)
		}
		into := filepath.Join(root, filepath.FromSlash(at))
		if err := os.MkdirAll(filepath.Dir(into), 0o755); err != nil {
			return nil, err
		}
		if err := b.Grid.Materialize(ctx, held, into); err != nil {
			return nil, fmt.Errorf("%s: what it reads at %s could not be brought: %w", label, at, err)
		}
	}
	home, tmp := filepath.Join(root, filepath.FromSlash(tools.Home)), filepath.Join(dir, "tmp")
	directory := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(action.ProjectRoot))
	for _, each := range []string{home, tmp, directory} {
		if err := os.MkdirAll(each, 0o755); err != nil {
			return nil, err
		}
	}
	// The program is where another action left it, in the home or
	// beneath the project, and nowhere else: followed through whatever
	// links lead to it, it is a file of what was brought.
	named := arguments[0]
	at := filepath.Join(directory, filepath.FromSlash(named))
	if rest, inHome := strings.CutPrefix(named, "~/"); inHome {
		at = filepath.Join(home, filepath.FromSlash(rest))
	}
	program, err := within(root, at)
	if err != nil {
		return nil, fmt.Errorf("%s runs %s, which nothing it reads left: %w", label, named, err)
	}
	environment := append(tools.Local(home, tmp), tools.Assignments(action.Declared.GetCommand().GetEnvironment())...)
	return &Brought{
		Label: label, dir: dir, program: program,
		arguments: slices.Clone(arguments[1:]), directory: directory, environment: environment,
	}, nil
}

// within returns a file as it is on the disk, any link followed, where
// it is a regular file that may be run and is beneath a directory.
func within(directory, at string) (string, error) {
	led, err := execport.Within(directory, at)
	switch {
	case errors.Is(err, execport.ErrOutside):
		return "", errors.New("it leads outside what was brought")
	case err != nil:
		return "", errors.New("there is no such file")
	}
	info, err := os.Stat(led)
	switch {
	case err != nil:
		return "", err
	case !info.Mode().IsRegular():
		return "", errors.New("it is not a file")
	case info.Mode()&0o111 == 0:
		return "", errors.New("it may not be run")
	}
	return led, nil
}

// Remove removes what was brought, and whatever the program wrote
// beside it. A program that was started removes it itself, when it
// ends. What the program left that may not be written, as the go
// command leaves the modules it fetched, is made so first. It is its own
// to delete: the directory Bring made for this program.
func (b *Brought) Remove() {
	if os.RemoveAll(b.dir) == nil {
		return
	}
	_ = filepath.WalkDir(b.dir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(b.dir)
}

// grace is how long a program told to end is given to, and drain how
// long what it printed is read for once it has ended: something it
// started that left its group may hold its output open for as long as
// it likes, and the run does not wait for it. Each program takes them
// as it is started, so what it does after is its own to the end.
var grace, drain = 10 * time.Second, 2 * time.Second

// Running is a program that was started.
type Running struct {
	command *exec.Cmd
	// ended is closed when the program has ended, everything it printed
	// has been said, and what was brought for it is removed.
	ended chan struct{}
	code  int
	once  sync.Once
	// stopping is why stopping it did less than it should, if it did.
	stopping error
	// grace and drain are those it was started with.
	grace, drain time.Duration
}

// Start starts the program with further arguments after its own. Each
// line it prints is said to printed, whole, in the order it was printed
// on that stream, without its newline: true for standard error. The
// program is the leader of a group of its own, so that what it starts
// ends with it.
func (b *Brought) Start(arguments []string, printed func(stderr bool, line []byte)) (*Running, error) {
	command := exec.Command(b.program, append(slices.Clone(b.arguments), arguments...)...)
	command.Dir, command.Env = b.directory, b.environment
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Its output is two pipes of the daemon's own, so that the program
	// is waited for by itself: something it started that still holds a
	// pipe does not keep it from being seen to have ended.
	var pipes [2]struct{ read, write *os.File }
	for i := range pipes {
		read, write, err := os.Pipe()
		if err != nil {
			for _, made := range pipes[:i] {
				made.read.Close()
				made.write.Close()
			}
			return nil, err
		}
		pipes[i].read, pipes[i].write = read, write
	}
	command.Stdout, command.Stderr = pipes[0].write, pipes[1].write
	err := command.Start()
	for _, each := range pipes {
		each.write.Close()
	}
	if err != nil {
		for _, each := range pipes {
			each.read.Close()
		}
		b.Remove()
		return nil, fmt.Errorf("%s could not be started: %w", b.Label, err)
	}
	running := &Running{command: command, ended: make(chan struct{}), grace: grace, drain: drain}
	var reading sync.WaitGroup
	for i, each := range pipes {
		reading.Go(func() {
			lines(each.read, func(line []byte) { printed(i == 1, line) })
		})
	}
	go func() {
		_ = command.Wait()
		running.code = exitCode(command.ProcessState)
		// Whatever it started and left behind ends with it, and with
		// that the pipes: everything printed is then read to its end.
		// What left its group is not ended, and may hold a pipe: the
		// reading stops a moment after the program has, all the same.
		_ = running.signal(syscall.SIGKILL)
		read := make(chan struct{})
		go func() { reading.Wait(); close(read) }()
		select {
		case <-read:
		case <-time.After(running.drain):
			for _, each := range pipes {
				each.read.Close()
			}
			<-read
		}
		for _, each := range pipes {
			each.read.Close()
		}
		b.Remove()
		close(running.ended)
	}()
	return running, nil
}

// lineAtMost is the longest line said whole; a longer one is said in
// parts of that length, each as a line, every byte of it in one of them.
const lineAtMost = 64 << 10

// lines says each line read, without what ends it, and what is left at
// the end where no newline ends it. Only what ends a line is taken off
// it: a line cut for its length is said in parts that together are the
// line, and what ends it after the last part is no line of its own.
func lines(from io.Reader, say func([]byte)) {
	reader := bufio.NewReaderSize(from, lineAtMost)
	cut := false
	for {
		line, err := reader.ReadSlice('\n')
		full := errors.Is(err, bufio.ErrBufferFull)
		// A carriage return at a cut may be half of what ends the line:
		// it is left to be read with what follows.
		if n := len(line); full && n > 1 && line[n-1] == '\r' && reader.UnreadByte() == nil {
			line = line[:n-1]
		}
		ends := false
		if n := len(line); n > 0 && line[n-1] == '\n' {
			ends, line = true, line[:n-1]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
		}
		// What only ends a line that was said in parts says nothing.
		if len(line) > 0 || (ends && !cut) {
			say(slices.Clone(line))
		}
		cut = full
		if err != nil && !full {
			return
		}
	}
}

// exitCode is what a program exited with: 128 and the signal's number
// where a signal ended it, as a shell says it.
func exitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// Ended is closed when the program has ended.
func (r *Running) Ended() <-chan struct{} { return r.ended }

// ExitCode is what the program exited with, once it has ended.
func (r *Running) ExitCode() int {
	<-r.ended
	return r.code
}

// Stop tells the program, and whatever it started, to end, and ends
// them where they have not in time. It returns once the program has
// ended. Stopping one that has ended does nothing. Where a process of
// the group the program leads could not be signalled, the error says so:
// what it started may then be left behind.
func (r *Running) Stop() error {
	r.once.Do(func() {
		select {
		case <-r.ended:
			return
		default:
		}
		r.stopping = r.signal(syscall.SIGTERM)
		select {
		case <-r.ended:
		case <-time.After(r.grace):
			if err := r.signal(syscall.SIGKILL); err != nil && r.stopping == nil {
				r.stopping = err
			}
		}
	})
	<-r.ended
	return r.stopping
}

// signal sends a signal to the program's group: the program and what it
// started (confine.SignalGroup, which a sandbox's refusal of a signal to
// a group does not stop).
func (r *Running) signal(signal syscall.Signal) error {
	return confine.SignalGroup(r.command.Process.Pid, signal)
}
