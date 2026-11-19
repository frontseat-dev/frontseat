package model

import "testing"

// A reference names something of the same project after a colon, or of
// another project as a label names it; anything else is no reference, or
// names nothing, and says so.
func TestAReferenceIsParsedAsWritten(t *testing.T) {
	for written, want := range map[string]Reference{
		":made/":                      {Path: "lab/chain", Rest: "made/", Written: ":made/"},
		":go.compile":                 {Path: "lab/chain", Rest: "go.compile", Written: ":go.compile"},
		":~/go/bin/":                  {Path: "lab/chain", Rest: "~/go/bin/", Written: ":~/go/bin/"},
		"//lab/other:made/":           {Path: "lab/other", Rest: "made/", Written: "//lab/other:made/"},
		"//lab:go.fetch/x.com/m@v1.0": {Path: "lab", Rest: "go.fetch/x.com/m@v1.0", Written: "//lab:go.fetch/x.com/m@v1.0"},
		"//:custom.tidy":              {Path: "", Rest: "custom.tidy", Written: "//:custom.tidy"},
	} {
		got, err := ParseReference("lab/chain", written)
		if err != nil || got != want {
			t.Errorf("ParseReference(%q) = %+v, %v; want %+v", written, got, err, want)
		}
	}
	for _, written := range []string{":", "//", "//:", "///lab:x", "//lab/chain", "//lab/chain:", "//lab//chain:x", "//lab/chain/:x", "made/", "=sources"} {
		if _, err := ParseReference("lab/chain", written); err == nil {
			t.Errorf("ParseReference(%q) was accepted", written)
		}
	}
}
