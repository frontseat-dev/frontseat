package testowner_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testowner"
)

// The owner's token is looked for where the command line reads it: a
// configuration directory that is no absolute path is ignored, as the
// XDG specification says and the command line does, and the one beneath
// the person's home is read. The control is an absolute one, which is
// read.
func TestARelativeConfigurationDirectoryIsIgnoredAsTheCommandLineIgnoresIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if got, want := testowner.File(t), filepath.Join(home, ".config", "frontseat", "local", "token"); got != want {
		t.Errorf("with a relative configuration directory the token is looked for at %s, want %s", got, want)
	}
	absolute := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", absolute)
	if got, want := testowner.File(t), filepath.Join(absolute, "frontseat", "local", "token"); got != want {
		t.Errorf("the control: with an absolute configuration directory the token is looked for at %s, want %s", got, want)
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
		response, err := testowner.Client(token, origin.URL).Get(at)
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
	response, err := testowner.Client(token, redirecting.URL).Get(redirecting.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || other != "" {
		t.Errorf("a redirect was followed (status %d), or the token went with it", response.StatusCode)
	}
}
