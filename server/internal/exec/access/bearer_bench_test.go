package access

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
)

// A call to a grid that asks who the daemon is reads the token's file.
// Measured at 13 µs and 15 allocations (go test -run '^$' -bench
// TheTokenIsRead -benchmem -count 6 ./server/internal/exec/access/ from
// the root, Go 1.27.3 on an M-series Mac, six runs of 12.4 to 14.5 µs),
// beside a call that crosses a network; and, of its first form, at
// 26.7 µs under a gate run on a loaded machine (an independent review's
// run of the gate's own line). The time allowed is
// three times that, so a loaded gate holds; the allocations, which a
// machine's load does not change, are held close.
func TestBudgetOfATokenReadForACall(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 80 * time.Microsecond, Allocs: 16}, BenchmarkTheTokenIsReadForACall)
}

// What reading the token's file costs a call: it is read for each one,
// in place of remembering it and asking the file whether it changed.
func BenchmarkTheTokenIsReadForACall(b *testing.B) {
	// The length of one of GitHub's.
	token := holding(b, "a.b."+strings.Repeat("a", 1500))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := token.GetRequestMetadata(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

// holding is the token of a file that holds one and a line's end.
func holding(tb testing.TB, token string) bearer {
	tb.Helper()
	at := filepath.Join(tb.TempDir(), "token")
	if err := os.WriteFile(at, []byte(token+"\n"), 0o600); err != nil {
		tb.Fatal(err)
	}
	return bearer(at)
}

// A call that was stopped reads no token and sends none, and is told it
// was stopped.
func TestACallThatWasStoppedReadsNoToken(t *testing.T) {
	token := holding(t, "aaa.bbb.ccc")
	stopped, stop := context.WithCancel(t.Context())
	stop()
	if said, err := token.GetRequestMetadata(stopped); status.Code(err) != codes.Canceled || said != nil {
		t.Errorf("stopped: %v, %v", said, err)
	}
	// Control: a call that goes on is told the token.
	if said, err := token.GetRequestMetadata(t.Context()); err != nil || said["authorization"] != "Bearer aaa.bbb.ccc" {
		t.Errorf("going on: %v, %v", said, err)
	}
}
