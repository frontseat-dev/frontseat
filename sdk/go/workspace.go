package frontseat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
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

// Spec is what a workspace holds and how it is made. Its source is in
// its sandbox, cloned there by its daemon: no directory of the caller's
// machine is given to it.
type Spec struct {
	// Name names the workspace; empty for the name the control plane
	// gives what is asked for.
	Name string
	// Image is the box image its sandbox runs; empty for the control
	// plane's own.
	Image string
	// Repositories are repositories for the daemon to clone.
	Repositories []Repository
	// Entities are entities whose source the workspace holds, each as a
	// person types one: gateway, system:kiosk, component:default/gateway.
	// The control plane resolves them through the Backstage catalog its
	// operator gives it, and refuses them where it has none.
	Entities []string
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

// Repository is a repository to clone: its remote, over http or https,
// and the branch or tag to stand at, empty for its default branch.
type Repository struct {
	Remote, Ref string
}

// Author is who what is committed in a workspace is by.
type Author struct {
	Name, Email string
}

// proto is the spec as the API carries it.
func (s Spec) proto() (*workspacev1.WorkspaceSpec, error) {
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

// refs are entity references as a person types them, as the API
// carries them: what each leaves out is left empty, for the control
// plane to find among the entities its catalog holds.
func refs(typed []string) ([]*typev1.EntityRef, error) {
	var out []*typev1.EntityRef
	for _, written := range typed {
		ref, err := entity.Typed(written)
		if err != nil {
			return nil, fmt.Errorf("%q names no entity: %w", written, err)
		}
		out = append(out, entity.Proto(ref))
	}
	return out, nil
}

// How is how a workspace came to be the one opened.
type How int

const (
	// Left is a workspace that was there, and is as it was left.
	Left How = iota
	// Made is a workspace that was made now.
	Made
	// Filled is a workspace that was there and held nothing yet, whose
	// daemon cloned now.
	Filled
)

// Opened is a workspace that was opened, or as much of one as there is
// where opening failed.
type Opened struct {
	// Workspace is the workspace; nil only where the control plane never
	// said which it is.
	Workspace *Workspace
	// How says how it came to be the one opened.
	How How
	// Held is the workspace as the control plane last said it was: once
	// it could be worked in, or when opening it failed.
	Held *workspacev1.Workspace
}

// Workspaces lists the workspaces the control plane keeps for the
// caller, oldest first.
func (c *Client) Workspaces(ctx context.Context) ([]*workspacev1.Workspace, error) {
	api := c.workspaces()
	return Listed(func(token string) ([]*workspacev1.Workspace, string, error) {
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
func (c *Client) Open(ctx context.Context, spec Spec) (Opened, error) {
	api := c.workspaces()
	wanted, err := spec.proto()
	if err != nil {
		return Opened{}, err
	}
	if spec.Name != "" && proto.Equal(wanted, &workspacev1.WorkspaceSpec{AuthorizedKey: spec.AuthorizedKey}) {
		// Asked for again as it is, it is the one there is.
		there, err := api.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: spec.Name})
		if err != nil {
			return Opened{}, err
		}
		wanted = proto.Clone(there.GetWorkspace().GetSpec()).(*workspacev1.WorkspaceSpec)
		wanted.AuthorizedKey = spec.AuthorizedKey
	}
	said, err := api.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: spec.Name, Spec: wanted})
	if err != nil {
		return Opened{}, err
	}
	first := said.GetWorkspace()
	opened := Opened{Workspace: c.Workspace(first.GetName()), Held: first}
	switch state := first.GetStatus().GetState(); {
	case said.GetCreated():
		opened.How = Made
	case state != workspacev1.WorkspaceStatus_STATE_READY && state != workspacev1.WorkspaceStatus_STATE_PAUSED:
		opened.How = Filled
	}
	last, err := api.WaitWorkspace(ctx, &workspacev1.WaitWorkspaceRequest{Name: first.GetName()})
	if err != nil {
		return opened, err
	}
	opened.Held = last.GetWorkspace()
	return opened, ready(first.GetName(), opened.Held)
}

// Workspace is a workspace the control plane keeps, by its name. Nothing
// is asked of the control plane until a call is made.
func (c *Client) Workspace(name string) *Workspace {
	return &Workspace{client: c, name: name}
}

// Here is the workspace whose daemon the Client was made for: inside a
// workspace's sandbox, the one workspace there is, whose daemon is asked
// to build, run and release what it holds. Its source is fixed there: it
// is described, filled, ended and opened a shell in from outside, by its
// name, and Here refuses each of those by name (ErrHere).
func (c *Client) Here() *Workspace {
	return &Workspace{client: c, daemon: c.api}
}

func (c *Client) workspaces() workspacev1alpha1connect.WorkspaceServiceClient {
	return workspacev1alpha1connect.NewWorkspaceServiceClient(c.api)
}

// Workspace is one workspace: what it holds, and the daemon in it that
// builds, runs and releases what it holds.
type Workspace struct {
	client *Client
	// name is the workspace's name; empty for the one Here is.
	name string

	mu     sync.Mutex
	daemon *connect.Client
}

// Name is the workspace's name; empty for the one Here is.
func (w *Workspace) Name() string { return w.name }

// ErrHere is what the workspace Here is refuses to do: what changes or
// describes a workspace is asked of the control plane that keeps it,
// from outside it, and inside a workspace's sandbox its source is fixed.
var ErrHere = errors.New("inside a workspace's sandbox its source is fixed, and its daemon is asked to build, run and release alone: describe, fill, end and open a shell in a workspace from outside it, by its name")

// Describe returns the workspace as it is now: what it was asked for,
// what it holds and where it is.
func (w *Workspace) Describe(ctx context.Context) (*workspacev1.Workspace, error) {
	if w.name == "" {
		return nil, ErrHere
	}
	said, err := w.client.workspaces().DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: w.name})
	return said.GetWorkspace(), err
}

