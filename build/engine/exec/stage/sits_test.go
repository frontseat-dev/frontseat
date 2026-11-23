package stage

import "testing"

func TestAnOutputSitsInItsProjectOrInTheActionsHome(t *testing.T) {
	cases := []struct {
		output, at, asked string
	}{
		{"made", "lab/tool/made", "made"},
		{"made/", "lab/tool/made", "made"},
		{"made/deep", "lab/tool/made/deep", "made/deep"},
		{"~/go/pkg/mod", ".frontseat/home/go/pkg/mod", "../../.frontseat/home/go/pkg/mod"},
		{"~/go/pkg/mod/", ".frontseat/home/go/pkg/mod", "../../.frontseat/home/go/pkg/mod"},
	}
	for _, c := range cases {
		at, asked, err := Sits("lab", "tool", "", c.output)
		if err != nil || at != c.at || asked != c.asked {
			t.Errorf("%s sits at %q and is asked for as %q (%v); want %q and %q", c.output, at, asked, err, c.at, c.asked)
		}
	}
	// A command that runs beneath its project leaves an output where one
	// that runs in the project does, and asks the grid for it from where
	// it runs.
	for _, c := range []struct{ output, at, asked string }{
		{"made", "lab/tool/made", "../../made"},
		{"cmd/serve/made", "lab/tool/cmd/serve/made", "made"},
		{"~/go/bin/", ".frontseat/home/go/bin", "../../../../.frontseat/home/go/bin"},
	} {
		at, asked, err := Sits("lab", "tool", "cmd/serve", c.output)
		if err != nil || at != c.at || asked != c.asked {
			t.Errorf("run in cmd/serve, %s sits at %q and is asked for as %q (%v); want %q and %q", c.output, at, asked, err, c.at, c.asked)
		}
	}
}
