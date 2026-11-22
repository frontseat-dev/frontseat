package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coder/websocket"
	"github.com/distribution/reference"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/internal/page"
	"github.com/frontseat-dev/frontseat/server/internal/plain"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
)

// Handler serves the sandbox API, and a sandbox's ports beneath the
// sandbox's own path: PublicURL/sandboxes/<id>/ports/<port>/ reaches
// what that port serves over HTTP, and
// PublicURL/sandboxes/<id>/tunnels/<port> is a WebSocket to the port
// itself, for what is not HTTP. It checks no one: whoever serves it says
// who may reach it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.mount(mux)
	return mux
}

// mount serves the sandbox API on a mux: its calls, and the ways to its
// sandboxes' ports.
func (s *Server) mount(mux *http.ServeMux) {
	api := connect.NewServer()
	sandboxv1alpha1connect.RegisterSandboxServiceHandler(api, s)
	connecthttp.Mount(mux, api)
	mux.HandleFunc("/sandboxes/{id}/ports/{port}/{rest...}", s.port)
	mux.HandleFunc("/sandboxes/{id}/tunnels/{port}", s.tunnel)
}

// Service is the sandbox service as whatever is built on it holds it, in
// its own process: its calls, and a way to a port inside a sandbox. It
// is served to no one as it is: what is built on it says what of it
// another may reach, and checks who asks.
type Service interface {
	sandboxv1alpha1connect.SandboxServiceHandler
	// Dial connects to a port inside a sandbox the service made.
	Dial(ctx context.Context, id string, port int) (net.Conn, error)
	// Proxy passes a request on to a port inside a sandbox.
	Proxy(w http.ResponseWriter, r *http.Request, id string, port int, rest string)
	// Tunnel answers a request with a tunnel to a port inside a sandbox.
	Tunnel(w http.ResponseWriter, r *http.Request, id string, port int)
}

