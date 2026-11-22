package config_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

// released is a repository whose component says how it is released.
func released(kind, lines string) files {
	return files{
		config.File:               "plugins:\n  tool:\n",
		"greet/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: " + kind + "\nmetadata:\n  name: Greet\nspec:\n  owner: team\n  frontseat:\n    release:\n" + lines,
	}
}

func TestWhatAnEntitySaysOfHowItIsReleasedIsRead(t *testing.T) {
	read, err := config.Read(context.Background(), "services", released("Component",
		"      grouping: independent\n      tag: '{unit}/v{version}'\n      initial-version: 0.77.0\n      versioning: release:conventional-commits\n      branching: release:release-flow\n      prerelease:\n        format: '{branch}-{sha}'\n        tagged: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	releases := read.Releases()
	if len(releases) != 1 {
		t.Fatalf("it says %d entities are released, want one", len(releases))
	}
	said := releases[0]
	if said.File != "services/greet/catalog-info.yaml" || said.Entity.String() != "component:default/greet" {
		t.Errorf("it is said by %s in %s", said.Entity, said.File)
	}
	want := map[string]string{"grouping": "independent", "tag": "{unit}/v{version}", "initial-version": "0.77.0", "versioning": "release:conventional-commits", "branching": "release:release-flow", "prerelease.format": "{branch}-{sha}", "prerelease.tagged": "true"}
	if len(said.Settings) != len(want) {
		t.Errorf("settings are %v, want %v", said.Settings, want)
	}
	for key, value := range want {
		if said.Settings[key] != value {
			t.Errorf("%s is %q, want %q", key, said.Settings[key], value)
		}
	}
	// An entity that says nothing of it says nothing.
	read, err = config.Read(context.Background(), "services", files{config.File: "plugins:\n  tool:\n", "greet/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: greet\nspec:\n  owner: team\n"})
	if err != nil || len(read.Releases()) != 0 {
		t.Errorf("an entity that says nothing is released as %v, %v", read.Releases(), err)
	}
}

// What is said is checked where it is said, and a mistake names the
// file, the setting and the line.
func TestAMistakeInHowAnEntityIsReleasedNamesTheSetting(t *testing.T) {
	if _, err := config.Read(context.Background(), "services", released("System", "      initial-version: 1.0.0\n")); err != nil {
		t.Fatalf("a sound setting was refused, so this test proves nothing: %v", err)
	}
	for lines, says := range map[string]string{
		"      channels: beta\n":                                       "services/greet/catalog-info.yaml: spec.frontseat.release.channels is no release setting",
		"      dependencies: {minor: patch}\n":                         "services/greet/catalog-info.yaml: spec.frontseat.release.dependencies is no release setting",
		"      grouping: locked\n":                                     "spec.frontseat.release.grouping must be synced or independent",
		"      grouping: Synced\n":                                     "spec.frontseat.release.grouping must be synced or independent",
		"      tag: '{unit}'\n":                                        "spec.frontseat.release.tag must hold {version} once",
		"      tag: '{version}-{version}'\n":                           "must hold {version} once",
		"      tag: '{name}/v{version}'\n":                             "spec.frontseat.release.tag names something other than {unit} and {version}",
		"      initial-version: '1.0'\n":                               "spec.frontseat.release.initial-version is no semantic version",
		"      initial-version: 1.0.0-rc.1\n":                          "is a major, a minor and a patch, and nothing after them",
		"      initial-version: 1.0.0+build\n":                         "is a major, a minor and a patch, and nothing after them",
		"      initial-version: v1.0.0\n":                              "is no semantic version",
		"      initial-version: 1.0\n":                                 "spec.frontseat.release.initial-version is text",
		"      initial-version: [1.0.0]\n":                             "spec.frontseat.release.initial-version is text",
		"      versioning: conventional-commits\n":                     "spec.frontseat.release.versioning must be <plugin>:<name>",
		"      branching: 'release:'\n":                                "spec.frontseat.release.branching must be <plugin>:<name>",
		"      branching: 'Release:flow'\n":                            "must be <plugin>:<name>",
		"      prerelease: {format: '{branch}-{date}'}\n":              "spec.frontseat.release.prerelease.format names what is not filled: it takes {branch}, {timestamp}, {epoch}, {sha} (line",
		"      prerelease: {format: '{branch}..{sha}'}\n":              "spec.frontseat.release.prerelease.format spells what is no prerelease of a semantic version",
		"      prerelease: {format: '{branch}+{sha}'}\n":               "spells what is no prerelease of a semantic version",
		"      prerelease: {format: '0{epoch}'}\n":                     "spells what is no prerelease of a semantic version",
		"      prerelease: {format: ''}\n":                             "spells what is no prerelease of a semantic version",
		"      prerelease: {format: '{branch}.{sha}'}\n":               "spells what is no prerelease of a semantic version, whose parts are letters, digits and dashes joined by dots, a part of digits alone beginning with no zero: a branch or a commit written in digits is a number",
		"      prerelease: {format: '{branch}.{timestamp}'}\n":         "spells what is no prerelease of a semantic version",
		"      prerelease: {tagged: 'yes'}\n":                          "spec.frontseat.release.prerelease.tagged is true or false",
		"      prerelease: {tagged: 'true'}\n":                         "spec.frontseat.release.prerelease.tagged is true or false",
		"      prerelease: {tagged: 1}\n":                              "spec.frontseat.release.prerelease.tagged is true or false",
		"      initial-version: 1.0.0\n      initial-version: 2.0.0\n": "initial-version",
		"      initial: 1.0.0\n":                                       "spec.frontseat.release.initial is no release setting; it takes branching, grouping, initial-version, prerelease, tag, versioning",
		"      prerelease-tag: true\n":                                 "spec.frontseat.release.prerelease-tag is no release setting",
		"      prerelease: '{branch}'\n":                               "spec.frontseat.release.prerelease is a map of format and tagged",
		"      prerelease: {form: '{branch}'}\n":                       "spec.frontseat.release.prerelease.form is no prerelease setting; it takes format and tagged",
	} {
		_, err := config.Read(context.Background(), "services", released("Component", lines))
		if err == nil || !strings.Contains(err.Error(), says) || !strings.Contains(err.Error(), "services/greet/catalog-info.yaml") {
			t.Errorf("%q: want a mistake saying %q, got %v", lines, says, err)
		}
	}
	if _, err := config.Read(context.Background(), "services", files{config.File: "plugins:\n  tool:\n", "greet/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: greet\nspec:\n  frontseat:\n    release: synced\n"}); err == nil || !strings.Contains(err.Error(), "is a map of settings") {
		t.Errorf("a release that is no map: %v", err)
	}
}

// An entity is named in the grammar every reference has: a name that
// only reads as ASCII once folded is refused, as it is everywhere.
func TestWhoSaysHowItIsReleasedIsNamedAsEveryEntityIs(t *testing.T) {
	file := func(name, namespace string) files {
		return files{config.File: "plugins:\n  tool:\n", "greet/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\nspec:\n  frontseat:\n    release:\n      initial-version: 1.0.0\n"}
	}
	read, err := config.Read(context.Background(), "services", file("Greet", "Edge"))
	if err != nil || read.Releases()[0].Entity.String() != "component:edge/greet" {
		t.Fatalf("an entity written in upper case is %v, %v", read.Releases(), err)
	}
	for _, c := range [][2]string{{"\u212Aelvin", "default"}, {"greet", "De\u212A"}, {"a b", "default"}} {
		if _, err := config.Read(context.Background(), "services", file(c[0], c[1])); err == nil || !strings.Contains(err.Error(), "an entity whose reference is none: a kind, namespace and name are") || strings.Contains(err.Error(), "component:") {
			t.Errorf("%q in %q says how it is released: %v", c[0], c[1], err)
		}
	}
}

// What is refused of how an entity is released is not repeated: a
// refusal says the setting, its line and what the setting takes.
func TestARefusedReleaseSettingIsNotRepeated(t *testing.T) {
	for _, lines := range []string{
		"      grouping: secret\n",
		"      tag: 'secret-{version}-{secret}'\n",
		"      initial-version: secret\n",
		"      initial-version: 1.0.0-secret\n",
		"      versioning: secret\n",
		"      branching: secret\n",
		"      prerelease: {format: 'secret-{secret}'}\n",
		"      prerelease: {format: 'secret..{sha}'}\n",
	} {
		_, err := config.Read(context.Background(), "services", released("Component", lines))
		if err == nil || !strings.Contains(err.Error(), "spec.frontseat.release.") || !strings.Contains(err.Error(), "(line 9)") || strings.Contains(err.Error(), "secret") {
			t.Errorf("%q: want it refused by its setting and line, and not repeated, got %v", lines, err)
		}
	}
	if _, err := config.Read(context.Background(), "services", released("Component", "      grouping: independent\n      tag: 'secret-{version}'\n      initial-version: 1.0.0\n      prerelease: {format: 'secret-{sha}'}\n")); err != nil {
		t.Errorf("control, settings that are sound: %v", err)
	}
}

// How an entity is released is said by what is released: a Domain, a
// System or a Component. On any other kind it would be read and never
// used, and is refused.
func TestHowAnEntityIsReleasedIsSaidByWhatIsReleased(t *testing.T) {
	for _, kind := range []string{"Domain", "System", "Component"} {
		if _, err := config.Read(context.Background(), "services", released(kind, "      initial-version: 1.0.0\n")); err != nil {
			t.Errorf("control, a %s says how it is released: %v", kind, err)
		}
	}
	for _, kind := range []string{"API", "Resource", "Location", "Group"} {
		_, err := config.Read(context.Background(), "services", released(kind, "      initial-version: 1.0.0\n"))
		if err == nil || !strings.Contains(err.Error(), "spec.frontseat.release is said by a Domain, a System or a Component (line 9)") {
			t.Errorf("a %s says how it is released: %v", kind, err)
		}
	}
}
