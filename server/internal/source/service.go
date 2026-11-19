package source

import "github.com/frontseat-dev/frontseat/server/internal/source/record"

import "github.com/frontseat-dev/frontseat/sdk/go/entity"

import (
	"context"
	"errors"
	"net/http"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/server/internal/refusal"
	"github.com/frontseat-dev/frontseat/server/internal/source/told"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
)

// refused is how a source's refusal to be brought is answered: what was
// asked is at fault where the daemon cannot hold it, and that it is
// bringing another, or brings nothing, otherwise.
func refused(err error) error {
	switch {
	case errors.Is(err, errPopulating):
		return refuse(connect.CodeFailedPrecondition, told.Populating, err.Error())
	case errors.Is(err, errMounted):
		return refuse(connect.CodeFailedPrecondition, told.Mounted, err.Error())
	}
	return refuse(connect.CodeInvalidArgument, told.Unfit, err.Error())
}

// refuse is a refusal of the source service, with its reason.
func refuse(code connect.Code, reason, message string) *connect.Error {
	return refusal.New(refusal.Source, code, reason, message)
}

// brought has a daemon bring what a spec names that it does not hold
// yet: the repositories named, and where the entities the control plane
// resolved are among them, which it records. It resolves nothing itself.
// What it holds stays, and the spec's generation is the one it holds
// from then on.
func brought(held *Workspace, req *sourcev1.SourceSpec) error {
	if held.Mounted() {
		return refused(errMounted)
	}
	if req.GetGeneration() <= 0 {
		return refuse(connect.CodeInvalidArgument, told.GenerationInvalid, "a source is told at a generation, counted from one")
	}
	if len(req.GetRepositories()) == 0 {
		return refuse(connect.CodeInvalidArgument, told.Missing, "name a repository to populate the workspace from")
	}
	var checkouts []record.Checkout
	for _, r := range req.GetRepositories() {
		checkouts = append(checkouts, record.Checkout{Name: r.GetName(), Remote: r.GetRemote(), Ref: r.GetRef(), Roots: r.GetRoots()})
	}
	var located []record.Located
	for _, l := range req.GetLocated() {
		ref := entity.FromProto(l.GetRef())
		if !ref.Whole() || ref.Validate() != nil || l.GetRef().GetKind() == "" || l.GetRef().GetNamespace() == "" {
			return refuse(connect.CodeInvalidArgument, told.Unfit, "an entity is located by no whole reference: the control plane says each as kind:namespace/name")
		}
		located = append(located, record.Located{Ref: ref, Repository: l.GetRepository(), Root: l.GetRoot()})
	}
	if err := held.Populate(req.GetGeneration(), checkouts, located); err != nil {
		return refused(err)
	}
	return nil
}

// holding serves frontseat.source.v1alpha1 for the source a daemon
// holds: it brings what it is told and says what it holds. It keeps no
// spec: the control plane that tells it keeps what was asked for, and
// tells a daemon started again anew.
type holding struct {
	workspace *Workspace
}

var _ sourcev1alpha1connect.SourceServiceHandler = (*holding)(nil)

// states are a source's states as the contract names them. One that
// holds nothing was told nothing, and so is ready.
var states = map[State]sourcev1.Source_State{
	Empty:      sourcev1.Source_STATE_READY,
	Populating: sourcev1.Source_STATE_POPULATING,
	Ready:      sourcev1.Source_STATE_READY,
	Failed:     sourcev1.Source_STATE_FAILED,
}

// now returns the source as the contract spells it, and a channel that
// is closed when it changes.
func (s *holding) now() (*sourcev1.Source, <-chan struct{}) {
	held, changed := s.workspace.Now()
	out := &sourcev1.Source{State: states[held.State], Failure: held.Message, Generation: held.Generation}
	if s.workspace.Mounted() {
		out.Repositories = append(out.Repositories, &sourcev1.Repository{})
	}
	for _, r := range held.Repositories {
		out.Repositories = append(out.Repositories, &sourcev1.Repository{Name: r.Name, Remote: r.Remote, Commit: r.Commit, Roots: r.Roots})
	}
	return out, changed
}

// SetSource brings what the spec names and the daemon does not hold.
// What it holds already stays, so that telling it again changes nothing.
func (s *holding) SetSource(_ context.Context, req *sourcev1.SetSourceRequest) (*sourcev1.SetSourceResponse, error) {
	if err := brought(s.workspace, req.GetSpec()); err != nil {
		return nil, err
	}
	now, _ := s.now()
	return &sourcev1.SetSourceResponse{Source: now}, nil
}

// WaitSource returns the source once it is not being brought, or at once
// where the generation it holds is not the one asked after. A caller
// that gave a deadline is answered before it, as the source is then,
// still being brought: so that a deadline its caller reaches means the
// daemon did not answer, never that it is busy.
func (s *holding) WaitSource(ctx context.Context, req *sourcev1.WaitSourceRequest) (*sourcev1.WaitSourceResponse, error) {
	var answer <-chan time.Time
	if deadline, ok := ctx.Deadline(); ok {
		timer := time.NewTimer(time.Until(deadline) * 4 / 5)
		defer timer.Stop()
		answer = timer.C
	}
	for {
		now, changed := s.now()
		if now.GetState() != sourcev1.Source_STATE_POPULATING || now.GetGeneration() != req.GetGeneration() {
			return &sourcev1.WaitSourceResponse{Source: now}, nil
		}
		select {
		case <-changed:
		case <-answer:
			return &sourcev1.WaitSourceResponse{Source: now}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Serve is the source API (frontseat.source.v1alpha1) over the source a
// daemon holds: what a daemon serves on its source port alone, which the
// control plane dials and leads no one to. Nothing else is served there.
func Serve(held *Workspace) http.Handler {
	api := connect.NewServer()
	sourcev1alpha1connect.RegisterSourceServiceHandler(api, &holding{workspace: held})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	return mux
}
