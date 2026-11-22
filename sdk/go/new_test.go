package frontseat

import (
	"errors"
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
)

// client is a Client of a server a test serves.
func client(t *testing.T, server string, options ...Option) *Client {
	t.Helper()
	fs, err := New(server, options...)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// A Client is made for a server named by its scheme, host and port
// alone, and refused, before anything is asked, for any other address,
// and for one that would present its token in clear to another machine.
// The controls are the addresses that are allowed, a trailing slash
// left out, and http to another machine with no token to present.
func TestAClientIsMadeForAServerNamedPlainly(t *testing.T) {
	for address, want := range map[string]string{
		"http://127.0.0.1:7700":           "http://127.0.0.1:7700",
		"http://127.0.0.1:7700/":          "http://127.0.0.1:7700",
		"https://frontseat.example":       "https://frontseat.example",
		"http://localhost:7700":           "http://localhost:7700",
		"http://[::1]:7700":               "http://[::1]:7700",
		"https://frontseat.example:8443/": "https://frontseat.example:8443",
	} {
		fs, err := New(address, WithToken("a-token"))
		if err != nil || fs.Server() != want {
			t.Errorf("%s: made for %v (%v), want %s", address, fs, err, want)
		}
	}
	for _, address := range []string{
		"", "127.0.0.1:7700", "frontseat.example", "ftp://frontseat.example", "https://",
		"https://someone@frontseat.example", "https://someone:secret@frontseat.example",
		"https://frontseat.example/api", "https://frontseat.example?a=b", "https://frontseat.example/?", "https://frontseat.example#top",
	} {
		if _, err := New(address); err == nil {
			t.Errorf("%q: a Client was made", address)
		}
	}
	if _, err := New("http://frontseat.example", WithToken("a-token")); !errors.Is(err, bearer.ErrInClear) {
		t.Errorf("http to another machine with a token: %v, want bearer.ErrInClear", err)
	}
	if _, err := New("http://frontseat.example"); err != nil {
		t.Errorf("the control: http to another machine with no token: %v", err)
	}
}
