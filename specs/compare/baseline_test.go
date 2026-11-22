package compare_test

// The baseline a case is judged against.

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/testsupport/testworkload"
)

// The baseline does what frontseat's actions do, as they do it: each of
// its actions runs on the grid in the runner sandboxes frontseat's run
// in, one action at a time each, each command begun by frontseat's
// confiner as frontseat's daemon begins one, since the grid's runner runs
// no other. So a write beside an action's root is refused, and the
// network is not reached; the control of the first is a write in the
// action's own directory, which is made, and of the second the same
// reach sent to the sandboxes that may reach the network (network=egress,
// as frontseat sends a fetch), which reaches it.
func TestTheBaselineIsConfinedAsFrontseatsActionsAre(t *testing.T) {
	var log string
	bz := bazelOnGrid(t, testworkload.Shape{Modules: 2, Group: 2, Salt: time.Now().Format(time.RFC3339Nano)}, &log)
	printed := bz.build(t)
	if ran := bz.ran(t, printed); ran != strconv.Itoa(2*len(testworkload.Actions)) {
		t.Fatalf("Bazel ran %s actions of the baseline, want every one", ran)
	}
	// And each did its work and nothing else: no action printed anything
	// Bazel then brought back.
	if strings.Contains(printed, "not found") || strings.Contains(printed, "INFO: From ") {
		t.Errorf("the baseline's actions did more than their work:\n%s", printed)
	}
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	escape := "echo x > ../escaped-" + nonce
	inside := "echo x > inside-" + nonce
	reach := "curl -s -o /dev/null --max-time 5 https://github.com && echo REACHED"
	bz.change(t, "probe/BUILD.bazel", `load("//:rules.bzl", "run")`+"\n"+probe("inside"+nonce, inside, false)+probe("escape"+nonce, escape, false)+
		probe("offline_reach"+nonce, reach, false)+probe("egress_reach"+nonce, reach, true))

	if printed, err := bz.try(t, "//probe:inside"+nonce); err != nil {
		t.Fatalf("a write in the action's own directory failed, so this test proves nothing: %v\n%s", err, printed)
	}
	if printed, err := bz.try(t, "//probe:escape"+nonce); err == nil {
		t.Errorf("a write beside the action's root was not refused:\n%s", printed)
	}
	if printed, err := bz.try(t, "//probe:egress_reach"+nonce); err != nil || !strings.Contains(printed, "REACHED") {
		t.Fatalf("an action sent where it may reach the network did not, so this test proves nothing: %v\n%s", err, printed)
	}
	if printed, err := bz.try(t, "//probe:offline_reach"+nonce); err == nil {
		t.Errorf("an action that is no fetch reached the network:\n%s", printed)
	}
}

// probe is an action of the workspace's run rule (testworkload) that runs
// a command, then writes its output: sent to the runner sandboxes that
// reach nothing, or, egress, to those that may reach the network, as
// frontseat sends a fetch.
func probe(name, command string, egress bool) string {
	properties := ""
	if egress {
		properties = `, exec_properties = {"network": "egress"}`
	}
	return `run(name = "` + name + `", cmd = ` + strconv.Quote(command) + properties + ")\n"
}
