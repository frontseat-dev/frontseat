package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/buildbarn/bb-remote-execution/pkg/proto/buildqueuestate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Ready returns nil once the grid's scheduler, asked for its state at an
// address of its own loopback, has the workers it is to have, counted
// over all its queues: a scheduler refuses an action of a platform it
// has no worker of, so a grid that answers is not yet one that builds.
// It asks once, and says how many workers there are where there are too
// few.
func Ready(ctx context.Context, scheduler string, workers int) error {
	conn, err := grpc.NewClient("passthrough:///"+scheduler, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("the scheduler: %w", err)
	}
	defer conn.Close()
	ctx, done := context.WithTimeout(ctx, 10*time.Second)
	defer done()
	said, err := buildqueuestate.NewBuildQueueStateClient(conn).ListPlatformQueues(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("the scheduler does not say what workers it has: %w", err)
	}
	has := 0
	for _, queue := range said.GetPlatformQueues() {
		for _, size := range queue.GetSizeClassQueues() {
			has += int(size.GetWorkersCount())
		}
	}
	if has < workers {
		return fmt.Errorf("the scheduler has %d of %d workers", has, workers)
	}
	return nil
}
