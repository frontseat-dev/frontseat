package config_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

// A null among a service's lists is refused where it is written: what is
// started is what was written, and a list read without one of its
// entries is not.
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

// A mistake in a service names the line it is written on and what its
// key takes: no line of anything else, and nothing of how it is read.
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
