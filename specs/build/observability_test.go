package build_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// A command's caller gives the API a trace context. The daemon's asynchronous
// run retains it after CreateBuild returns, and exports action output and metric
// exemplars associated with that same trace. The collector receives real OTLP;
// the build goes through the API and the real grid.
// Observability / keeps a command's run, action logs and metric exemplars in one trace.
func TestKeepsACommandSRunActionLogsAndMetricExemplarsInOneTrace(t *testing.T) {
	spec := newScenario(t)

	collector := spec.observeBuilds()
	// A module whose test prints, so that an action has output.
	w := spec.workspaceOf("fixtures")
	exporter, err := otlptracegrpc.New(context.Background())
	spec.Expect(err).NotTo(HaveOccurred())
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	spec.Cleanup(func() { spec.Expect(provider.Shutdown(context.Background())).To(Succeed()) })
	ctx, command := provider.Tracer("frontseat-usecase").Start(context.Background(), "command")
	id := command.SpanContext().TraceID().String()
	// Through the SDK, which carries the caller's trace to the daemon.
	created, err := w.daemon().Build(ctx, frontseat.BuildRequest{})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Eventually(func() buildv1.Build_State {
		got, err := created.Describe(ctx)
		spec.Expect(err).NotTo(HaveOccurred())
		if got.GetState() != buildv1.Build_STATE_RUNNING {
			spec.Expect(got.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS))
		}
		return got.GetState()
	}, 2*time.Minute, 100*time.Millisecond).ShouldNot(Equal(buildv1.Build_STATE_RUNNING))
	command.End()
	// Control: our parent reached this collector, so a missing daemon
	// span cannot pass as a successful export test.
	spec.Eventually(func() bool {
		for _, r := range collector.Snapshot().Traces {
			for _, scope := range r.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name == "command" && hex.EncodeToString(span.TraceId) == id {
						return true
					}
				}
			}
		}
		return false
	}, 5*time.Second).Should(BeTrue())
	spec.Eventually(func() bool {
		for _, r := range collector.Snapshot().Traces {
			for _, scope := range r.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name == "BUILD build" && hex.EncodeToString(span.TraceId) == id {
						return true
					}
				}
			}
		}
		return false
	}, 5*time.Second).Should(BeTrue(), "the daemon's run must continue the caller's trace")
	spec.Eventually(func() bool {
		for _, r := range collector.Snapshot().Logs {
			for _, scope := range r.ScopeLogs {
				for _, entry := range scope.LogRecords {
					output := false
					for _, attr := range entry.Attributes {
						if attr.Key == "log.iostream" {
							output = attr.Value.GetStringValue() == "stdout" || attr.Value.GetStringValue() == "stderr"
						}
					}
					if output && hex.EncodeToString(entry.TraceId) == id && len(entry.SpanId) > 0 && entry.Body.GetStringValue() != "" {
						return true
					}
				}
			}
		}
		return false
	}, 5*time.Second).Should(BeTrue(), "action output must be correlated with its trace and span")
	spec.Eventually(func() bool {
		for _, r := range collector.Snapshot().Metrics {
			for _, scope := range r.ScopeMetrics {
				for _, metric := range scope.Metrics {
					if metric.Name != "cicd.pipeline.run.duration" {
						continue
					}
					for _, point := range metric.GetHistogram().GetDataPoints() {
						for _, exemplar := range point.Exemplars {
							if hex.EncodeToString(exemplar.TraceId) == id && len(exemplar.SpanId) > 0 {
								return true
							}
						}
					}
				}
			}
		}
		return false
	}, 5*time.Second).Should(BeTrue(), "measurements must link back to the command through exemplars")
}

// Observability / links newly executed actions to this command's cached dependencies.
func TestLinksNewlyExecutedActionsToThisCommandSCachedDependencies(t *testing.T) {
	spec := newScenario(t)

	collector := spec.observeBuilds()
	w := spec.workspaceOf("tool")
	before := w.build(of("//lab/tool:go.compile"))
	spec.Expect(before.outcome()).To(Equal("SUCCESS"))
	// The dependency lock is unchanged; unique source forces compilation on a
	// worker while its archive and metadata fetches use the populated cache.
	w.write("lab/tool/main.go", "package main\n\nimport \"github.com/spf13/cobra\"\n\nfunc main() { _ = (&cobra.Command{Use: \""+rand.Text()+"\"}).Execute() }")
	after := w.build(of("//lab/tool:go.compile"))
	spec.Expect(after.outcome()).To(Equal("SUCCESS"))
	fetched := spec.fetches(after)
	spec.Expect(fetched).NotTo(BeEmpty())
	for _, label := range fetched {
		spec.Expect(after.cameFrom(label)).To(Equal("grid_cache"), label)
	}
	spec.Expect(after.cameFrom("//lab/tool:go.compile")).To(Equal("executed"))
	spec.Expect(after.needsOf("//lab/tool:go.compile")).To(ConsistOf(fetched))
	previous := spec.actionSpans(collector, before)
	current := spec.actionSpans(collector, after)
	for _, label := range fetched {
		spec.Expect(current[label].TraceId).NotTo(Equal(previous[label].TraceId))
		spec.Expect(current[label].SpanId).NotTo(Equal(previous[label].SpanId))
	}
	spec.expectTraceDependencies(after, current)
}

// Observability / keeps skipped actions linked to the dependencies that failed.
func TestKeepsSkippedActionsLinkedToTheDependenciesThatFailed(t *testing.T) {
	spec := newScenario(t)

	collector := spec.observeBuilds()
	w := spec.workspaceOf("tool")
	allowed := w.build(of("//lab/tool:go.compile"))
	spec.Expect(allowed.outcome()).To(Equal("SUCCESS"))
	w.write("lab/tool/go.sum", spec.wrongChecksum(testforge.ToolLock, "github.com/spf13/pflag", false))
	refused := w.build(of("//lab/tool:go.compile"))
	spec.Expect(refused.outcomeOf(fetchOf("github.com/spf13/pflag@v1.0.5"))).To(Equal("failed"))
	spec.Expect(refused.outcomeOf("//lab/tool:go.compile")).To(Equal("skipped"))
	spans := spec.actionSpans(collector, refused)
	spec.Expect(spanAttribute(spans["//lab/tool:go.compile"], "cicd.pipeline.task.run.result")).To(Equal("skip"))
	spec.expectTraceDependencies(refused, spans)
}
