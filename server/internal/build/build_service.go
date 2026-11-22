package build

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	connect "connectrpc.com/connect/v2"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// buildService serves the contract's BuildService over a daemon's
// builds.
type buildService struct {
	buildv1alpha1connect.UnimplementedBuildServiceHandler
	runs *runs
	// asked are the runs each request id made.
	asked requests
}

var _ buildv1alpha1connect.BuildServiceHandler = (*buildService)(nil)

func (s *buildService) CreateBuild(ctx context.Context, req *buildv1.CreateBuildRequest) (*buildv1.CreateBuildResponse, error) {
	if _, err := sourceOf(s.runs.workspace); err != nil {
		return nil, refuse(connect.CodeFailedPrecondition, "WORKSPACE_NOT_READY", err.Error())
	}
	profile, base, err := profileOf(req.GetProfile())
	if err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "PROFILE_UNKNOWN", err.Error())
	}
	find := func(id string) (*run, bool) {
		r, err := s.runs.get(id)
		return r, err == nil
	}
	r, err := once(&s.asked, req.GetRequestId(), find, func() (*run, string, error) {
		r, err := s.runs.create(ctx, profile, base, req.GetTargets(), req.GetOnly(), req.GetFix(), req.GetTests(), nil, nil)
		switch {
		case errors.Is(err, errStopped):
			return nil, "", refuse(connect.CodeUnavailable, "DAEMON_ENDING", err.Error())
		case err != nil:
			return nil, "", connect.NewError(connect.CodeInternal, err.Error())
		}
		return r, r.snapshot().GetId(), nil
	})
	if err != nil {
		return nil, err
	}
	return &buildv1.CreateBuildResponse{Build: r.snapshot()}, nil
}

func (s *buildService) find(id string) (*run, error) {
	r, err := s.runs.get(id)
	if errors.Is(err, errNotFound) {
		return nil, refuse(connect.CodeNotFound, "BUILD_NOT_FOUND", fmt.Sprintf("no build has the id %q", id))
	}
	return r, err
}

func (s *buildService) DescribeBuild(_ context.Context, req *buildv1.DescribeBuildRequest) (*buildv1.DescribeBuildResponse, error) {
	r, err := s.find(req.GetBuildId())
	if err != nil {
		return nil, err
	}
	return &buildv1.DescribeBuildResponse{Build: r.snapshot()}, nil
}

