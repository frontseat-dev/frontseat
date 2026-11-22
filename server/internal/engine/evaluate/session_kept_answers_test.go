package evaluate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// The answers an evaluation that is kept rests on are asked for by every
// request answered from it, though no plugin is: they are not let go as
// answers nothing asked for, however long the daemon is asked only what it
// has kept. After hours of such requests, one module changed has its one
// plugin asked again and no other. The control is a session asked nothing
// in those hours, whose answers were let go.
func TestAnswersAKeptEvaluationRestsOnAreNotLetGo(t *testing.T) {
	plugins := retainedPlugins(t)
	for name, askedMeanwhile := range map[string]bool{"asked every hour": true, "asked nothing meanwhile": false} {
		t.Run(name, func(t *testing.T) {
			root := retainedFixture(t, 6)
			now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
			files, answers := &source.Keeper{}, &Answers{now: func() time.Time { return now }}
			s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, answers, takesNoSource)
			defer s.Close()
			build := func() Work {
				t.Helper()
				return wholeBuild(t, s, files, root).Work
			}
			first := build()
			if first.Asked == 0 {
				t.Fatalf("the first build asked no plugin: %+v", first)
			}
			for range 5 {
				now = now.Add(time.Hour)
				if askedMeanwhile {
					if got := build(); !got.Retained || got.Asked != 0 {
						t.Fatalf("an unchanged workspace was not answered from what was kept: %+v", got)
					}
				}
			}
			retainedWrite(t, root, "m0003/go.mod", "module example.com/m0003\n\ngo 1.27\n// changed\n")
			got := build()
			if askedMeanwhile && got.Asked != 1 {
				t.Errorf("after five hours of requests, one module changed had %d plugins asked again, want its one: %+v", got.Asked, got)
			}
			if !askedMeanwhile && got.Asked <= 1 {
				t.Errorf("answers nothing asked for in five hours were kept: %d asked again, of %d at first, so this test proves nothing", got.Asked, first.Asked)
			}
		})
	}
}

// A workspace that does not fit what a session keeps is evaluated for
// every request, as by a daemon that keeps no evaluation, and no plugin is
// asked again for it: the answers its evaluation rested on stay. What an
// evaluation no longer rests on goes: a module changed again and again
// leaves no more kept than the workspace needs. The control is the same
// workspace within the limits, which is evaluated once.
func TestAWorkspaceOverTheLimitsAsksNoPluginAgain(t *testing.T) {
	plugins := retainedPlugins(t)
	for name, limits := range map[string]RetentionLimits{"within": {8192, 8 << 20}, "over": {8, 8 << 20}} {
		t.Run(name, func(t *testing.T) {
			root := retainedFixture(t, 6)
			files, answers := &source.Keeper{}, &Answers{}
			s := newSession(t, plugins, limits, 1, answers, takesNoSource)
			defer s.Close()
			build := func() Work {
				t.Helper()
				return wholeBuild(t, s, files, root).Work
			}
			kept := func() int {
				answers.mu.Lock()
				defer answers.mu.Unlock()
				return len(answers.kept)
			}
			if first := build(); first.Asked == 0 {
				t.Fatalf("the first build asked no plugin: %+v", first)
			}
			needed := kept()
			again := build()
			if again.Asked != 0 {
				t.Errorf("an unchanged workspace had %d plugins asked again: %+v", again.Asked, again)
			}
			if within := name == "within"; again.Retained != within {
				t.Errorf("the evaluation was kept=%v, want %v", again.Retained, within)
			}
			// A request that is refused between two that are not takes no
			// answer with it, over the limits as within them.
			said, err := os.ReadFile(filepath.Join(root, "frontseat.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			retainedWrite(t, root, "frontseat.yaml", "unknown: refused\n")
			if _, err := s.Build(t.Context(), retainedTake(t, files, root), typev1.Profile_PROFILE_DEVELOPMENT, nil, nil, nil); err == nil {
				t.Fatal("a repository that says what it may not was built")
			}
			retainedWrite(t, root, "frontseat.yaml", string(said))
			if mended := build(); mended.Asked != 0 {
				t.Errorf("after a refused request, the workspace as it was had %d plugins asked again: %+v", mended.Asked, mended)
			}
			for i := range 8 {
				retainedWrite(t, root, "m0003/go.mod", "module example.com/m0003\n\ngo 1.27\n// "+string(rune('a'+i))+"\n")
				if got := build(); got.Asked != 1 {
					t.Fatalf("one module changed had %d plugins asked: %+v", got.Asked, got)
				}
			}
			if name == "over" && kept() > needed {
				t.Errorf("after a module changed eight times %d answers are kept, and the workspace needed %d at first", kept(), needed)
			}
		})
	}
}
