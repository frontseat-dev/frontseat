// Package controlplane serves workspaces as a control plane keeps them
// (frontseat.workspace.v1alpha1), over a sandbox service it knows and
// that knows nothing of it. It alone serves that API, and is the one
// writer of what a workspace is asked for. A workspace is kept as what
// was asked for; the control plane then works until what is so says the
// same: it makes the workspace one sandbox, written in the sandbox
// layer's own terms, tells the supervisor there what the workspace is to
// hold through the supervisor's source API (frontseat.source.v1alpha1),
// at a generation it keeps, and reads back what the supervisor holds. It
// goes on from what was asked for after it or the supervisor was
// stopped. A workspace's owner is carried, as bytes, to its shell and to
// a port of its sandbox; to the supervisor's source API, no one is. The
// control plane neither serves nor forwards the build API: a workspace
// is built from inside it, by a build daemon the command line starts
// there.
//
// Every call is from someone: a call that presents no token its
// Authenticator knows is refused before anything is asked of it. The
// sandbox service is the control plane's own, in its process, and is
// served to no one.
package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/frontseat-dev/frontseat/common/record/records"
	"github.com/frontseat-dev/frontseat/common/told"
	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
	"github.com/frontseat-dev/frontseat/common/vcs"
	"github.com/frontseat-dev/frontseat/core/page"
	"github.com/frontseat-dev/frontseat/core/plain"
	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/core/refusal"
	"github.com/frontseat-dev/frontseat/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1/credentialv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/workspace/server/backstage"
	"github.com/frontseat-dev/frontseat/workspace/server/controlplane/identity"
	"github.com/frontseat-dev/frontseat/workspace/server/controlplane/internal/ownertoken"
	"github.com/frontseat-dev/frontseat/workspace/server/workspaces/sources"
)

// Config is what a control plane is served with.
type Config struct {
	// Sandboxes makes the sandboxes workspaces live in. It is the control
	// plane's own: none of it is served.
	Sandboxes sandbox.Service
	// Callers says who a call is from; a control plane answers no call
	// it does not know the caller of.
	Callers identity.Authenticator
	// PublicURL is the address clients reach the control plane at.
	PublicURL string
	// State is a directory of the control plane's own, where its
	// workspaces are kept for the one that comes after.
	State string
	// Box is the image a workspace's sandbox runs where none is asked
	// for; empty where one must be.
	Box string
	// Builder is where the program that builds is in a box: the command
	// line, which is the build daemon it starts too, and the program a
	// workspace's way to the grid is for.
	Builder string
	// Grid is the grid every workspace is built on.
	Grid Grid
	// Environment is what every workspace's sandbox is given beside, by
	// its operator: what the programs there read of their environment
	// and are told no other way (OpenTelemetry's exporters).
	Environment map[string]string
	// Fetching is what the version control system says of a repository
	// fetched from a sandbox; with none, no workspace fetches one.
	Fetching vcs.Fetching
	// Entities resolves the entities a workspace is asked for to the
	// repositories and directories that hold them: the Backstage catalog
	// its operator gives it. With none, a workspace asked for by entity
	// is refused UNIMPLEMENTED, ENTITIES_NOT_SERVED. No request names it.
	Entities Entities
	// Sources is what serves the stored repositories of a remote
	// workspace, which holds its repositories from a snapshot store and
	// clones none: what frontseat-enterprise gives the control plane it
	// composes. With none, a workspace asked for with a stored
	// repository, a task or a landing is refused UNIMPLEMENTED,
	// STORE_NOT_SERVED, and every workspace is local. No request names
	// it.
	Sources sources.Sources
	// Every is how often a workspace that is not yet as asked is worked
	// on; zero for a quarter of a second.
	Every time.Duration
	// Answers is how long a supervisor is waited for once its sandbox runs,
	// before the workspace has failed; zero for two minutes.
	Answers time.Duration
	// Starts is how long a sandbox is waited for to run, before the
	// workspace has failed; zero for five minutes, an image's pull among
	// it.
	Starts time.Duration
}

// Entities resolves entities, as a person typed them, for whoever owns
// the workspace they are asked for in: to the repositories to clone, and
// where each entity is among them. A refusal it gives by name is a
// *backstage.Refused.
type Entities interface {
	Resolve(ctx context.Context, owner string, typed []entity.Pattern) (backstage.Resolution, error)
}

// Plane is a control plane that is serving.
type Plane struct {
	// Handler serves its API to callers it knows: workspaces, and the
	// ways to each workspace's shell and the ports of its sandbox.
	http.Handler
	// Sandboxes is the sandbox service it makes sandboxes with, for the
	// process that serves it, and never served by it.
	Sandboxes sandbox.Service
	stop      func()
}

// Close stops working on workspaces, and ends every connection it is
// carrying to one (carry): it returns once each has ended, both ways,
// and nothing it started is running. The workspaces stay as they are.
func (p *Plane) Close() { p.stop() }

// Serve returns a control plane over a sandbox service, taking up the
// workspaces kept by one before it.
func Serve(c Config) (*Plane, error) {
	if c.Every == 0 {
		c.Every = 250 * time.Millisecond
	}
	if c.Answers == 0 {
		c.Answers = 2 * time.Minute
	}
	if c.Starts == 0 {
		c.Starts = 5 * time.Minute
	}
	if c.Callers == nil {
		return nil, errors.New("a control plane answers callers it knows, and was told of no way to know one")
	}
	kept, err := records.Open(c.State)
	if err != nil {
		return nil, fmt.Errorf("the workspaces' records: %w", err)
	}
	s := &service{Config: c, records: kept, held: map[string]*held{}, making: map[string]*sync.Mutex{}, woken: make(chan struct{}, 1)}
	if err := s.recover(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, s)
	credentialv1alpha1connect.RegisterCredentialServiceHandler(api, &credentials{s: s})
	connecthttp.Mount(mux, api)
	// A workspace's shell, and a port of its sandbox: bytes, carried to
	// its owner alone, by one way.
	mux.HandleFunc("/workspaces/{name}/shell", s.carried(func(*http.Request) (int, bool) { return ports.Shell, true }))
	mux.HandleFunc("/workspaces/{name}/ports/{port...}", s.carried(func(r *http.Request) (int, bool) { return plain.Port(r.PathValue("port")) }))
	ctx, cancel := context.WithCancel(context.Background())
	s.serving = ctx
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.work(ctx)
	}()
	return &Plane{Handler: s.known(mux), Sandboxes: c.Sandboxes, stop: func() {
		// No connection is carried from here on, and those that are end
		// with the context.
		s.carrying.Lock()
		s.stopped = true
		s.carrying.Unlock()
		cancel()
		<-done
		s.working.Wait()
		s.tunnels.Wait()
	}}, nil
}

// refusals writes a refusal as the caller's protocol says it.
var refusals = connecthttp.NewErrorWriter()

