package history

import (
	"errors"
	"fmt"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"os"
	"strings"
	"testing"
	"time"
)

// begun is a store of runs in a directory of the test's own, a run, and
// the record the store began for it.
func begun(t *testing.T) (Store, *buildv1.Build, *Record) {
	t.Helper()
	kept, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	made := &buildv1.Build{Id: "build_0123456789ab"}
	record, err := kept.Begin(made)
	if err != nil {
		t.Fatal(err)
	}
	return kept, made, record
}

// An event is in the run's record a moment after it happened, while the
// run goes on: a daemon that dies leaves all but the last moment of its
// runs to the one that comes after.
func TestAnEventIsKeptAMomentAfterItHappenedThoughTheRunGoesOn(t *testing.T) {
	kept, made, record := begun(t)
	defer record.Close()
	if err := record.Event(&buildv1.BuildEvent{Sequence: 1}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * settles)
	for {
		read, err := kept.one(made.GetId())
		if read.Record != nil {
			_ = read.Record.Close()
		}
		if err == nil && len(read.Events) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the record holds %d events, err %v, long after the event; want the event", len(read.Events), err)
		}
		time.Sleep(settles / 10)
	}
}

// The events of a run are not written one at a time: a build of
// thousands of actions is thousands of events, and a write for each is
// most of what such a build costs when it has nothing to do. Nothing of
// them is in the file before the moment has passed or the run has
// ended, and all of them are after.
func TestEventsAreWrittenTogether(t *testing.T) {
	kept, made, record := begun(t)
	size := func() int64 {
		t.Helper()
		info, err := record.file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	begun := size()

	for n := range 100 {
		if err := record.Event(&buildv1.BuildEvent{Sequence: int64(n + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if written := size() - begun; written != 0 {
		t.Errorf("%d bytes of a hundred events were written as they happened; want them written together, a moment later", written)
	}

	if err := record.Close(); err != nil {
		t.Fatal(err)
	}
	read, err := kept.one(made.GetId())
	if read.Record != nil {
		defer read.Record.Close()
	}
	if err != nil || len(read.Events) != 100 {
		t.Errorf("the record holds %d events once the run has ended, err %v; want the hundred", len(read.Events), err)
	}
}

// An actual failed file proves the storage boundary; build's fake tests only
// prove how run completion reacts to the error this boundary reports.
func TestRecordReportsUnderlyingWriteFailures(t *testing.T) {
	for _, large := range []bool{false, true} {
		for _, broken := range []bool{false, true} {
			t.Run(fmt.Sprintf("large=%v/broken=%v", large, broken), func(t *testing.T) {
				store, err := Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				record, err := store.Begin(&buildv1.Build{Id: "build_0123456789ab"})
				if err != nil {
					t.Fatal(err)
				}
				defer record.Close()
				if broken {
					if err := record.file.Close(); err != nil {
						t.Fatal(err)
					}
				}
				event := &buildv1.BuildEvent{Sequence: 1}
				if large {
					event.Event = &buildv1.BuildEvent_Planned_{Planned: &buildv1.BuildEvent_Planned{Roots: []string{strings.Repeat("x", 128<<10)}}}
				}
				appendErr := record.Event(event)
				flushErr := record.Written()
				if broken {
					if !errors.Is(flushErr, os.ErrClosed) {
						t.Fatalf("flush: want actual closed file, got %v", flushErr)
					}
					if large && !errors.Is(appendErr, os.ErrClosed) {
						t.Fatalf("direct write: %v", appendErr)
					}
					if err := record.Event(&buildv1.BuildEvent{Sequence: 2}); !errors.Is(err, os.ErrClosed) {
						t.Fatalf("lost first write failure: %v", err)
					}
				} else if appendErr != nil || flushErr != nil {
					t.Fatalf("working file: append=%v flush=%v", appendErr, flushErr)
				}
			})
		}
	}
}
