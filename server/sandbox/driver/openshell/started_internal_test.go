package openshell

import (
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
)

// A sandbox runs its wrapper first, then the image's entrypoint and its
// arguments, or the image's own command where it names none; the image's
// environment stands where the record says nothing. The control is a
// record with no wrapper, which runs as the image says.
func TestASandboxRunsThroughItsWrapper(t *testing.T) {
	started := &Started{Entrypoint: []string{"docker-entrypoint.sh"}, Command: []string{"postgres"}, Environment: map[string]string{"PGDATA": "/data", "SAID": "image"}}
	for name, c := range map[string]struct {
		record driver.Sandbox
		want   []string
	}{
		"no wrapper":              {driver.Sandbox{}, []string{"docker-entrypoint.sh", "postgres"}},
		"a wrapper":               {driver.Sandbox{Wrapper: []string{"/bb/relay", "--"}}, []string{"/bb/relay", "--", "docker-entrypoint.sh", "postgres"}},
		"a wrapper and arguments": {driver.Sandbox{Wrapper: []string{"/bb/relay", "--"}, Arguments: []string{"postgres", "-c", "fsync=off"}}, []string{"/bb/relay", "--", "docker-entrypoint.sh", "postgres", "-c", "fsync=off"}},
	} {
		c.record.Environment = map[string]string{"SAID": "record"}
		command, environment := startedWith(c.record, started)
		if !slices.Equal(command, c.want) {
			t.Errorf("%s: runs %q, want %q", name, command, c.want)
		}
		if environment["PGDATA"] != "/data" || environment["SAID"] != "record" {
			t.Errorf("%s: is given %v, want the image's environment with the record's over it", name, environment)
		}
	}
	// Where what the image starts with is not known, the record's own
	// arguments alone run, after the wrapper.
	if command, _ := startedWith(driver.Sandbox{Wrapper: []string{"/bb/relay"}, Arguments: []string{"x"}}, nil); !slices.Equal(command, []string{"/bb/relay", "x"}) {
		t.Errorf("with the image unknown, runs %q", command)
	}
}