// refusedFor is a refusal of the workspace service, of a code, with its
// reason by name.
func refusedFor(code connect.Code, reason, message string) *connect.Error {
	return refusal.New(refusal.Workspace, code, reason, message)
}

// known answers a request only where its caller is known: one that
// presents no bearer token, or one the control plane's Authenticator
// does not know, is refused as unauthenticated, and nothing of it is
// asked. Neither the token nor anything of it is said back or logged.
func (s *service) known(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, err := s.caller(r)
		if err != nil {
			reason, said := "IDENTITY_INVALID", "the call presents a token that is not one known"
			if errors.Is(err, identity.ErrMissing) {
				reason, said = "IDENTITY_MISSING", "the call presents no token: give the owner's, as a bearer token"
			}
			_ = refusals.Write(w, r, refusal.New(refusal.ControlPlane, connect.CodeUnauthenticated, reason, said))
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.With(r.Context(), caller)))
	})
}

// caller is who a request is from, by the bearer token it presents.
func (s *service) caller(r *http.Request) (identity.Caller, error) {
	presented := r.Header.Values("Authorization")
	switch {
	case len(presented) == 0:
		return identity.Caller{}, identity.ErrMissing
	case len(presented) > 1:
		return identity.Caller{}, identity.ErrInvalid
	}
	scheme, token, _ := strings.Cut(presented[0], " ")
	if !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return identity.Caller{}, identity.ErrInvalid
	}
	caller, err := s.Callers.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, identity.ErrMissing) {
			return identity.Caller{}, identity.ErrMissing
		}
		return identity.Caller{}, identity.ErrInvalid
	}
	return caller, nil
}

// OwnerToken knows the one person a local control plane answers, its
// owner, by the token a file holds: one is minted there where there is
// none, and the file is this process's alone. Whoever presents the token
// is the owner, "local".
func OwnerToken(file string) (identity.Authenticator, error) {
	return ownertoken.Open(file)
}

type service struct {
	Config
	// records are where its workspaces are kept, one record each, named
	// by the workspace's name (common/record/records).
	records records.Dir

	mu sync.Mutex
	// held are the workspaces, by name.
	held map[string]*held
	// making holds, for a name, whoever is asking for its workspace: two
	// who ask at once are answered one after the other.
	making map[string]*sync.Mutex
	// woken wakes the work on workspaces before its time.
	woken chan struct{}
	// working are the passes under way.
	working sync.WaitGroup
	// serving ends when the control plane is closed.
	serving context.Context
	// carrying guards how many connections are carried to each workspace
	// (held.carried) and stopped, after which none is; tunnels are those
	// under way.
	carrying sync.Mutex
	stopped  bool
	tunnels  sync.WaitGroup
}

// most is how many connections the control plane carries at once to one
// workspace's sandbox, its shell's among them: each holds a stream of
// whatever provides the sandbox for as long as it lasts. A person's
// shells, and a browser's six connections to each of a handful of
// servers, are well beneath it.
const most = 64

// held is one workspace: what is kept of it, and what the last pass over
// it found.
type held struct {
	// carried is how many connections are carried to its sandbox now,
	// guarded by the service's carrying.
	carried int
	// pass is held by whoever works on the workspace: one at a time.
	pass sync.Mutex
	// stopping guards stop, which ends the pass working on it now, if
	// one is: an ending does not wait on what a pass asks of its supervisor.
	// interrupted holds that an ending was asked for and is not yet
	// recorded, so that a pass begun after it is stopped too; turn names
	// the pass whose stop it holds. An ending pass is never stopped.
	stopping    sync.Mutex
	stop        context.CancelFunc
	interrupted bool
	turn        uint64
	// What follows is read and written under pass.

	// workspace is its name, what it was asked for with, and when.
	workspace *workspacev1.Workspace
	// made is the spec its sandbox was made for.
	made *workspacev1.WorkspaceSpec
	// resolved is what its entities resolved to, each once, when it was
	// asked for: its supervisor is told from it, and it is never resolved
	// again.
	resolved told.Resolved
	// stood is what is held for it of the store, where it is remote:
	// where each stored repository stands, and the directory its sandbox
	// is given for each, as what serves them said when it was made.
	stood sources.Held
	// sandbox is the sandbox it lives in, once one was asked for.
	sandbox string
	// owner is who asked for it first: whose it is, and whose
	// credentials its sandbox is let through with.
	owner string
	// requests are what each sandbox asked for it was asked for under,
	// kept before it was asked: a sandbox is its own where it carries one
	// of them (Made), whether or not its making was answered.
	requests []string
	// ending is whether it was asked to end.
	ending bool
	// generation counts what its supervisor was told to hold: one for what it
	// was made with, and one more for each spec its supervisor took after.
	// A supervisor that holds another, started again or told otherwise from
	// inside its sandbox, is told this one.
	generation int64
	// failed is why its sandbox could not be made or its supervisor told;
	// empty while neither failed. It stands until it is asked for again.
	failed string
	// again asks that what failed be tried again, and resume that a
	// paused sandbox be gone on with.
	again, resume bool
	// running is since when its sandbox runs with no answer from its
	// supervisor; zero while it answers or does not run.
	running time.Time
	// starting is since when its sandbox was found not yet running; zero
	// once it runs.
	starting time.Time
	// settled is when it was last found as asked, to be looked at less
	// often.
	settled time.Time

	// seen is the workspace as the last pass found it, for whoever asks,
	// and living the sandbox it lived in then: what its shell and the
	// ports its owner is carried to lead to, which its status does not
	// say.
	seen   atomic.Pointer[workspacev1.Workspace]
	living atomic.Pointer[string]
}

// show is what a workspace is said to be from now on, as a pass found it
// with its pass held.
func (h *held) show(w *workspacev1.Workspace) {
	sandbox := h.sandbox
	h.living.Store(&sandbox)
	h.seen.Store(w)
}

// derived is the name given to what was asked for where none is given:
// the first entity's name, or else the first stored repository's, or
// else the first repository's, with its ref where one is given. It is ""
// where that is no name.
func derived(spec *workspacev1.WorkspaceSpec) string {
	var from string
	switch {
	case len(spec.GetEntities()) > 0:
		from = spec.GetEntities()[0].GetName()
	case len(spec.GetStored()) > 0:
		first := spec.GetStored()[0]
		if from = first.GetName(); from == "" {
			from = first.GetRepository()
		}
	case len(spec.GetRepositories()) > 0:
		first := spec.GetRepositories()[0]
		at := first.GetRemote()
		if parsed, err := url.Parse(at); err == nil && parsed.Path != "" {
			at = parsed.Path
		}
		from = strings.TrimSuffix(path.Base(strings.TrimRight(at, "/")), ".git")
		if first.GetRef() != "" {
			from += "-" + first.GetRef()
		}
	}
	var out strings.Builder
	dashed := true
	for _, r := range strings.ToLower(from) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			dashed = false
		} else if !dashed {
			out.WriteByte('-')
			dashed = true
		}
	}
	made := strings.Trim(out.String(), "-")
	if len(made) > 48 {
		made = strings.Trim(made[:48], "-")
	}
	return made
}

