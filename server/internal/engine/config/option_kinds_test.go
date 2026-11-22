package config_test

import (
	"context"
	"strings"
	"testing"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// bounded are options that say what a value may be: a length of time, a
// map whose values are words, a bounded number and a worded list.
func bounded() []*pluginv1.Option {
	least, most := int64(1), int64(8)
	return []*pluginv1.Option{
		{Name: "timeout", Kind: pluginv1.Option_KIND_DURATION, Default: &typev1.OptionValue{Value: &typev1.OptionValue_DurationValue{DurationValue: durationpb.New(10 * time.Minute)}}},
		{Name: "variables", Kind: pluginv1.Option_KIND_STRING_MAP, Allowed: []string{"version", "commit"}, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringMapValue{StringMapValue: &typev1.OptionValue_StringMap{}}}},
		{Name: "shards", Kind: pluginv1.Option_KIND_INT, Minimum: &least, Maximum: &most, Default: &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: 1}}},
		{Name: "levels", Kind: pluginv1.Option_KIND_STRING_LIST, Allowed: []string{"low", "high"}, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{}}}},
	}
}

// held is what frontseat.yaml's options of the plugin hold, by name, or
// why they were refused.
func held(written string) (map[string]*typev1.OptionValue, error) {
	read, err := config.Read(context.Background(), "repo", files{config.File: "plugins:\n  tool:\n" + written})
	if err != nil {
		return nil, err
	}
	options, err := read.Options("tool", bounded(), "")
	if err != nil {
		return nil, err
	}
	out := map[string]*typev1.OptionValue{}
	for _, option := range options {
		out[option.Name] = option.Value
	}
	return out, nil
}

// A value an option does not take is refused where it is written, at its
// line, naming the option and the rule and never the value; the control
// is each written as the option takes it.
func TestAnOptionsValueIsHeldToWhatItTakesWhereItIsWritten(t *testing.T) {
	got, err := held("    timeout: 1h30m\n    variables: {main.version: version, main.commit: commit}\n    shards: 8\n    levels: [low, high]\n")
	if err != nil {
		t.Fatalf("values each option takes were refused: %v", err)
	}
	if got["timeout"].GetDurationValue().AsDuration() != 90*time.Minute || got["variables"].GetStringMapValue().GetValues()["main.commit"] != "commit" || got["shards"].GetIntValue() != 8 || len(got["levels"].GetStringListValue().GetValues()) != 2 {
		t.Fatalf("the values were read as %v", got)
	}
	for written, says := range map[string]string{
		"    timeout: 90\n":                        "line 3: timeout is a length of time, not below zero, as 90s or 10m",
		"    timeout: -1s\n":                       "line 3: timeout is a length of time",
		"    timeout: soon\n":                      "line 3: timeout is a length of time",
		"    variables: [main.version]\n":          "line 3: variables is a map of text by name",
		"    variables: {main.version: [a]}\n":     "line 3: variables is a map of text by name",
		"    variables: {a: version, a: commit}\n": "a is set twice",
		"    variables: {main.version: secret1}\n": "line 3: a value of variables is one of version, commit",
		"    shards: 0\n":                          "line 3: shards is at least 1",
		"    shards: 9\n":                          "line 3: shards is at most 8",
		"    levels: [low, secret2]\n":             "line 3: an entry of levels is one of low, high",
	} {
		_, err := held(written)
		if err == nil || !strings.Contains(err.Error(), "repo/"+config.File) || !strings.Contains(err.Error(), says) {
			t.Errorf("%q: %v, want it refused of its file saying %q", written, err, says)
		}
		if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "soon")) {
			t.Errorf("%q: the refusal repeats what it refused: %v", written, err)
		}
	}
}
