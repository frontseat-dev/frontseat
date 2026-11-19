package local_test

// Written by an independent review of fe67226..f74db45.

import (
	"os"
	"regexp"
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
