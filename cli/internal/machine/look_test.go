package machine

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
)

// A look at what a build of a directory is made from is told by the
// settings the directory's daemon is told by: outside a sandbox the
// command line's own, and inside one those the sandbox's supervisor
// wrote. So the settings that would make another daemon are the ones
// that look otherwise when they are written. Each is the control of the
// other: the settings of the one place written, a look from the other
// sees nothing.
func TestALookIsToldByTheSettingsItsDaemonIsToldBy(t *testing.T) {
	_, outside := settled(t)
	inASandbox(t)
	told, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boxed.settings, told, 0o600); err != nil {
		t.Fatal(err)
	}
	here := t.TempDir()
	seen := func(inside bool) *life.Seen {
		t.Helper()
		for {
			now, err := Look(t.Context(), here, inside)
			if err != nil {
				t.Fatal(err)
			}
			if now.Settled() {
				return now
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	for _, tc := range []struct {
		written string
		inside  bool
	}{{outside, false}, {boxed.settings, true}} {
		from, other := seen(tc.inside), seen(!tc.inside)
		if err := os.WriteFile(tc.written, append(told, "# more\n"...), 0o600); err != nil {
			t.Fatal(err)
		}
		if changed := seen(tc.inside).Since(from); !slices.Equal(changed, []string{tc.written}) {
			t.Errorf("inside: %t, with %s written, %v look otherwise; want it", tc.inside, tc.written, changed)
		}
		if changed := seen(!tc.inside).Since(other); len(changed) != 0 {
			t.Errorf("the control: inside: %t, with %s written, %v look otherwise; want nothing", !tc.inside, tc.written, changed)
		}
	}
}
