package stage

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

func TestRuntimeEnvironmentUsesTheConfinedExactValuePath(t *testing.T) {
	snapshot := sourcetest.Taken(t, nil)
	p := installed()
	action := plan.Action{Environment: map[string]string{"FIXTURE": "literal value"}, Declared: &pluginv1.Action{Test: true, Command: &pluginv1.Command{Arguments: []string{"true"}}}}
	staged, err := Of(t.Context(), snapshot, action, p, nil, nil)
	if err != nil || len(staged.Environment) != 0 || !strings.Contains(strings.Join(staged.Arguments, " "), "literal value") {
		t.Fatalf("runtime environment escaped confinement or was lost: %+v, %v", staged, err)
	}
	action.Declared.Command.Environment = map[string]string{"FIXTURE": "adapter"}
	if _, err := Of(t.Context(), snapshot, action, p, nil, nil); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("adapter value was replaced: %v", err)
	}
	action.Declared.Command.Environment = nil
	action.Beside = &plan.Beside{Environment: map[string]string{"FIXTURE": "service"}}
	if _, err := Of(t.Context(), snapshot, action, p, nil, nil); err == nil || !strings.Contains(err.Error(), "both test configuration and a service") {
		t.Fatalf("service binding was replaced: %v", err)
	}
	action.Beside = nil
	action.Environment = map[string]string{"HOME": "override"}
	if _, err := Of(t.Context(), snapshot, action, p, nil, nil); err == nil || !strings.Contains(err.Error(), "sets HOME itself") {
		t.Fatalf("runtime data moved confinement's home: %v", err)
	}
}
