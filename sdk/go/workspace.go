package frontseat

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/sdk/go/internal/transport"
)

// WorkspaceSpec is what a workspace holds and how it is made. Its source is in
// its sandbox, cloned there by its daemon: no directory of the caller's
// machine is given to it.
type WorkspaceSpec struct {
	// Name names the workspace; empty for the name the control plane
	// gives what is asked for.
	Name string
	// Image is the box image its sandbox runs; empty for the control
	// plane's own.
	Image string
	// Repositories are repositories for the daemon to clone.
	Repositories []Repository
	// Entities are entities whose source the workspace holds, each as a
	// person types one (entity.ParsePattern reads gateway, system:kiosk,
	// component:default/gateway). The control plane resolves them through
	// the Backstage catalog its operator gives it, and refuses them where
	// it has none.
	Entities []entity.Pattern
	// Whole clones the entities' repositories whole, not only the
	// directories the entities govern.
	Whole bool
	// Push lets it push to the repositories it clones.
	Push bool
	// Author is who commits there; nobody where it is empty.
	Author Author
	// AuthorizedKey is the public key, a line of authorized_keys, that
	// may open a shell in it (Workspace.Shell); empty for none. It is
	// said when it is made: one opened again with another key is
	// refused, and one opened again with none is as it was made.
	AuthorizedKey string
	// Lifetime is how long it lasts from when it was made, however much
	// it is used, after which the control plane ends it; zero for one
	// kept until it is ended.
	Lifetime time.Duration
}