// recover takes up the sandboxes a server before this one made. One
// that is gone is forgotten; one that is there is the service's again,
// with the request that made it.
func (s *Server) recover(ctx context.Context) error {
	recorded, err := s.kept.all()
	if err != nil {
		return fmt.Errorf("the sandboxes' records: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range recorded {
		if _, err := s.driver.Observe(ctx, id); errors.Is(err, driver.ErrNotFound) {
			if err := s.kept.drop(id); err != nil {
				return err
			}
			continue
		}
		// One the provider could not answer for is still recorded, so it
		// is still the service's.
		if r.wanted, err = s.asks(r.spec, id); err != nil {
			return fmt.Errorf("the record of the sandbox %s: %w", id, err)
		}
		s.records[id] = r
		if r.request != "" {
			made := &asking{done: make(chan struct{}), id: id}
			close(made.done)
			s.asked[r.request] = made
		}
	}
	return nil
}

// asks turns a spec into what the driver is asked for, for a sandbox of
// an id: the spec, and beneath it what every sandbox is told of where it
// runs.
func (s *Server) asks(spec *sandboxv1.SandboxSpec, id string) (driver.Sandbox, error) {
	wanted, err := wanted(spec)
	if err != nil {
		return driver.Sandbox{}, err
	}
	wanted.ID = id
	// Where nothing is said of what a sandbox may reach, it is the
	// driver's default: anything, or, under a driver that lets nothing be
	// reached unnamed, what the spec's rules name and no other.
	if spec.GetEgress().GetMode() == sandboxv1.Egress_MODE_UNSPECIFIED && !s.driver.Capabilities().EgressOpen {
		wanted.Egress.Listed = true
	}
	return wanted, nil
}

// Close lets go of what its driver holds open, where the driver holds
// anything: a gateway's connection. A service is closed by whoever made
// it once it is done with it.
func (s *Server) Close() error {
	if closer, ok := s.driver.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Server is the sandbox service over one driver, made by New: it is a
// Service, to whatever holds it in its own process, and its Handler
// serves it over a network.
type Server struct {
	driver    driver.Driver
	publicURL string

	// kept is where the records outlive the server.
	kept kept

	mu      sync.Mutex
	records map[string]*record
	// asked is the sandbox each request made or is making, by the
	// request's id, so that a request sent again makes nothing more. A
	// request is remembered as long as the sandbox it made.
	asked map[string]*asking
}

// asking is a request that makes a sandbox. One that arrives while the
// same request is still being served waits for it, and is answered with
// what it made.
type asking struct {
	// done is closed when the request has been served.
	done chan struct{}
	// id is the sandbox it made; err is why it made none.
	id  string
	err error
}

// record is a sandbox as it was asked for.
type record struct {
	spec    *sandboxv1.SandboxSpec
	wanted  driver.Sandbox
	created time.Time
	// request is the id of the request that made it, where it had one.
	request string
	// turn is held while the sandbox is being changed.
	turn turn
}

// turn is a sandbox's: what changes it holds it, one change at a time,
// and what waits for it gives up when its asker does.
type turn chan struct{}

func newTurn() turn { return make(turn, 1) }

// take holds the turn, or says why it was not waited for.
func (t turn) take(ctx context.Context) error {
	select {
	case t <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// give lets the next have it.
func (t turn) give() { <-t }

var _ sandboxv1alpha1connect.SandboxServiceHandler = (*Server)(nil)

func (s *Server) CreateSandbox(ctx context.Context, req *sandboxv1.CreateSandboxRequest) (*sandboxv1.CreateSandboxResponse, error) {
	// The spec is the service's own from here, checked and kept as one:
	// a caller in its process keeps the one it asked with, and what it
	// changes after is neither checked nor kept.
	spec := proto.Clone(req.GetSpec()).(*sandboxv1.SandboxSpec)
	if spec.GetImage() == "" {
		return nil, refuse(connect.CodeInvalidArgument, "SPEC_INVALID", "a sandbox needs an image")
	}
	if err := imageOf(spec.GetImage()); err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "SPEC_INVALID", "the sandbox: "+err.Error())
	}
	if _, err := wanted(spec); err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "SPEC_INVALID", err.Error())
	}
	// A request sent again, as after a lost answer or beside itself,
	// finds the sandbox it made the first time. The request is taken
	// before anything is made, so that two of them never both make one.
	if request := req.GetRequestId(); request != "" {
		s.mu.Lock()
		earlier, again := s.asked[request]
		mine := &asking{done: make(chan struct{})}
		if !again {
			s.asked[request] = mine
		}
		s.mu.Unlock()
		if again {
			select {
			case <-earlier.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if earlier.err != nil {
				return nil, earlier.err
			}
			now, err := s.observe(ctx, earlier.id)
			if connect.CodeOf(err) == connect.CodeNotFound {
				// The sandbox the request made is gone, and with it the
				// request, whoever forgot it: sent again, it makes one, or
				// finds the one another made meanwhile.
				s.lost(ctx, earlier.id, true)
				s.mu.Lock()
				forgotten := s.asked[request] != earlier
				s.mu.Unlock()
				if forgotten {
					return s.CreateSandbox(ctx, req)
				}
			}
			if err != nil {
				return nil, err
			}
			// One that is ending is no sandbox to hand back as made.
			if now.GetState() == sandboxv1.Sandbox_STATE_ENDING {
				return nil, refuse(connect.CodeFailedPrecondition, "SANDBOX_ENDING", fmt.Sprintf("the sandbox %q this request made is ending: the request is sent again once it is gone", earlier.id))
			}
			return &sandboxv1.CreateSandboxResponse{Sandbox: now}, nil
		}
		made, err := s.make(ctx, spec, request)
		s.mu.Lock()
		mine.id, mine.err = made.GetId(), err
		if err != nil {
			// A request that made nothing may be sent again and make one.
			delete(s.asked, request)
		}
		s.mu.Unlock()
		close(mine.done)
		if err != nil {
			return nil, err
		}
		return &sandboxv1.CreateSandboxResponse{Sandbox: made}, nil
	}
	made, err := s.make(ctx, spec, "")
	if err != nil {
		return nil, err
	}
	return &sandboxv1.CreateSandboxResponse{Sandbox: made}, nil
}

// make makes a sandbox for a request, of a spec that is the service's
// own (CreateSandbox clones it). It is recorded before it is made:
// a server that stops between the two finds the record when it starts,
// and the sandbox, if there is one, is still the service's.
func (s *Server) make(ctx context.Context, spec *sandboxv1.SandboxSpec, request string) (*sandboxv1.Sandbox, error) {
	id := newID()
	wanted, err := s.asks(spec, id)
	if err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "SPEC_INVALID", err.Error())
	}
	r := &record{spec: spec, wanted: wanted, created: time.Now(), request: request, turn: newTurn()}
	if err := s.kept.keep(id, r); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	_, failed := s.driver.Apply(ctx, wanted)
	if failed != nil {
		// Where nothing was made the record goes. Where the provider did
		// not make it whole, or did not say, there may be a sandbox to
		// lose: it stays the service's, to list and to end.
		if _, gone := s.driver.Observe(ctx, id); errors.Is(gone, driver.ErrNotFound) {
			_ = s.kept.drop(id)
			return nil, refused(failed)
		}
	}
	s.mu.Lock()
	s.records[id] = r
	s.mu.Unlock()
	if failed != nil {
		return nil, refused(failed)
	}
	return s.observe(ctx, id)
}