// source is what a spec asks a workspace to hold, apart from what it may
// do and who asks.
func source(spec *workspacev1.WorkspaceSpec) *workspacev1.WorkspaceSpec {
	return &workspacev1.WorkspaceSpec{Repositories: spec.GetRepositories(), Entities: spec.GetEntities(), WholeRepositories: spec.GetWholeRepositories(), Stored: spec.GetStored()}
}

// digest is a short name for a message, the same for the same message.
func digest(m proto.Message) string {
	raw, _ := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// more says what a workspace asked for again asks beyond what the one
// there has; "" where it asks the same or less. What is said once, an
// image, a key let into its shell, a lifetime, a task, a landing, is the
// same where it is asked for again, or not said.
func more(there, asked *workspacev1.WorkspaceSpec) string {
	switch {
	case lacks(source(asked), source(there)) != "":
		return "it holds other source than is asked for"
	case asked.GetLifetime() != nil && !proto.Equal(asked.GetLifetime(), there.GetLifetime()):
		return "it lasts otherwise than is asked for, which is said when it is made"
	case asked.GetTask() != "" && asked.GetTask() != there.GetTask():
		return "it works on another task than is asked for, or on none, which is said when it is made"
	case !there.GetLand() && asked.GetLand():
		return "it was made without landing, which is said when it is made"
	case !there.GetPush() && asked.GetPush():
		return "it was made without push, and what a workspace may reach is said when it is made"
	case asked.GetImage() != "" && asked.GetImage() != there.GetImage():
		return "it runs another image than is asked for"
	case asked.GetAuthorizedKey() != "" && asked.GetAuthorizedKey() != there.GetAuthorizedKey():
		return "it lets another key into its shell than is asked for, which is said when it is made"
	}
	return ""
}

func answered(err error) error {
	var refused *unmet
	if errors.As(err, &refused) {
		return refusedFor(refused.code, refused.reason, refused.why)
	}
	if connect.CodeOf(err) != connect.CodeUnknown {
		return err
	}
	return connect.NewError(connect.CodeInternal, err.Error())
}

func (s *service) CreateWorkspace(ctx context.Context, req *workspacev1.CreateWorkspaceRequest) (*workspacev1.CreateWorkspaceResponse, error) {
	spec := req.GetSpec()
	// What is not served is said first, by name, and nothing is made.
	if err := s.storeServed(spec); err != nil {
		return nil, err
	}
	if len(spec.GetRepositories()) == 0 && len(spec.GetEntities()) == 0 && len(spec.GetStored()) == 0 {
		return nil, refusedFor(connect.CodeInvalidArgument, "SOURCE_MISSING", "a workspace is asked for with a repository, an entity or a stored repository, and this one with none")
	}
	if err := s.addressed(spec); err != nil {
		return nil, answered(err)
	}
	if life := spec.GetLifetime(); life != nil && (!life.IsValid() || life.AsDuration() <= 0) {
		return nil, refusedFor(connect.CodeInvalidArgument, "LIFETIME_INVALID", "a workspace's lifetime is a while to come")
	}
	if err := s.served(spec.GetEntities()); err != nil {
		return nil, err
	}
	called := req.GetName()
	if called == "" {
		if called = derived(spec); called == "" {
			return nil, refusedFor(connect.CodeInvalidArgument, "NAME_INVALID", "what is asked for gives the workspace no name: give one")
		}
	}
	if !frontseat.IsWorkspaceName(called) {
		return nil, refusedFor(connect.CodeInvalidArgument, "NAME_INVALID", "the name is no workspace's: one is lower-case letters, digits and dashes, at most 48")
	}
	// One at a time for a name: whoever asks second finds the first's.
	s.mu.Lock()
	one := s.making[called]
	if one == nil {
		one = &sync.Mutex{}
		s.making[called] = one
	}
	s.mu.Unlock()
	one.Lock()
	defer one.Unlock()

	s.mu.Lock()
	there := s.held[called]
	s.mu.Unlock()
	if there != nil {
		if err := ownedBy(ctx, there); err != nil {
			return nil, err
		}
		there.pass.Lock()
		was := there.workspace.GetSpec()
		ending := there.ending
		there.pass.Unlock()
		if ending {
			return nil, refusedFor(connect.CodeFailedPrecondition, "WORKSPACE_ENDING", fmt.Sprintf("the workspace %s is ending: it is asked for again once it is gone", called))
		}
		if differs := more(was, spec); differs != "" {
			return nil, refusedFor(connect.CodeAlreadyExists, "WORKSPACE_DIFFERS", fmt.Sprintf("the workspace %s is there, and %s: it is not made again, so ask for it as it is, or give another name", called, differs))
		}
		// It is returned as it was found, which says whether it was whole;
		// one that was paused is gone on with, and one that failed is
		// tried again.
		found := s.pass(ctx, there)
		there.pass.Lock()
		there.again, there.resume = true, true
		there.pass.Unlock()
		s.wake()
		return &workspacev1.CreateWorkspaceResponse{Workspace: found}, nil
	}

	// What cannot be given is said now, and nothing is kept of it.
	kept := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	// A workspace that lands, told no task, lands on one of its own name.
	if kept.GetLand() && kept.GetTask() == "" {
		kept.Task = called
	}
	owner, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	resolved, err := s.resolve(ctx, owner, kept.GetEntities(), told.Resolved{})
	if err != nil {
		return nil, err
	}
	// What it holds of the store is cloned no more.
	asked, resolved, err := s.storedOf(kept, resolved)
	if err != nil {
		return nil, answered(err)
	}
	if err := s.heldOnce(kept, resolved); err != nil {
		return nil, err
	}
	in, err := s.sandbox(ctx, called, owner, kept, resolved, sources.Held{})
	if err != nil {
		return nil, answered(err)
	}
	kept.Image = in.GetImage()
	made := &held{workspace: &workspacev1.Workspace{Name: called, Spec: kept, CreateTime: timestamppb.Now()}, made: kept, resolved: resolved, owner: owner, generation: 1}
	// Its stored repositories are held last, once nothing else of what is
	// asked can be refused, and once: what came of it is kept with the
	// workspace, and let go of where the workspace cannot be kept.
	if len(asked) > 0 {
		if made.stood, err = s.hold(ctx, sources.Workspace{Name: called, Owner: owner, Stored: asked, Land: kept.GetLand()}); err != nil {
			return nil, err
		}
	}
	if err := s.keep(made); err != nil {
		if holds(made.stood) {
			_ = s.release(ctx, called, owner)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	first := s.view(made, &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING})
	made.show(first)
	s.mu.Lock()
	s.held[called] = made
	s.mu.Unlock()
	s.wake()
	return &workspacev1.CreateWorkspaceResponse{Workspace: first, Created: true}, nil
}

// view is a workspace as it is said to whoever asks: what is kept of it,
// and a status.
func (s *service) view(h *held, status *workspacev1.WorkspaceStatus) *workspacev1.Workspace {
	out := proto.Clone(h.workspace).(*workspacev1.Workspace)
	status.Entities = told.Entities(h.resolved)
	out.Status, out.Owner = status, h.owner
	return out
}

// owned is the workspace of a name, where it is the caller's: one
// another owns is refused, PERMISSION_DENIED with NOT_OWNER.
func (s *service) owned(ctx context.Context, called string) (*held, error) {
	h, err := s.named(called)
	if err != nil {
		return nil, err
	}
	if err := ownedBy(ctx, h); err != nil {
		return nil, err
	}
	return h, nil
}

// ownedBy refuses a workspace the caller does not own.
func ownedBy(ctx context.Context, h *held) error {
	who, err := caller(ctx)
	if err != nil {
		return err
	}
	if h.owner != who {
		return refusedFor(connect.CodePermissionDenied, "NOT_OWNER", fmt.Sprintf("the workspace %s is another's", h.workspace.GetName()))
	}
	return nil
}

func (s *service) named(called string) (*held, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.held[called]
	if h == nil {
		return nil, refusedFor(connect.CodeNotFound, "WORKSPACE_NOT_FOUND", "there is no workspace of the name asked for")
	}
	return h, nil
}

func (s *service) DescribeWorkspace(ctx context.Context, req *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	h, err := s.owned(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	// As the last pass found it: describing works on nothing.
	return &workspacev1.DescribeWorkspaceResponse{Workspace: h.seen.Load()}, nil
}

func (s *service) ListWorkspaces(ctx context.Context, req *workspacev1.ListWorkspacesRequest) (*workspacev1.ListWorkspacesResponse, error) {
	who, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	var all []*held
	for _, h := range s.held {
		if h.owner == who {
			all = append(all, h)
		}
	}
	s.mu.Unlock()
	out := &workspacev1.ListWorkspacesResponse{}
	for _, h := range all {
		// As the last pass found each: a list is not held up by one
		// workspace whose supervisor is slow to answer.
		if seen := h.seen.Load(); seen != nil {
			out.Workspaces = append(out.Workspaces, seen)
		}
	}
	sort.Slice(out.Workspaces, func(i, j int) bool {
		if one, other := out.Workspaces[i].GetCreateTime().AsTime(), out.Workspaces[j].GetCreateTime().AsTime(); !one.Equal(other) {
			return one.Before(other)
		}
		return out.Workspaces[i].GetName() < out.Workspaces[j].GetName()
	})
	kept, next, err := page.Of(out.Workspaces, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, refusedFor(connect.CodeInvalidArgument, refusal.PageTokenInvalid, err.Error())
	}
	out.Workspaces, out.NextPageToken = kept, next
	return out, nil
}

func (s *service) WaitWorkspace(ctx context.Context, req *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	var ended *workspacev1.Workspace
	err := poll.Until(ctx, s.Every, func() (bool, error) {
		h, err := s.owned(ctx, req.GetName())
		if err != nil {
			return false, err
		}
		// What it is and whether more is asked of it are read together:
		// read apart, a pass between them would leave a state it no longer
		// is beside nothing left to do, and waiting would end on it.
		h.pass.Lock()
		now, pending := h.seen.Load(), h.again || h.resume
		h.pass.Unlock()
		// A workspace that is paused is gone on with only when it is
		// asked for, so waiting for it ends there too.
		switch now.GetStatus().GetState() {
		case workspacev1.WorkspaceStatus_STATE_READY, workspacev1.WorkspaceStatus_STATE_FAILED, workspacev1.WorkspaceStatus_STATE_PAUSED:
			if !pending {
				ended = now
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return &workspacev1.WaitWorkspaceResponse{Workspace: ended}, nil
}

func (s *service) UpdateWorkspace(ctx context.Context, req *workspacev1.UpdateWorkspaceRequest) (*workspacev1.UpdateWorkspaceResponse, error) {
	h, err := s.owned(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	wanted := req.GetSpec()
	if err := s.storeServed(wanted); err != nil {
		return nil, err
	}
	if err := s.addressed(wanted); err != nil {
		return nil, answered(err)
	}
	h.pass.Lock()
	defer h.pass.Unlock()
	if h.ending {
		return nil, refusedFor(connect.CodeFailedPrecondition, "WORKSPACE_ENDING", fmt.Sprintf("the workspace %s is ending", h.workspace.GetName()))
	}
	has := h.workspace.GetSpec()
	if left := lacks(has, wanted); left != "" {
		return nil, refusedFor(connect.CodeInvalidArgument, "SPEC_INCOMPLETE", "the spec leaves out "+left+", which the workspace has: one is given whole, with what is added")
	}
	once := func(spec *workspacev1.WorkspaceSpec) *workspacev1.WorkspaceSpec {
		return &workspacev1.WorkspaceSpec{Push: spec.GetPush(), Author: spec.GetAuthor(), Image: spec.GetImage(), Lifetime: spec.GetLifetime(), AuthorizedKey: spec.GetAuthorizedKey(), Stored: spec.GetStored(), Task: spec.GetTask(), Land: spec.GetLand()}
	}
	asked := once(wanted)
	if asked.GetImage() == "" {
		asked.Image = has.GetImage()
	}
	if !proto.Equal(once(has), asked) {
		return nil, refusedFor(connect.CodeInvalidArgument, "SPEC_FIXED", "the spec changes what is said once of a workspace: whether it pushes, who commits, whose key gets in, its image, its lifetime, its stored repositories, its task, whether it lands")
	}
	if holds(h.stood) {
		if added := fixed(has, wanted); added != "" {
			return nil, refusedFor(connect.CodeInvalidArgument, "SPEC_FIXED", added)
		}
	}
	// What is added is told its supervisor now, and kept only where the
	// supervisor took it: what it would refuse is refused here, and is never
	// kept to be told again and again.
	if h.sandbox == "" || h.seen.Load().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		return nil, refusedFor(connect.CodeFailedPrecondition, "WORKSPACE_NOT_READY", fmt.Sprintf("the workspace %s is not ready, and is brought more once it is", h.workspace.GetName()))
	}
	// The entities it had are not resolved again: those added are.
	var added []*typev1.EntityRef
	for _, each := range wanted.GetEntities() {
		if !slices.ContainsFunc(has.GetEntities(), func(had *typev1.EntityRef) bool { return proto.Equal(had, each) }) {
			added = append(added, each)
		}
	}
	resolved, err := s.resolve(ctx, h.owner, added, h.resolved)
	if err != nil {
		return nil, err
	}
	if beyond := reaches(h.made, h.resolved, wanted, resolved); beyond != "" {
		return nil, refusedFor(connect.CodeUnimplemented, "REACH_NOT_SERVED", beyond)
	}
	if err := s.heldOnce(wanted, resolved); err != nil {
		return nil, err
	}
	kept := proto.Clone(has).(*workspacev1.WorkspaceSpec)
	kept.Repositories, kept.Entities, kept.WholeRepositories = wanted.GetRepositories(), wanted.GetEntities(), wanted.GetWholeRepositories()
	// An ending asked for meanwhile stops it, as it stops a pass.
	telling, cancel := h.stoppable(ctx, 2*time.Minute)
	answered, err := s.supervisor(h.sandbox).SetSource(telling, &sourcev1.SetSourceRequest{Spec: s.said(kept, h.generation+1, resolved, h.stood)})
	stopped := errors.Is(telling.Err(), context.Canceled) && ctx.Err() == nil
	cancel()
	if stopped {
		return nil, refusedFor(connect.CodeFailedPrecondition, "WORKSPACE_ENDING", fmt.Sprintf("the workspace %s is ending", h.workspace.GetName()))
	}
	if err != nil {
		return nil, relayed(err)
	}
	// The supervisor took the generation: it is never given again, though
	// what was told with it is not kept where keeping fails.
	h.generation++
	before, resolvedBefore := h.workspace.Spec, h.resolved
	h.workspace.Spec, h.resolved = kept, resolved
	if err := s.keep(h); err != nil {
		h.workspace.Spec, h.resolved = before, resolvedBefore
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	h.again = true
	out := s.view(h, told.Status(answered.GetSource()))
	h.show(out)
	s.wake()
	return &workspacev1.UpdateWorkspaceResponse{Workspace: out}, nil
}

// lacks names something a workspace has that a spec leaves out; "" where
// the spec holds all of it.
func lacks(has, wanted *workspacev1.WorkspaceSpec) string {
	for _, repository := range has.GetRepositories() {
		kept := false
		for _, each := range wanted.GetRepositories() {
			// A repository is kept where it is named again, by its
			// address and its ref.
			if each.GetRemote() == repository.GetRemote() && each.GetRef() == repository.GetRef() {
				kept = true
			}
		}
		if !kept {
			return "the repository " + repository.GetRemote()
		}
	}
	for _, entity := range has.GetEntities() {
		kept := false
		for _, each := range wanted.GetEntities() {
			kept = kept || proto.Equal(each, entity)
		}
		if !kept {
			return "the entity " + entity.GetName()
		}
	}
	if has.GetWholeRepositories() && !wanted.GetWholeRepositories() {
		return "its repositories whole"
	}
	for _, repository := range has.GetStored() {
		// A stored repository is kept where it is named again, as it was
		// asked for.
		if !slices.ContainsFunc(wanted.GetStored(), func(each *workspacev1.StoredRepository) bool { return proto.Equal(each, repository) }) {
			return "a stored repository"
		}
	}
	return ""
}

func (s *service) DeleteWorkspace(ctx context.Context, req *workspacev1.DeleteWorkspaceRequest) (*workspacev1.DeleteWorkspaceResponse, error) {
	h, err := s.owned(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	// What is being worked on for it is stopped, as a pass that ran out
	// of time is: the ending is worked on next, and promptly.
	h.interrupt()
	h.pass.Lock()
	var kept error
	if !h.ending {
		h.ending = true
		if kept = s.keep(h); kept != nil {
			h.ending = false
		} else {
			h.show(s.view(h, &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_ENDING}))
		}
	}
	h.ended()
	h.pass.Unlock()
	if kept != nil {
		return nil, connect.NewError(connect.CodeInternal, kept.Error())
	}
	s.wake()
	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

// sandboxOf reads which workspace a request names and which sandbox it
// is in, and answers the request itself where there is none, or none in
// a sandbox yet: that one is refused as any workspace that cannot be
// worked in yet, WORKSPACE_NOT_READY.
func (s *service) sandboxOf(w http.ResponseWriter, r *http.Request) (*held, string, bool) {
	h, err := s.owned(r.Context(), r.PathValue("name"))
	if err != nil {
		_ = refusals.Write(w, r, err)
		return nil, "", false
	}
	id := ""
	if living := h.living.Load(); living != nil {
		id = *living
	}
	if id == "" {
		_ = refusals.Write(w, r, refusedFor(connect.CodeFailedPrecondition, "WORKSPACE_NOT_READY", fmt.Sprintf("the workspace %s is in no sandbox yet, and is reached once it is", h.workspace.GetName())))
		return nil, "", false
	}
	return h, id, true
}

// carry answers a request with a tunnel to a port of a workspace's
// sandbox, and returns when the tunnel has ended, both ways
// (sandbox.Carry): when either end does, when the request does, or when
// the control plane is closed, which waits for it. A workspace is
// carried at most so many at once (most): one more is refused by name,
// RESOURCE_EXHAUSTED with TUNNELS_EXHAUSTED, until one has ended. A port
// that is not reached is refused by name too, FAILED_PRECONDITION with
// PORT_UNREACHED, as the sandbox service refuses its own (and for its
// reason: sandbox.Server.Tunnel): what the sandbox service said of it is
// logged here and told to no caller, since it holds whatever stands
// between the control plane and the sandbox cared to say.
func (s *service) carry(w http.ResponseWriter, r *http.Request, h *held, in string, port int) {
	s.carrying.Lock()
	switch {
	case s.stopped:
		s.carrying.Unlock()
		_ = refusals.Write(w, r, connect.NewError(connect.CodeUnavailable, "the control plane is stopping"))
		return
	case h.carried >= most:
		s.carrying.Unlock()
		_ = refusals.Write(w, r, refusedFor(connect.CodeResourceExhausted, "TUNNELS_EXHAUSTED", fmt.Sprintf("the workspace %s is carried as many connections as one is at once, %d: another is carried once one of them has ended", h.workspace.GetName(), most)))
		return
	}
	h.carried++
	s.tunnels.Add(1)
	s.carrying.Unlock()
	defer func() {
		s.carrying.Lock()
		h.carried--
		s.carrying.Unlock()
		s.tunnels.Done()
	}()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(s.serving, cancel)()
	inside, err := s.Sandboxes.Dial(ctx, in, port)
	if err != nil {
		log.Printf("controlplane: the port %d of the workspace %s was not reached: %v", port, h.workspace.GetName(), err)
		_ = refusals.Write(w, r, refusedFor(connect.CodeFailedPrecondition, "PORT_UNREACHED", "the port of the workspace's sandbox was not reached: nothing answers there, or the sandbox is not running"))
		return
	}
	sandbox.Carry(w, r.WithContext(ctx), inside)
}

// carried answers a request with a tunnel to a port of the sandbox of
// the workspace it names, for the workspace's owner alone: bytes, both
// ways, of which the control plane reads nothing, so that it knows
// nothing of what speaks there. Which port is read from the request,
// with whether it names one. What names none, a port written as
// anything but its number among it, is refused by name, and so is the
// port of the supervisor's source API, which the control plane alone dials
// and leads no caller to (AGENTS.md, "A workspace has one writer"):
// each PORT_NOT_SERVED, with nothing of the sandbox asked, and without
// what was written said back. Whoever is not the owner is refused
// first, as for any way to the workspace, and told nothing of its
// ports. What is carried, how many at once, and what a port that is not
// reached is told, is carry's.
func (s *service) carried(port func(*http.Request) (int, bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, id, ok := s.sandboxOf(w, r)
		if !ok {
			return
		}
		switch to, written := port(r); {
		case !written:
			_ = refusals.Write(w, r, refusedFor(connect.CodeUnimplemented, "PORT_NOT_SERVED", "the control plane carries a connection to a port of a workspace's sandbox, a number from 1 to 65535 written in digits alone, and what was asked for is none"))
		case to == ports.Source:
			_ = refusals.Write(w, r, refusedFor(connect.CodeUnimplemented, "PORT_NOT_SERVED", "the control plane carries no connection to the port of a workspace's source API, which is the control plane's alone"))
		default:
			s.carry(w, r, h, id, to)
		}
	}
}

// passing is the context of a pass over a workspace, called with its
// pass held: a minute, or, for a pass that does not end it, until it is
// asked to end, though that was asked just before the pass began.
func (h *held) passing(ctx context.Context) (context.Context, context.CancelFunc) {
	return h.stoppable(ctx, time.Minute)
}

// stoppable is the context of work on a workspace, called with its pass
// held: for so long, or, for work that does not end it, until it is
// asked to end, though that was asked just before the work began.
func (h *held) stoppable(ctx context.Context, within time.Duration) (context.Context, context.CancelFunc) {
	passing, cancel := context.WithTimeout(ctx, within)
	h.stopping.Lock()
	h.turn++
	mine := h.turn
	switch {
	case h.ending || h.expired():
		// The ending itself, asked for or come with its lifetime's end:
		// nothing stops it.
	case h.interrupted:
		cancel()
	default:
		h.stop = cancel
	}
	h.stopping.Unlock()
	return passing, func() {
		h.stopping.Lock()
		if h.turn == mine {
			h.stop = nil
		}
		h.stopping.Unlock()
		cancel()
	}
}

// expired is whether the workspace's lifetime has run out. Called with
// its pass held.
func (h *held) expired() bool {
	life := h.workspace.GetSpec().GetLifetime()
	return life != nil && time.Since(h.workspace.GetCreateTime().AsTime()) > life.AsDuration()
}

// interrupt stops the pass working on a workspace now, if one is, and
// any begun before the ending is recorded (ended).
func (h *held) interrupt() {
	h.stopping.Lock()
	defer h.stopping.Unlock()
	h.interrupted = true
	if h.stop != nil {
		h.stop()
	}
}

// ended records that the ending was asked for: passes are stopped by
// it no more. Called with the pass held.
func (h *held) ended() {
	h.stopping.Lock()
	defer h.stopping.Unlock()
	h.interrupted = false
}

func (s *service) wake() {
	select {
	case s.woken <- struct{}{}:
	default:
	}
}

// work goes over the workspaces until it is stopped: each that is not as
// asked at every turn, and each that is from time to time, since a
// sandbox may stop and a lifetime may end with no one asking.
func (s *service) work(ctx context.Context) {
	for {
		s.mu.Lock()
		var all []*held
		for _, h := range s.held {
			all = append(all, h)
		}
		s.mu.Unlock()
		for _, h := range all {
			// One that is being worked on is left to whoever has it.
			if !h.pass.TryLock() {
				continue
			}
			if !h.settled.IsZero() && time.Since(h.settled) < 12*s.Every && !h.again && !h.resume && !h.ending {
				h.pass.Unlock()
				continue
			}
			s.working.Add(1)
			go func() {
				defer s.working.Done()
				defer h.pass.Unlock()
				passing, cancel := h.passing(ctx)
				defer cancel()
				s.reconcile(passing, h)
			}()
		}
		select {
		case <-time.After(s.Every):
		case <-s.woken:
		case <-ctx.Done():
			return
		}
	}
}

// pass works on a workspace once, now, and returns it as it was found;
// where another is working on it, as that one last found it.
func (s *service) pass(ctx context.Context, h *held) *workspacev1.Workspace {
	if h.pass.TryLock() {
		passing, cancel := h.passing(context.WithoutCancel(ctx))
		s.reconcile(passing, h)
		cancel()
		h.pass.Unlock()
	}
	return h.seen.Load()
}

// reconcile takes a workspace one step towards what was asked for, and
// records what it found. It is called with the workspace's pass held.
func (s *service) reconcile(ctx context.Context, h *held) {
	failed, again := h.failed, h.again
	status := s.step(ctx, h)
	if status == nil {
		return // it is gone
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		// Stopped for its ending: what it found is no failure of the
		// workspace's, and the ending is worked on next.
		h.failed, h.again = failed, again
		return
	}
	switch status.GetState() {
	case workspacev1.WorkspaceStatus_STATE_READY, workspacev1.WorkspaceStatus_STATE_FAILED, workspacev1.WorkspaceStatus_STATE_PAUSED:
		h.settled = time.Now()
	default:
		h.settled = time.Time{}
	}
	h.show(s.view(h, status))
}

// supervisor is a client of the source API of the supervisor in a
// workspace's sandbox, on its source port, reached inside this server
// and through nothing a client could reach instead: no route of the
// control plane's leads there.
func (s *service) supervisor(sandbox string) sourcev1alpha1connect.SourceServiceClient {
	over := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return s.Sandboxes.Dial(ctx, sandbox, ports.Source)
		},
		DisableKeepAlives: true,
	}}
	return sourcev1alpha1connect.NewSourceServiceClient(connect.NewClient(connecthttp.NewTransport(over, "http://source")))
}

// step is one step, and returns what is so of the workspace now; nil
// where it is gone.
func (s *service) step(ctx context.Context, h *held) *workspacev1.WorkspaceStatus {
	called := h.workspace.GetName()
	failed := func(why string) *workspacev1.WorkspaceStatus {
		return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_FAILED, Failure: why}
	}
	// A lifetime that has run out ends it, as if it were asked to end.
	if !h.ending && h.expired() {
		h.ending = true
		if err := s.keep(h); err != nil {
			h.ending = false
		}
	}
	if h.ending {
		// Every sandbox it made goes: the one it lives in, and any that a
		// making that was stopped left behind, which carries a request it
		// kept. A sandbox that carries its name and no request of its own
		// is another's, and is not its to end.
		listed, err := page.All(func(token string) ([]*sandboxv1.Sandbox, string, error) {
			page, err := s.Sandboxes.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{Labels: map[string]string{Named: called}, PageToken: token})
			return page.GetSandboxes(), page.GetNextPageToken(), err
		})
		if err != nil {
			return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_ENDING, Failure: err.Error()}
		}
		ends := false
		for _, each := range listed {
			if each.GetId() != h.sandbox && !slices.Contains(h.requests, each.GetSpec().GetLabels()[Made]) {
				continue
			}
			if _, err := s.Sandboxes.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: each.GetId()}); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
				return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_ENDING, Failure: err.Error()}
			}
			ends = true
		}
		// What is held for it of the store is bound in its sandbox, and is
		// let go of once no sandbox of its own is there any more: one that
		// was asked to end just now is looked for again, the next time.
		// What is held is the workspace's own to let go of: it was held
		// for it, by its name and owner, when it was made (stood).
		if holds(h.stood) {
			if ends {
				return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_ENDING}
			}
			if err := s.release(ctx, called, h.owner); err != nil {
				return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_ENDING, Failure: err.Error()}
			}
		}
		if err := s.records.Drop(called); err != nil {
			return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_ENDING, Failure: err.Error()}
		}
		s.mu.Lock()
		if s.held[called] == h {
			delete(s.held, called)
		}
		s.mu.Unlock()
		return nil
	}
	if h.again {
		h.failed = ""
	}
	if h.failed != "" {
		return failed(h.failed)
	}
	// Its sandbox, made for what it was asked for with: the request is
	// named for the workspace and for that, so that a sandbox made for
	// another asking is never taken for this one's, and one whose making
	// was stopped half-way is found again.
	if h.sandbox == "" {
		in, err := s.sandbox(ctx, called, h.owner, h.made, h.resolved, h.stood)
		if err != nil {
			h.failed, h.again = err.Error(), false
			return failed(h.failed)
		}
		// The request is kept before it is asked, so that a sandbox made
		// for it is known to be its own though the answer is lost.
		request := digest(in)
		if !slices.Contains(h.requests, request) {
			h.requests = append(h.requests, request)
			if err := s.keep(h); err != nil {
				h.requests = h.requests[:len(h.requests)-1]
				return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING, Failure: err.Error()}
			}
		}
		in.Labels[Made] = request
		created, err := s.Sandboxes.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: in, RequestId: "workspace-" + called + "-" + request})
		if err != nil {
			h.failed, h.again = "its sandbox could not be made: "+err.Error(), false
			return failed(h.failed)
		}
		h.sandbox = created.GetSandbox().GetId()
		if err := s.keep(h); err != nil {
			// The sandbox is found again by its request, the next time.
			h.sandbox = ""
			return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING}
		}
	}
	now, err := s.Sandboxes.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: h.sandbox})
	if connect.CodeOf(err) == connect.CodeNotFound {
		h.again = false
		return failed("its sandbox is gone: it is ended, and asked for anew")
	}
	if err != nil {
		// What is not known is not said to have failed.
		if seen := h.seen.Load().GetStatus(); seen != nil {
			return proto.Clone(seen).(*workspacev1.WorkspaceStatus)
		}
		return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING}
	}
	switch now.GetSandbox().GetState() {
	case sandboxv1.Sandbox_STATE_PAUSED:
		h.running = time.Time{}
		if !h.resume {
			h.again = false
			return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_PAUSED}
		}
		if _, err := s.Sandboxes.ResumeSandbox(ctx, &sandboxv1.ResumeSandboxRequest{SandboxId: h.sandbox}); err != nil {
			h.resume, h.again = false, false
			return failed("its sandbox could not be gone on with: " + err.Error())
		}
		h.resume = false
		return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING}
	case sandboxv1.Sandbox_STATE_FAILED:
		h.again, h.resume = false, false
		return failed(strings.TrimSuffix("its sandbox failed: "+now.GetSandbox().GetFailure(), ": "))
	case sandboxv1.Sandbox_STATE_ENDING:
		h.again, h.resume = false, false
		return failed("its sandbox is ending: it is ended, and asked for anew")
	case sandboxv1.Sandbox_STATE_RUNNING:
		h.resume, h.starting = false, time.Time{}
	default:
		// One that does not come to run has failed.
		if h.starting.IsZero() {
			h.starting = time.Now()
		}
		if time.Since(h.starting) > s.Starts {
			h.again = false
			return failed("its sandbox did not come to run")
		}
		return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING}
	}
	// Its supervisor, which holds its source: what the workspace is to hold
	// is told to it, at the generation kept, and what it holds is read
	// from it. Nothing it says is kept as what was asked for.
	supervisor := s.supervisor(h.sandbox)
	asking, cancel := context.WithTimeout(ctx, 5*time.Second)
	// It answers before the deadline, bringing or not: one that does
	// not has not answered.
	waited, err := supervisor.WaitSource(asking, &sourcev1.WaitSourceRequest{Generation: h.generation})
	cancel()
	if err != nil {
		// It starts a moment after its sandbox does; one that does not
		// come to answer has failed.
		if h.running.IsZero() {
			h.running = time.Now()
		}
		if time.Since(h.running) > s.Answers {
			h.again = false
			return failed("its supervisor does not answer: " + err.Error())
		}
		return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_STARTING}
	}
	h.running = time.Time{}
	held := waited.GetSource()
	busy := held.GetState() == sourcev1.Source_STATE_POPULATING
	// It is told where it does not hold the generation kept: one started
	// again holds none, and one told otherwise from inside its sandbox
	// holds another. It is told again where what it brought failed and
	// the workspace was asked for anew.
	telling := held.GetGeneration() != h.generation || (h.again && held.GetState() == sourcev1.Source_STATE_FAILED)
	if telling && !busy {
		telling, cancel := context.WithTimeout(ctx, 2*time.Minute)
		answered, err := supervisor.SetSource(telling, &sourcev1.SetSourceRequest{Spec: s.said(h.workspace.GetSpec(), h.generation, h.resolved, h.stood)})
		cancel()
		// One that is bringing what it was told before is told again
		// once that is done: it says so by its reason, never its words.
		if reason, domain := frontseat.Reason(err); domain == refusal.Source && reason == told.Populating {
			return &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_POPULATING}
		}
		h.again = false
		if err != nil {
			h.failed = "its supervisor could not be had to bring what was asked for: " + err.Error()
			return failed(h.failed)
		}
		held = answered.GetSource()
	} else if !busy {
		h.again = false
	}
	return told.Status(held)
}

