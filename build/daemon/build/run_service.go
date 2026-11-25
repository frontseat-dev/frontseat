package build

import (
	"context"
	"errors"

	connect "connectrpc.com/connect/v2"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// runService serves the contract's RunService over the programs a
// daemon runs.
type runService struct {
	buildv1alpha1connect.UnimplementedRunServiceHandler
	programs *runs
	// asked are the runs each request id made.
	asked requests
}

var _ buildv1alpha1connect.RunServiceHandler = (*runService)(nil)

// CreateRun runs a program, and returns at once.
func (s *runService) CreateRun(ctx context.Context, request *buildv1.CreateRunRequest) (*buildv1.CreateRunResponse, error) {
	if request.GetTarget() == "" {
		return nil, refuse(connect.CodeInvalidArgument, "TARGET_MISSING", "name the program to run, as it is printed")
	}
	if _, _, err := sourceOf(s.programs.builds.workspace, nil); err != nil {
		return nil, refuse(connect.CodeFailedPrecondition, "WORKSPACE_NOT_READY", err.Error())
	}
	profile, err := profileOf(request.GetProfile())
	if err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "PROFILE_UNKNOWN", err.Error())
	}
	find := func(id string) (*programRun, bool) {
		r, err := s.programs.get(id)
		return r, err == nil
	}
	made, err := once(&s.asked, request.GetRequestId(), find, func() (*programRun, string, error) {
		made, err := s.programs.create(ctx, profile, request)
		switch {
		case errors.Is(err, errStopped):
			return nil, "", refuse(connect.CodeUnavailable, "DAEMON_ENDING", "the daemon is ending, and runs nothing")
		case errors.Is(err, errTooManyRuns):
			return nil, "", refuse(connect.CodeResourceExhausted, "RUNS_EXHAUSTED", err.Error())
		case err != nil:
			return nil, "", connect.NewError(connect.CodeInternal, err.Error())
		}
		return made, made.snapshot().GetId(), nil
	})
	if err != nil {
		return nil, err
	}
	return &buildv1.CreateRunResponse{Run: made.snapshot()}, nil
}

func (s *runService) find(id string) (*programRun, error) {
	found, err := s.programs.get(id)
	if err != nil {
		return nil, refuse(connect.CodeNotFound, "RUN_NOT_FOUND", "no run has the id asked for")
	}
	return found, nil
}

// DescribeRun returns a run as it is now.
func (s *runService) DescribeRun(_ context.Context, request *buildv1.DescribeRunRequest) (*buildv1.DescribeRunResponse, error) {
	found, err := s.find(request.GetRunId())
	if err != nil {
		return nil, err
	}
	return &buildv1.DescribeRunResponse{Run: found.snapshot()}, nil
}

// ListRuns returns the runs the daemon remembers, newest first.
func (s *runService) ListRuns(_ context.Context, request *buildv1.ListRunsRequest) (*buildv1.ListRunsResponse, error) {
	listed, next, err := paged(s.programs.list(), request.GetPageSize(), request.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &buildv1.ListRunsResponse{NextPageToken: next}
	for _, each := range listed {
		out.Runs = append(out.Runs, each.snapshot())
	}
	return out, nil
}

// CancelRun tells a run to end, and returns at once, as CancelBuild
// does: WaitRun returns once it has ended.
func (s *runService) CancelRun(_ context.Context, request *buildv1.CancelRunRequest) (*buildv1.CancelRunResponse, error) {
	found, err := s.find(request.GetRunId())
	if err != nil {
		return nil, err
	}
	found.end()
	return &buildv1.CancelRunResponse{Run: found.snapshot()}, nil
}

// WaitRun returns a run once it has ended.
func (s *runService) WaitRun(ctx context.Context, request *buildv1.WaitRunRequest) (*buildv1.WaitRunResponse, error) {
	found, err := s.find(request.GetRunId())
	if err != nil {
		return nil, err
	}
	select {
	case <-found.ended:
		return &buildv1.WaitRunResponse{Run: found.snapshot()}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// StreamRunLogs sends what a run's program prints, as it prints
// it, until the run has ended and every line is sent.
func (s *runService) StreamRunLogs(ctx context.Context, request *buildv1.StreamRunLogsRequest, stream buildv1alpha1connect.RunServiceStreamRunLogsServerStream) error {
	found, err := s.find(request.GetRunId())
	if err != nil {
		return err
	}
	var next int64
	for {
		var lines []*typev1.LogLine
		var ended bool
		var changed <-chan struct{}
		lines, next, ended, changed = found.since(next)
		// What is kept is sent in parts no larger than a part of an
		// action's output is.
		for len(lines) > 0 {
			size, n := 0, 0
			for n < len(lines) && (n == 0 || size+sent(lines[n]) <= part) {
				size += sent(lines[n])
				n++
			}
			if err := stream.Send(&buildv1.StreamRunLogsResponse{Lines: lines[:n]}); err != nil {
				return err
			}
			lines = lines[n:]
		}
		if ended {
			// Lines added between reading them and seeing it ended are
			// read once more: nothing is added after it has.
			if more, _, _, _ := found.since(next); len(more) == 0 {
				return nil
			}
			continue
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
