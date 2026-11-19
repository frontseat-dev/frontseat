package local_test

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The worker's pool for what actions write holds as many actions as
// run at once, each writing as much as it may: it is sized from how
// many run, one in each slot and each test with services, so that telling
// the worker to run more does not leave the pool too small for them.
func TestThePoolHoldsWhatTheActionsRunAtOnceMayWrite(t *testing.T) {
	const at = "image/rootfs/etc/frontseat/bb/worker.jsonnet"
	worker, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}
	for what, written := range map[string]string{
		"how many run at once is the worker's own setting": `local concurrency = std.parseInt(std.extVar('GRID_CONCURRENCY'));`,
		"a slot for each, one action at a time":            `} for slot in std.range(1, concurrency)]`,
		"so many tests with services at once":              `concurrency: services,`,
		"each slot runs one":                               `concurrency: 1,`,
		"an action may write so much":                      `maximumFilePoolSizeBytes: written,`,
		"the pool holds that much for each that runs":      `sizeBytes: (concurrency + services) * written`,
		"a setting that runs none is refused":              `assert concurrency > 0`,
	} {
		if !regexp.MustCompile(regexp.QuoteMeta(written)).Match(worker) {
			t.Errorf("%s: %s does not say %s", what, at, written)
		}
	}
}

// mapped is the id of this machine's kernel that a container's user is,
// as rootless Podman maps it from the subordinate ids an /etc/subuid
// line gives (user:start:count): id 0 is the user's own, and id n the
// nth subordinate id, start + n - 1. An id beyond the count is not
// mapped at all.
func mapped(subuid string, id int) (int, error) {
	var user string
	var start, count int
	if _, err := fmt.Sscanf(strings.ReplaceAll(subuid, ":", " "), "%s %d %d", &user, &start, &count); err != nil {
		return 0, fmt.Errorf("%q is no subordinate id line: %w", subuid, err)
	}
	if id < 1 || id > count {
		return 0, fmt.Errorf("id %d is beyond the %d subordinate ids of %q", id, count, subuid)
	}
	return start + id - 1, nil
}

// Each slot's directory is owned by the runner image's user as the
// stack's rootless Podman maps it: the id the worker gives it is worked
// out from the subordinate ids the stack's image gives frontseat and the
// user the box, laid on the runner image, runs as, and not written
// apart from them.
func TestASlotIsOwnedByTheRunnersUserAsPodmanMapsIt(t *testing.T) {
	read := func(at string) string {
		t.Helper()
		content, err := os.ReadFile(at)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	subuid := regexp.MustCompile(`echo (frontseat:\d+:\d+) > /etc/subuid`).FindStringSubmatch(read("image/Dockerfile"))
	user := regexp.MustCompile(`(?m)^USER (\d+):\d+$`).FindStringSubmatch(read("box.Dockerfile"))
	owner := regexp.MustCompile(`chown (\d+):(\d+) /var/lib/frontseat/worker/\$\{slot\}`).FindStringSubmatch(read("image/rootfs/etc/s6-overlay/s6-rc.d/worker/run"))
	if subuid == nil || user == nil || owner == nil {
		t.Fatalf("the subordinate ids (%q), the runner's user (%q) or the slot's owner (%q) is not said where it was", subuid, user, owner)
	}
	id, _ := strconv.Atoi(user[1])
	want, err := mapped(subuid[1], id)
	if err != nil {
		t.Fatal(err)
	}
	if owner[1] != strconv.Itoa(want) || owner[2] != strconv.Itoa(want) {
		t.Errorf("a slot is owned by %s:%s, not by the runner's user %d as Podman maps it, %d", owner[1], owner[2], id, want)
	}
	// The control: an id beyond the subordinate ids is mapped to none.
	if got, err := mapped("frontseat:100000:65536", 65537); err == nil {
		t.Errorf("an id beyond the subordinate ids was mapped to %d", got)
	}
}