// Add has the workspace hold more: what more names is added to what it
// was asked for, and it is waited for until it can be worked in again.
// Only the source more names is read of it.
func (w *Workspace) Add(ctx context.Context, more Spec) error {
	if w.name == "" {
		return ErrHere
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

// Delete ends the workspace, and returns when it is gone and its
// sandbox with it, so that what is done next, stopping the control
// plane among it, leaves no sandbox behind. What was changed in it and
// not pushed goes with it. One there is none of is said to be none.
// Whoever gives up waiting is told what the control plane says keeps the
// workspace, where it says anything, and the workspace goes on ending.
func (w *Workspace) Delete(ctx context.Context) error {
	if w.name == "" {
		return ErrHere
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
		asking, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
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
// in with a key the workspace was made to let in (Spec.AuthorizedKey).
// The connection lasts as long as the context.
func (w *Workspace) Shell(ctx context.Context) (net.Conn, error) {
	if w.name == "" {
		return nil, ErrHere
	}
	return transport.Tunnel(ctx, w.client.server+"/workspaces/"+url.PathEscape(w.name)+"/shell", w.client.reach)
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
		return nil, fmt.Errorf("the workspace %s is %s, and has no daemon to ask yet", w.name, stateOf(held))
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
		return fmt.Errorf("the workspace %s failed: %s", name, w.GetStatus().GetFailure())
	case workspacev1.WorkspaceStatus_STATE_PAUSED:
		return fmt.Errorf("the workspace %s is paused: open it to go on with it", name)
	}
	return fmt.Errorf("the workspace %s is %s", name, stateOf(w))
}

// stateOf is where a workspace is, in a word: ready, starting, failed.
func stateOf(w *workspacev1.Workspace) string {
	return strings.ToLower(strings.TrimPrefix(w.GetStatus().GetState().String(), "STATE_"))
}
