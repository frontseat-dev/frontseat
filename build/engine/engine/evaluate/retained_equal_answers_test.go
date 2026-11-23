package evaluate

import (
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestRetainedUnchangedAnswerIsSharedAcrossReassembly(t *testing.T) {
	root := retainedFixture(t, 2)
	p, files := newRetainedEngine(), &source.Keeper{}
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	answer := func() *answer {
		for _, q := range p.trace.queries {
			if q.method != "Analyze" {
				continue
			}
			req := &pluginv1.AnalyzeRequest{}
			if err := proto.Unmarshal(q.request, req); err != nil {
				t.Fatal(err)
			}
			if len(req.Roots) == 1 && req.Roots[0] == "m0000" {
				return q.value
			}
		}
		t.Fatal("control query not captured")
		return nil
	}
	retainedOracle(t, p, files, root, profile, nil)
	before := answer()
	retainedWrite(t, root, "m0001/main.go", "package library\nfunc Exported() {}\n")
	retainedOracle(t, p, files, root, profile, nil)
	if p.evaluations != 2 {
		t.Fatal("control did not reconstruct the evaluation")
	}
	if answer() != before {
		t.Fatal("unchanged query response was copied during reassembly")
	}
}

func TestRetainedEqualValuesKeepEvaluationAndChangedValuesRebuild(t *testing.T) {
	root, p, files := warmRetained(t, 20)
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// comment\n")
	retainedOracle(t, p, files, root, profile, nil)
	if p.evaluations != 1 || p.plans != 1 || p.reasked != 1 {
		t.Fatalf("equal reply: evaluations=%d plans=%d reasked=%d", p.evaluations, p.plans, p.reasked)
	}
	retainedWrite(t, root, "m0001/main.go", "package main\nfunc main() { println(3) }\n")
	retainedOracle(t, p, files, root, profile, nil)
	if p.evaluations != 1 || p.reasked != 2 {
		t.Fatal("equal source reply reconstructed evaluation")
	}
	retainedWrite(t, root, "m0001/main.go", "package library\nfunc Exported() {}\n")
	retainedOracle(t, p, files, root, profile, nil)
	if p.evaluations != 2 || p.plans != 2 {
		t.Fatal("changed source reply did not reconstruct evaluation and plan")
	}
}
