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
		"arguments": "      type: container\n      arguments: [a, ~, b]\n",
		"command":   "      type: container\n      ready: [{command: [sh, ~, x]}]\n",
		"ready":     "      type: container\n      ready: [~, {port: a}]\n",
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
		"ready-timeout": "      type: container\n      ready-timeout: 30\n",
		"http":          "      type: container\n      ready: [{http: {port: a, path: /, x: 1}}]\n",
	} {
		_, err := config.Read(t.Context(), "repo", resourceWith(text))
		if err == nil || strings.Contains(err.Error(), "line 1:") || strings.Contains(err.Error(), "struct {") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A name refused among a service's ports, exports or variables is
// refused on the line it is written on, and by which of its name and
// its number is wrong: the refusal repeats neither, so the line is how
// its author finds it. The control is the same service with the name as
// it may be.
func TestAServiceEntryIsRefusedOnItsOwnLine(t *testing.T) {
	const service = "      type: container\n      ports:\n        http: 80\n        admin: 81\n        extra: 82\n      exports:\n        URL: x\n        EXPORT: y\n      environment:\n        A: x\n        VARIABLE: z\n"
	if _, err := config.Read(t.Context(), "repo", resourceWith(service)); err != nil {
		t.Fatalf("control: %v", err)
	}
	for says, change := range map[string][2]string{
		"line 13: test-service.ports: a port is named in":                     {"extra: 82", "Bad: 82"},
		"line 12: test-service.ports: a port is a number from 1 to":           {"admin: 81", "admin: 70000"},
		"line 16: test-service.exports: a name the shell can export":          {"EXPORT: y", "EX-PORT: y"},
		"line 19: test-service.environment: a name the shell can export is l": {"VARIABLE: z", "VARI-ABLE: z"},
	} {
		_, err := config.Read(t.Context(), "repo", resourceWith(strings.Replace(service, change[0], change[1], 1)))
		name, _, _ := strings.Cut(change[1], ":")
		if err == nil || !strings.Contains(err.Error(), says) || strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "70000") {
			t.Errorf("want %q and nothing of what was written, got %v", says, err)
		}
	}
}
