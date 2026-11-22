package cmd

import (
	"strings"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// A profile is named on the command line and asked of the daemon as the
// API says it: a name that is no profile is refused before anything is
// asked. The control is each profile by its name, and none, which asks
// for none and so for the daemon's default.
func TestAProfileIsNamedAndAskedAsTheAPISaysIt(t *testing.T) {
	g := &graphed{}
	if _, _, err := asking(t, g, "build", "gateway", "--profile", "staging"); err == nil || !strings.Contains(err.Error(), "the profiles are development and production") || len(g.built) != 0 {
		t.Fatalf("a profile nobody defined: %v, and asked for %v", err, g.built)
	}
	for _, c := range []struct {
		args []string
		want typev1.Profile
	}{
		{[]string{"build", "gateway"}, typev1.Profile_PROFILE_UNSPECIFIED},
		{[]string{"build", "gateway", "--profile", "development"}, typev1.Profile_PROFILE_DEVELOPMENT},
		{[]string{"build", "gateway", "--profile", "production"}, typev1.Profile_PROFILE_PRODUCTION},
	} {
		g := &graphed{}
		if _, _, err := asking(t, g, c.args...); err != nil || len(g.built) != 1 || g.built[0].GetProfile() != c.want {
			t.Errorf("%v: %v, asked for %v, want %v", c.args, err, g.built, c.want)
		}
	}
}
