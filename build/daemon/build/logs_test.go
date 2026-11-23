package build

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// Over the API as a client reads it: a stream of logs is every line an
// action printed, standard output then standard error, in parts.
func TestAClientReadsEveryLineAnActionPrinted(t *testing.T) {
	printed := strings.Repeat("0123456789\n", 20_000)
	c := serve(t, answering{as: func(r *exec.Result) { r.Stdout, r.Stderr = []byte(printed), []byte("wrong\n") }}, oneModule(t), true)
	run := wait(t, c.runs, create(t, c.runs).GetId())
	lines, err := logsOf(c.runs, run.GetId(), "//hello:go.lint")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 20_001 {
		t.Fatalf("%d lines, want 20001", len(lines))
	}
	if final := lines[len(lines)-1]; final.GetText() != "wrong" || final.GetStream().String() != "LOG_STREAM_STDERR" {
		t.Errorf("the last line is %q to %v, want what was printed to standard error", final.GetText(), final.GetStream())
	}
}

// lintPrinted is what the lint of one Go module printed, read over the
// API, where the grid has it print this.
func lintPrinted(t *testing.T, stdout string) ([]*typev1.LogLine, error) {
	t.Helper()
	c := serve(t, answering{as: func(r *exec.Result) { r.Stdout = []byte(stdout) }}, oneModule(t), true)
	run := wait(t, c.runs, create(t, c.runs).GetId())
	return logsOf(c.runs, run.GetId(), "//hello:go.lint")
}

// What an action printed that is no text is read all the same, each
// byte that is none given as U+FFFD, and the lines after it too.
func TestOutputThatIsNotTextIsReadWithWhatFollowsIt(t *testing.T) {
	lines, err := lintPrinted(t, "caf\xe9\nafter\n")

	if err != nil || len(lines) != 2 || lines[0].GetText() != "caf�" || lines[1].GetText() != "after" {
		t.Errorf("the output reads %v, %v", lines, err)
	}
}

// One line longer than a message of the API holds is read whole, with
// the lines around it: it is sent in parts that say they continue.
func TestALineLongerThanAMessageIsReadWithTheLinesAroundIt(t *testing.T) {
	long := strings.Repeat("x", 5<<20)
	lines, err := lintPrinted(t, "first\nsecond\n"+long+"\nlast\n")

	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 || lines[0].GetText() != "first" || lines[1].GetText() != "second" || lines[2].GetText() != long || lines[3].GetText() != "last" {
		t.Errorf("%d lines were read, want the four printed, the third of five mebibytes", len(lines))
	}
}
