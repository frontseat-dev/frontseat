package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"
)

// the file is looked at, then read. What is written to it
// between the two is handed to whoever reads as the file of the run,
// though the run began before it was written. The read here stands for
// a reader that is slower than a writer: it writes the file in place
// once the snapshot has looked at it, then reads.
func TestAFileWrittenBetweenTheLookAndTheReadIsNotOfTheRun(t *testing.T) {
	tree, file := one(t)
	run := take(t, new(Keeper), tree)
	var content []byte
	_, found, err := run.ask("", "file", func(opened *os.File) (string, int64, error) {
		// Its author writes it now: after the run began, and after the
		// snapshot found it looking as it did.
		if err := os.WriteFile(file, []byte("written after the run began"), 0o644); err != nil {
			return "", 0, err
		}
		var err error
		if content, err = io.ReadAll(opened); err != nil {
			return "", 0, err
		}
		sum := sha256.Sum256(content)
		return hex.EncodeToString(sum[:]), int64(len(content)), nil
	})

	var changed *Changed
	if !errors.As(err, &changed) {
		t.Errorf("read %q, found %v, err %v; want the file named as changed: the run began when it held %q", content, found, err, "as it is")
	}
}

// a read of a file is under way when the run rewrites the same
// file. The read ends after the rewrite and records what it read, over
// what the rewrite recorded: from then on the run says the file holds
// what it held before it was rewritten.
func TestAReadUnderWayWhenTheFileIsRewrittenDoesNotUndoWhatTheRunKnows(t *testing.T) {
	tree, file := one(t)
	run := take(t, new(Keeper), tree)
	before, _, _, err := run.File(file)
	if err != nil {
		t.Fatal(err)
	}
	// The control: rewritten with no read under way, the run says the
	// file holds what was written.
	if err := run.Apply("", Change{Path: "file", Kind: Replace, From: before, Content: []byte("first rewrite")}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("first rewrite"))
	if hash, _, _, err := run.File(file); err != nil || hash != hex.EncodeToString(sum[:]) || hash == before {
		t.Fatalf("the control: the run says %s, err %v; want what was written", hash, err)
	}

	// A read that has opened the file, and read it as Read does, when the
	// rewrite happens.
	var content []byte
	read := reading(&content)
	_, _, err = run.ask("", "file", func(opened *os.File) (string, int64, error) {
		hash, size, err := read(opened)
		if err != nil {
			return "", 0, err
		}
		first := sha256.Sum256([]byte("first rewrite"))
		if err := run.Apply("", Change{Path: "file", Kind: Replace, From: hex.EncodeToString(first[:]), Content: []byte("second rewrite")}); err != nil {
			return "", 0, err
		}
		return hash, size, nil
	})
	if err != nil {
		t.Logf("the read under way: %v", err)
	}

	on, err := os.ReadFile(file)
	if err != nil || string(on) != "second rewrite" {
		t.Fatalf("the file holds %q, err %v", on, err)
	}
	sum = sha256.Sum256([]byte("second rewrite"))
	hash, _, _, err := run.File(file)
	if err != nil || hash != hex.EncodeToString(sum[:]) {
		t.Errorf("the run says the file holds %s, err %v; want %s, which is what the run wrote and the file holds", hash, err, hex.EncodeToString(sum[:]))
	}
	if _, _, err := run.Read(context.Background(), "", "file"); err != nil {
		t.Errorf("read after the run's own rewrite: %v", err)
	}
}
