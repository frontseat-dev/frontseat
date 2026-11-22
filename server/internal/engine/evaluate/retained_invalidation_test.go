package evaluate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func retainedSuccess(t *testing.T, p retainedRunner, tree source.Tree, files *source.Keeper, targets []string) retainedResult {
	t.Helper()
	snapshot, err := files.Take(t.Context(), tree)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.run(t.Context(), snapshot, typev1.Profile_PROFILE_DEVELOPMENT, targets)
	if err != nil {
		t.Fatalf("retained must succeed: %v", err)
	}
	fresh, err := new(source.Keeper).Take(t.Context(), tree)
	if err != nil {
		t.Fatal(err)
	}
	want, err := retainedFresh(t.Context(), fresh, typev1.Profile_PROFILE_DEVELOPMENT, targets, &Answers{})
	if err != nil {
		t.Fatalf("fresh control must succeed: %v", err)
	}
	a, b := retainedSemantics(t, got.evaluation, want.evaluation)
	if !bytes.Equal(a, b) {
		t.Fatal("evaluation differs from successful fresh control")
	}
	if len(got.actions) != len(want.actions) {
		t.Fatal("action counts differ")
	}
	for i := range got.actions {
		g, w := got.actions[i], want.actions[i]
		if g.Label != w.Label || !proto.Equal(g.Declared, w.Declared) || !proto.Equal(g.Context, w.Context) {
			t.Fatalf("action %d differs, including complete protobuf values", i)
		}
	}
	return got
}

func TestRetainedCatalogConfigurationAndCutoff(t *testing.T) {
	root := retainedFixture(t, 2)
	tree := source.Tree{Root: root, Paths: map[string]string{"": "."}}
	p, files := newRetainedEngine(), &source.Keeper{}
	first := retainedSuccess(t, p, tree, files, nil)
	if len(first.actions) == 0 {
		t.Fatal("fixture has no actions")
	}
	for i := range 3 {
		retainedWrite(t, root, "m0000/go.mod", fmt.Sprintf("module example.com/m0000\n\ngo 1.27\n// comment %d\n", i))
		retainedSuccess(t, p, tree, files, nil)
	}
	if p.plans != 1 {
		t.Fatalf("equal values replanned %d times", p.plans)
	}
	retainedWrite(t, root, "m0000/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: alpha\nspec:\n  type: service\n  lifecycle: experimental\n  owner: team\n")
	withCatalog := retainedSuccess(t, p, tree, files, nil)
	if len(withCatalog.evaluation.Entities) != 1 {
		t.Fatal("new catalog was not discovered")
	}
	retainedWrite(t, root, "frontseat.yaml", "plugins:\n  go: {test-shards: 2}\n  backstage: {}\n")
	retainedSuccess(t, p, tree, files, nil)
	retainedWrite(t, root, "m0000/main_test.go", "package main\nimport \"testing\"\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n")
	withTests := retainedSuccess(t, p, tree, files, nil)
	if len(withTests.actions) <= len(withCatalog.actions) {
		t.Fatal("new tests did not add actions")
	}
	retainedWrite(t, root, "frontseat.yaml", "plugins:\n  backstage: {}\n")
	disabled := retainedSuccess(t, p, tree, files, nil)
	if len(disabled.evaluation.Projects) >= len(withTests.evaluation.Projects) {
		t.Fatal("disabled plugin kept its projects")
	}
	retainedWrite(t, root, "frontseat.yaml", "plugins:\n  go: {}\n  backstage: {}\n")
	retainedSuccess(t, p, tree, files, nil)
	if err := os.Remove(filepath.Join(root, "m0000/catalog-info.yaml")); err != nil {
		t.Fatal(err)
	}
	removed := retainedSuccess(t, p, tree, files, nil)
	if len(removed.evaluation.Entities) != 0 {
		t.Fatal("removed catalog entity survived")
	}
}

