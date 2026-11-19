package evaluate

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func queryProbe(t *testing.T, p *retainedEngine) *retainedQuery {
	t.Helper()
	for i := range p.trace.queries {
		q := &p.trace.queries[i]
		if q.method != "Analyze" {
			continue
		}
		request := new(pluginv1.AnalyzeRequest)
		if err := proto.Unmarshal(q.request, request); err != nil {
			t.Fatal(err)
		}
		if slices.Equal(request.Roots, []string{"m0000"}) {
			return q
		}
	}
	t.Fatal("Go Analyze query missing")
	return nil
}

func TestRetainedQuerySemanticAndWireEquality(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "equivalent encoding"
		if unknown {
			name = "unknown field differs"
		}
		t.Run(name, func(t *testing.T) {
			root, p, files := warmRetained(t, 1)
			profile := pluginv1.Profile_PROFILE_DEVELOPMENT
			q := queryProbe(t, p)
			original := q.value
			var alternate []byte
			if unknown {
				alternate = protowire.AppendVarint(protowire.AppendTag(slices.Clone(original.response), 9999, protowire.VarintType), 1)
			} else {
				// The sole Analysis repeats its root field with the same value:
				// a legal different wire representation of the same message.
				number, kind, n := protowire.ConsumeTag(original.response)
				body, m := protowire.ConsumeBytes(original.response[n:])
				if number != 1 || kind != protowire.BytesType || n < 0 || m < 0 || n+m != len(original.response) {
					t.Fatal("fixture must contain one Analysis")
				}
				body = protowire.AppendString(protowire.AppendTag(slices.Clone(body), 1, protowire.BytesType), "m0000")
				alternate = protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), body)
			}
			before, after := new(pluginv1.AnalyzeResponse), new(pluginv1.AnalyzeResponse)
			if err := proto.Unmarshal(original.response, before); err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(alternate, after); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(original.response, alternate) || proto.Equal(before, after) == unknown {
				t.Fatal("wire/equality control does not distinguish the intended case")
			}
			// Replace only the trace's prior value, never cache-owned bytes.
			q.value = &answer{response: alternate, reads: original.reads, last: original.last}
			retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// comment\n")
			retainedOracle(t, p, files, root, profile, nil)
			want := 1
			if unknown {
				want = 2
			}
			if p.evaluations != want {
				t.Fatalf("evaluations=%d; want %d", p.evaluations, want)
			}
		})
	}
}

func TestRetainedQueryEqualReplyReplacesAnswerAndReads(t *testing.T) {
	root := retainedFixture(t, 1)
	retainedWrite(t, root, ".probe/new-input", "blue")
	p, files := newRetainedEngine(), &source.Keeper{}
	profile := pluginv1.Profile_PROFILE_DEVELOPMENT
	retainedOracle(t, p, files, root, profile, nil)
	q := queryProbe(t, p)
	old := q.value
	key := ""
	for k, v := range p.answers.kept {
		if v == old {
			key = k
		}
	}
	if key == "" {
		t.Fatal("captured value is not owned by Answers")
	}
	retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// changed\n")
	view := repositoryView{source: retainedTake(t, files, root)}
	recorded := &pluginhost.Recorder{Source: view}
	for _, name := range []string{"m0000/go.mod", ".probe/new-input"} {
		if _, _, err := recorded.Read(t.Context(), name); err != nil {
			t.Fatal(err)
		}
	}
	// Fault-inject a valid replacement cache record with the same real reply
	// and a new dependency. This tests handle/dependency handover, not a claim
	// that the current Go plugin itself reads .probe/new-input.
	newValue := &answer{response: old.response, reads: recorded.Reads(), last: p.answers.clock()}
	p.answers.kept[key] = newValue
	if _, err := p.run(t.Context(), retainedTake(t, files, root), profile, nil); err != nil {
		t.Fatal(err)
	}
	if p.evaluations != 1 || q.value != newValue {
		t.Fatal("equal reply did not replace the retained handle")
	}
	before := p.reasked
	retainedWrite(t, root, ".probe/new-input", "red")
	retainedOracle(t, p, files, root, profile, nil)
	if p.reasked != before+1 || p.evaluations != 1 {
		t.Fatal("new observation was not replayed")
	}
	if q.value == newValue || !bytes.Equal(old.response, newValue.response) {
		t.Fatal("replacement mutated or retained the obsolete cache record")
	}
}