// selector reads which labels a list keeps: name=value, separated by
// commas, all of which a sandbox must carry.
func selector(given string) (map[string]string, error) {
	out := map[string]string{}
	for _, each := range strings.Split(given, ",") {
		if each = strings.TrimSpace(each); each == "" {
			continue
		}
		name, value, found := strings.Cut(each, "=")
		if !found || name == "" {
			return nil, errors.New("the label selector is not name=value, separated by commas")
		}
		out[name] = value
	}
	return out, nil
}

// forget drops a sandbox that is gone: its record, and the request that
// made it. The caller holds the lock.
func (s *Server) forget(id string, r *record) error {
	if err := s.kept.drop(id); err != nil {
		return err
	}
	delete(s.records, id)
	if made, asked := s.asked[r.request]; asked && made.id == id {
		delete(s.asked, r.request)
	}
	return nil
}

// lost forgets a sandbox its driver holds no more, and says whether it
// did: one that ended without the service ending it, as one whose ending
// outlasted the Delete that asked for it, is not kept for ever, nor the
// request that made it. It is looked at again in its turn, since what
// had the turn before may have changed it; whoever does not wait for
// the turn leaves one that is being changed to whoever changes it. A
// record is dropped on the driver's one word that there is no such
// sandbox, as it is when a server starts (recover).
func (s *Server) lost(ctx context.Context, id string, wait bool) bool {
	s.mu.Lock()
	r, there := s.records[id]
	s.mu.Unlock()
	if !there {
		return false
	}
	if wait {
		if err := r.turn.take(ctx); err != nil {
			return false
		}
	} else {
		select {
		case r.turn <- struct{}{}:
		default:
			return false
		}
	}
	defer r.turn.give()
	if _, err := s.driver.Observe(ctx, id); !errors.Is(err, driver.ErrNotFound) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// What had the turn before may have forgotten it.
	return s.records[id] == r && s.forget(id, r) == nil
}

// turnOf takes the turn of a sandbox this service made, and returns its
// record: every change to a sandbox, pausing, resuming and deleting it,
// is made in its turn, one after another. The record is read again once the turn is taken, since
// what had the turn before may have ended the sandbox. The turn is taken
// before the service's lock, never while it is held, and waited for only
// as long as the asker waits. The caller gives the turn back.
func (s *Server) turnOf(ctx context.Context, id string) (*record, error) {
	s.mu.Lock()
	r, there := s.records[id]
	s.mu.Unlock()
	if there {
		if err := r.turn.take(ctx); err != nil {
			code := connect.CodeCanceled
			if errors.Is(err, context.DeadlineExceeded) {
				code = connect.CodeDeadlineExceeded
			}
			return nil, refuse(code, "SANDBOX_BUSY", fmt.Sprintf("the sandbox %q is being changed, and was not waited for: %v", id, err))
		}
		s.mu.Lock()
		there = s.records[id] == r
		s.mu.Unlock()
		if !there {
			r.turn.give()
		}
	}
	if !there {
		return nil, refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", "no sandbox has the id asked for")
	}
	return r, nil
}