// stored is a workspace as its record keeps it.
type stored struct {
	// Workspace is its name, what it was asked for with, and when.
	Workspace json.RawMessage `json:"workspace"`
	// Made is the spec its sandbox was made for.
	Made json.RawMessage `json:"made"`
	// Sandbox is the sandbox it lives in, once one was asked for.
	Sandbox string `json:"sandbox,omitempty"`
	// Requests are what each sandbox asked for it was asked for under.
	Requests []string `json:"requests,omitempty"`
	// Owner is who asked for it first.
	Owner string `json:"owner,omitempty"`
	// Ending is whether it was asked to end.
	Ending bool `json:"ending,omitempty"`
	// Generation counts what its supervisor was told to hold.
	Generation int64 `json:"generation,omitempty"`
	// Resolved is what its entities resolved to.
	Resolved told.Resolved `json:"resolved,omitzero"`
	// Stood is what is held for it of the store, where it is remote.
	Stood sources.Held `json:"stood,omitzero"`
}

// keep writes a workspace's record: whole, or the one before.
func (s *service) keep(h *held) error {
	workspace, err := protojson.Marshal(&workspacev1.Workspace{Name: h.workspace.GetName(), Spec: h.workspace.GetSpec(), CreateTime: h.workspace.GetCreateTime()})
	if err != nil {
		return err
	}
	made, err := protojson.Marshal(h.made)
	if err != nil {
		return err
	}
	if err := s.records.Keep(h.workspace.GetName(), stored{Workspace: workspace, Made: made, Sandbox: h.sandbox, Requests: h.requests, Owner: h.owner, Ending: h.ending, Generation: h.generation, Resolved: h.resolved, Stood: h.stood}); err != nil {
		return fmt.Errorf("keeping the record of the workspace %s: %w", h.workspace.GetName(), err)
	}
	return nil
}