// workspaceName is a workspace's name, as IsWorkspaceName says it.
var workspaceName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?$`)

// IsWorkspaceName is whether a name is one a workspace may have:
// lower-case letters, digits and dashes, at most 48, beginning and ending
// with a letter or a digit. A control plane refuses any other, and a
// client may say so before it asks.
func IsWorkspaceName(name string) bool { return workspaceName.MatchString(name) }

// Repository is a repository to clone.
type Repository struct {
	// Remote is where it is cloned from, over http or https. It names no
	// user and holds no password or token: a credential for its host is
	// the control plane's to keep (Credentials).
	Remote string
	// Ref is the branch or tag to stand at; empty for the remote's
	// default branch.
	Ref string
}

// Author is who what is committed in a workspace is by.
type Author struct {
	// Name is the author's name, as a commit says it.
	Name string
	// Email is the author's address, as a commit says it.
	Email string
}

// proto is the spec as the API carries it.
func (s WorkspaceSpec) proto() (*workspacev1.WorkspaceSpec, error) {
	spec := &workspacev1.WorkspaceSpec{Whole: s.Whole, Push: s.Push, Image: s.Image, AuthorizedKey: s.AuthorizedKey}
	for _, r := range s.Repositories {
		spec.Repositories = append(spec.Repositories, &workspacev1.RepositorySource{Remote: r.Remote, Ref: r.Ref})
	}
	refs, err := refs(s.Entities)
	if err != nil {
		return nil, err
	}
	spec.Entities = refs
	if s.Author != (Author{}) {
		spec.Author = &workspacev1.Author{Name: s.Author.Name, Email: s.Author.Email}
	}
	if s.Lifetime > 0 {
		spec.Lifetime = durationpb.New(s.Lifetime)
	}
	return spec, nil
}

// refs are patterns as the API carries them: what each leaves out is
// left empty, for the server to find among the entities it holds. One
// that is no pattern is refused before anything is asked.
func refs(patterns []entity.Pattern) ([]*typev1.EntityRef, error) {
	var out []*typev1.EntityRef
	for _, p := range patterns {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("%q names no entity: %w", p, err)
		}
		out = append(out, entity.PatternProto(p))
	}
	return out, nil
}

// OpenOutcome is how a workspace came to be the one Open returned.
type OpenOutcome int

const (
	// OpenOutcomeUnspecified is no outcome: Open returned no workspace.
	OpenOutcomeUnspecified OpenOutcome = iota
	// OpenOutcomeExisting is a workspace that was there, and is as it was
	// left.
	OpenOutcomeExisting
	// OpenOutcomeCreated is a workspace that was made now.
	OpenOutcomeCreated
	// OpenOutcomeFilled is a workspace that was there and held nothing
	// yet, whose daemon cloned now.
	OpenOutcomeFilled
)

// String says the outcome in a person's words: "made", "filled", "as it
// was left".
func (o OpenOutcome) String() string {
	switch o {
	case OpenOutcomeExisting:
		return "as it was left"
	case OpenOutcomeCreated:
		return "made"
	case OpenOutcomeFilled:
		return "filled"
	}
	return "unspecified"
}

// OpenResult is a workspace that was opened, or as much of one as there
// is where opening failed.
type OpenResult struct {
	// Workspace is the workspace; nil only where the control plane never
	// said which it is.
	Workspace *Workspace
	// Outcome says how it came to be the one opened.
	Outcome OpenOutcome
	// Status is the workspace as the control plane last said it was: once
	// it could be worked in, or when opening it failed.
	Status *workspacev1.Workspace
}

// Workspaces are the workspaces the control plane keeps for the
// caller, oldest first, asked for a page at a time as they are read.
func (c *Client) Workspaces(ctx context.Context) iter.Seq2[*workspacev1.Workspace, error] {
	api := c.workspaces()
	return paged(func(token string) ([]*workspacev1.Workspace, string, error) {
		page, err := api.ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{PageToken: token})
		return page.GetWorkspaces(), page.GetNextPageToken(), err
	})
}

// Open gives the workspace a spec asks for: the one the control plane
// keeps, as it was left, or a new one, whose daemon clones what the
// spec names. It returns once the workspace can be worked in. A spec
// that names a workspace and nothing more than the key that may open a
// shell in it opens the one there is by that name, and makes none.
//
// It ends nothing, whatever fails: a workspace that is there may hold
// work, and one that could not be opened is named in what is returned.
func (c *Client) Open(ctx context.Context, spec WorkspaceSpec) (OpenResult, error) {
	api := c.workspaces()
	wanted, err := spec.proto()
	if err != nil {
		return OpenResult{}, err
	}
	if spec.Name != "" && proto.Equal(wanted, &workspacev1.WorkspaceSpec{AuthorizedKey: spec.AuthorizedKey}) {
		// Asked for again as it is, it is the one there is.
		there, err := api.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: spec.Name})
		if err != nil {
			return OpenResult{}, err
		}
		wanted = proto.Clone(there.GetWorkspace().GetSpec()).(*workspacev1.WorkspaceSpec)
		wanted.AuthorizedKey = spec.AuthorizedKey
	}
	said, err := api.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: spec.Name, Spec: wanted})
	if err != nil {
		return OpenResult{}, err
	}
	first := said.GetWorkspace()
	opened := OpenResult{Workspace: c.Workspace(first.GetName()), Outcome: OpenOutcomeExisting, Status: first}
	switch state := first.GetStatus().GetState(); {
	case said.GetCreated():
		opened.Outcome = OpenOutcomeCreated
	case state != workspacev1.WorkspaceStatus_STATE_READY && state != workspacev1.WorkspaceStatus_STATE_PAUSED:
		opened.Outcome = OpenOutcomeFilled
	}
	last, err := api.WaitWorkspace(ctx, &workspacev1.WaitWorkspaceRequest{Name: first.GetName()})
	if err != nil {
		return opened, err
	}
	opened.Status = last.GetWorkspace()
	return opened, ready(first.GetName(), opened.Status)
}

// Workspace is a workspace the control plane keeps, by its name. Nothing
// is asked of the control plane until a call is made. A name is given:
// with none, every call fails with ErrWorkspaceUnnamed before anything is asked,
// and the workspace a daemon's Client is in is Here, never an empty
// name.
func (c *Client) Workspace(name string) *Workspace {
	return &Workspace{client: c, name: name}
}

// Here is the workspace whose daemon the Client was made for: inside a
// workspace's sandbox, the one workspace there is, whose daemon is asked
// to build, run and release what it holds. Its source is fixed there: it
// is described, filled, ended and opened a shell in from outside, by its
// name, and Here refuses each of those by name (ErrHere).
func (c *Client) Here() *Workspace {
	return &Workspace{client: c, here: true, daemon: c.api}
}

func (c *Client) workspaces() workspacev1alpha1connect.WorkspaceServiceClient {
	return workspacev1alpha1connect.NewWorkspaceServiceClient(c.api)
}

// Workspace is one workspace: what it holds, and the daemon in it that
// builds, runs and releases what it holds.
type Workspace struct {
	client *Client
	// name is the workspace's name; empty for the one Here is, and for
	// one given none.
	name string
	// here is whether it is the one Here is, asked by no name.
	here bool

	mu     sync.Mutex
	daemon *connect.Client
}

// Name is the workspace's name; empty for the one Here is, and for one
// given none.
func (w *Workspace) Name() string { return w.name }

// ErrHere is what the workspace Here is refuses to do: what changes or
// describes a workspace is asked of the control plane that keeps it,
// from outside it, and inside a workspace's sandbox its source is fixed.
var ErrHere = errors.New("inside a workspace's sandbox its source is fixed, and its daemon is asked to build, run and release alone: describe, fill, end and open a shell in a workspace from outside it, by its name")

// ErrWorkspaceUnnamed is what a workspace given no name is refused
// with: nothing is asked of a control plane for it.
var ErrWorkspaceUnnamed = errors.New("a workspace is asked for by its name, and this one was given none")

// ErrWorkspaceFailed is what a workspace that failed is refused with by
// Open and Add: Error.Message says why. It is ErrWorkspaceNotReady too.
var ErrWorkspaceFailed = errors.New("the workspace failed")

// ErrWorkspacePaused is what a workspace that is paused is refused with
// by Open and Add: open it to go on with it. It is ErrWorkspaceNotReady
// too.
var ErrWorkspacePaused = errors.New("the workspace is paused")

// named refuses what is asked of a workspace by its name where it has
// none: the one Here is (ErrHere), and one the caller named with
// nothing (ErrWorkspaceUnnamed), which is no workspace and not the one Here is.
func (w *Workspace) named() error {
	switch {
	case w.here:
		return ErrHere
	case w.name == "":
		return ErrWorkspaceUnnamed
	}
	return nil
}

// Describe returns the workspace as it is now: what it was asked for,
// what it holds and where it is.
func (w *Workspace) Describe(ctx context.Context) (*workspacev1.Workspace, error) {
	if err := w.named(); err != nil {
		return nil, err
	}
	said, err := w.client.workspaces().DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: w.name})
	return said.GetWorkspace(), err
}

// Add has the workspace hold more: what more names is added to what it
// was asked for, and it is waited for until it can be worked in again.
// Only the source more names is read of it.
func (w *Workspace) Add(ctx context.Context, more WorkspaceSpec) error {
	if err := w.named(); err != nil {
		return err
	}
	api := w.client.workspaces()
	said, err := api.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: w.name})
	if err != nil {
		return err
	}
	adding, err := more.proto()
	if err != nil {
		return err
	}
	spec := &workspacev1.WorkspaceSpec{}
	if had := said.GetWorkspace().GetSpec(); had != nil {
		spec = proto.Clone(had).(*workspacev1.WorkspaceSpec)
	}
	for _, repository := range adding.GetRepositories() {
		if !slices.ContainsFunc(spec.Repositories, func(had *workspacev1.RepositorySource) bool { return proto.Equal(had, repository) }) {
			spec.Repositories = append(spec.Repositories, repository)
		}
	}
	for _, ref := range adding.GetEntities() {
		if !slices.ContainsFunc(spec.Entities, func(had *typev1.EntityRef) bool { return proto.Equal(had, ref) }) {
			spec.Entities = append(spec.Entities, ref)
		}
	}
	spec.Whole = spec.Whole || adding.GetWhole()
	if _, err := api.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: w.name, Spec: spec}); err != nil {
		return err
	}
	last, err := api.WaitWorkspace(ctx, &workspacev1.WaitWorkspaceRequest{Name: w.name})
	if err != nil {
		return err
	}
	return ready(w.name, last.GetWorkspace())
}

// askedAgain is how long Delete asks, once whoever waited has given up,
// what keeps the workspace it ended.
const askedAgain = 5 * time.Second

// Delete ends the workspace, and returns when it is gone and its
// sandbox with it, so that what is done next, stopping the control
// plane among it, leaves no sandbox behind. What was changed in it and
// not pushed goes with it. One there is none of is said to be none.
// Whoever gives up waiting is told what the control plane says keeps the
// workspace, where it says anything, and the workspace goes on ending.
func (w *Workspace) Delete(ctx context.Context) error {
	if err := w.named(); err != nil {
		return err
	}
	api := w.client.workspaces()
	if _, err := api.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: w.name}); err != nil {
		return err
	}
	// gone reads how waiting came to an end: that there is none is what
	// was waited for. Where the caller gave up, the control plane is asked
	// once more, in a moment of its own, what keeps the workspace.
	gone := func(err error) error {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		}
		if ctx.Err() == nil {
			return err
		}
		asking, cancel := context.WithTimeout(context.WithoutCancel(ctx), askedAgain)
		defer cancel()
		still, described := api.DescribeWorkspace(asking, &workspacev1.DescribeWorkspaceRequest{Name: w.name})
		if why := still.GetWorkspace().GetStatus().GetFailure(); described == nil && why != "" {
			return fmt.Errorf("the workspace %s is still ending: %s: %w", w.name, why, err)
		}
		return err
	}
	last, err := api.WaitWorkspace(ctx, &workspacev1.WaitWorkspaceRequest{Name: w.name})
	if err != nil {
		return gone(err)
	}
	// A wait ends so only on a workspace that is there to work in: the one
	// asked to end is gone, and another asked for one of its name
	// meanwhile.
	return fmt.Errorf("a workspace %s is there again, %s: the one that was ended is gone, and another was made by its name", w.name, stateOf(last.GetWorkspace()))
}

// Shell returns a connection to the workspace's shell, through the
// control plane that keeps it: what an SSH client speaks over, logging
// in with a key the workspace was made to let in (WorkspaceSpec.AuthorizedKey).
// The connection lasts as long as the context.
func (w *Workspace) Shell(ctx context.Context) (net.Conn, error) {
	if err := w.named(); err != nil {
		return nil, err
	}
	return transport.Tunnel(ctx, w.client.Server()+"/workspaces/"+url.PathEscape(w.name)+"/shell", w.client.reach)
}

// at is the connection to the workspace's daemon: the Client's own for
// Here, or else the address the control plane says the daemon is at,
// asked once.
func (w *Workspace) at(ctx context.Context) (*connect.Client, error) {
	w.mu.Lock()
	known := w.daemon
	w.mu.Unlock()
	if known != nil {
		return known, nil
	}
	// Asked in the caller's own context, with no lock held: a lookup that
	// fails keeps nothing, and the next call asks again.
	held, err := w.Describe(ctx)
	if err != nil {
		return nil, err
	}
	address := held.GetStatus().GetBuildUri()
	if address == "" {
		return nil, notReady(nil, "the workspace %s is %s, and has no daemon to ask yet", w.name, stateOf(held))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.daemon == nil {
		w.daemon = w.client.at(address)
	}
	return w.daemon, nil
}

// ready says why a workspace that waiting ended on cannot be worked in;
// nil where it can.
func ready(name string, w *workspacev1.Workspace) error {
	if name == "" {
		name = w.GetName()
	}
	switch w.GetStatus().GetState() {
	case workspacev1.WorkspaceStatus_STATE_READY:
		return nil
	case workspacev1.WorkspaceStatus_STATE_FAILED:
		return notReady(ErrWorkspaceFailed, "the workspace %s failed: %s", name, w.GetStatus().GetFailure())
	case workspacev1.WorkspaceStatus_STATE_PAUSED:
		return notReady(ErrWorkspacePaused, "the workspace %s is paused: open it to go on with it", name)
	}
	return notReady(nil, "the workspace %s is %s", name, stateOf(w))
}

// notReady is the SDK's own refusal of a workspace that cannot be worked
// in, as a control plane would refuse it: ErrWorkspaceNotReady, and the
// sentinel of the state it is in, where one is given.
func notReady(state error, format string, a ...any) error {
	return &Error{Code: connect.CodeFailedPrecondition, Reason: ReasonWorkspaceNotReady, Domain: DomainWorkspace, Message: fmt.Sprintf(format, a...), cause: state}
}

// stateOf is where a workspace is, in a word: ready, starting, failed.
func stateOf(w *workspacev1.Workspace) string {
	return strings.ToLower(strings.TrimPrefix(w.GetStatus().GetState().String(), "STATE_"))
}
