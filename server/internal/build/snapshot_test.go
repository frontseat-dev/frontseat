package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
)

// asking is an executor that asks, as a grid does, what each input of
// an action held, and runs nothing where it is not told. Where edit is
// set, the first action it is given has one of its inputs written to
// before it asks: a file changed while the run was under way.
type asking struct {
	scripted
	edit   bool
	edited *sync.Once
	// ran, if set, is told of each action it is asked to run.
	ran *ran
}

// ran is the actions an executor was asked to run, by label.
type ran struct {
	mu     sync.Mutex
	labels []string
}

func (r *ran) note(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.labels = append(r.labels, label)
}

// since returns the actions run since it was last asked, sorted.
func (r *ran) since() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.labels
	r.labels = nil
	sort.Strings(out)
	return out
}

// Key says what an action is as a grid does: by what it runs, where, and
// what each of its inputs held.
func (a asking) Key(_ context.Context, action exec.Action) (string, error) {
	sum := sha256.New()
	fmt.Fprintln(sum, action.Arguments, action.WorkingDirectory, action.Environment, action.Outputs, action.Platform, action.Held, action.Copies)
	// What is written for it is by its content's digest, as a grid has it.
	written := make([]string, 0, len(action.Written))
	for at := range action.Written {
		written = append(written, at)
	}
	sort.Strings(written)
	for _, at := range written {
		fmt.Fprintf(sum, "%s %x %v\n", at, sha256.Sum256(action.Written[at].Content), action.Written[at].Executable)
	}
	if action.Files == nil && len(action.Inputs) > 0 {
		return "", errors.New("the action was sent without what its files held")
	}
	for _, input := range action.Inputs {
		hash, size, executable, err := action.Files.File(filepath.Join(action.Root, filepath.FromSlash(input)))
		if err != nil {
			return "", err
		}
		fmt.Fprintln(sum, input, hash, size, executable)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func (a asking) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if installs(action) {
		return a.scripted.Execute(ctx, action)
	}
	if action.Files == nil {
		return exec.Result{}, errors.New("the action was sent without what its files held")
	}
	if a.ran != nil {
		a.ran.note(action.Label)
	}
	if a.edit && len(action.Inputs) > 0 {
		var failed error
		a.edited.Do(func() {
			file, err := os.OpenFile(filepath.Join(action.Root, filepath.FromSlash(action.Inputs[0])), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				failed = err
				return
			}
			defer file.Close()
			_, failed = file.WriteString("\n// written while the run was under way\n")
		})
		if failed != nil {
			return exec.Result{}, failed
		}
	}
	for _, input := range action.Inputs {
		if _, _, _, err := action.Files.File(filepath.Join(action.Root, filepath.FromSlash(input))); err != nil {
			return exec.Result{}, err
		}
	}
	return a.scripted.Execute(ctx, action)
}

// A run is of the files as they were when it began. An action is sent
// what they held then; one whose file has changed since is not sent as
// something else, and says which file it was.
func TestARunIsOfTheFilesAsTheyWereWhenItBegan(t *testing.T) {
	for name, edit := range map[string]bool{"the control, nothing changes": false, "a file changes": true} {
		t.Run(name, func(t *testing.T) {
			forge := testforge.Make(t)
			c := serve(t, asking{edit: edit, edited: &sync.Once{}}, t.TempDir(), false)
			run := ranGateway(t, c, forge)

			changed := 0
			for label, action := range actions(t, c, run) {
				switch {
				case strings.Contains(action.GetMessage(), "changed while the run was under way"):
					changed++
					if action.GetOutcome() != buildv1.Outcome_OUTCOME_ERRORED || action.GetProvenance() != buildv1.Provenance_PROVENANCE_UNSPECIFIED {
						t.Errorf("%s ended %s, from %s; want an error no worker had part in", label, action.GetOutcome(), action.GetProvenance())
					}
				case action.GetOutcome() == buildv1.Outcome_OUTCOME_ERRORED:
					t.Errorf("%s: %s", label, action.GetMessage())
				}
			}
			if edit && changed == 0 {
				t.Errorf("no action said a file had changed; the run ended %s", run.GetExitCode())
			}
			if !edit && (changed != 0 || run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS) {
				t.Errorf("%d actions said a file had changed and the run ended %s; want it to pass", changed, run.GetExitCode())
			}
			if edit && run.GetExitCode() == buildv1.ExitCode_EXIT_CODE_SUCCESS {
				t.Error("the run passed; want it to fail")
			}
		})
	}
}