// wanted turns a spec into what a driver is asked for.
func wanted(spec *sandboxv1.SandboxSpec) (out driver.Sandbox, err error) {
	out = driver.Sandbox{
		Image:       spec.GetImage(),
		Arguments:   spec.GetArguments(),
		Wrapper:     spec.GetWrapper(),
		User:        spec.GetUser(),
		Writable:    spec.GetWritable(),
		Environment: spec.GetEnvironment(),
		Labels:      spec.GetLabels(),
	}
	if gate := spec.GetBeforeContainers(); gate != nil {
		if len(spec.GetContainers()) == 0 || len(gate.GetArguments()) == 0 || gate.GetArguments()[0] == "" || gate.GetTimeout() == nil || gate.GetTimeout().CheckValid() != nil || gate.GetTimeout().AsDuration() <= 0 || gate.GetTimeout().AsDuration() > 2*time.Minute {
			return driver.Sandbox{}, errors.New("a startup gate needs further containers, a command and a timeout of at most two minutes")
		}
		for _, argument := range gate.GetArguments() {
			if strings.ContainsRune(argument, 0) {
				return driver.Sandbox{}, errors.New("a startup gate argument contains a nul")
			}
		}
		out.BeforeContainers = &driver.StartupGate{Arguments: slices.Clone(gate.GetArguments()), Timeout: gate.GetTimeout().AsDuration()}
	}
	// A user is a number, and never root's: no sandbox runs as root.
	if out.User != "" {
		if !plain.User(out.User) {
			return driver.Sandbox{}, errors.New("a sandbox's user is a number, and not root's")
		}
	}
	// What it writes is a directory named plainly, and never the whole of
	// its image.
	if len(out.Writable) > maxWritable {
		return driver.Sandbox{}, fmt.Errorf("a sandbox writes %d directories of its image, and no more than %d", len(out.Writable), maxWritable)
	}
	for _, dir := range out.Writable {
		if !plain.Absolute(dir) {
			return driver.Sandbox{}, errors.New("a sandbox writes an absolute directory, plainly named, and not /")
		}
	}
	// What runs before its image's entrypoint is a program named
	// absolutely, and words a command line holds.
	if len(out.Wrapper) > 0 && !plain.Absolute(out.Wrapper[0]) {
		return driver.Sandbox{}, errors.New("a sandbox's wrapper begins with a program named absolutely and plainly")
	}
	for _, word := range out.Wrapper {
		if strings.ContainsRune(word, 0) {
			return driver.Sandbox{}, errors.New("a sandbox's wrapper holds a word no command line holds")
		}
	}
	switch spec.GetEgress().GetMode() {
	case sandboxv1.Egress_MODE_UNSPECIFIED, sandboxv1.Egress_MODE_ALLOW_ALL:
	case sandboxv1.Egress_MODE_BLOCK:
		out.Egress.Block = true
	case sandboxv1.Egress_MODE_ALLOW_LISTED:
		out.Egress.Listed = true
	default:
		return driver.Sandbox{}, fmt.Errorf("the egress mode %d is none this server knows", spec.GetEgress().GetMode())
	}
	// A rule's host is read as the driver port reads one, a name and a
	// port; what its programs and its requests may be is the driver's to
	// hold, which knows what it can enforce; that each is there at all is
	// held here.
	for _, rule := range spec.GetEgress().GetRules() {
		if rule.GetHost() == "" {
			return driver.Sandbox{}, errors.New("a rule names no host")
		}
		host, err := driver.ParseEndpoint(rule.GetHost())
		if err != nil {
			return driver.Sandbox{}, fmt.Errorf("a rule's host: %w", err)
		}
		if name := rule.GetCredential(); name != "" && !driver.CredentialID(name) {
			return driver.Sandbox{}, errors.New("a rule names a credential by what is no credential's name")
		}
		asked := driver.Rule{Host: host, Programs: rule.GetPrograms(), CredentialID: rule.GetCredential()}
		for _, program := range rule.GetPrograms() {
			if program == "" {
				return driver.Sandbox{}, errors.New("a rule names a program by no path")
			}
		}
		for _, request := range rule.GetRequests() {
			if request.GetMethod() == "" || request.GetPath() == "" {
				return driver.Sandbox{}, errors.New("a rule lets through a request of no method or no path")
			}
			asked.Requests = append(asked.Requests, driver.Request{Method: request.GetMethod(), Path: request.GetPath(), Query: request.GetQuery()})
		}
		out.Egress.Rules = append(out.Egress.Rules, asked)
	}
	// A sandbox that reaches all or nothing names nothing it reaches.
	if len(out.Egress.Rules) > 0 && (out.Egress.Block || spec.GetEgress().GetMode() == sandboxv1.Egress_MODE_ALLOW_ALL) {
		return driver.Sandbox{}, errors.New("a sandbox that reaches all or nothing names no rule")
	}
	for _, port := range spec.GetPorts() {
		out.Ports = append(out.Ports, int(port))
	}
	if out.Volumes, err = volumesOf(spec.GetVolumes()); err != nil {
		return driver.Sandbox{}, err
	}
	named := map[string]bool{"main": true}
	for _, c := range spec.GetContainers() {
		if !containerName.MatchString(c.GetName()) || named[c.GetName()] {
			return driver.Sandbox{}, errors.New("a container is named by lower-case letters, digits and dashes, once, and not main")
		}
		named[c.GetName()] = true
		if err := imageOf(c.GetImage()); err != nil {
			return driver.Sandbox{}, fmt.Errorf("a container's image: %w", err)
		}
		container := driver.SandboxContainer{Name: c.GetName(), Image: c.GetImage(), Arguments: c.GetArguments(), Environment: c.GetEnvironment()}
		if container.Volumes, err = volumesOf(c.GetVolumes()); err != nil {
			return driver.Sandbox{}, err
		}
		out.Containers = append(out.Containers, container)
	}
	return out, nil
}

// imageOf checks an image is named as an image is: a reference Docker's
// own parser reads (distribution/reference), by name or by id. What a
// caller names is never handed to a runtime as anything but an image.
func imageOf(image string) error {
	if image == "" {
		return errors.New("it names no image")
	}
	if _, err := reference.ParseAnyReference(image); err != nil {
		return errors.New("it is not named by an image's reference")
	}
	return nil
}

