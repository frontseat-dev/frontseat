// Package history stores run events and output, and recovers durable records.
package history

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/encoding/protodelim"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// Store is where a daemon's runs outlive it: a directory of the
// daemon's own, with a directory for each run, named by the run's id.
// In it are the run's record, which is the run as it was made and then
// each of its events as it happened, and what its actions printed, each
// under its digest.
//
// A run is what its events say: the daemon that comes after reads the
// record and has the run. Nothing of it is kept anywhere else.
type Store struct {
	dir string
	// held is the directory's lock, held from Open until Release.
	held *os.File
}

// A run's id names its directory: IDPrefix, then IDBytes random bytes
// in lowercase hex. Whoever makes a run names it so, and what is in the
// directory under another name is no run's.
const (
	IDPrefix = "build_"
	IDBytes  = 6
)

// lockFile is what a daemon holds while it keeps its runs in a
// directory.
const lockFile = "lock"

// Release lets the directory go, for the daemon that comes after.
func (h *Store) Release() {
	if h.held != nil {
		h.held.Close()
		h.held = nil
	}
}

const (
	recordFile = "record"
	printedDir = "printed"
	// writing begins the name of what an action printed while it is
	// being written, beside where it will be.
	writing = ".writing-"
)

// Open returns the runs kept in a directory, making it where it is
// not there, and takes the directory for one daemon until Release. Two
// daemons that kept their runs in one directory would each take the
// other's runs under way for runs a daemon died under, end them, and
// let them go. So a directory is one daemon's at a time, as Bazel's
// output base and Buck2's daemon directory are, and a daemon that is
// given another's says so and does not start.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("the daemon was given no directory to keep its runs in (--state)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the runs' records: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the runs' records: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("the directory %s is where another daemon keeps its runs: a directory is one daemon's at a time", dir)
	}
	return &Store{dir: dir, held: file}, nil
}

// Record is one run's place in the history, open to be added to.
type Record struct {
	dir string

	// The events are written through a buffer, which is written out a
	// moment after the first event it holds and when the run ends: an
	// Event at a time is a write for each, six thousand of them for a
	// build of three thousand actions with nothing to do. Bazel writes
	// its build's events to a file the same way, as often
	// (FileTransport, a quarter of a second).
	mu      sync.Mutex
	file    *os.File
	events  *bufio.Writer
	pending *time.Timer
	closed  bool
	// failed is the first error a write of the record had.
	failed error
}

// settles is how long an event may be held before it is in the record.
const settles = 250 * time.Millisecond

// Begin makes a run's place and writes the run as it was made.
func (h *Store) Begin(made *buildv1.Build) (*Record, error) {
	dir := filepath.Join(h.dir, made.GetId())
	if err := os.MkdirAll(filepath.Join(dir, printedDir), 0o700); err != nil {
		return nil, fmt.Errorf("keeping the run %s: %w", made.GetId(), err)
	}
	file, err := os.OpenFile(filepath.Join(dir, recordFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("keeping the run %s: %w", made.GetId(), err)
	}
	r := &Record{dir: dir, file: file, events: bufio.NewWriterSize(file, 64<<10)}
	// The run as it was made is in its record at once: a record without
	// it is no run.
	if _, err := protodelim.MarshalTo(file, made); err != nil {
		file.Close()
		return nil, fmt.Errorf("keeping the run %s: %w", made.GetId(), err)
	}
	return r, nil
}

// Event adds an event to the run's record. It is in the record a moment
// later, or when the run ends.
func (r *Record) Event(e *buildv1.BuildEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("the run's record is closed")
	}
	if r.failed != nil {
		return r.failed
	}
	if _, err := protodelim.MarshalTo(r.events, e); err != nil {
		r.failed = err
		return err
	}
	if r.pending == nil {
		r.pending = time.AfterFunc(settles, r.settle)
	}
	return nil
}

// settle writes out the events held. What could not be written is
// said to whoever adds the next event, or asks whether the record is
// whole.
func (r *Record) settle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = nil
	if !r.closed && r.failed == nil {
		r.failed = r.events.Flush()
	}
}

// Written writes out the events held, and returns the first error any
// write of the record has had: a run is told before it says how it
// ended whether what it did is kept.
func (r *Record) Written() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed && r.failed == nil {
		r.failed = r.events.Flush()
	}
	return r.failed
}

// Close lets the record go: the run has had its last event, and every
// Event is in the record.
func (r *Record) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.pending != nil {
		r.pending.Stop()
	}
	err := r.failed
	if flushed := r.events.Flush(); err == nil {
		err = flushed
	}
	if closed := r.file.Close(); err == nil {
		err = closed
	}
	return err
}

