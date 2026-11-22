package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"
)

// asOnADiskWhoseClockDidNotMove makes a run know a file as it looks now:
// what a disk leaves a run with where the file was written again within
// the moment its times are told in, as Linux before 6.13 tells them (a
// few milliseconds), with content of its size. Nothing of the look tells
// the two apart there; on a disk that tells times finely, something
// does, and a test cannot write a file that looks as it did.
func asOnADiskWhoseClockDidNotMove(t *testing.T, run *Snapshot, file string) {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	now := look(info)
	run.mu.Lock()
	defer run.mu.Unlock()
	of := run.repositories[""]
	of.listed["file"] = now
	known := of.held["file"]
	known.looked = now
	of.held["file"] = known
}

// A file a run read when it had only just been written, and that looks
// as it did, is read again when the run is asked what it held: written
// again within the moment its times are told in, it would look the
// same. So a change the look cannot show is seen by what the file holds.
// Control: what was read once the file had settled is gone by, as the
// look then shows any write since, and the file is not read again.
func TestAFileThatLooksAsItDidIsReadAgainWhereItHadNotSettled(t *testing.T) {
	before := sha256.Sum256([]byte("as it is"))
	for name, tc := range map[string]struct {
		keeper  *Keeper
		changed bool
	}{
		"read when it had only just been written": {&Keeper{}, true},
		"read once it had settled":                {&Keeper{now: later}, false},
	} {
		t.Run(name, func(t *testing.T) {
			tree, file := one(t)
			run := take(t, tc.keeper, tree)
			if _, _, _, err := run.File(file); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("as it IS"), 0o644); err != nil {
				t.Fatal(err)
			}
			asOnADiskWhoseClockDidNotMove(t, run, file)

			hash, _, _, err := run.File(file)

			var changed *Changed
			if tc.changed && !errors.As(err, &changed) {
				t.Errorf("hash %s, err %v; want the file named as changed", hash, err)
			}
			if !tc.changed && (err != nil || hash != hex.EncodeToString(before[:])) {
				t.Errorf("hash %s, err %v; want what the run read, unread again", hash, err)
			}
		})
	}
}

// What a run read of a file that had only just been written settles:
// asked again once the moment has passed, the run reads the file once
// more, finds it as it was, and goes by its look from then on.
func TestWhatARunReadOfAFileSettles(t *testing.T) {
	tree, file := one(t)
	clock := later
	keeper := &Keeper{now: func() time.Time { return clock() }}
	clock = time.Now
	run := take(t, keeper, tree)
	held := func() kept {
		run.mu.Lock()
		defer run.mu.Unlock()
		return run.repositories[""].held["file"]
	}
	if _, _, _, err := run.File(file); err != nil || held().settled {
		t.Fatalf("read at once: err %v, settled %v; want it read and not yet settled", err, held().settled)
	}
	clock = later
	if _, _, _, err := run.File(file); err != nil || !held().settled {
		t.Fatalf("read later: err %v, settled %v; want it read again and settled", err, held().settled)
	}
}
