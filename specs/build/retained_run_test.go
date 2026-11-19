package build_test

import (
	"crypto/rand"

	. "github.com/onsi/gomega"
	"testing"
)

// reuses an unchanged evaluation, validates changed plugin input, and keeps per-run traces.
func TestRetainedRunReusesValidatedEvaluationAndKeepsPerRunTraces(t *testing.T) {
	spec := newScenario(t)
	compile := "//lab/tool:go.compile"
	collector, w, _, firstActions := spec.firstRetainedToolRun(compile)

	second := w.build(of("./lab/tool"), only("compile"))
	expectRetainedBuildSucceeded(second)
	spec.Expect(second.cameFrom(compile)).To(Equal("grid_cache"))
	secondStages := spec.retainedRunStages(collector, second)
	spec.expectEvaluateStage(secondStages.evaluate, true, false)
	spec.Expect(spec.requiredSpanBool(secondStages.plan, "retained")).To(BeTrue())
	secondActions := spec.actionSpans(collector, second)
	spec.expectTraceDependencies(second, secondActions)
	spec.Expect(secondActions[compile].TraceId).NotTo(Equal(firstActions[compile].TraceId))
	spec.Expect(secondActions[compile].SpanId).NotTo(Equal(firstActions[compile].SpanId))

	// The Go plugin reads go.mod. Its comment changes the input bytes while
	// leaving the module answer and selected action plan the same.
	w.write("lab/tool/go.mod", w.file("lab/tool/go.mod")+"\n// equal plugin answer after this comment")
	w.write("lab/tool/main.go", toolMain(rand.Text()))
	third := w.build(of("./lab/tool"), only("compile"))
	expectRetainedBuildSucceeded(third)
	spec.Expect(third.cameFrom(compile)).To(Equal("executed"))
	thirdStages := spec.retainedRunStages(collector, third)
	spec.expectEvaluateStage(thirdStages.evaluate, true, true)
	spec.Expect(spec.requiredSpanBool(thirdStages.plan, "retained")).To(BeTrue())
	spec.Expect(third.actions()).To(Equal(second.actions()), "the plugin reask must preserve the selected plan")
	spec.Expect(third.needsOf(compile)).To(ConsistOf(second.needsOf(compile)))
	thirdActions := spec.actionSpans(collector, third)
	spec.expectTraceDependencies(third, thirdActions)
	spec.Expect(thirdActions[compile].TraceId).NotTo(Equal(secondActions[compile].TraceId))
}

// reuses evaluation but replans when the requested action filter changes.
func TestRetainedRunReplansWhenTheRequestedFilterChanges(t *testing.T) {
	spec := newScenario(t)
	compile := "//lab/tool:go.compile"
	lint := "//lab/tool:go.lint"
	collector, w, _, _ := spec.firstRetainedToolRun(compile)

	second := w.build(of("./lab/tool"), only("lint"))
	expectRetainedBuildSucceeded(second)
	spec.Expect(second.actions()).To(ConsistOf(append(spec.fetches(second), lint)))
	spec.Expect(second.actions()).NotTo(ContainElement(compile))
	secondStages := spec.retainedRunStages(collector, second)
	spec.expectEvaluateStage(secondStages.evaluate, true, false)
	spec.Expect(spec.requiredSpanBool(secondStages.plan, "retained")).To(BeFalse())
	secondActions := spec.actionSpans(collector, second)
	spec.expectTraceDependencies(second, secondActions)
}

func toolMain(use string) string {
	return "package main\n\nimport \"github.com/spf13/cobra\"\n\nfunc main() { _ = (&cobra.Command{Use: \"" + use + "\"}).Execute() }"
}

func expectRetainedBuildSucceeded(b *Build) {
	b.Helper()
	if b.outcome() != "SUCCESS" {
		b.Logf("Run failure: %s\n", b.says())
		for _, action := range b.results() {
			b.Logf("Action %s: %s\n%s\n", action.GetLabel(), action, b.printed(action.GetLabel()))
		}
	}
	b.Expect(b.outcome()).To(Equal("SUCCESS"), b.says())
}
