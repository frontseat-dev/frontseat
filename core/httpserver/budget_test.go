package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
)

func BenchmarkRequestAdmission(b *testing.B) {
	requests := Track(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	for b.Loop() {
		requests.ServeHTTP(response, request)
	}
}

// Apple M4 Max: 8.598–8.675 ns/op, zero bytes and allocations, measured with:
// mise exec -- go test -run '^$' -bench BenchmarkRequestAdmission -benchmem -count=3 ./core/httpserver
// The time allowance accommodates machine variation; admission allocates nothing.
func TestBudgetOfRequestAdmission(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 100 * time.Nanosecond, Allocs: 0}, BenchmarkRequestAdmission)
}
