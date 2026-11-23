package model

import "testing"

// A reference names something of the same project after a colon, or of
// another project of the same repository from the repository's root,
// beneath the repository's name in the workspace; anything else is no
// reference, or names nothing, or names something outside the
// repository, and says so.
func TestAReferenceIsParsedAsWritten(t *testing.T) {
	for written, want := range map[string]Reference{
		":made/":                   {Path: "lab/chain", Rest: "made/", Written: ":made/"},
		":go.compile":              {Path: "lab/chain", Rest: "go.compile", Written: ":go.compile"},
		":~/go/bin/":               {Path: "lab/chain", Rest: "~/go/bin/", Written: ":~/go/bin/"},
		"//other:made/":            {Path: "lab/other", Rest: "made/", Written: "//other:made/"},
		"//:go.fetch/x.com/m@v1.0": {Path: "lab", Rest: "go.fetch/x.com/m@v1.0", Written: "//:go.fetch/x.com/m@v1.0"},
		"//chain/x.y:custom.tidy":  {Path: "lab/chain/x.y", Rest: "custom.tidy", Written: "//chain/x.y:custom.tidy"},
	} {
		got, err := ParseReference("lab", "chain", written)
		if err != nil || got != want {
			t.Errorf("ParseReference(%q) = %+v, %v; want %+v", written, got, err, want)
		}
	}
	for _, written := range []string{":", "//", "//:", "///lab:x", "//lab/chain", "//lab/chain:", "//lab//chain:x", "//lab/chain/:x", "//../other:x", "//..:x", "//.:x", "//chain/../../other:x", "//chain/./x:y", "made/", "=sources"} {
		if _, err := ParseReference("lab", "chain", written); err == nil {
			t.Errorf("ParseReference(%q) was accepted", written)
		}
	}
}

// The control: a repository at the workspace's top reads every
// reference by the same path, without a name before it.
func TestAReferenceOfALoneRepositoryIsItsPathAsWritten(t *testing.T) {
	for written, want := range map[string]string{":made/": "chain", "//other:made/": "other", "//:custom.tidy": ""} {
		if got, err := ParseReference("", "chain", written); err != nil || got.Path != want {
			t.Errorf("ParseReference(%q) = %+v, %v; want the project %q", written, got, err, want)
		}
	}
}