// containerName is what a container's name is made of.
var containerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// volumesOf turns a spec's volumes into what a driver is asked for. A
// subpath is a directory beneath the volume's root: what a caller names
// is checked before it reaches a mount.
func volumesOf(volumes []*sandboxv1.Volume) ([]driver.Volume, error) {
	var out []driver.Volume
	for _, v := range volumes {
		volume := driver.Volume{Name: v.GetName(), Path: v.GetPath(), Source: v.GetSource(), ReadOnly: v.GetReadOnly(), Subpath: v.GetSubpath()}
		switch v.GetKind() {
		case sandboxv1.Volume_KIND_UNSPECIFIED, sandboxv1.Volume_KIND_EPHEMERAL:
			volume.Kind = driver.Ephemeral
			if volume.ReadOnly {
				return nil, errors.New("a volume starts empty and may not be written: nothing could ever be in it")
			}
		case sandboxv1.Volume_KIND_HOST:
			volume.Kind = driver.Host
		default:
			return nil, errors.New("a volume is of no kind this server knows")
		}
		if sub := volume.Subpath; sub != "" {
			if volume.Kind == driver.Ephemeral {
				return nil, errors.New("a volume starts empty and names a subpath: it has none to mount")
			}
			if path.IsAbs(sub) || path.Clean(sub) != sub || sub == "." || sub == ".." || strings.HasPrefix(sub, "../") || strings.ContainsRune(sub, '\\') {
				return nil, errors.New("a volume's subpath is not a plain directory beneath its root")
			}
		}
		out = append(out, volume)
	}
	return out, nil
}

// refused turns a driver's refusal into the contract's: a capability not
// served, named, or a sandbox that is ending.
func refused(err error) error {
	if mend := callers(err); mend != nil {
		return mend
	}
	if errors.Is(err, driver.ErrEnding) {
		return refuse(connect.CodeFailedPrecondition, "SANDBOX_ENDING", err.Error())
	}
	return connect.NewError(connect.CodeInternal, err.Error())
}

// observe returns a sandbox as it is now.
func (s *Server) observe(ctx context.Context, id string) (*sandboxv1.Sandbox, error) {
	s.mu.Lock()
	r, ok := s.records[id]
	s.mu.Unlock()
	if !ok {
		return nil, refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", "no sandbox has the id asked for")
	}
	now, err := s.driver.Observe(ctx, id)
	if errors.Is(err, driver.ErrNotFound) {
		return nil, refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", fmt.Sprintf("the sandbox %q is gone", id))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	out := &sandboxv1.Sandbox{
		Id:         id,
		Spec:       proto.Clone(r.spec).(*sandboxv1.SandboxSpec),
		Driver:     s.driver.Name(),
		CreateTime: timestamppb.New(r.created),
	}
	switch now.State {
	case driver.Starting:
		out.State = sandboxv1.Sandbox_STATE_STARTING
	case driver.Running:
		out.State = sandboxv1.Sandbox_STATE_RUNNING
	case driver.Paused:
		out.State = sandboxv1.Sandbox_STATE_PAUSED
	case driver.Failed:
		out.State, out.Failure = sandboxv1.Sandbox_STATE_FAILED, now.Message
	case driver.Ending:
		out.State = sandboxv1.Sandbox_STATE_ENDING
	}
	for _, port := range r.spec.GetPorts() {
		out.Endpoints = append(out.Endpoints, &sandboxv1.Endpoint{
			Port: port,
			Url:  fmt.Sprintf("%s/sandboxes/%s/ports/%d", s.publicURL, id, port),
		})
	}
	return out, nil
}

func (s *Server) DescribeSandbox(ctx context.Context, req *sandboxv1.DescribeSandboxRequest) (*sandboxv1.DescribeSandboxResponse, error) {
	now, err := s.observe(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	return &sandboxv1.DescribeSandboxResponse{Sandbox: now}, nil
}

func (s *Server) ListSandboxes(ctx context.Context, req *sandboxv1.ListSandboxesRequest) (*sandboxv1.ListSandboxesResponse, error) {
	selected, err := selector(req.GetLabelSelector())
	if err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "LABEL_SELECTOR_INVALID", err.Error())
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.records))
	for id, r := range s.records {
		labelled := true
		for name, value := range selected {
			labelled = labelled && r.spec.GetLabels()[name] == value
		}
		if labelled {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return s.records[ids[i]].created.Before(s.records[ids[j]].created) })
	s.mu.Unlock()
	out := &sandboxv1.ListSandboxesResponse{}
	for _, id := range ids {
		now, err := s.observe(ctx, id)
		if connect.CodeOf(err) == connect.CodeNotFound {
			// What the driver holds no more is forgotten, unless it is
			// being changed: a list waits for no sandbox's turn.
			s.lost(ctx, id, false)
			continue
		}
		if err != nil {
			return nil, err
		}
		out.Sandboxes = append(out.Sandboxes, now)
	}
	kept, next, err := page.Of(out.Sandboxes, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, refuse(connect.CodeInvalidArgument, refusal.PageTokenInvalid, err.Error())
	}
	out.Sandboxes, out.NextPageToken = kept, next
	return out, nil
}

