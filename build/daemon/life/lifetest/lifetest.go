// Package lifetest asks a build daemon's life what a command asks of
// it, with the daemon's token, as a test of the daemon's life asks it:
// a daemon is reached at the address it says it serves at (life.Told).
package lifetest

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
)

// Client is an HTTP client that presents a token to the daemon at an
// address, and to nothing else, as the command line presents a
// daemon's token (bearer); with no token, it presents none.
func Client(address, token string) *http.Client {
	return bearer.NewClient(nil, token, &url.URL{Scheme: "http", Host: address})
}

// Asked asks the daemon at an address one of its calls, at a path, with
// a token, or with none where it is empty, and returns the status it
// answered with.
func Asked(t testing.TB, address, method, path, token string) int {
	t.Helper()
	request, err := http.NewRequest(method, "http://"+address+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := Client(address, token).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = answer.Body.Close()
	return answer.StatusCode
}
