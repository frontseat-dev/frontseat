package bearer_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
)

// A token goes to its origin alone, and a redirect to another is the
// answer, not followed: the other is never asked. The control is the
// origin, asked directly and presented the token.
func TestATokenGoesToItsOriginAloneAndNoRedirectIsFollowed(t *testing.T) {
	seen := map[string]string{}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen["other"+r.URL.Path] = r.Header.Get("Authorization")
	}))
	t.Cleanup(other.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen["origin"+r.URL.Path] = r.Header.Get("Authorization")
		if r.URL.Path == "/away" {
			http.Redirect(w, r, other.URL+"/led", http.StatusFound)
		}
	}))
	t.Cleanup(origin.Close)
	client := bearer.Client(nil, "a-token", origin.URL)
	for _, at := range []string{origin.URL + "/here", origin.URL + "/away", other.URL + "/there"} {
		response, err := client.Get(at)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if at == origin.URL+"/away" && response.StatusCode != http.StatusFound {
			t.Errorf("a redirect was answered %d, want the redirect itself", response.StatusCode)
		}
	}
	want := map[string]string{"origin/here": "Bearer a-token", "origin/away": "Bearer a-token", "other/there": ""}
	for at, presented := range want {
		if got, asked := seen[at]; !asked || got != presented {
			t.Errorf("%s was presented %q (asked %v), want %q", at, got, asked, presented)
		}
	}
	if _, led := seen["other/led"]; led {
		t.Error("the redirect was followed")
	}
}

// asked is a transport that records what it is asked and answers each
// request itself: nothing leaves the test.
type asked struct{ presented []string }

func (a *asked) RoundTrip(r *http.Request) (*http.Response, error) {
	a.presented = append(a.presented, r.Header.Get("Authorization"))
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: r}, nil
}

// A token is never sent in clear to another machine: a request that
// would carry it over http to a host that is not this machine is refused
// before anything is sent, by an error that names neither. The controls
// are the same request over TLS, and to this machine in clear, by its
// name and by its loopback addresses, each presented the token.
func TestATokenIsNotPresentedInClearToAnotherMachine(t *testing.T) {
	for _, origin := range []string{"http://frontseat.example", "http://frontseat.example:7700", "http://192.0.2.1:7700", "http://localhost.example"} {
		over := &asked{}
		response, err := bearer.Client(&http.Client{Transport: over}, "a-token", origin).Get(origin + "/here")
		if err == nil {
			response.Body.Close()
		}
		if !errors.Is(err, bearer.ErrInClear) {
			t.Errorf("a request in clear to %s: %v, want it refused", origin, err)
		}
		if len(over.presented) != 0 {
			t.Errorf("%s was asked, presented %q", origin, over.presented)
		}
		if err != nil && (strings.Contains(err.Error(), "a-token") || strings.Contains(bearer.ErrInClear.Error(), "example")) {
			t.Errorf("the refusal repeats what it refused: %v", err)
		}
	}
	for _, origin := range []string{"https://frontseat.example", "http://127.0.0.1:7700", "http://localhost:7700", "http://[::1]:7700"} {
		over := &asked{}
		response, err := bearer.Client(&http.Client{Transport: over}, "a-token", origin).Get(origin + "/here")
		if err != nil {
			t.Errorf("the control: a request to %s: %v", origin, err)
			continue
		}
		response.Body.Close()
		if len(over.presented) != 1 || over.presented[0] != "Bearer a-token" {
			t.Errorf("the control: %s was presented %q, want the token", origin, over.presented)
		}
	}
}
