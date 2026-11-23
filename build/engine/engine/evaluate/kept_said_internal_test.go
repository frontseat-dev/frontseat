package evaluate

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/model"
)

// what a repository says is read again when a file that said it
// is gone, and what it says wrongly is refused each time and is not
// kept in place of what it said before.
func TestWhatARepositorySaysWhenAFileIsGoneOrWrong(t *testing.T) {
	ctx := context.Background()
	source := &counted{files: map[string]string{"frontseat.yaml": "plugins: {}\n", "a/catalog-info.yaml": componentFile, "b/catalog-info.yaml": componentFile}}
	answers := &Answers{}
	said := func() (int, error) {
		answers.begin()
		repository, _, err := answers.said(ctx, repositoryView{source: source, repository: ""})
		if err != nil {
			return 0, err
		}
		return len(repository.Actions()), nil
	}
	if actions, err := said(); err != nil || actions != 2 {
		t.Fatalf("%d actions, err %v; want 2", actions, err)
	}
	delete(source.files, "b/catalog-info.yaml")
	if actions, err := said(); err != nil || actions != 1 {
		t.Errorf("with a catalog file gone: %d actions, err %v; want 1", actions, err)
	}
	good := source.files["a/catalog-info.yaml"]
	source.files["a/catalog-info.yaml"] = good + "    unknown: true\n"
	for range 3 {
		var mistake *model.Mistake
		if _, err := said(); !errors.As(err, &mistake) {
			t.Errorf("with a key frontseat does not take: err %v; want a mistake, each time", err)
		}
	}
	source.files["a/catalog-info.yaml"] = good
	if actions, err := said(); err != nil || actions != 1 {
		t.Errorf("mended: %d actions, err %v; want 1", actions, err)
	}
	delete(source.files, "frontseat.yaml")
	if _, err := said(); err == nil {
		t.Error("with no frontseat.yaml: want a mistake")
	}
}

// each returns the error of the first, in their order, whichever
// failed first in time.
func TestEachReportsTheFirstInOrder(t *testing.T) {
	for range 200 {
		var ran atomic.Int32
		err := each(64, func(i int) error {
			ran.Add(1)
			if i%7 == 3 {
				return fmt.Errorf("%d", i)
			}
			return nil
		})
		if err == nil || err.Error() != "3" || ran.Load() != 64 {
			t.Fatalf("err %v after %d were done; want the error of the third, and all 64 done", err, ran.Load())
		}
	}
	if err := each(0, func(int) error { return errors.New("never") }); err != nil {
		t.Errorf("of nothing: %v", err)
	}
}