func (s *Server) DeleteSandbox(ctx context.Context, req *sandboxv1.DeleteSandboxRequest) (*sandboxv1.DeleteSandboxResponse, error) {
	// Only what this service made is its to delete: what makes sandboxes
	// may hold other things by other names.
	r, err := s.turnOf(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer r.turn.give()
	// One the driver no longer holds is gone all the same.
	if err := s.driver.Delete(ctx, req.GetSandboxId()); err != nil && !errors.Is(err, driver.ErrNotFound) {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	s.mu.Lock()
	err = s.forget(req.GetSandboxId(), r)
	s.mu.Unlock()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	return &sandboxv1.DeleteSandboxResponse{}, nil
}

// pause brings a sandbox to running or paused, as asked.
func (s *Server) pause(ctx context.Context, id string, paused bool) (*sandboxv1.Sandbox, error) {
	// One change to a sandbox at a time: what the driver is asked for is
	// what was asked last, and it is asked in that order.
	r, err := s.turnOf(ctx, id)
	if err != nil {
		return nil, err
	}
	defer r.turn.give()
	s.mu.Lock()
	r.wanted.Paused = paused
	wanted := r.wanted
	s.mu.Unlock()
	if _, err := s.driver.Apply(ctx, wanted); err != nil {
		return nil, refused(err)
	}
	return s.observe(ctx, id)
}

func (s *Server) PauseSandbox(ctx context.Context, req *sandboxv1.PauseSandboxRequest) (*sandboxv1.PauseSandboxResponse, error) {
	now, err := s.pause(ctx, req.GetSandboxId(), true)
	if err != nil {
		return nil, err
	}
	return &sandboxv1.PauseSandboxResponse{Sandbox: now}, nil
}

func (s *Server) ResumeSandbox(ctx context.Context, req *sandboxv1.ResumeSandboxRequest) (*sandboxv1.ResumeSandboxResponse, error) {
	now, err := s.pause(ctx, req.GetSandboxId(), false)
	if err != nil {
		return nil, err
	}
	return &sandboxv1.ResumeSandboxResponse{Sandbox: now}, nil
}

// WaitSandbox asks the driver where a sandbox is until it is no longer
// starting, or the caller gives up.
func (s *Server) WaitSandbox(ctx context.Context, req *sandboxv1.WaitSandboxRequest) (*sandboxv1.WaitSandboxResponse, error) {
	for {
		now, err := s.observe(ctx, req.GetSandboxId())
		if err != nil {
			return nil, err
		}
		if now.GetState() != sandboxv1.Sandbox_STATE_STARTING {
			return &sandboxv1.WaitSandboxResponse{Sandbox: now}, nil
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// execOutput is the most of what a command printed that ExecSandbox
// returns.
const execOutput = 1 << 20

func (s *Server) ExecSandbox(ctx context.Context, req *sandboxv1.ExecSandboxRequest) (*sandboxv1.ExecSandboxResponse, error) {
	if len(req.GetArguments()) == 0 {
		return nil, refuse(connect.CodeInvalidArgument, "COMMAND_MISSING", "ExecSandbox runs a command, and none is given")
	}
	execer, ok := s.driver.(driver.Execer)
	if !ok {
		return nil, refuse(connect.CodeUnimplemented, "CAPABILITY_NOT_SERVED", (&driver.Unsupported{Driver: s.driver.Name(), Capability: "a command run in a sandbox"}).Error())
	}
	s.mu.Lock()
	r, ok := s.records[req.GetSandboxId()]
	s.mu.Unlock()
	if !ok {
		return nil, refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", "no sandbox has the id asked for")
	}
	// A container is one the sandbox was made with, named as it was: the
	// name is never handed to the runtime otherwise.
	container := req.GetContainer()
	if container == "main" {
		container = ""
	}
	if container != "" && !slices.ContainsFunc(r.wanted.Containers, func(c driver.SandboxContainer) bool { return c.Name == container }) {
		return nil, refuse(connect.CodeNotFound, "CONTAINER_NOT_FOUND", fmt.Sprintf("the sandbox %q has no container of the name asked for", req.GetSandboxId()))
	}
	now, err := s.driver.Observe(ctx, req.GetSandboxId())
	if errors.Is(err, driver.ErrNotFound) {
		return nil, refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", fmt.Sprintf("the sandbox %q is gone", req.GetSandboxId()))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	if now.State != driver.Running {
		return nil, refuse(connect.CodeFailedPrecondition, "SANDBOX_NOT_RUNNING", fmt.Sprintf("the sandbox %q is not running", req.GetSandboxId()))
	}
	timeout := time.Minute
	if t := req.GetTimeout(); t != nil {
		if timeout = t.AsDuration(); timeout <= 0 {
			return nil, refuse(connect.CodeInvalidArgument, "TIMEOUT_INVALID", "a timeout is more than nothing")
		}
	}
	within, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	exit, output, _, err := execer.Exec(within, req.GetSandboxId(), container, req.GetArguments(), execOutput)
	// A caller who stopped waiting, by a deadline of their own or by
	// going away, is told that: the command did not run past its timeout.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(within.Err(), context.DeadlineExceeded) {
		return nil, refuse(connect.CodeDeadlineExceeded, "COMMAND_TIMED_OUT", fmt.Sprintf("the command ran past %s, and was ended", timeout))
	}
	if err != nil {
		return nil, refused(err)
	}
	return &sandboxv1.ExecSandboxResponse{ExitCode: int32(exit), Output: output}, nil
}

// held reads which sandbox and which port of it a request names, and
// answers the request itself where it names none the service made.
func (s *Server) held(w http.ResponseWriter, r *http.Request) (id string, port int, ok bool) {
	id = r.PathValue("id")
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		http.Error(w, "the port is not a number", http.StatusBadRequest)
		return "", 0, false
	}
	s.mu.Lock()
	_, made := s.records[id]
	s.mu.Unlock()
	if !made {
		http.Error(w, "no sandbox has that id", http.StatusNotFound)
		return "", 0, false
	}
	return id, port, true
}

// message is the most one message of a tunnel may hold. What is written
// to a tunnel is sent as it was written, and nothing that speaks through
// one writes near this much at once.
const message = 1 << 20

// tunnel is a connection to a port inside a sandbox, for what is not
// HTTP: SSH above all. It is a WebSocket of binary messages, whose bytes
// are the port's, so that it passes whatever speaks HTTP in front of
// the server. It ends, both ways, when either end does: a tunnel
// carries no closing of one direction alone.
func (s *Server) tunnel(w http.ResponseWriter, r *http.Request) {
	id, port, ok := s.held(w, r)
	if !ok {
		return
	}
	s.Tunnel(w, r, id, port)
}

// Dial connects to a port inside a sandbox the service made.
func (s *Server) Dial(ctx context.Context, id string, port int) (net.Conn, error) {
	s.mu.Lock()
	_, made := s.records[id]
	s.mu.Unlock()
	if !made {
		return nil, errors.New("no sandbox has the id asked for")
	}
	return s.driver.Dial(ctx, id, port)
}

// Tunnel answers a request with a tunnel to a port of a sandbox.
func (s *Server) Tunnel(w http.ResponseWriter, r *http.Request, id string, port int) {
	inside, err := s.Dial(r.Context(), id, port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer inside.Close()
	socket, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept has answered the request.
		return
	}
	outside := websocket.NetConn(r.Context(), socket, websocket.MessageBinary)
	socket.SetReadLimit(message)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(inside, outside); done <- struct{}{} }()
	go func() { _, _ = io.Copy(outside, inside); done <- struct{}{} }()
	<-done
	// The socket is told it is over, so that the other end reads the end
	// of the port and not a connection that broke.
	_ = socket.Close(websocket.StatusNormalClosure, "")
}

// port passes a request on to a port inside a sandbox.
func (s *Server) port(w http.ResponseWriter, r *http.Request) {
	id, port, ok := s.held(w, r)
	if !ok {
		return
	}
	s.Proxy(w, r, id, port, r.PathValue("rest"))
}

// Proxy passes a request on to a port inside a sandbox, asking there for
// the rest of its path.
func (s *Server) Proxy(w http.ResponseWriter, r *http.Request, id string, port int, rest string) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "sandbox"
			pr.Out.URL.Path = "/" + rest
			pr.Out.URL.RawPath = ""
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return s.Dial(ctx, id, port)
			},
			DisableKeepAlives: true,
		},
		// A stream is passed on as it is written.
		FlushInterval: -1,
		// A port nothing listens on yet closes the connection unanswered,
		// and a caller may go away: each is answered 502 and is no news.
		// Anything else is said, as the proxy would have said it.
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if !noNews(err) {
				log.Printf("http: proxy error: %v", err)
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// noNews says whether a proxied request failed as one does while a port
// is not yet listening or its caller has gone: the connection was closed
// unanswered, or the request was cancelled or ran out of time.
func noNews(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("sandbox: no randomness: %v", err))
	}
	return "sbx_" + hex.EncodeToString(b[:])
}

