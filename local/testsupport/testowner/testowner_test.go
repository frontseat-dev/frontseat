package testowner

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The owner's token is the local stack's where the command line is
// told of no other control plane, and refused where it is told of
// another, since the token beside that is for it. The control is a
// server file that names the local stack's own.
func TestATokenForAnotherControlPlaneIsRefused(t *testing.T) {
	const token = "a-token-of-this-test"
	written := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(written, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for server, refused := range map[string]bool{"": false, Stack: false, "https://frontseat.example": true} {
		t.Setenv("XDG_CONFIG_HOME", LayOut(t, written, server))
		got, err := held()
		if refused && (err == nil || got != "" || strings.Contains(err.Error(), token)) {
			t.Errorf("with the server %q the token was read (%v), or the refusal said it", server, err)
		}
		if !refused && (err != nil || got != token) {
			t.Errorf("the control: with the server %q the token was not read: %v", server, err)
		}
	}
}

// The owner's token is presented to the origin it is for and to no
// other, and a redirect is not followed with it. The control is the
// origin itself, which is presented it.
func TestTheOwnersTokenIsPresentedToItsOriginAlone(t *testing.T) {
	presented := func(seen *string) http.Handler {
		return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { *seen = r.Header.Get("Authorization") })
	}
	var own, other string
	origin := httptest.NewServer(presented(&own))
	defer origin.Close()
	elsewhere := httptest.NewServer(presented(&other))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusTemporaryRedirect))
	defer redirecting.Close()

	const token = "a-token-of-this-test"
	for _, at := range []string{origin.URL, elsewhere.URL} {
		response, err := Client(token, origin.URL).Get(at)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if own != "Bearer "+token {
		t.Errorf("the control: the origin was presented %q", own)
	}
	if other != "" {
		t.Error("another origin was presented the token")
	}
	response, err := Client(token, redirecting.URL).Get(redirecting.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || other != "" {
		t.Errorf("a redirect was followed (status %d), or the token went with it", response.StatusCode)
	}
}