// recover takes up the workspaces a control plane before this one kept:
// each is worked on from what was asked for, wherever the one before
// stopped. A record of no one's workspace, or of one whose supervisor was
// told of no generation, is none this server wrote, and is refused: a
// workspace is its owner's alone.
func (s *service) recover() error {
	names, err := s.records.Names()
	if err != nil {
		return err
	}
	for _, called := range names {
		if !frontseat.IsWorkspaceName(called) {
			// No workspace's record: no workspace has the name.
			continue
		}
		unread := func(err error) error {
			return fmt.Errorf("the record of the workspace %s, %s, is not one this server reads (%w): remove the file, and end its sandbox through the gateway if it is still there", called, s.records.File(called), err)
		}
		var kept stored
		h := &held{workspace: &workspacev1.Workspace{}, made: &workspacev1.WorkspaceSpec{}}
		if _, err := s.records.Read(called, &kept); err != nil {
			return unread(err)
		}
		if err := protojson.Unmarshal(kept.Workspace, h.workspace); err != nil {
			return unread(err)
		}
		if h.workspace.GetName() != called {
			return unread(errors.New("it is of another workspace"))
		}
		if err := protojson.Unmarshal(kept.Made, h.made); err != nil {
			return unread(err)
		}
		if kept.Owner == "" || kept.Generation < 1 {
			return unread(errors.New("it names no owner, or no generation its supervisor was told"))
		}
		h.sandbox, h.requests, h.owner, h.ending, h.generation, h.resolved, h.stood = kept.Sandbox, kept.Requests, kept.Owner, kept.Ending, kept.Generation, kept.Resolved, kept.Stood
		state := workspacev1.WorkspaceStatus_STATE_STARTING
		if h.ending {
			state = workspacev1.WorkspaceStatus_STATE_ENDING
		}
		h.show(s.view(h, &workspacev1.WorkspaceStatus{State: state}))
		s.held[called] = h
	}
	return nil
}

