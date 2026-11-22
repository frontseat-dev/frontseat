package config_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
)

// files is a repository of the files a test gives it.
type files = pluginhosttest.Files

// declared are the options of a plugin that takes one of each kind.
var declared = []*pluginv1.Option{
	{Name: "strict", Kind: pluginv1.Option_KIND_BOOL, Default: &typev1.OptionValue{Value: &typev1.OptionValue_BoolValue{BoolValue: false}}, EntitySettable: true},
	{Name: "jobs", Kind: pluginv1.Option_KIND_INT, Default: &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: 1}}, EntitySettable: true},
	{Name: "flavour", Kind: pluginv1.Option_KIND_STRING, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: "plain"}}, EntitySettable: true},
}

// set returns the value an option holds at the repository's root, where
// frontseat.yaml sets the plugin's options to what is given.
func set(t *testing.T, options, option string) (*typev1.OptionValue, error) {
	t.Helper()
	read, err := config.Read(context.Background(), "repo", files{config.File: "plugins:\n  tool:\n" + options})
	if err != nil {
		return nil, err
	}
	held, err := read.Options("tool", declared, "")
	if err != nil {
		return nil, err
	}
	for _, each := range held {
		if each.Name == option {
			return each.Value, nil
		}
	}
	t.Fatalf("the plugin was given no option %s", option)
	return nil, nil
}

// YAML writes true and false in three ways each, and a switch that is
// set is set whichever was used.
func TestASwitchIsReadHoweverYAMLWritesIt(t *testing.T) {
	for written, want := range map[string]bool{"true": true, "True": true, "TRUE": true, "false": false, "False": false, "FALSE": false} {
		got, err := set(t, "    strict: "+written+"\n", "strict")
		if err != nil {
			t.Errorf("strict: %s: %v", written, err)
			continue
		}
		if got.GetBoolValue() != want {
			t.Errorf("strict: %s is read as %v, want %v", written, got.GetBoolValue(), want)
		}
	}
	if _, err := set(t, "    strict: \"true\"\n", "strict"); err == nil {
		t.Error("text was taken for a switch")
	}
}

// Every signed 64-bit integer reaches a plugin exactly; out-of-range and
// non-integer values are refused with their source and option named.
func TestANumberIsExactOrRefused(t *testing.T) {
	for written, want := range map[string]int64{"8": 8, "-3": -3, "9007199254740992": 9007199254740992, "-9007199254740992": -9007199254740992, "9007199254740993": 9007199254740993, "-9007199254740993": -9007199254740993, "9223372036854775807": 9223372036854775807, "-9223372036854775808": -9223372036854775808} {
		got, err := set(t, "    jobs: "+written+"\n", "jobs")
		if err != nil || got.GetIntValue() != want {
			t.Errorf("jobs: %s is read as %v (%v), want %v", written, got.GetIntValue(), err, want)
		}
	}
	for _, written := range []string{"9223372036854775808", "-9223372036854775809", "1.5", "many"} {
		if got, err := set(t, "    jobs: "+written+"\n", "jobs"); err == nil {
			t.Errorf("jobs: %s was taken, as %v", written, got.GetIntValue())
		} else if !strings.Contains(err.Error(), "repo/"+config.File) || !strings.Contains(err.Error(), "jobs") {
			t.Errorf("jobs: %s is refused with %q, want the file and the option named", written, err)
		}
	}
}

