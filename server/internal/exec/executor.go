// Package exec is what the engine asks of whatever runs its actions. An
// action is described so that it can run anywhere: its inputs are named,
// its command is relative to them, and nothing in it is a path on the
// machine the daemon runs on but the one directory the inputs are read
// from.
package exec

import (
	"context"
	"errors"
	"time"
)

// Executor runs one action and says what happened. The grid is one.
type Executor interface {
	// Execute runs the action to its end, or until ctx ends. An action
	// that ran and failed is a Result with its exit code, not an error;
	// an error is an action that could not be run.
	Execute(ctx context.Context, action Action) (Result, error)
}

// Merging is an executor that can put directories it holds together:
// what several actions an action needs left in one place.
type Merging interface {
	// Merge returns the directory that holds what all do. Where one path
	// would hold two different things, it returns a *Clash.
	Merge(ctx context.Context, all []Held) (Held, error)
}

// Listing reads the files of a worker-produced directory by content identity.
// Paths are relative and validated; links are refused. File bytes remain in CAS.
type Listing interface {
	List(ctx context.Context, directory Held) (map[string]Held, error)
}

// Materializing is an executor that can write a directory it holds to
// where the daemon runs: a tool, so that a fix, which runs where the
// checkout is, runs with the same tool the check on the grid runs with.
type Materializing interface {
	// Materialize writes what is held to the path, which must not be
	// there yet: a directory with each file as the grid holds it, or a
	// file, as a release's artifact is brought to be published.
	Materialize(ctx context.Context, directory Held, into string) error
}

// ErrCannotMerge is an executor that is not one that can.
var ErrCannotMerge = errors.New("the grid cannot put directories together")

// Clash is one path that would hold two different things.
type Clash struct {
	// Path is where, relative to what was being put together; empty
	// where that itself is what differs.
	Path string
	// First and Second are which of those given hold the two.
	First, Second int
}

func (c *Clash) Error() string { return "two different things at " + c.Path }

// ErrNotKept is content a store does not hold.
var ErrNotKept = errors.New("the grid keeps nothing under that digest")

// NetworkProperty is the platform property an action that reaches the
// network is sent by, and Egress its value: the runner sandboxes whose
// policy lets them reach it advertise it, as tenant zero's do; every other
// action is sent to those that reach nothing.
const (
	NetworkProperty = "network"
	Egress          = "egress"
)

// Reaching is a platform with the network property added: what an action
// that reaches the network is sent with. The platform given is left as it
// is, being every action's.
func Reaching(platform map[string]string) map[string]string {
	out := make(map[string]string, len(platform)+1)
	for name, value := range platform {
		out[name] = value
	}
	out[NetworkProperty] = Egress
	return out
}

// Action is what an executor is asked to run.
type Action struct {
	// Label is the action's label, for what the executor reports.
	Label string
	// Root is the directory the inputs are read from, where the daemon
	// runs.
	Root string
	// Inputs are the files the action reads, as paths relative to Root.
	// Each sits at the same path in the action's root.
	Inputs []string
	// Files says what each file read for the action held when the run
	// began: its inputs and its copies are sent as that, and a file that
	// is no longer that is not sent as something else. Without it, a
	// file is sent as it is when the action is.
	Files Files
	// Held is content the grid holds already, by the path each sits at
	// in the action's root: a tool, what an action it needs left. It may
	// sit beside inputs in a directory, and never on one.
	Held map[string]Held
	// Copies are files of the workspace that sit elsewhere in the action's
	// root than where they are under Root: each set by the directory of
	// the root it sits in. Nothing else sits in that directory.
	Copies map[string]Copy
	// Written are files that are nowhere but in the action's root: each
	// by the path it sits at there, with its content.
	Written map[string]Written
	// Directories are input directories needed even when they hold no files,
	// including the working directories of prepared service programs.
	Directories []string
	// Prepared are independently staged input roots placed beneath this root.
	// Each retains its own source snapshot and composition checks. Commands and
	// platforms are described separately by the runtime manifest.
	Prepared map[string]Action
	// WorkingDirectory is where the command runs, relative to the
	// action's root.
	WorkingDirectory string
	// Arguments are the program and its arguments.
	Arguments []string
	// Environment is the command's whole environment.
	Environment map[string]string
	// Outputs are the files and directories the action leaves, relative
	// to its working directory.
	Outputs []string
	// Platform is what the action needs of the worker that runs it.
	Platform map[string]string
}

// Files says what the files where the daemon runs held at one moment.
type Files interface {
	// File returns what the file at a path held: the hash and the size
	// of its content, and whether it may be run. A file that is no
	// longer what it was is an error.
	File(path string) (hash string, size int64, executable bool, err error)
	// Content returns what the file at a path held, read for whoever
	// must know it and not only send it: the pins a tool is found by.
	Content(path string) ([]byte, error)
}

// Copy is files of one directory, to sit in another.
type Copy struct {
	// From is the directory they are read from, where the daemon runs.
	From string
	// Files are the files, as paths relative to From. Each sits at the
	// same path in the directory it is copied to.
	Files []string
}

// Written is a file written for an action.
type Written struct {
	// Content is its bytes.
	Content []byte
	// Executable is whether it may be run.
	Executable bool
}

// Held is content the grid holds: a file, or a directory and everything
// under it.
type Held struct {
	// Digest names it: a file's bytes, or a directory's root.
	Digest Digest
	// Directory is whether it is a directory, and not a file.
	Directory bool
	// Executable is whether a file may be run.
	Executable bool
}

// Digest names content the grid holds.
type Digest struct {
	// Hash is the content's hash, in hex.
	Hash string
	// Size is its size in bytes.
	Size int64
}

// Valid says whether a digest's hash is a SHA-256 as the grid prints
// one: sixty-four lowercase hexadecimal digits, and so a plain file name.
func (d Digest) Valid() bool {
	if len(d.Hash) != 64 {
		return false
	}
	for _, c := range d.Hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Result is what an action did.
type Result struct {
	// ExitCode is the command's exit code.
	ExitCode int
	// Stdout is what it printed.
	Stdout []byte
	// Stderr is what it printed to standard error.
	Stderr []byte
	// Duration is how long the action took to answer: to be sent, to
	// wait its turn, to run and to be read back.
	Duration time.Duration
	// Started is when a worker started running it, and Worked how long
	// that worker ran it, as the worker says; zero where none ran it for
	// this answer, or said nothing.
	Started time.Time
	Worked  time.Duration
	// Cached is whether the answer is one the grid kept from an earlier
	// execution.
	Cached bool
	// Outputs are the outputs it left, by the path each was asked for
	// at. What was asked for and is not here was not left.
	Outputs map[string]Held
	// Action is what the grid knows the action by: the digest of
	// everything that decides its answer. Two actions with one digest are
	// the same action.
	Action Digest
}