// Grid is the grid every workspace is built on, as whoever serves the
// control plane says it: the control plane knows no grid of its own.
type Grid struct {
	// Hosts are where a build reaches it, each host:port: the grid, and
	// its asset service. A workspace's sandbox reaches them by the
	// program that builds there (Builder).
	Hosts []string
	// Arguments are the flags that tell a box's supervisor of the grid
	// (frontseat-box --help), each one argument written with its "=":
	// the supervisor writes them where a build in the box reads its
	// settings.
	Arguments []string
}

// Builder is where the program that builds is in a box
// (stack/box.Dockerfile): the command line, frontseat, which is the
// build daemon it starts there too (frontseat daemon). It is the program
// a workspace's way to the grid is for: the gateway lets a connection
// through for that program or any that started it, so the build daemon
// and what it starts reach the grid, and the box's supervisor and a
// shell it serves do not.
const Builder = "/opt/frontseat/bin/frontseat"

// Gateway is an OpenShell gateway that makes sandboxes: where it is
// asked, over https, the directory holding what it is known by and knows
// the control plane by (ca.crt, tls.crt, tls.key), and the command line
// of the container runtime it makes them with (docker, podman), which is
// asked what an image is started with.
type Gateway struct {
	Address, Identity, Runtime string
	// HostVolumes is whether a workspace's sandbox may be given a
	// directory of the gateway's machine, which the gateway must then be
	// let bind: what a control plane that serves stored repositories
	// gives its remote workspaces, and no other gives any.
	HostVolumes bool
}

