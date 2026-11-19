package bearer_test

import (
	"net/http"
	"net/http/httptest"
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
