package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	connect "connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

const (
	toOut = typev1.LogStream_LOG_STREAM_STDOUT
	toErr = typev1.LogStream_LOG_STREAM_STDERR
)

// printedBy is a run, kept in a directory, whose one action //a:b
// printed this; kept says which of the two the directory holds.
func printedBy(t *testing.T, stdout, stderr string, kept ...string) (*buildService, string) {
	t.Helper()
	dir, id := filepath.Join(t.TempDir(), "runs"), "build_0123456789ab"
	messages := whole(id, 0, id)
	made, last := messages[0], messages[1].(*buildv1.BuildEvent)
	last.Sequence = 3
	last.GetFinished().GetBuild().LastSequence = 3
	result := &buildv1.ActionResult{Label: "//a:b", Stdout: digestOf(stdout), Stderr: digestOf(stderr)}
	crafted(t, dir, id, made,
		&buildv1.BuildEvent{Sequence: 1, Event: &buildv1.BuildEvent_Planned_{Planned: &buildv1.BuildEvent_Planned{Actions: []*buildv1.BuildEvent_PlannedAction{{Label: "//a:b"}}}}},
		&buildv1.BuildEvent{Sequence: 2, Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: result}}},
		last)
	for _, content := range kept {
		if err := os.WriteFile(filepath.Join(dir, id, printedDir, digestOf(content).GetHash()), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rs := readRuns(t, dir)
	return &buildService{runs: rs}, id
}

func digestOf(content string) *buildv1.Digest {
	sum := sha256.Sum256([]byte(content))
	return &buildv1.Digest{Hash: hex.EncodeToString(sum[:]), SizeBytes: int64(len(content))}
}

// said is a line as the contract carries one.
type said struct {
	stream typev1.LogStream
	text   string
}

// many is a line, many times.
func many(n int, line said) []said {
	all := make([]said, n)
	for i := range all {
		all[i] = line
	}
	return all
}

// What an action printed is sent in parts of lines, none larger than a
// part is meant to be unless it is one line, and all of them together
// are what it printed: standard output, then standard error, each line
// marked with which. A line longer than a part is sent as several, each
// a part at most and cut between characters, every one but the last
// saying it continues: what is held of a line is a part, however long
// it is.
func TestWhatAnActionPrintedIsSentInPartsOfLines(t *testing.T) {
	long := strings.Repeat("x", 3*part)
	// wide is a line of characters of two bytes, which a cut at a part's
	// end would fall within were it not cut between them.
	wide := "x" + strings.Repeat("é", part)
	for name, printed := range map[string]struct {
		out, err string
		want     []said
		// parts is how many parts it comes in, where that is known.
		parts int
	}{
		"nothing":               {},
		"one line":              {out: "one\n", want: []said{{toOut, "one"}}, parts: 1},
		"no newline at the end": {out: "one\ntwo", want: []said{{toOut, "one"}, {toOut, "two"}}, parts: 1},
		"a line of nothing":     {out: "\n", want: []said{{toOut, ""}}, parts: 1},
		"many lines of nothing": {out: strings.Repeat("\n", 300_000), want: many(300_000, said{toOut, ""})},
		"lines of one byte":     {out: strings.Repeat("x\n", 300_000), want: many(300_000, said{toOut, "x"})},
		"a line longer than a part": {out: "before\n" + long + "\nafter\n",
			want: []said{{toOut, "before"}, {toOut, long}, {toOut, "after"}}, parts: 5},
		"a line longer than a part, with no end": {out: long, want: []said{{toOut, long}}, parts: 4},
		"a line of characters cut between them":  {out: wide + "\n", want: []said{{toOut, wide}}},
		"a line whose end a part falls within":   {out: strings.Repeat("x", part-1) + "\r\nafter\n", want: []said{{toOut, strings.Repeat("x", part-1)}, {toOut, "after"}}},
		// Sixteen bytes are counted for what goes with a line's text.
		"two lines that fill a part": {out: strings.Repeat("x", part/2-16) + "\n" + strings.Repeat("y", part/2-16) + "\n",
			want: []said{{toOut, strings.Repeat("x", part/2-16)}, {toOut, strings.Repeat("y", part/2-16)}}, parts: 1},
		"two lines, a byte more than a part": {out: strings.Repeat("x", part/2-16) + "\n" + strings.Repeat("y", part/2-15) + "\n",
			want: []said{{toOut, strings.Repeat("x", part/2-16)}, {toOut, strings.Repeat("y", part/2-15)}}, parts: 2},
		"both streams": {out: strings.Repeat("o\n", 40_000), err: strings.Repeat("e\n", 40_000),
			want: append(many(40_000, said{toOut, "o"}), many(40_000, said{toErr, "e"})...)},
		"standard error alone": {err: "wrong\n", want: []said{{toErr, "wrong"}}, parts: 1},
		"carriage returns, and no end": {out: "one\r\ntwo\r\n", err: "th\rree\r",
			want: []said{{toOut, "one"}, {toOut, "two"}, {toErr, "th\rree"}}, parts: 1},
		// A message's text is UTF-8. What is not is given as U+FFFD, and
		// the lines after it come all the same.
		"bytes that are no text": {out: "caf\xe9\n\xff\xfe\x00\nafter\n",
			want: []said{{toOut, "caf�"}, {toOut, "�\x00"}, {toOut, "after"}}, parts: 1},
	} {
		t.Run(name, func(t *testing.T) {
			service, id := printedBy(t, printed.out, printed.err, printed.out, printed.err)
			{
				var got []said
				parts, continuing := 0, false
				err := service.logs(t.Context(), &buildv1.StreamBuildLogsRequest{BuildId: id, Label: "//a:b"}, func(sent *buildv1.StreamBuildLogsResponse) error {
					parts++
					if len(sent.GetLines()) == 0 {
						t.Errorf("part %d has no line", parts)
					}
					if size := proto.Size(sent); size > part && len(sent.GetLines()) > 1 {
						t.Errorf("part %d is %d bytes in %d lines, more than the %d a part is", parts, size, len(sent.GetLines()), part)
					}
					if _, err := proto.Marshal(sent); err != nil {
						t.Errorf("part %d cannot be sent: %v", parts, err)
					}
					for _, line := range sent.GetLines() {
						if len(line.GetText()) > part {
							t.Errorf("part %d holds a line of %d bytes, more than a part", parts, len(line.GetText()))
						}
						if !utf8.ValidString(line.GetText()) || (utf8.ValidString(printed.out+printed.err) && strings.ContainsRune(line.GetText(), utf8.RuneError)) {
							t.Errorf("part %d holds a line cut within a character", parts)
						}
						// A line that continues is joined to the next of
						// its stream, which is what was printed.
						if n := len(got); n > 0 && continuing {
							got[n-1].text += line.GetText()
						} else {
							got = append(got, said{line.GetStream(), line.GetText()})
						}
						continuing = line.GetContinues()
					}
					return nil
				})

				if err != nil {
					t.Fatal(err)
				}
				rest := printed.want
				if len(got) != len(rest) {
					t.Fatalf("%d lines came in %d parts, want the %d it printed", len(got), parts, len(rest))
				}
				for i := range rest {
					if got[i] != rest[i] {
						t.Fatalf("line %d is %v %.40q (%d bytes), want %v %.40q (%d bytes)", i, got[i].stream, got[i].text, len(got[i].text), rest[i].stream, rest[i].text, len(rest[i].text))
					}
				}
				if printed.parts > 0 && parts != printed.parts {
					t.Errorf("it came in %d parts, want %d", parts, printed.parts)
				}
				if least := (len(printed.out) + len(printed.err)) / part; printed.parts == 0 && parts < least {
					t.Errorf("%d bytes came in %d parts", len(printed.out)+len(printed.err), parts)
				}
			}
		})
	}
}

// Nothing more is read or sent once a part could not be sent.
func TestAStreamOfLogsThatBreaksSendsNothingMore(t *testing.T) {
	printed := strings.Repeat("0123456789\n", 50_000)
	service, id := printedBy(t, printed, "last\n", printed, "last\n")
	broke := errors.New("the stream broke")
	sends := 0

	err := service.logs(t.Context(), &buildv1.StreamBuildLogsRequest{BuildId: id, Label: "//a:b"}, func(sent *buildv1.StreamBuildLogsResponse) error {
		if sends++; sends > 2 {
			return broke
		}
		return nil
	})

	if !errors.Is(err, broke) {
		t.Fatalf("a stream whose third part could not be sent ended with %v", err)
	}
	if sends != 3 {
		t.Errorf("%d parts were tried after one could not be sent", sends-3)
	}
}

// Whoever asked and has gone is sent nothing more, and nothing more is
// read for them.
func TestLogsAreNotReadForACallerThatHasGone(t *testing.T) {
	printed := strings.Repeat("0123456789\n", 50_000)
	service, id := printedBy(t, printed, "", printed)
	ctx, gone := context.WithCancel(t.Context())
	sends := 0

	err := service.logs(ctx, &buildv1.StreamBuildLogsRequest{BuildId: id, Label: "//a:b"}, func(*buildv1.StreamBuildLogsResponse) error {
		sends++
		gone()
		return nil
	})

	if !errors.Is(err, context.Canceled) || sends != 1 {
		t.Errorf("a caller that went after the first part was sent %d parts, and the stream ended with %v", sends, err)
	}
}

// An action whose output is not all there is refused as no longer kept,
// and no part of what is there is sent: whichever of the two is missing.
func TestNoPartIsSentOfOutputThatIsNotAllThere(t *testing.T) {
	for name, kept := range map[string][]string{"standard output is missing": {"wrong\n"}, "standard error is missing": {"fine\n"}, "both are missing": nil} {
		t.Run(name, func(t *testing.T) {
			service, id := printedBy(t, "fine\n", "wrong\n", kept...)
			sends := 0

			err := service.logs(t.Context(), &buildv1.StreamBuildLogsRequest{BuildId: id, Label: "//a:b"}, func(*buildv1.StreamBuildLogsResponse) error {
				sends++
				return nil
			})

			if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeNotFound || reason != "LOG_NOT_KEPT" || sends != 0 {
				t.Errorf("%d parts were sent, and the stream ended with %v; want none and LOG_NOT_KEPT", sends, err)
			}
		})
	}
}

// An action nobody planned is not found, nor a run nobody made.
func TestLogsAreRefusedForWhatIsNotThere(t *testing.T) {
	service, id := printedBy(t, "fine\n", "", "fine\n")
	for name, asked := range map[string]struct {
		request *buildv1.StreamBuildLogsRequest
		want    connect.Code
	}{
		"an action nobody planned": {&buildv1.StreamBuildLogsRequest{BuildId: id, Label: "//a:c"}, connect.CodeNotFound},
		"a run nobody made":        {&buildv1.StreamBuildLogsRequest{BuildId: "build_ffffffffffff", Label: "//a:b"}, connect.CodeNotFound},
	} {
		err := service.logs(t.Context(), asked.request, func(*buildv1.StreamBuildLogsResponse) error {
			t.Errorf("%s: something was sent", name)
			return nil
		})
		if connect.CodeOf(err) != asked.want {
			t.Errorf("%s: %v, want %v", name, err, asked.want)
		}
	}
}