// credentialed returns the driver as one that keeps a credential outside
// its sandboxes, or the refusal of one that keeps none.
func (s *Server) credentialed() (driver.Credentialed, error) {
	keeps, ok := s.driver.(driver.Credentialed)
	if !ok || !s.driver.Capabilities().Credentials {
		return nil, refuse(connect.CodeUnimplemented, "CAPABILITY_NOT_SERVED", (&driver.Unsupported{Driver: s.driver.Name(), Capability: "a credential kept outside its sandboxes"}).Error())
	}
	return keeps, nil
}

// credentialOf is the name a credential call names, or the refusal of
// one that names none.
func credentialOf(call, name string) error {
	if !driver.CredentialID(name) {
		return refuse(connect.CodeInvalidArgument, "CREDENTIAL_NAME_INVALID", call+" is given a credential's name: lower-case letters, digits and dashes, at most 63")
	}
	return nil
}

// SetCredential gives the driver a host's credential to keep, under an
// id. Nothing of the secret is kept here, logged or said back.
func (s *Server) SetCredential(ctx context.Context, req *sandboxv1.SetCredentialRequest) (*sandboxv1.SetCredentialResponse, error) {
	given := req.GetCredential()
	if err := credentialOf("SetCredential", given.GetName()); err != nil {
		return nil, err
	}
	if given.GetHost() == "" || given.GetSecret() == "" {
		return nil, refuse(connect.CodeInvalidArgument, "CREDENTIAL_INVALID", "SetCredential is given a host and a secret")
	}
	keeps, err := s.credentialed()
	if err != nil {
		return nil, err
	}
	host, err := driver.ParseEndpoint(given.GetHost())
	if err != nil {
		return nil, said(err)
	}
	if err := keeps.Credential(ctx, given.GetName(), host, given.GetUsername(), given.GetSecret()); err != nil {
		return nil, said(err)
	}
	// What is said back is what is kept, as DescribeCredential says it.
	kept, username, variable, err := keeps.Kept(ctx, given.GetName())
	if err != nil {
		return nil, said(err)
	}
	return &sandboxv1.SetCredentialResponse{Credential: &sandboxv1.Credential{Name: given.GetName(), Host: kept.String(), Username: username, Variable: variable}}, nil
}