// What an entity says of how it is built is under spec.frontseat, and a
// key there that frontseat does not read is a mistake: a misspelt one
// would otherwise set nothing and say nothing.
func TestAKeyFrontseatDoesNotReadIsAMistake(t *testing.T) {
	entity := func(under string) files {
		return files{
			config.File:                 "plugins:\n  tool:\n",
			"service/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: service\nspec:\n  type: service\n  frontseat:\n" + under,
		}
	}

	_, err := config.Read(context.Background(), "repo", entity("    plugin:\n      tool:\n        strict: true\n"))

	if err == nil || !strings.Contains(err.Error(), "repo/service/catalog-info.yaml") || !strings.Contains(err.Error(), "plugin") {
		t.Errorf("err = %v, want the file and the key it does not read named", err)
	}
	for name, under := range map[string]string{
		"its plugins' options": "    plugins:\n      tool:\n        strict: true\n",
		"nothing":              "    {}\n",
	} {
		if _, err := config.Read(context.Background(), "repo", entity(under)); err != nil {
			t.Errorf("an entity that sets %s was refused: %v", name, err)
		}
	}
	// What is beside it is Backstage's, and is not frontseat's to read.
	if _, err := config.Read(context.Background(), "repo", files{
		config.File:                 "plugins:\n  tool:\n",
		"service/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: service\nspec:\n  type: service\n  lifecycle: production\n  anything: else\n",
	}); err != nil {
		t.Errorf("an entity with keys of Backstage's own was refused: %v", err)
	}
}

func TestCatalogAliasesDoNotWeakenExplicitFrontseatConfiguration(t *testing.T) {
	base := files{config.File: "plugins:\n  tool: {}\n"}
	for _, body := range []string{
		"metadata: &metadata {name: service}\nspec: {owner: team}\nx-copy: *metadata\n",
		"base: &base {owner: team}\nspec: {<<: *base, frontseat: {plugins: {tool: {strict: true}}}}\n",
	} {
		base["catalog-info.yaml"] = "kind: Component\n" + body
		if _, err := config.Read(context.Background(), "repo", base); err != nil {
			t.Fatalf("valid catalog extension/alias refused: %v", err)
		}
	}
	for _, body := range []string{
		"base: &base {plugins: {tool: {strict: true}}}\nspec: {frontseat: *base}\n",
		"base: &base {frontseat: {plugins: {tool: {strict: true}}}}\nspec: *base\n",
		"base: &base {frontseat: {plugins: {tool: {strict: true}}}}\nspec: {<<: *base}\n",
		"spec: {frontseat: true}\n",
	} {
		base["catalog-info.yaml"] = "kind: Component\n" + body
		if _, err := config.Read(context.Background(), "repo", base); err == nil {
			t.Fatalf("accepted implicit or malformed Frontseat configuration: %s", body)
		}
	}
	base = files{config.File: "base: &base {}\nplugins: *base\n"}
	if _, err := config.Read(context.Background(), "repo", base); err == nil {
		t.Fatal("accepted an aliased repository recipe")
	}
}

// An action of an entity's own takes the keys it is read by: any other
// is refused where it is written, with what an action takes. The
// control is a key it takes.
func TestAnActionRefusesAKeyItDoesNotTake(t *testing.T) {
	read := func(key string) (*config.Repository, error) {
		return config.Read(t.Context(), "", files{
			config.File:         "plugins: {}\n",
			"catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    actions:\n      serve:\n        " + key + ": true\n        tools: [caddy]\n        command: [caddy, run]\n",
		})
	}
	if _, err := read("serves"); err == nil || !strings.Contains(err.Error(), "line 8: an action takes no key serves; it takes check, command,") {
		t.Errorf("an action with a key it does not take: %v", err)
	}
	if got, err := read("program"); err != nil || len(got.Actions()) != 1 || !got.Actions()[0].Program {
		t.Errorf("control, program: true: %v", err)
	}
}

