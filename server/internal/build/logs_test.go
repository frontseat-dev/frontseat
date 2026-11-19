package build

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// Over the API as a client reads it: a stream of logs starts at the line
// asked for, and each part says which line is next, so that a client
// whose stream broke asks again from there.
func TestAClientReadsLogsFromALineAndIsToldTheNext(t *testing.T) {
	printed := strings.Repeat("0123456789\n", 20_000)
	c := serve(t, answering{as: func(r *exec.Result) { r.Stdout, r.Stderr = []byte(printed), []byte("wrong\n") }}, oneModule(t), true)
	run := wait(t, c.runs, create(t, c.runs).GetId())
	// The first part, and no more of the stream.
	stream, err := c.runs.StreamBuildLogs(context.Background(), &buildv1.StreamBuildLogsRequest{BuildId: run.GetId(), Label: "//hello:go.lint"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Receive()
	stream.Close()
	if err != nil || len(first.GetLines()) == 0 || first.GetNextLine() != int64(len(first.GetLines())) || first.GetNextLine() >= 20_000 {
		t.Fatalf("the first part is %d lines and says line %d is next, %v", len(first.GetLines()), first.GetNextLine(), err)
	}

	again, err := c.runs.StreamBuildLogs(context.Background(), &buildv1.StreamBuildLogsRequest{BuildId: run.GetId(), Label: "//hello:go.lint", StartLine: first.GetNextLine()})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	rest, last := 0, first.GetNextLine()
	var end *buildv1.StreamBuildLogsResponse
	parts, err := frontseat.Received(again)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		rest += len(part.GetLines())
		if part.GetNextLine() != last+int64(len(part.GetLines())) {
			t.Fatalf("a part of %d lines after line %d says line %d is next", len(part.GetLines()), last, part.GetNextLine())
		}
		last, end = part.GetNextLine(), part
	}

	if len(first.GetLines())+rest != 20_001 || last != 20_001 {
		t.Errorf("%d lines, then %d from line %d on, ending at line %d; want 20001 in all", len(first.GetLines()), rest, first.GetNextLine(), last)
	}
	if final := end.GetLines()[len(end.GetLines())-1]; final.GetText() != "wrong" || final.GetStream().String() != "LOG_STREAM_STDERR" {
		t.Errorf("the last line is %q to %v, want what was printed to standard error", final.GetText(), final.GetStream())
	}
	// From past the end, the stream ends with nothing; from before the
	// first line, it is refused.
	if lines, err := logsFrom(c, run.GetId(), 20_001); err != nil || len(lines) != 0 {
		t.Errorf("from past the last line: %d lines, %v", len(lines), err)
	}
	if _, err := logsFrom(c, run.GetId(), -1); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("from line -1: %v, want an invalid argument", err)
	}
}

func logsFrom(c clients, run string, start int64) ([]string, error) {
	stream, err := c.runs.StreamBuildLogs(context.Background(), &buildv1.StreamBuildLogsRequest{BuildId: run, Label: "//hello:go.lint", StartLine: start})
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	parts, err := frontseat.Received(stream)
	var lines []string
	for _, part := range parts {
		for _, line := range part.GetLines() {
			lines = append(lines, line.GetText())
		}
	}
	return lines, err
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

// OPEN. One line longer than a message of the API holds is a part of its
// own, and no client reads it: the lines before it are read, and the
// stream ends saying so. Dividing such a line is a change to the
// platform's LogLine.
func TestALineLongerThanAMessageEndsTheStreamAfterWhatCameBeforeIt(t *testing.T) {
	lines, err := lintPrinted(t, "first\nsecond\n"+strings.Repeat("x", 5<<20)+"\nlast\n")

	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("the stream ended with %v; a line of five mebibytes is read now, and this test and what the plan says is open are to go", err)
	}
	if len(lines) != 2 || lines[0].GetText() != "first" || lines[1].GetText() != "second" {
		t.Errorf("%d lines came before the stream ended, want the two before the long one", len(lines))
	}
}
