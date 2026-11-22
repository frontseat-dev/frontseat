package config_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

// shipping is a repository whose entities say what they ship for: one
// at its root, one in a directory, and one beneath that.
func shipping(root, tools, nested string) files {
	entity := func(name, platforms string) string {
		said := "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: " + name + "\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n"
		if platforms != "" {
			said += "  frontseat:\n    platforms: " + platforms + "\n"
		}
		return said
	}
	return files{
		config.File:                        "plugins:\n  tool:\n",
		"catalog-info.yaml":                entity("all", root),
		"tools/catalog-info.yaml":          entity("tools", tools),
		"tools/inner/catalog-info.yaml":    entity("inner", nested),
		"services/greet/catalog-info.yaml": entity("greet", ""),
	}
}

// What is in a directory ships for the platforms the entity nearest
// above it says, and for none where none says any: an entity that says
// its own is not given another's beside them.
func TestWhatADirectoryShipsForIsWhatTheNearestEntityAboveItSays(t *testing.T) {
	read, err := config.Read(context.Background(), "services", shipping("[linux/amd64]", "[linux/amd64, darwin/arm64]", "[linux/arm64]"))
	if err != nil {
		t.Fatal(err)
	}
	for directory, want := range map[string][]string{
		"":               {"linux/amd64"},
		"services/greet": {"linux/amd64"},
		"tools":          {"linux/amd64", "darwin/arm64"},
		"tools/cmd/x":    {"linux/amd64", "darwin/arm64"},
		"tools/inner":    {"linux/arm64"},
		"toolsmith":      {"linux/amd64"},
	} {
		if got := read.Platforms(directory); !slices.Equal(got, want) {
			t.Errorf("%q ships for %v, want %v", directory, got, want)
		}
	}
	// An entity that says none ships for none, whatever one above says.
	opted, err := config.Read(context.Background(), "services", shipping("[linux/amd64]", "[]", ""))
	if err != nil || len(opted.Platforms("tools")) != 0 || len(opted.Platforms("tools/inner")) != 0 || len(opted.Platforms("services/greet")) != 1 {
		t.Errorf("tools said none and ships for %v, what is beneath it for %v, and its neighbour for %v: %v", opted.Platforms("tools"), opted.Platforms("tools/inner"), opted.Platforms("services/greet"), err)
	}
	// Control: where no entity says any, nothing ships for any.
	none, err := config.Read(context.Background(), "services", shipping("", "", ""))
	if err != nil || len(none.Platforms("tools")) != 0 {
		t.Errorf("with nothing said, tools ships for %v, %v", none.Platforms("tools"), err)
	}
}

// A platform is an operating system and an architecture, written once.
func TestWhatIsNoPlatformIsRefusedWhereItIsSaid(t *testing.T) {
	for written, says := range map[string]string{
		"linux/amd64":                "spec.frontseat.platforms is a list of the platforms what is built ships for",
		"[linux]":                    "spec.frontseat.platforms holds what is no platform: one is written <os>/<arch>",
		"[Linux/amd64]":              "holds what is no platform",
		"[linux/amd64/v3]":           "holds what is no platform",
		"[linux/amd64, 7]":           "holds what is no platform",
		"[linux/amd64, linux/amd64]": "spec.frontseat.platforms names a platform twice",
	} {
		_, err := config.Read(context.Background(), "services", shipping(written, "", ""))
		if err == nil || !strings.Contains(err.Error(), says) || !strings.Contains(err.Error(), "services/catalog-info.yaml") {
			t.Errorf("%s: %v, want %q of the catalog file", written, err, says)
		}
	}
}

// What ships for a platform is what is built beneath a directory: a
// Component says it, once for its directory. A System is no directory,
// and a second saying for one directory is refused, naming the first.
func TestPlatformsAreSaidByAComponentOnceForItsDirectory(t *testing.T) {
	system := "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: kiosk\nspec:\n  owner: team\n  frontseat:\n    platforms: [linux/amd64]\n"
	component := func(name string) string {
		return "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: " + name + "\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    platforms: [linux/amd64]\n"
	}
	for name, c := range map[string]struct {
		catalog string
		says    string
	}{
		"a System":              {system, "spec.frontseat.platforms is said by a Component, for what is built beneath its directory"},
		"twice for a directory": {component("one") + "---\n" + component("two"), "spec.frontseat.platforms is said twice for one directory, here and in services/catalog-info.yaml"},
	} {
		_, err := config.Read(context.Background(), "services", files{config.File: "plugins:\n  tool:\n", "catalog-info.yaml": c.catalog})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want %q", name, err, c.says)
		}
	}
	if _, err := config.Read(context.Background(), "services", files{config.File: "plugins:\n  tool:\n", "catalog-info.yaml": component("one")}); err != nil {
		t.Errorf("the control, a Component alone, was refused: %v", err)
	}
}