// OverGateway serves a control plane whose workspaces live in sandboxes
// an OpenShell gateway makes: what frontseat-server serves. What runs in
// a workspace's sandbox may write the workspace and what its supervisor
// and its builds keep, and nowhere else. A gateway that does not answer is an error
// here, and nothing is served over it.
//
// It is configured as any control plane is, but that its sandbox
// service is made here, over the gateway, and the program that builds
// is a box's. The state is a directory of the control plane's own: its
// sandboxes' records are kept there, and its workspaces' beneath it.
func OverGateway(c Config, gateway Gateway) (*Plane, error) {
	sandboxes, err := sandbox.New(sandbox.Options{PublicURL: c.PublicURL, State: c.State, Gateway: &sandbox.Gateway{Address: gateway.Address, Identity: gateway.Identity, Runtime: gateway.Runtime, Writable: written, HostVolumes: gateway.HostVolumes}})
	if err != nil {
		return nil, err
	}
	c.Sandboxes, c.Builder, c.State = sandboxes, Builder, filepath.Join(c.State, "workspaces")
	return Serve(c)
}

// written is what runs in a workspace's sandbox may write of a box
// (stack/box.Dockerfile): the workspace, and what the box's supervisor
// and the build daemons started there keep (ports.Lock, ports.Settings,
// ports.Builds).
var written = []string{"/work", "/var/lib/frontseat"}

// relayed is a supervisor's refusal as the control plane says it on: what
// the supervisor refused by name is refused by the same reason, in the
// workspace API's domain, and anything else with its code and words.
func relayed(err error) error {
	var said *connect.Error
	if !errors.As(err, &said) {
		return connect.NewError(connect.CodeUnavailable, err.Error())
	}
	if reason, domain := frontseat.Reason(err); domain == refusal.Source && reason != "" {
		return refusedFor(said.Code(), reason, said.Message())
	}
	return connect.NewError(said.Code(), said.Message())
}
