package pins_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/pins"
)

const lock = `
[[tools.go]]
version = "1.27.3"
backend = "core:go"

[tools.go."platforms.linux-arm64"]
checksum = "sha256:a06e"
url = "https://dl.google.com/go/go1.27.3.linux-arm64.tar.gz"

[tools.go."platforms.linux-x64"]
url = "https://dl.google.com/go/go1.27.3.linux-amd64.tar.gz"

[[tools.clang]]
version = "21.1.0"
backend = "conda:clang"

[tools.clang."platforms.linux-arm64"]
checksum = "sha256:10a3"
conda_deps = ["libzlib-1.3.2", "zstd-1.5.7"]

[tools.clang."platforms.linux-x64"]
checksum = "sha256:77f0"
conda_deps = ["libzlib-1.3.2", "zstd-1.5.7"]

[[tools."http:rust"]]
version = "1.99.1"
backend = "http:rust"

[tools."http:rust"."platforms.linux-arm64"]
checksum = "sha256:a48f"

[[tools."http:zig"]]
version = "0.15.0"

[[tools."conda:zig"]]
version = "0.15.0"

[conda-packages.linux-arm64."libzlib-1.3.2"]
checksum = "sha256:76ef"

[conda-packages.linux-arm64."zstd-1.5.7"]
checksum = "sha256:9d15"

[conda-packages.linux-x64."libzlib-1.3.2"]
checksum = "sha256:1c2b"
`

func TestAToolIsLockedWhereTheLockHoldsItsChecksumForThePlatform(t *testing.T) {
	cases := []struct {
		tool, platform, refused string
	}{
		{"go", "linux-arm64", ""},
		{"go", "linux-x64", "holds no checksum of go for linux-x64"},
		// A tool is found by its name, whoever provides it.
		{"rust", "linux-arm64", ""},
		{"rust", "linux-x64", "holds no checksum of rust for linux-x64"},
		{"zig", "linux-arm64", "pins zig twice, as conda:zig and as http:zig"},
		{"node", "linux-arm64", `pins no tool "node"`},
		// A tool that is made of packages is locked where each of them is.
		{"clang", "linux-arm64", ""},
		{"clang", "linux-x64", "holds no checksum of zstd-1.5.7, which clang is made of, for linux-x64"},
	}
	for _, c := range cases {
		err := pins.Locked([]byte(lock), c.tool, c.platform)
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%s on %s: %v", c.tool, c.platform, err)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("%s on %s: err = %v, want one that says %q", c.tool, c.platform, err, c.refused)
		}
	}
}

func TestAToolIsPinnedUnderItsNameOrUnderWhatProvidesIt(t *testing.T) {
	cases := []struct{ tool, named string }{
		{"go", "go"},
		{"rust", "http:rust"},
		{"clang", "clang"},
	}
	for _, c := range cases {
		if got, err := pins.Named([]byte(lock), c.tool); err != nil || got != c.named {
			t.Errorf("%s is pinned as %q (%v), want %q", c.tool, got, err, c.named)
		}
	}
}

func TestAToolIsPinnedByAVersionOrByATableThatHoldsOne(t *testing.T) {
	got, err := pins.Versions([]byte("[tools]\ngo = \"1.27.3\"\n\"http:rust\" = { version = \"1.99.1\", url = \"https://example.com/rust.tar.xz\" }\n\"github:buildbarn/bb-clientd\" = \"20261008T163232Z-ffb6815\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	for tool, want := range map[string]string{"go": "1.27.3", "rust": "1.99.1", "buildbarn/bb-clientd": "20261008T163232Z-ffb6815"} {
		if got[tool] != want {
			t.Errorf("%s is pinned at %q, want %q", tool, got[tool], want)
		}
	}
}

func TestAFileThatPinsNothingPinsNothing(t *testing.T) {
	got, err := pins.Versions([]byte("[env]\nA = \"b\"\n"))
	if err != nil || len(got) != 0 {
		t.Fatalf("Versions = %v, %v", got, err)
	}
}
