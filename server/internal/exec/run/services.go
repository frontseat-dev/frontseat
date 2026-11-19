package run

import (
	"context"
	"fmt"
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
	"slices"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/schedule"
	"github.com/frontseat-dev/frontseat/server/internal/exec/stage"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"google.golang.org/protobuf/proto"
)

// forNeeds prevents runtime-only artifacts from being handed to the consuming
// test, and one provider's files from being handed to another provider.
func forNeeds(handed []schedule.Handed, needs []model.Label) []schedule.Handed {
	var out []schedule.Handed
	for _, each := range handed {
		if slices.Contains(needs, each.From) {
			out = append(out, each)
		}
	}
	return out
}

func (runner Runner) serviceInputs(ctx context.Context, snapshot *source.Snapshot, tools *tools.Provisioner, action plan.Action, handed []schedule.Handed, staged exec.Action) (exec.Action, error) {
	if action.Beside == nil {
		return staged, nil
	}
	var description runnerv1.RunnerSandbox
	if err := proto.Unmarshal(action.Beside.Sandbox, &description); err != nil {
		return exec.Action{}, err
	}
	programs := map[string]exec.Action{}
	for _, service := range description.GetServices() {
		program, found := action.Beside.Programs[service.GetName()]
		if !found {
			continue
		}
		inputs, err := runner.generated(ctx, snapshot.Tree, program, forNeeds(handed, program.Needs))
		if err != nil {
			return exec.Action{}, err
		}
		left, clash, err := runner.together(ctx, inputs)
		if err != nil {
			return exec.Action{}, err
		}
		if clash != "" {
			return exec.Action{}, fmt.Errorf("%s runtime inputs: %s", program.Label, clash)
		}
		prepared, err := stage.Program(ctx, snapshot, program, tools, runner.Downloads, left, service.GetService().GetEnvironment())
		if err != nil {
			return exec.Action{}, err
		}
		programs[service.GetName()] = prepared
	}
	return stage.Runtimes(staged, &description, programs)
}
