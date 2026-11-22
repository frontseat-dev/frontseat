package config_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

// What every Component has unless it says otherwise is said once, in
// frontseat.yaml's defaults, under the keys an entity says it by: its
// platforms, what its tests are given, and how it is released. An
// entity that says one overrides it.
func TestARepositorySaysWhatEveryComponentHasUnlessItSaysOtherwise(t *testing.T) {
	read, err := config.Read(t.Context(), "", files{
		config.File: `plugins: {backstage: {}}
defaults:
  platforms: [linux/amd64, linux/arm64]
  test:
    tools: [buf]
    environment: {FRONTSEAT_TEST_STACK: none}
  release:
    tag: "{unit}@{version}"
    initial-version: 1.0.0
    prerelease: {tagged: true}
`,
		"ctl/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: ctl}\nspec:\n  frontseat:\n    platforms: [darwin/arm64]\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	defaults := read.Defaults()
	if defaults.File != config.File || !slices.Equal(defaults.Test.Tools, []string{"buf"}) || !maps.Equal(defaults.Test.Environment, map[string]string{"FRONTSEAT_TEST_STACK": "none"}) {
		t.Errorf("test defaults read as %+v", defaults)
	}
	if want := map[string]string{config.ReleaseTag: "{unit}@{version}", config.ReleaseInitialVersion: "1.0.0", config.ReleasePrereleaseTagged: "true"}; !maps.Equal(defaults.Release, want) {
		t.Errorf("release defaults read as %v, want %v", defaults.Release, want)
	}
	if got := read.Platforms("server/internal"); !slices.Equal(got, []string{"linux/amd64", "linux/arm64"}) {
		t.Errorf("a directory no entity says platforms of ships for %v, want the default", got)
	}
	if got := read.Platforms("ctl/cmd"); !slices.Equal(got, []string{"darwin/arm64"}) {
		t.Errorf("a directory its entity says platforms of ships for %v, want the entity's", got)
	}
	// Without defaults, nothing is defaulted.
	bare, err := config.Read(t.Context(), "", files{config.File: "plugins: {}\n"})
	if err != nil || bare.Platforms("x") != nil || len(bare.Defaults().Release) != 0 || bare.Defaults().Test.Tools != nil {
		t.Errorf("a repository with no defaults: %+v, %v", bare, err)
	}
}

// Defaults take what is safe to default, each as an entity writes it,
// and refuse the rest by the keys taken.
func TestADefaultIsWhatAnEntityMaySayAndIsSafeToDefault(t *testing.T) {
	for written, says := range map[string]string{
		"defaults: {actions: {}}\n":                        "line 2: defaults takes no key actions; it takes platforms, release and test",
		"defaults: {test: {inputs: [x]}}\n":                "line 2: defaults.test takes no key inputs; it takes environment and tools",
		"defaults: {release: {grouping: synced}}\n":        "defaults.release.grouping is no default: whether a group is synced is each entity's own",
		"defaults: {platforms: [Linux]}\n":                 "defaults.platforms holds what is no platform",
		"defaults: {release: {tag: \"{unit}\"}}\n":         "defaults.release.tag must hold {version} once",
		"defaults: {test: {environment: {BAD-NAME: x}}}\n": "defaults.test.environment",
		"defaults: []\n":                                   "line 2: defaults is a map of platforms, release and test",
	} {
		if _, err := config.Read(t.Context(), "", files{config.File: "plugins: {}\n" + written}); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%q: want %q, got %v", written, says, err)
		}
	}
	if _, err := config.Read(t.Context(), "", files{config.File: "plugins: {}\ndefaults: {platforms: [linux/amd64], test: {tools: [buf]}, release: {tag: \"{unit}@{version}\"}}\n"}); err != nil {
		t.Errorf("control: %v", err)
	}
}
