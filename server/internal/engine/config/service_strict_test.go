package config_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

// a null among a service's lists is dropped without a word, so
// what is started is not what was written: args [a, ~, b] becomes
// [a, b], a readiness command loses an argument, and a null readiness
// check is no check. Configuration is read strictly.
func TestANullAmongAServiceSListsIsNotDroppedWithoutAWord(t *testing.T) {
	for name, text := range map[string]string{
		"args":    "      as: container\n      args: [a, ~, b]\n",
		"command": "      as: container\n      ready: [{command: [sh, ~, x]}]\n",
		"ready":   "      as: container\n      ready: [~, {port: a}]\n",
	} {
		read, err := config.Read(t.Context(), "repo", resourceWith(text))
		if err == nil {
			t.Errorf("%s: a null entry was accepted, and read as %v", name, read.Services()[0].Said)
		}
	}
}

// what the YAML decoder says of a re-marshalled node names
// "line 1" of nothing the author wrote, after the true line, and a Go
// struct type.
func TestAServiceMistakeNamesOnlyTheLineItIsWrittenOn(t *testing.T) {
	for name, text := range map[string]string{
		"timeout": "      as: container\n      timeout: \"30\"\n",
		"http":    "      as: container\n      ready: [{http: {port: a, path: /, x: 1}}]\n",
	} {
		_, err := config.Read(t.Context(), "repo", resourceWith(text))
		if err == nil || strings.Contains(err.Error(), "line 1:") || strings.Contains(err.Error(), "struct {") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
