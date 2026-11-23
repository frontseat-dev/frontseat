package reapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
)

// said is what files held at one moment, by the path of each: the
// content the run read of it.
type said map[string]string

// reading is an action that runs command in dir, over the inputs named,
// whose files the run found are files.
func reading(dir string, inputs []string, files exec.Files, command string) exec.Action {
	return exec.Action{
		Label:     "//test:files",
		Root:      dir,
		Inputs:    inputs,
		Files:     files,
		Arguments: []string{"/bin/sh", "-c", command},
		Platform:  testgrid.Platform(),
	}
}

func (s said) File(path string) (string, int64, bool, error) {
	content, there := s[path]
	if !there {
		return "", 0, false, errors.New(path + " was not there")
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:]), int64(len(content)), false, nil
}

func (s said) Content(path string) ([]byte, error) {
	content, there := s[path]
	if !there {
		return nil, errors.New(path + " was not there")
	}
	return []byte(content), nil
}

// An action is sent what its files held when the run began, not what
// they hold when it is sent: its key is of the one, and so is what it
// reads.
func TestAnActionIsSentWhatItsFilesHeld(t *testing.T) {
	grid := dial(t)
	unique := strconv.FormatInt(time.Now().UnixNano(), 10)
	was, is := "as the run found it "+unique+"\n", "as it was left since "+unique+"\n"
	dir := root(t, map[string]string{"greeting.txt": was})
	greeting := filepath.Join(dir, "greeting.txt")
	action := reading(dir, []string{"greeting.txt"}, said{greeting: was}, "cat greeting.txt")

	// The control: the file is what the run found.
	done, err := grid.Execute(context.Background(), action)
	if err != nil || string(done.Stdout) != was {
		t.Fatalf("printed %q, err %v; want the file", done.Stdout, err)
	}

	// The grid holds what the run found, and the file has changed since:
	// the action reads what the run found.
	if err := os.WriteFile(greeting, []byte(is), 0o644); err != nil {
		t.Fatal(err)
	}
	action.Arguments = []string{"/bin/sh", "-c", "cat greeting.txt; echo again"}
	done, err = grid.Execute(context.Background(), action)
	if err != nil || string(done.Stdout) != was+"again\n" {
		t.Errorf("printed %q, err %v; want what the run found, whatever the file holds now", done.Stdout, err)
	}
}

// What the run found of a file and the grid does not hold cannot be
// sent once the file has changed. Nothing is sent in its place, and
// nothing runs.
func TestAFileThatChangedBeforeItWasSentIsNotSentAsSomethingElse(t *testing.T) {
	grid := dial(t)
	unique := strconv.FormatInt(time.Now().UnixNano(), 10)
	was, is := "found and never sent "+unique+"\n", "left in its place "+unique+"\n"
	dir := root(t, map[string]string{"greeting.txt": is})
	action := reading(dir, []string{"greeting.txt"}, said{filepath.Join(dir, "greeting.txt"): was}, "cat greeting.txt")

	done, err := grid.Execute(context.Background(), action)

	if err == nil {
		t.Fatalf("printed %q and no error; want the action not run", done.Stdout)
	}
	if !strings.Contains(err.Error(), "sending its inputs") {
		t.Errorf("err %v; want it to say what could not be sent", err)
	}
}

// A file the run did not find is not an input that is quietly left
// out.
func TestAnInputTheRunDidNotFindIsAnError(t *testing.T) {
	grid := dial(t)
	dir := root(t, map[string]string{"greeting.txt": "hello\n"})
	action := reading(dir, []string{"greeting.txt"}, said{}, "ls")

	_, err := grid.Execute(context.Background(), action)

	if err == nil || !strings.Contains(err.Error(), "was not there") {
		t.Errorf("err %v; want what the files said of it", err)
	}
}
