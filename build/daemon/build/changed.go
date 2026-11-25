package build

import (
	"context"
	"net/http"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// lookEvery is how often a wait for what a build read to change looks
// again at it, where looking takes little time (source.Looks.Await).
const lookEvery = 250 * time.Millisecond

// readKept is how many of the builds that have ended keep what they
// read, the newest: a build's listing and what it read by name, which
// is as much as the directory has files, kept for whoever waits for it
// to change, who waits on the build it made last. Every build that has
// not ended keeps it.
const readKept = 4

// read is what a run read, for whoever waits for it to change: the
// workspace's manifest, looked at before the run read it, and, as far
// as the run got, what it listed and read of its repositories.
type read struct {
	manifest *source.Looks
	// snapshot is what the run listed and read; nil where it did not
	// get as far. unread is, where the run could not list its
	// repositories, what could be listed of them when it failed to
	// (source.Unread); nil where it did not get as far.
	snapshot *source.Snapshot
	unread   *source.Looks
}

// reading tells the run what it read as it reads it.
func (r *run) reading(as func(*read)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.read != nil {
		as(r.read)
	}
}

// readLooks is how what the run read looked to it, with what the daemon
// is made of as the daemon looked at it before it read it, a Looks of
// its own for each who waits; nil where the daemon no longer keeps it,
// or never did: the run is one a daemon before it made. A run that got
// no further than its manifest read none of its repositories, and is
// held to the manifest alone.
func (r *run) readLooks(made *source.Looks) *source.Looks {
	r.mu.Lock()
	of := r.read
	r.mu.Unlock()
	switch {
	case of == nil:
		return nil
	case of.snapshot != nil:
		return of.snapshot.Looks(made, of.manifest)
	}
	return of.unread.With(made, of.manifest)
}

// forget lets go of what the run read: whoever waits for it to change is
// refused (BUILD_SOURCE_NOT_KEPT).
func (r *run) forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.read = nil
}

func (s *buildService) WaitSourceChanged(ctx context.Context, req *buildv1.WaitSourceChangedRequest) (*buildv1.WaitSourceChangedResponse, error) {
	r, err := s.find(req.GetBuildId())
	if err != nil {
		return nil, err
	}
	// What a run read is all it read once it has ended.
	if !r.wait(ctx.Done()) {
		return nil, ctx.Err()
	}
	looks := r.readLooks(s.runs.made)
	if looks == nil {
		return nil, refuse(connect.CodeFailedPrecondition, "BUILD_SOURCE_NOT_KEPT", "the daemon no longer keeps what the build read: a build made now reads the files as they are")
	}
	changed, err := looks.Await(ctx, s.runs.every)
	if err != nil {
		return nil, err
	}
	return &buildv1.WaitSourceChangedResponse{SourceChanged: &buildv1.SourceChanged{BuildId: req.GetBuildId(), Paths: changed}}, nil
}

// Yields says whether a call gives way to whoever would end the daemon
// to start another: a wait for what a build read to change, which
// keeps the daemon from ending itself for having nothing to do, and is
// ended, and its caller asks the daemon there is then. Every other call
// under way is someone's, and keeps the daemon.
func (d *Daemon) Yields(r *http.Request) bool {
	return r.URL.Path == buildv1alpha1connect.BuildServiceWaitSourceChangedProcedure
}
