package evaluate

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

// printedAtMost is the most of what a check printed that its plugin is
// handed, of each stream: findings are at the top.
const printedAtMost = 1 << 20

// Report asks the plugin that declared a check what the check found,
// from how it ended and what it printed, and returns the findings bound
// to the check's repository. A check no plugin declared, or one whose
// plugin does not serve the call, has no findings. What the plugin says
// is held to the contract: a rule it qualifies, a message, a file of the
// repository or none.
func Report(ctx context.Context, src Source, plugins []*pluginhost.Plugin, answers *Answers, action plan.Action, exitCode int, stdout, stderr []byte) ([]*typev1.Diagnostic, error) {
	var plugin *pluginhost.Plugin
	for _, each := range plugins {
		if each.Description.GetName() == action.Label.Plugin {
			plugin = each
		}
	}
	if plugin == nil || action.Context == nil {
		return nil, nil
	}
	request := &pluginv1.ReportRequest{
		Context:  action.Context,
		Root:     action.ProjectRoot,
		Action:   action.Name(),
		ExitCode: int32(exitCode),
		Stdout:   cut(stdout),
		Stderr:   cut(stderr),
	}
	response := &pluginv1.ReportResponse{}
	_, err := answers.ask(ctx, plugin, repositoryView{source: src, repository: action.Repository}, "Report", request, response)
	if errors.Is(err, pluginhost.ErrUnsupported) {
		return nil, nil
	}
	if err != nil {
		return nil, mistaken(action.Repository, err)
	}
	if err := pluginhost.CheckReport(action.Label.Plugin, response); err != nil {
		return nil, fmt.Errorf("plugin %s, asked what %s found: %w", action.Label.Plugin, action.Label, err)
	}
	for _, found := range response.GetDiagnostics() {
		if location := found.GetSourceLocation(); location != nil {
			location.Repository = action.Repository
		}
	}
	return response.GetDiagnostics(), nil
}

// cut is the start of what was printed, as much as a plugin is handed,
// ending at a line's end: a finding is a line, and a plugin is handed no
// part of one.
func cut(printed []byte) []byte {
	if len(printed) <= printedAtMost {
		return printed
	}
	end := bytes.LastIndexByte(printed[:printedAtMost], '\n')
	if end < 0 {
		return nil
	}
	return printed[:end+1]
}