func (s *buildService) ListBuilds(_ context.Context, req *buildv1.ListBuildsRequest) (*buildv1.ListBuildsResponse, error) {
	listed, next, err := paged(s.runs.list(), req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &buildv1.ListBuildsResponse{NextPageToken: next}
	for _, r := range listed {
		out.Builds = append(out.Builds, r.snapshot())
	}
	return out, nil
}

func (s *buildService) CancelBuild(_ context.Context, req *buildv1.CancelBuildRequest) (*buildv1.CancelBuildResponse, error) {
	r, err := s.find(req.GetBuildId())
	if err != nil {
		return nil, err
	}
	r.cancel()
	return &buildv1.CancelBuildResponse{Build: r.snapshot()}, nil
}

func (s *buildService) WaitBuild(ctx context.Context, req *buildv1.WaitBuildRequest) (*buildv1.WaitBuildResponse, error) {
	r, err := s.find(req.GetBuildId())
	if err != nil {
		return nil, err
	}
	if !r.wait(ctx.Done()) {
		return nil, ctx.Err()
	}
	return &buildv1.WaitBuildResponse{Build: r.snapshot()}, nil
}

func (s *buildService) StreamBuildEvents(ctx context.Context, req *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	r, err := s.find(req.GetBuildId())
	if err != nil {
		return err
	}
	next := req.GetFromSequence()
	if next < 1 {
		next = 1
	}
	if last := r.snapshot().GetLastSequence(); r.ended() && next > last+1 {
		return refuse(connect.CodeOutOfRange, "SEQUENCE_OUT_OF_RANGE", fmt.Sprintf("the run's last event is %d", last))
	}
	for {
		events, ended, changed := r.since(next)
		if len(events) > 0 {
			if err := stream.Send(&buildv1.StreamBuildEventsResponse{Events: events}); err != nil {
				return err
			}
			next += int64(len(events))
		}
		if ended && len(events) == 0 {
			return nil
		}
		if ended {
			continue
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *buildService) ListBuildActions(_ context.Context, req *buildv1.ListBuildActionsRequest) (*buildv1.ListBuildActionsResponse, error) {
	r, err := s.find(req.GetBuildId())
	if err != nil {
		return nil, err
	}
	listed, next, err := paged(r.actions(), req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	return &buildv1.ListBuildActionsResponse{Actions: listed, NextPageToken: next}, nil
}

// part is how much of what an action printed is sent at once, in bytes:
// what Buildbarn sends of a blob at once. A line longer than that is a
// part of its own.
const part = 1 << 16

func (s *buildService) StreamBuildLogs(ctx context.Context, req *buildv1.StreamBuildLogsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildLogsServerStream) error {
	return s.logs(ctx, req, stream.Send)
}

// logs sends what one action printed, in parts of whole
// lines. It is read as it is sent, so that what is held of it is a part
// and a line, however much the action printed.
func (s *buildService) logs(ctx context.Context, req *buildv1.StreamBuildLogsRequest, send func(*buildv1.StreamBuildLogsResponse) error) error {
	r, err := s.find(req.GetBuildId())
	if err != nil {
		return err
	}
	for _, action := range r.actions() {
		if action.GetLabel() != req.GetLabel() {
			continue
		}
		// Both streams are opened before any of either is sent: an
		// action whose output is not there is an error, and no part of
		// it.
		streams := []struct {
			of     typev1.LogStream
			digest *buildv1.Digest
			from   io.ReadCloser
		}{{of: typev1.LogStream_LOG_STREAM_STDOUT, digest: action.GetStdout()}, {of: typev1.LogStream_LOG_STREAM_STDERR, digest: action.GetStderr()}}
		for i := range streams {
			if streams[i].from, err = r.printed(streams[i].digest); err != nil {
				return connect.NewError(connect.CodeInternal, fmt.Sprintf("what %s printed: %v", action.GetLabel(), err))
			}
			if streams[i].from != nil {
				defer streams[i].from.Close()
			}
		}
		sending := parts{send: send}
		for _, stream := range streams {
			if stream.from == nil {
				continue
			}
			if err := lines(ctx, stream.from, func(line []byte) error { return sending.add(stream.of, line) }); err != nil {
				return err
			}
		}
		return sending.flush()
	}
	return refuse(connect.CodeNotFound, "ACTION_NOT_FOUND", fmt.Sprintf("the run has no action %q", req.GetLabel()))
}

// parts gathers lines into parts and sends each as it fills.
type parts struct {
	send func(*buildv1.StreamBuildLogsResponse) error
	held []*typev1.LogLine
	size int
}

// add gathers a line, sending what was gathered first where the line
// would make it more than a part. What of the line is not UTF-8, which
// a message's text has to be, is given as U+FFFD, as Docker gives it.
func (p *parts) add(stream typev1.LogStream, text []byte) error {
	line := &typev1.LogLine{Stream: stream, Text: strings.ToValidUTF8(string(text), "\uFFFD")}
	if len(p.held) > 0 && p.size+sent(line) > part {
		if err := p.flush(); err != nil {
			return err
		}
	}
	p.held, p.size = append(p.held, line), p.size+sent(line)
	return nil
}

// flush sends what was gathered, if anything was.
func (p *parts) flush() error {
	if len(p.held) == 0 {
		return nil
	}
	err := p.send(&buildv1.StreamBuildLogsResponse{Lines: p.held})
	p.held, p.size = nil, 0
	return err
}

// sent is no less than the bytes a line is in a message: its text, and
// what names it, its stream and its length.
func sent(line *typev1.LogLine) int { return len(line.GetText()) + 16 }

// lines gives each line of what an action printed to a stream, whole and
// without what ends it: a line of any length, and the last where no
// newline ends it. It stops when whoever asked has gone.
func lines(ctx context.Context, from io.Reader, each func(line []byte) error) error {
	reader := bufio.NewReaderSize(from, part)
	var long []byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		piece, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, piece...)
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return connect.NewError(connect.CodeInternal, fmt.Sprintf("reading what was printed: %v", err))
		}
		line := piece
		if len(long) > 0 {
			line = append(long, piece...)
			long = nil
		}
		if len(line) > 0 {
			if err := each(bytes.TrimRight(line, "\r\n")); err != nil {
				return err
			}
		}
		if err != nil {
			return nil
		}
	}
}
