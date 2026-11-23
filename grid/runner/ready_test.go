package runner_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/buildbarn/bb-remote-execution/pkg/proto/buildqueuestate"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/frontseat-dev/frontseat/grid/runner"
)

// queues is a scheduler that says the workers each of its queues has.
type queues struct {
	buildqueuestate.UnimplementedBuildQueueStateServer
	workers [][]uint32
}

func (q queues) ListPlatformQueues(context.Context, *emptypb.Empty) (*buildqueuestate.ListPlatformQueuesResponse, error) {
	said := &buildqueuestate.ListPlatformQueuesResponse{}
	for _, sizes := range q.workers {
		queue := &buildqueuestate.PlatformQueueState{}
		for _, count := range sizes {
			queue.SizeClassQueues = append(queue.SizeClassQueues, &buildqueuestate.SizeClassQueueState{WorkersCount: count})
		}
		said.PlatformQueues = append(said.PlatformQueues, queue)
	}
	return said, nil
}

// scheduler serves a scheduler's state on the loopback and returns its
// address.
func scheduler(t *testing.T, q queues) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	buildqueuestate.RegisterBuildQueueStateServer(server, q)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// A grid is ready once its scheduler has every worker it is to have, in
// whichever of its queues: an action asked before then is refused for
// want of a worker, which a grid just started says of every action. One
// with fewer is not ready, and says how many it has; nor is one that
// does not answer. The control is the same scheduler asked for the
// workers it has.
func TestAGridIsReadyOnceItsSchedulerHasEveryWorker(t *testing.T) {
	at := scheduler(t, queues{workers: [][]uint32{{6}, {2}, {4}}})
	if err := runner.Ready(t.Context(), at, 12); err != nil {
		t.Fatalf("the control: a scheduler with its twelve workers is not ready: %v", err)
	}
	if err := runner.Ready(t.Context(), at, 13); err == nil || !strings.Contains(err.Error(), "12 of 13") {
		t.Errorf("a scheduler with twelve of thirteen workers: %v; want it not ready, saying how many it has", err)
	}
	if err := runner.Ready(t.Context(), scheduler(t, queues{}), 1); err == nil || !strings.Contains(err.Error(), "0 of 1") {
		t.Errorf("a scheduler with no queue yet: %v; want it not ready", err)
	}
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = gone.Close()
	if err := runner.Ready(t.Context(), gone.Addr().String(), 1); err == nil {
		t.Error("a scheduler that does not answer is ready")
	}
}
