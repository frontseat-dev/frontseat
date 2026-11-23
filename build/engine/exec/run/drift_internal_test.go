package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/model"
)

// bringing is a grid that lists what a generator produced and, asked to
// bring it, writes it where it is asked to; one that fails says so once
// it has written.
type bringing struct {
	exec.Executor
	files map[string]exec.Held
	fails error
}

func (b *bringing) List(context.Context, exec.Held) (map[string]exec.Held, error) {
	return b.files, nil
}

func (b *bringing) Materialize(_ context.Context, _ exec.Held, into string) error {
	if err := os.MkdirAll(into, 0o755); err != nil {
		return err
	}
	for file := range b.files {
		if err := os.WriteFile(filepath.Join(into, file), []byte("produced\n"), 0o644); err != nil {
			return err
		}
	}
	return b.fails
}

// What a generator produced is brought beside the daemon's own to be
// compared with the checkout's copy, and is gone once it has been:
// where bringing it fails as where it does not.
func TestWhatIsBroughtToCompareAGeneratedCopyIsGoneHoweverItEnds(t *testing.T) {
	producer := model.Label{Path: "api", Plugin: "buf", Name: "generate"}
	for name, fails := range map[string]error{"brought": nil, "not brought": errors.New("the grid let it go")} {
		t.Run(name, func(t *testing.T) {
			snapshot := sourcetest.Taken(t, map[string]string{"api/gen/a.pb.go": "committed\n"})
			grid := &bringing{files: map[string]exec.Held{"a.pb.go": {Digest: exec.Digest{Hash: "other", Size: 9}}}, fails: fails}
			runner := Runner{Answers: &exec.Remembering{Executor: grid}, Kept: t.TempDir()}
			action := plan.Action{
				Label: producer, ProjectRoot: "api",
				GeneratedSources: []plan.GeneratedSource{{ProjectRoot: "api", Directory: "api/gen", Output: "out/", Producer: producer, Patterns: []string{"*.pb.go"}}},
			}
			left := map[string]exec.Held{"api/out": {Directory: true, Digest: exec.Digest{Hash: "tree", Size: 1}}}

			changes, err := runner.drift(t.Context(), snapshot, action, left)

			if !errors.Is(err, fails) {
				t.Fatalf("drift: %v, want %v", err, fails)
			}
			if fails == nil && (len(changes) != 1 || string(changes[0].Content) != "produced\n") {
				t.Errorf("changes = %+v; want the copy replaced by what was produced", changes)
			}
			if kept, _ := os.ReadDir(runner.Kept); len(kept) != 0 {
				t.Errorf("%v was left in what the daemon keeps", kept)
			}
		})
	}
}
