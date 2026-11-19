package config_test

import (
	"context"
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

// An action of an entity's own that says launch, as a program once did,
// is refused by name, saying how a program is said now; the control,
// program: true, is a program.
func TestAnActionThatSaysLaunchIsRefusedByName(t *testing.T) {
	read := func(key string) (*config.Repository, error) {
		return config.Read(t.Context(), "", files{
			config.File:         "plugins: {}\n",
			"catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    actions:\n      serve:\n        " + key + ": true\n        tools: [caddy]\n        command: [caddy, run]\n",
		})
	}
	if _, err := read("launch"); err == nil || !strings.Contains(err.Error(), "an action takes no key launch: a program is said by program: true") {
		t.Errorf("an action that says launch: %v", err)
	}
	if got, err := read("program"); err != nil || len(got.Actions()) != 1 || !got.Actions()[0].Program {
		t.Errorf("control, program: true: %v", err)
	}
}