// Print keeps what an action printed and returns its digest. The file
// is whole or it is not there: it is written beside and moved into
// place. It is not held back until the disk has it, which costs two
// milliseconds a file and is for a machine that loses power: a record
// is kept for the daemon that comes after one that died, and what a
// daemon wrote the machine still has.
func (r *Record) Print(content []byte) (*buildv1.Digest, error) {
	sum := sha256.Sum256(content)
	digest := &buildv1.Digest{Hash: hex.EncodeToString(sum[:]), SizeBytes: int64(len(content))}
	if len(content) == 0 {
		return digest, nil
	}
	at := filepath.Join(r.dir, printedDir, digest.GetHash())
	if _, err := os.Stat(at); err == nil {
		return digest, nil
	}
	beside, err := os.CreateTemp(filepath.Dir(at), writing)
	if err != nil {
		return digest, err
	}
	_, err = beside.Write(content)
	if closed := beside.Close(); err == nil {
		err = closed
	}
	if err == nil {
		err = os.Rename(beside.Name(), at)
	}
	if err != nil {
		os.Remove(beside.Name())
	}
	return digest, err
}

// Printed opens what an action printed, by its digest; nothing, where it
// Printed nothing.
func (r *Record) Printed(digest *buildv1.Digest) (io.ReadCloser, error) {
	if digest.GetSizeBytes() == 0 {
		return nil, nil
	}
	// A digest is a name here, and is held to being one.
	if raw, err := hex.DecodeString(digest.GetHash()); err != nil || len(raw) != sha256.Size {
		return nil, fmt.Errorf("%q is no digest", digest.GetHash())
	}
	return os.Open(filepath.Join(r.dir, printedDir, digest.GetHash()))
}

// Drop lets a run go, with all that was kept of it.
func (h *Store) Drop(id string) error {
	return os.RemoveAll(filepath.Join(h.dir, id))
}

// Read is a run as its record holds it.
type Read struct {
	Made   *buildv1.Build
	Events []*buildv1.BuildEvent
	Record *Record
}

// All returns the runs kept, oldest first, each with its record open to
// be added to. A record's last event may be cut short, where the daemon
// that wrote it died writing: it is taken off the record, and the run
// is the events before it. A run whose record does not hold the run as
// it was made was never made, and is let go.
func (h *Store) All() ([]Read, error) {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return nil, err
	}
	var out []Read
	for _, entry := range entries {
		// What is here and is not a run's is not the daemon's, and is
		// left as it is.
		if !entry.IsDir() || !isID(entry.Name()) {
			continue
		}
		one, err := h.one(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("the run %s: %w", entry.Name(), err)
		}
		if one.Made == nil {
			if err := h.Drop(entry.Name()); err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, one)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Made.GetCreateTime().AsTime().Before(out[j].Made.GetCreateTime().AsTime())
	})
	return out, nil
}

// one reads a run's record.
func (h *Store) one(id string) (Read, error) {
	dir := filepath.Join(h.dir, id)
	file, err := os.OpenFile(filepath.Join(dir, recordFile), os.O_RDWR|os.O_APPEND, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return Read{}, nil
	}
	if err != nil {
		return Read{}, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return Read{}, err
	}
	// A message is as large as its run made it, a plan of twenty
	// thousand actions among them: it is held to the size of the file,
	// which no message in it is larger than, and to nothing less.
	sized := protodelim.UnmarshalOptions{MaxSize: max(info.Size(), 1)}
	whole := &counted{from: bufio.NewReader(file)}
	made := &buildv1.Build{}
	if err := sized.UnmarshalFrom(whole, made); err != nil || made.GetId() != id {
		file.Close()
		return Read{}, nil
	}
	// A run that says nothing of what it counts or costs counts and
	// costs nothing yet.
	if made.GetCounts() == nil {
		made.Counts = &buildv1.Counts{}
	}
	if made.GetCost() == nil {
		made.Cost = &buildv1.Cost{}
	}
	out := Read{Made: made, Record: &Record{dir: dir, file: file, events: bufio.NewWriterSize(file, 64<<10)}}
	// What a daemon was writing of an action's output when it died is
	// nobody's: no result names it.
	if beside, err := filepath.Glob(filepath.Join(dir, printedDir, writing+"*")); err == nil {
		for _, each := range beside {
			_ = os.Remove(each)
		}
	}
	good := whole.read
	for {
		event := &buildv1.BuildEvent{}
		err := sized.UnmarshalFrom(whole, event)
		if errors.Is(err, io.EOF) {
			break
		}
		// An event that ends a run says how the run ended, and is this
		// run's or no event of it: what a record names is not gone by to
		// find the run, or to let it go.
		if ended := event.GetFinished().GetBuild(); err == nil && event.GetFinished() != nil && ended.GetId() != id {
			err = errors.New("the event ends another run")
		}
		// What follows the last whole event is what was being written
		// when its writer died.
		if err != nil || event.GetSequence() != int64(len(out.Events)+1) {
			if err := file.Truncate(good); err != nil {
				file.Close()
				return Read{}, err
			}
			break
		}
		out.Events = append(out.Events, event)
		good = whole.read
	}
	return out, nil
}

// counted reads, and knows how much it has read.
type counted struct {
	from *bufio.Reader
	read int64
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.from.Read(p)
	c.read += int64(n)
	return n, err
}

func (c *counted) ReadByte() (byte, error) {
	b, err := c.from.ReadByte()
	if err == nil {
		c.read++
	}
	return b, err
}

// isID is whether a name is one a build is given.
func isID(name string) bool {
	rest, is := strings.CutPrefix(name, IDPrefix)
	raw, err := hex.DecodeString(rest)
	return is && err == nil && len(raw) == IDBytes && rest == strings.ToLower(rest)
}
