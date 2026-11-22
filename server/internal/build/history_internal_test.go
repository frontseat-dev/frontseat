package build

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// A run that has not ended is never let go, however many have ended
// since it began: whoever follows it is still being told of it.
func TestARunThatHasNotEndedIsNeverLetGo(t *testing.T) {
	dir := t.TempDir()
	endedRuns(t, dir, 1, endedKept+1)
	rs := readRuns(t, dir)
	defer rs.stop()
	// The oldest of all, and still under way.
	under := begun(t, rs.history, "build_aaaaaaaaaaaa")
	rs.mu.Lock()
	rs.byID["build_aaaaaaaaaaaa"] = under
	rs.order = append([]*run{under}, rs.order...)
	rs.mu.Unlock()
	late := "build_bbbbbbbbbbbb"
	crafted(t, dir, late, whole(late, 30, late)...)
	records, err := rs.history.All()
	if err != nil {
		t.Fatal(err)
	}
	rs.mu.Lock()
	for _, record := range records {
		if record.Made.GetId() == late {
			rs.order = append(rs.order, recorded(record, nil))
		} else {
			_ = record.Record.Close()
		}
	}
	rs.mu.Unlock()

	rs.prune()

	found := false
	for _, r := range rs.list() {
		found = found || r == under
	}
	if _, err := os.Stat(filepath.Join(dir, "build_aaaaaaaaaaaa", recordFile)); !found || err != nil {
		t.Errorf("the run under way is listed: %v, and its record: %v; want it kept", found, err)
	}
	if len(rs.list()) != endedKept+1 {
		t.Errorf("%d runs are kept; want the %d newest that ended and the one under way", len(rs.list()), endedKept)
	}
}

// What follows the last whole event of a record is taken off it, so
// that what the daemon adds follows what is whole: the daemon after
// reads the run as this one left it.
func TestWhatWasCutShortIsTakenOffTheRecord(t *testing.T) {
	dir := t.TempDir()
	id := "build_0123456789ab"
	started := whole(id, 0, id)[0]
	crafted(t, dir, id, started, &buildv1.BuildEvent{Sequence: 1, Event: &buildv1.BuildEvent_StageChanged_{StageChanged: &buildv1.BuildEvent_StageChanged{Stage: buildv1.Build_STAGE_EVALUATING}}})
	file, err := os.OpenFile(filepath.Join(dir, id, recordFile), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The beginning of an event that was never finished.
	if _, err := file.Write([]byte{0x40, 0x08, 0x02}); err != nil {
		t.Fatal(err)
	}
	file.Close()

	first := readRuns(t, dir)
	said := first.list()[0].snapshot()
	first.stop()
	left, err := os.Stat(filepath.Join(dir, id, recordFile))
	if err != nil {
		t.Fatal(err)
	}
	next := readRuns(t, dir)
	defer next.stop()

	// The run was ended by the daemon before: this one has nothing to
	// add to its record, and nothing to take off it.
	if now, err := os.Stat(filepath.Join(dir, id, recordFile)); err != nil || now.Size() != left.Size() {
		t.Errorf("the record is %d bytes after the second daemon read it, err %v; want the %d the first left, a run that had ended", now.Size(), err, left.Size())
	}

	if len(next.list()) != 1 || !proto.Equal(next.list()[0].snapshot(), said) || said.GetState() != buildv1.Build_STATE_FAILED {
		t.Errorf("the daemon after says %v; want what the daemon before it said, which ended the run: %v", next.list(), said)
	}
	if events := len(next.list()[0].events); events != 2 {
		t.Errorf("the run has %d events; want the one that was whole and the one that ended it", events)
	}
}

// What a daemon was writing of an action's output when it died is
// cleared away by the daemon that reads the run.
func TestWhatWasBeingWrittenWhenADaemonDiedIsClearedAway(t *testing.T) {
	dir := t.TempDir()
	id := "build_0123456789ab"
	crafted(t, dir, id, whole(id, 0, id)...)
	left := filepath.Join(dir, id, printedDir, writing+"123")
	kept := filepath.Join(dir, id, printedDir, "0123")
	for _, file := range []string{left, kept} {
		if err := os.WriteFile(file, []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rs := readRuns(t, dir)
	defer rs.stop()

	if _, err := os.Stat(left); err == nil {
		t.Error("what was being written is still there")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("what was written whole is gone: %v", err)
	}
}

// A daemon keeps its runs somewhere, or does not start.
func TestADaemonGivenNowhereToKeepItsRunsDoesNotStart(t *testing.T) {
	if _, err := newBuilds(nil, nil, nil, nil, nil, nil, "", nil); err == nil {
		t.Error("a daemon given no directory for its runs started")
	}
}