// A Component sets a plugin's options for its directory, once: a second
// setting for the directory is refused, and so is one by an entity that
// is no directory. The control is one Component
// that sets them.
func TestAPluginsOptionsAreSetOnceForADirectoryByAComponent(t *testing.T) {
	document := func(kind, name, strict string) string {
		return "apiVersion: backstage.io/v1alpha1\nkind: " + kind + "\nmetadata: {name: " + name + "}\nspec:\n  frontseat:\n    plugins: {tool: {strict: " + strict + "}}\n"
	}
	read := func(catalog string) (*config.Repository, error) {
		return config.Read(t.Context(), "repo", files{config.File: "plugins:\n  tool:\n", "service/catalog-info.yaml": catalog})
	}
	got, err := read(document("Component", "service", "true"))
	if err != nil {
		t.Fatalf("control, one Component that sets an option: %v", err)
	}
	if held, err := got.Options("tool", declared, "service/cmd"); err != nil || !held[2].Value.GetBoolValue() || held[2].Origin != "repo/service/catalog-info.yaml" {
		t.Fatalf("control, the option beneath its directory: %v, %v", held, err)
	}
	if _, err := read(document("Component", "service", "true") + "---\n" + document("Component", "other", "false")); err == nil || !strings.Contains(err.Error(), "line 13: spec.frontseat.plugins is said twice for one directory, here and in repo/service/catalog-info.yaml") {
		t.Errorf("two settings for one directory: %v", err)
	}
	for _, kind := range []string{"System", "Domain", "Resource", "API"} {
		if _, err := read(document(kind, "service", "true")); err == nil || !strings.Contains(err.Error(), "line 6: spec.frontseat.plugins is said by a Component") {
			t.Errorf("a %s that sets an option: %v", kind, err)
		}
	}
}

// Of two options a plugin does not take, the first by name is the one
// refused, every time the file is read.
func TestWhichOptionIsRefusedDoesNotChangeBetweenReads(t *testing.T) {
	for range 50 {
		read, err := config.Read(t.Context(), "repo", files{config.File: "plugins:\n  tool:\n    zeta: 1\n    alpha: 2\n    mid: 3\n"})
		if err != nil {
			t.Fatal(err)
		}
		if err := read.Check("tool", declared); err == nil || !strings.Contains(err.Error(), "line 4: the tool plugin takes no option alpha") {
			t.Fatalf("the option refused: %v", err)
		}
	}
}

// A name that is no action's name and a platform said twice are refused
// by what a name and a list of platforms are, and are not repeated.
func TestARefusedNameIsNotRepeated(t *testing.T) {
	for written, says := range map[string]string{
		"    actions: {Secret_Name: {command: [x]}}\n": "an action's name is lower-case letters, digits and dashes",
		"    platforms: [secret/arch, secret/arch]\n":  "spec.frontseat.platforms names a platform twice",
	} {
		_, err := config.Read(t.Context(), "repo", files{config.File: "plugins: {}\n", "catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n" + written})
		if err == nil || !strings.Contains(err.Error(), says) || strings.Contains(strings.ToLower(err.Error()), "secret") {
			t.Errorf("%q: %v, want %q and nothing of what was written", written, err, says)
		}
	}
	if _, err := config.Read(t.Context(), "repo", files{config.File: "plugins: {}\n", "catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    actions: {lint: {command: [x]}}\n    platforms: [linux/amd64, linux/arm64]\n"}); err != nil {
		t.Errorf("control, a name and two platforms: %v", err)
	}
}

// An action of an entity's own says it publishes, and which variables
// of where the daemon runs it is given; one that does not publish and
// names some is refused. The control is one that publishes.
func TestAnActionOfAnEntitysOwnSaysItPublishesAndWhatItIsGiven(t *testing.T) {
	read := func(keys string) (*config.Repository, error) {
		return config.Read(t.Context(), "", files{
			config.File:         "plugins: {}\n",
			"catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    actions:\n      release:\n        tools: [gh]\n        command: [gh, release, create]\n" + keys,
		})
	}
	got, err := read("        publish: true\n        environment: [GH_TOKEN]\n")
	if err != nil || len(got.Actions()) != 1 || !got.Actions()[0].Publish || !slices.Equal(got.Actions()[0].Environment, []string{"GH_TOKEN"}) {
		t.Errorf("control, an action that publishes: %v, %v", got.Actions(), err)
	}
	for keys, says := range map[string]string{
		"        environment: [GH_TOKEN]\n":                      "the action release says an environment, which only an action that publishes is given",
		"        publish: 'yes'\n":                               "publish is true or false",
		"        publish: true\n        environment: GH_TOKEN\n": "environment is a list",
	} {
		if _, err := read(keys); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%q: %v, want %q", keys, err, says)
		}
	}
}
