package evaluate

import (
	"bytes"
	"context"
	"errors"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/record"
	"testing"
	"time"
)

// newSession is a session NewSession made of what it is given; a
// session that cannot be made fails the test. Closing it is the caller's.
func newSession(t testing.TB, plugins []*pluginhost.Plugin, limits RetentionLimits, admitted int, answers *Answers, provide func(context.Context) (Source, []record.Located, error)) *Session {
	t.Helper()
	s, err := NewSession(plugins, limits, admitted, answers, provide)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// takesTree gives a session a snapshot of the whole directory root, taken
// through files, and no entities.
func takesTree(files *source.Keeper, root string) func(context.Context) (Source, []record.Located, error) {
	return func(ctx context.Context) (Source, []record.Located, error) {
		snapshot, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
		return snapshot, nil, err
	}
}

// takesNoSource is for a session that is only asked to build: a build
// reads its run's snapshot, and never asks the session for one.
func takesNoSource(context.Context) (Source, []record.Located, error) {
	panic("a build uses its run's snapshot")
}

func TestRetainedSessionCloseCancelsAndJoinsSnapshotAcquisition(t *testing.T) {
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s := newSession(t, nil, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		return nil, nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := s.Projects(ctx); result <- err }()
	<-entered
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-cancelled:
	case <-closed:
		cancel()
		close(release)
		<-result
		t.Fatal("Close returned without cancelling active source acquisition")
	case <-time.After(5 * time.Second):
		cancel()
		close(release)
		<-result
		t.Fatal("Close did not cancel source acquisition")
	}
	select {
	case <-closed:
		close(release)
		<-result
		t.Fatal("Close returned before source cleanup completed")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("active call: %v", err)
	}
	if _, err := s.Projects(t.Context()); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session accepted a call: %v", err)
	}
}

func TestRetainedSessionSuppliedEntitiesInvalidateFacts(t *testing.T) {
	root, plugins, files := sessionFixture(t, 2)
	entities := []record.Located{{Ref: entity.New("component", "default", "first"), Root: "m0000"}}
	s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
		src, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
		return src, entities, err
	})
	defer s.Close()
	for i := range 3 {
		got, err := s.Facts(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := Evaluate(t.Context(), retainedTake(t, &source.Keeper{}, root), plugins, &Answers{}, typev1.Profile_PROFILE_DEVELOPMENT, entities)
		if err != nil {
			t.Fatal(err)
		}
		a, b := retainedSemantics(t, got, fresh)
		if !bytes.Equal(a, b) {
			t.Fatalf("supplied workspace entities stale at change %d", i)
		}
		if i == 0 {
			entities[0].Ref.Name = "second"
			entities[0].Root = "m0001"
		} else {
			entities = nil
		}
	}
}