// DescribeCredential says what the driver keeps under an id, and never
// its secret.
func (s *Server) DescribeCredential(ctx context.Context, req *sandboxv1.DescribeCredentialRequest) (*sandboxv1.DescribeCredentialResponse, error) {
	if err := credentialOf("DescribeCredential", req.GetName()); err != nil {
		return nil, err
	}
	keeps, err := s.credentialed()
	if err != nil {
		return nil, err
	}
	host, username, variable, err := keeps.Kept(ctx, req.GetName())
	if errors.Is(err, driver.ErrNotFound) {
		return nil, refuse(connect.CodeNotFound, "CREDENTIAL_NOT_FOUND", "no credential is kept by the name asked for")
	}
	if err != nil {
		return nil, said(err)
	}
	return &sandboxv1.DescribeCredentialResponse{Credential: &sandboxv1.Credential{Name: req.GetName(), Host: host.String(), Username: username, Variable: variable}}, nil
}

// DeleteCredential has the driver let go of the credential under an id.
func (s *Server) DeleteCredential(ctx context.Context, req *sandboxv1.DeleteCredentialRequest) (*sandboxv1.DeleteCredentialResponse, error) {
	if err := credentialOf("DeleteCredential", req.GetName()); err != nil {
		return nil, err
	}
	keeps, err := s.credentialed()
	if err != nil {
		return nil, err
	}
	if err := keeps.Forget(ctx, req.GetName()); err != nil {
		if errors.Is(err, driver.ErrInUse) {
			return nil, refuse(connect.CodeFailedPrecondition, "CREDENTIAL_IN_USE", "a sandbox is still let through with the credential: it is let go of once those sandboxes have ended")
		}
		return nil, said(err)
	}
	return &sandboxv1.DeleteCredentialResponse{}, nil
}

// refuse is a refusal of the sandbox service, with its reason.
func refuse(code connect.Code, reason, message string) *connect.Error {
	return refusal.New(refusal.Sandbox, code, reason, message)
}

// said is a driver's failure as a caller is told it: what was asked and
// is not well formed is the caller's to mend, and anything else is not.
func said(err error) error {
	if mend := callers(err); mend != nil {
		return mend
	}
	return connect.NewError(connect.CodeUnavailable, err.Error())
}

// callers is a driver's failure where it is the caller's to mend, as the
// contract says it: a capability the driver has not, which is not served
// and is named, and what is not well formed; and nil where
// it is neither.
func callers(err error) error {
	var unsupported *driver.Unsupported
	if errors.As(err, &unsupported) {
		return refuse(connect.CodeUnimplemented, "CAPABILITY_NOT_SERVED", unsupported.Error())
	}
	if errors.Is(err, driver.ErrInvalid) {
		return refuse(connect.CodeInvalidArgument, "SPEC_INVALID", err.Error())
	}
	return nil
}

// maxWritable is how many directories of its image a sandbox may write.
const maxWritable = 32
