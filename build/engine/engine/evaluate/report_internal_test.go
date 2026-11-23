package evaluate

import (
	"bytes"
	"testing"
)

// What a plugin is handed of a check's output ends at a line's end: a
// finding is a line, and a plugin is handed no part of one.
func TestWhatAPluginIsHandedEndsAtALinesEnd(t *testing.T) {
	line := bytes.Repeat([]byte("x"), 1000)
	line = append(line, '\n')
	printed := bytes.Repeat(line, printedAtMost/len(line)+2)
	got := cut(printed)
	if len(got) > printedAtMost || got[len(got)-1] != '\n' || len(got)%len(line) != 0 {
		t.Fatalf("handed %d bytes ending %q", len(got), got[len(got)-1])
	}
	if got := cut(bytes.Repeat([]byte("y"), printedAtMost+1)); got != nil {
		t.Fatalf("one line longer than the limit was handed in part: %d bytes", len(got))
	}
	if got := cut([]byte("short\n")); string(got) != "short\n" {
		t.Fatalf("a short output was cut: %q", got)
	}
}
