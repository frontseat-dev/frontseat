package evaluate

import (
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// What plugins answered is kept once for the daemon: what evaluates
// beside the session, as a run and a release plan do, asks no plugin
// again for what the session was answered, and what it is answered is
// counted among what the session keeps. The control is the same
// evaluation given a record of its own, which asks every plugin.
func TestWhatEvaluatesBesideASessionAsksNothingAgain(t *testing.T) {
	root := retainedFixture(t, 2)
	plugins := retainedPlugins(t)
	files, answers := &source.Keeper{}, &Answers{}
	s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, answers, takesTree(files, root))
	defer s.Close()
	if _, err := s.Facts(t.Context()); err != nil {
		t.Fatal(err)
	}
	apart, err := Evaluate(t.Context(), retainedTake(t, files, root), plugins, &Answers{}, typev1.Profile_PROFILE_DEVELOPMENT, nil)
	if err != nil || apart.Asked == 0 {
		t.Fatalf("an evaluation with a record of its own asked %d plugins, %v: this test proves nothing", apart.Asked, err)
	}
	beside, err := Evaluate(t.Context(), retainedTake(t, files, root), plugins, answers, typev1.Profile_PROFILE_DEVELOPMENT, nil)
	if err != nil || beside.Asked != 0 {
		t.Fatalf("an evaluation beside the session asked %d plugins again, %v", beside.Asked, err)
	}
	// What is kept for either is one count, under the session's limits.
	kept := func() int {
		answers.mu.Lock()
		defer answers.mu.Unlock()
		return len(answers.kept)
	}
	if kept() == 0 || int64(kept()) > s.core.retainedUsage().Records {
		t.Fatalf("the daemon keeps %d answers and the session counts %d records", kept(), s.core.retainedUsage().Records)
	}
}
