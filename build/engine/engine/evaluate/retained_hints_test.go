package evaluate

import (
	"bytes"
	"context"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"strings"
	"sync"
	"testing"
	"time"
)

// The snapshot a daemon evaluates is a source that counts and hints: a
// change to either method's shape fails here, where it would otherwise
// be dropped without a word.
var _ interface {
	Source
	Counting
	Hinting
} = (*source.Snapshot)(nil)

type hintedProbe struct {
	Source
	mu    sync.Mutex
	first string
}

type misleadingHints struct {
	Source
	value bool
}

func (s misleadingHints) MayHaveChanged(string, string) bool { return s.value }

type hintedRunner struct {
	engine *retainedEngine
	value  bool
}

func (p hintedRunner) run(ctx context.Context, src Source, profile typev1.Profile, targets []string) (retainedResult, error) {
	return p.engine.run(ctx, misleadingHints{Source: src, value: p.value}, profile, targets)
}

func TestRetainedHintsAreNeverEvidenceOfFreshness(t *testing.T) {
	for _, value := range []bool{false, true} {
		name := "all missed"
		if value {
			name = "all dirty"
		}
		t.Run(name, func(t *testing.T) {
			root := retainedFixture(t, 3)
			p, files := hintedRunner{engine: newRetainedEngine(), value: value}, &source.Keeper{}
			profile := typev1.Profile_PROFILE_DEVELOPMENT
			retainedOracle(t, p, files, root, profile, nil)
			retainedWrite(t, root, "m0001/main.go", "package library\nfunc Exported() {}\n")
			retainedOracle(t, p, files, root, profile, nil)
			if p.engine.evaluations != 2 {
				t.Fatal("wrong hints hid changed analysis")
			}
			retainedWrite(t, root, "m0002/extra.go", "package main\nfunc Extra() {}\n")
			retainedOracle(t, p, files, root, profile, nil)
			// A second provider of the same module must be refused, then
			// repaired, under either misleading hint source.
			retainedWrite(t, root, "m0002/go.mod", "module example.com/m0000\n\ngo 1.27\n")
			retainedCompare(t, p, files, root, profile, nil, true)
			retainedWrite(t, root, "m0002/go.mod", "module example.com/m0002\n\ngo 1.27\n")
			retainedOracle(t, p, files, root, profile, nil)
		})
	}
}

func (s *hintedProbe) MayHaveChanged(repository, path string) bool {
	if hints, ok := s.Source.(interface{ MayHaveChanged(string, string) bool }); ok {
		return hints.MayHaveChanged(repository, path)
	}
	return false
}

func (s *hintedProbe) Digest(ctx context.Context, repository, path string) (string, bool, error) {
	if strings.HasSuffix(path, "/main.go") {
		s.mu.Lock()
		if s.first == "" {
			s.first = path
		}
		s.mu.Unlock()
	}
	return s.Source.Digest(ctx, repository, path)
}

// The changed Analyze query is deliberately last among its peers. Snapshot
// metadata must move it ahead, without moving Arrange ahead of Analyze.
func TestRetainedHintsCheckTheChangedAnalysisFirst(t *testing.T) {
	root := retainedFixture(t, 3)
	// Keeper does not retain metadata from a file's first 20 milliseconds.
	time.Sleep(30 * time.Millisecond)
	p, files := newRetainedEngine(), &source.Keeper{}
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	retainedOracle(t, p, files, root, profile, nil)
	var changed, analyses, arrangements []retainedQuery
	for _, q := range p.trace.queries {
		if q.method != "Analyze" {
			arrangements = append(arrangements, q)
			continue
		}
		found := false
		for _, read := range q.value.reads {
			if read.Import == "read" && read.Argument == "m0000/main.go" {
				found = true
			}
		}
		if found {
			changed = append(changed, q)
		} else {
			analyses = append(analyses, q)
		}
	}
	if len(changed) != 1 || len(analyses) < 1 {
		t.Fatal("fixture did not capture independent analyses")
	}
	p.trace.queries = append(append(analyses, changed...), arrangements...)
	retainedWrite(t, root, "m0000/main.go", "package library\nfunc Exported() {}\n")
	probe := &hintedProbe{Source: retainedTake(t, files, root)}
	got, err := p.run(t.Context(), probe, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if probe.first != "m0000/main.go" {
		t.Errorf("first checked %q; want changed analysis m0000/main.go", probe.first)
	}
	if p.evaluations != 2 {
		t.Fatal("changed analysis did not rebuild")
	}
	want, err := retainedFresh(t.Context(), retainedTake(t, &source.Keeper{}, root), profile, nil, &Answers{})
	if err != nil {
		t.Fatal(err)
	}
	a, b := retainedSemantics(t, got.evaluation, want.evaluation)
	if !bytes.Equal(a, b) {
		t.Fatal("hinted evaluation differs from fresh evaluator")
	}
}