func TestRetainedRepositoryMembershipAndTargetRepair(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		retainedWrite(t, root, name+"/go.mod", "module example.com/"+name+"\n\ngo 1.27\n")
		retainedWrite(t, root, name+"/main.go", "package main\nfunc main() {}\n")
		testpins.Go(t, filepath.Join(root, name))
	}
	p, files := newRetainedEngine(), &source.Keeper{}
	tree := source.Tree{Root: root, Paths: map[string]string{"a": "a"}}
	one := retainedSuccess(t, p, tree, files, nil)
	tree.Paths = map[string]string{"a": "a", "b": "b"}
	two := retainedSuccess(t, p, tree, files, nil)
	if len(two.actions) <= len(one.actions) {
		t.Fatal("added repository was missed")
	}
	targets := []string{"a/"}
	retainedSuccess(t, p, tree, files, targets)
	targets[0] = "not-present/"
	snapshot, err := files.Take(t.Context(), tree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.run(t.Context(), snapshot, typev1.Profile_PROFILE_DEVELOPMENT, targets); err == nil {
		t.Fatal("unknown target was accepted")
	}
	retainedSuccess(t, p, tree, files, []string{"a/"})
	tree.Paths = map[string]string{"b": "b"}
	retainedSuccess(t, p, tree, files, nil)
	tree.Paths = map[string]string{"a": "a", "b": "b"}
	retainedSuccess(t, p, tree, files, nil)
}

type retainedDigestGate struct {
	Source
	once             sync.Once
	started, release chan struct{}
}

func (s *retainedDigestGate) Digest(ctx context.Context, repository, path string) (string, bool, error) {
	s.once.Do(func() {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return s.Source.Digest(ctx, repository, path)
}

func TestRetainedQueuedCancellationDoesNotAbortActiveReuse(t *testing.T) {
	root := retainedFixture(t, 2)
	tree := source.Tree{Root: root, Paths: map[string]string{"": "."}}
	p, files := newRetainedEngine(), &source.Keeper{}
	retainedSuccess(t, p, tree, files, nil)
	gate := &retainedDigestGate{Source: retainedTake(t, files, root), started: make(chan struct{}), release: make(chan struct{})}
	active := make(chan error, 1)
	go func() { _, err := p.run(t.Context(), gate, typev1.Profile_PROFILE_DEVELOPMENT, nil); active <- err }()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("active reuse did not reach validation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	queued := make(chan error, 1)
	go func() {
		_, err := p.run(ctx, retainedTake(t, &source.Keeper{}, root), typev1.Profile_PROFILE_DEVELOPMENT, nil)
		queued <- err
	}()
	cancel()
	cancelledWithin(t, queued, "queued", "queued cancellation did not return")
	close(gate.release)
	if err := <-active; err != nil {
		t.Fatalf("active reuse: %v", err)
	}
	retainedSuccess(t, p, tree, files, nil)
	if p.evaluations != 1 || p.plans != 1 {
		t.Fatal("queued cancellation invalidated successful active reuse")
	}
}

type retainedFailedDigest struct {
	Source
	failure error
}

func (s retainedFailedDigest) Digest(context.Context, string, string) (string, bool, error) {
	return "", false, s.failure
}

func TestRetainedReplayErrorDoesNotPublishOrPoisonRepair(t *testing.T) {
	root := retainedFixture(t, 1)
	tree := source.Tree{Root: root, Paths: map[string]string{"": "."}}
	p, files := newRetainedEngine(), &source.Keeper{}
	retainedSuccess(t, p, tree, files, nil)
	failure := errors.New("review validation failure")
	_, err := p.run(t.Context(), retainedFailedDigest{Source: retainedTake(t, files, root), failure: failure}, typev1.Profile_PROFILE_DEVELOPMENT, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("validation error replaced by %v", err)
	}
	if _, again := p.run(t.Context(), retainedFailedDigest{Source: retainedTake(t, files, root), failure: failure}, typev1.Profile_PROFILE_DEVELOPMENT, nil); !errors.Is(again, failure) {
		t.Fatalf("a failed evaluation was answered from what was kept: %v", again)
	}
	retainedSuccess(t, p, tree, files, nil)
	retainedSuccess(t, p, tree, files, nil)
}
