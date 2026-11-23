package testrequired_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// child runs one test of this binary in a process of its own, told the
// probe and, where stack is not empty, the gate's word on the stack, and
// nothing of the gate that runs this test.
func child(t *testing.T, test, probe, stack string) (string, error) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.v")
	for _, each := range os.Environ() {
		if !strings.HasPrefix(each, testrequired.Variable+"=") {
			command.Env = append(command.Env, each)
		}
	}
	command.Env = append(command.Env, "FRONTSEAT_REQUIRED_PROBE="+probe)
	if stack != "" {
		command.Env = append(command.Env, testrequired.Variable+"="+stack)
	}
	out, err := command.CombinedOutput()
	return string(out), err
}

// Exercise Go's real skip/fail/cleanup behavior in a child test process.
// The optional control skips successfully, while the same prerequisite is
// a failure for a required scenario. Cleanup must run in either case.
func TestMissingRequiredPrerequisitesFailInsteadOfSkipping(t *testing.T) {
	for _, operation := range []string{"Skip", "Skipf", "SkipNow"} {
		for _, mode := range []string{"optional", "required"} {
			t.Run(mode+"/"+operation, func(t *testing.T) {
				out, err := child(t, "TestPrerequisiteChild", mode+":"+operation, "")
				if (err != nil) != (mode == "required") {
					t.Fatalf("%s prerequisite: exit=%v\n%s", mode, err, out)
				}
				if !strings.Contains(out, "prerequisite cleanup ran") {
					t.Fatalf("cleanup did not run:\n%s", out)
				}
				if mode == "required" && (!strings.Contains(out, "required test prerequisite unavailable") || strings.Contains(out, "--- SKIP")) {
					t.Fatalf("missing prerequisite was not a failure:\n%s", out)
				}
			})
		}
	}
}

// A gate that runs with no stack skips what needs one, required or not,
// by Stack alone: any other skip of a required prerequisite is still a
// failure there. Another word than "none" is no such word.
func TestAGateWithNoStackSkipsWhatNeedsOne(t *testing.T) {
	for _, probe := range []string{"stack:", "required-stack:", "double-stack:"} {
		out, err := child(t, "TestStackChild", probe, testrequired.NoStack)
		if err != nil || !strings.Contains(out, "--- SKIP") || strings.Contains(out, "went on") {
			t.Fatalf("%s with no stack: exit=%v\n%s", probe, err, out)
		}
		// The control: where the gate says nothing, or says another word,
		// a test that needs the stack goes on to ask it.
		for _, word := range []string{"", "None"} {
			out, err = child(t, "TestStackChild", probe, word)
			if err != nil || strings.Contains(out, "--- SKIP") || !strings.Contains(out, "went on") {
				t.Fatalf("%s where the gate says %q: exit=%v\n%s", probe, word, err, out)
			}
		}
	}
	// A TB held within another value is not looked into: Stack's skip
	// of it fails the test, and says why, rather than letting a skip
	// through that is not Stack's.
	if out, err := child(t, "TestStackChild", "embedded-stack:", testrequired.NoStack); err == nil || !strings.Contains(out, "required test prerequisite unavailable: needs the local stack") {
		t.Fatalf("embedded-stack: with no stack: exit=%v\n%s", err, out)
	}
	for _, operation := range []string{"Skip", "Skipf", "SkipNow"} {
		out, err := child(t, "TestPrerequisiteChild", "required:"+operation, testrequired.NoStack)
		if err == nil || !strings.Contains(out, "required test prerequisite unavailable") {
			t.Fatalf("with no stack, a required prerequisite's own %s passed: exit=%v\n%s", operation, err, out)
		}
	}
}

// A gate that runs against the stack fails a test whose helper finds what
// it needs of the stack missing, and names what is missing. The controls
// are the same helper where the gate says nothing, or another word, which
// skips the test.
func TestAGateAgainstTheStackFailsATestWhoseStackIsMissing(t *testing.T) {
	out, err := child(t, "TestStackChild", "missing:", testrequired.Required)
	if err == nil || strings.Contains(out, "--- SKIP") || !strings.Contains(out, "no grid answers at nowhere") {
		t.Fatalf("missing, where the stack is required: exit=%v\n%s", err, out)
	}
	for _, word := range []string{"", "Required"} {
		out, err := child(t, "TestStackChild", "missing:", word)
		if err != nil || !strings.Contains(out, "--- SKIP") || !strings.Contains(out, "no grid answers at nowhere") {
			t.Fatalf("missing, where the gate says %q: exit=%v\n%s", word, err, out)
		}
	}
}

func TestPrerequisiteChild(t *testing.T) {
	mode, operation, child := strings.Cut(os.Getenv("FRONTSEAT_REQUIRED_PROBE"), ":")
	if !child || strings.HasSuffix(mode, "stack") {
		return
	}
	var tb testing.TB = t
	if mode == "required" {
		tb = testrequired.TB(t)
	}
	tb.Cleanup(func() { t.Log("prerequisite cleanup ran") })
	switch operation {
	case "Skip":
		tb.Skip("grid is unavailable")
	case "Skipf":
		tb.Skipf("%s is unavailable", "grid")
	case "SkipNow":
		tb.SkipNow()
	default:
		t.Fatal("unknown prerequisite probe")
	}
	t.Fatal("a missing prerequisite continued execution")
}

func TestStackChild(t *testing.T) {
	var tb testing.TB = t
	switch os.Getenv("FRONTSEAT_REQUIRED_PROBE") {
	case "stack:":
	case "required-stack:":
		tb = testrequired.TB(t)
	case "embedded-stack:":
		tb = struct{ testing.TB }{TB: testrequired.TB(t)}
	case "double-stack:":
		tb = testrequired.TB(testrequired.TB(t))
	case "missing:":
		testrequired.Stack(tb)
		testrequired.Missing(tb, "no grid answers at %s", "nowhere")
		t.Fatal("a missing stack continued execution")
	default:
		return
	}
	testrequired.Stack(tb)
	t.Log("went on")
}
