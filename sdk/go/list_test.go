package frontseat

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// lister is a daemon that lists its builds a page at a time, counting
// the pages it is asked for, and describes its runs.
type lister struct {
	daemon
	pages     atomic.Int32
	described atomic.Int32
}

func (l *lister) ListBuilds(_ context.Context, r *buildv1.ListBuildsRequest) (*buildv1.ListBuildsResponse, error) {
	l.pages.Add(1)
	if r.GetPageToken() == "" {
		return &buildv1.ListBuildsResponse{Builds: []*buildv1.Build{{Id: "build_1"}, {Id: "build_2"}}, NextPageToken: "more"}, nil
	}
	return &buildv1.ListBuildsResponse{Builds: []*buildv1.Build{{Id: "build_3"}}}, nil
}

func (l *lister) DescribeRun(_ context.Context, r *buildv1.DescribeRunRequest) (*buildv1.DescribeRunResponse, error) {
	l.described.Add(1)
	return &buildv1.DescribeRunResponse{Run: &buildv1.Run{Id: r.GetRunId(), BuildId: "build_9"}}, nil
}

// A list is asked for a page at a time, as it is read: one read to its
// end asks for every page, and one left after its first item asks for
// the first alone.
func TestAListIsAskedForAPageAtATimeAsItIsRead(t *testing.T) {
	l := &lister{}
	here := servedDaemon(t, l)
	ctx := context.Background()
	var ids []string
	for build, err := range here.Builds(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, build.GetId())
	}
	if !slices.Equal(ids, []string{"build_1", "build_2", "build_3"}) || l.pages.Load() != 2 {
		t.Errorf("read %v over %d pages, want three builds over two", ids, l.pages.Load())
	}
	l.pages.Store(0)
	for range here.Builds(ctx) {
		break
	}
	if l.pages.Load() != 1 {
		t.Errorf("a list left after its first item asked for %d pages, want one", l.pages.Load())
	}
}

// A run made by Run knows its build's id as the daemon answered it, and
// asks nothing more; one by its id alone asks the daemon once.
func TestARunsBuildIsKnownAsItWasAnsweredOrAsked(t *testing.T) {
	l := &lister{}
	here := servedDaemon(t, l)
	ctx := context.Background()
	run, err := here.Run(ctx, RunRequest{Target: "//a:go.run/a"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := run.BuildID(ctx); err != nil || id != "build_1" || run.ID() != "run_1" || l.described.Load() != 0 {
		t.Errorf("a run made is of %q (%v), asking %d times", id, err, l.described.Load())
	}
	if id, err := here.RunByID("run_7").BuildID(ctx); err != nil || id != "build_9" || l.described.Load() != 1 {
		t.Errorf("a run by its id is of %q (%v), asking %d times", id, err, l.described.Load())
	}
}