func TestRetainedQueryCachedBytesAndExpiry(t *testing.T) {
	root := retainedFixture(t, 1)
	p, files := newRetainedEngine(), &source.Keeper{}
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	p.answers.now = func() time.Time { return now }
	profile := pluginv1.Profile_PROFILE_DEVELOPMENT
	retainedOracle(t, p, files, root, profile, nil)
	q := queryProbe(t, p)
	old := q.value
	requestBytes, responseBytes := slices.Clone(q.request), slices.Clone(old.response)
	request, response := new(pluginv1.AnalyzeRequest), new(pluginv1.AnalyzeResponse)
	if err := proto.Unmarshal(q.request, request); err != nil {
		t.Fatal(err)
	}
	again, wire, value, err := p.answers.answerObserved(t.Context(), q.plugin, repositoryView{source: retainedTake(t, files, root)}, q.method, request, response)
	if err != nil || !again || value != old {
		t.Fatalf("cache control: again=%v value_same=%v err=%v", again, value == old, err)
	}
	request.Roots[0] = "modified"
	response.Analyses[0].Projects[0].Name = "modified"
	if !bytes.Equal(q.request, requestBytes) || !bytes.Equal(wire, requestBytes) || !bytes.Equal(old.response, responseBytes) {
		t.Fatal("decoded values alias retained wire bytes")
	}
	now = now.Add(4 * time.Hour)
	p.answers.begin()
	if len(p.answers.kept) != 0 {
		t.Fatal("expiry control did not empty cache")
	}
	retainedOracle(t, p, files, root, profile, nil)
	if p.evaluations != 1 || q.value != old {
		t.Fatal("cache eviction invalidated an independently retained immutable answer")
	}
	retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// after expiry\n")
	retainedOracle(t, p, files, root, profile, nil)
	if p.evaluations != 1 || q.value == old || !bytes.Equal(old.response, responseBytes) {
		t.Fatal("fresh answer after eviction damaged prior value or reran assembly")
	}
}

type queryReadGate struct {
	Source
	once    sync.Once
	started chan struct{}
}

func (s *queryReadGate) Read(ctx context.Context, repository, path string) ([]byte, bool, error) {
	if path == "m0000/go.mod" {
		s.once.Do(func() { close(s.started) })
		<-ctx.Done()
		return nil, false, ctx.Err()
	}
	return s.Source.Read(ctx, repository, path)
}

func TestRetainedQueryWarmCancellationClearsPublishedState(t *testing.T) {
	root, p, files := warmRetained(t, 2)
	profile := pluginv1.Profile_PROFILE_DEVELOPMENT
	retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// changed\n")
	gate := &queryReadGate{Source: retainedTake(t, files, root), started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := p.run(ctx, gate, profile, nil); finished <- err }()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("warm query did not start")
	}
	cancel()
	cancelledWithin(t, finished, "cancelled", "cancellation hung")
	// A caller that gave up takes nothing of what was kept with it; what
	// is kept is held to the source again by the next request (retainedOracle).
	if !p.valid || p.trace == nil {
		t.Fatal("a caller that gave up took what was kept with it")
	}
	retainedOracle(t, p, files, root, profile, nil)
	if !p.valid {
		t.Fatal("successful repair not published")
	}
}

func TestRetainedQueryGuardsAndUnsupportedViews(t *testing.T) {
	root := retainedFixture(t, 1)
	p := newRetainedEngine()
	retainedOracle(t, p, &source.Keeper{}, root, pluginv1.Profile_PROFILE_DEVELOPMENT, nil)
	q := queryProbe(t, p)
	plugin := q.plugin
	request := new(pluginv1.AnalyzeRequest)
	if err := proto.Unmarshal(q.request, request); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"eligible", "overlay", "guarded"} {
		t.Run(mode, func(t *testing.T) {
			observed := &trackedSource{Source: retainedTake(t, &source.Keeper{}, root), recorders: map[string]*pluginhost.Recorder{}}
			asking := &retainedAsking{Answers: &Answers{}, observed: observed}
			view := repositoryView{source: observed}
			switch mode {
			case "overlay":
				view.source = overlaid{Source: observed, repository: "", path: "m0000/main.go", content: []byte("package main\nfunc main() {}\n")}
			case "guarded":
				view.source = generatedView{Source: observed, generated: []plan.GeneratedSource{{Directory: "m0000", Patterns: []string{"go.mod"}}}}
			}
			_, _, err := asking.askObserved(t.Context(), plugin, view, "Analyze", request, new(pluginv1.AnalyzeResponse))
			if mode == "guarded" {
				if err == nil || !strings.Contains(err.Error(), "analysis cannot read generated source") || len(asking.queries) != 0 {
					t.Fatalf("guard bypass: queries=%d err=%v", len(asking.queries), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reads := observed.reads().byRepository[""]
			if mode == "eligible" {
				if len(asking.queries) != 1 || len(reads) != 0 {
					t.Fatal("eligible reads were not separated")
				}
			} else if len(asking.queries) != 0 || len(reads) == 0 {
				t.Fatal("unsupported source view bypassed structural recording")
			}
		})
	}
}
