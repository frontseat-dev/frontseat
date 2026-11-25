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

	"github.com/frontseat-dev/frontseat/core/page"
	"github.com/frontseat-dev/frontseat/core/plain"
	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/core/refusal"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
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

// recover takes up the sandboxes a server before this one made, each
// with the request that made it. One whose making that server had not
// ended is made again, as it was asked for: its driver makes what it
// does not hold, and changes nothing of what it does. One its driver
// could not make is failed still, saying why, whatever the driver left
// of it. Any other that is gone is forgotten; one that is there is the
// service's again.
func (s *Server) recover(ctx context.Context) error {
	recorded, err := s.kept.all()
	if err != nil {
		return fmt.Errorf("the sandboxes' records: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range recorded {
		remade := r.making && !r.ending
		if !remade && r.failure == "" {
			if _, err := s.driver.Observe(ctx, id); errors.Is(err, driver.ErrNotFound) {
				if err := s.kept.drop(id); err != nil {
					return err
				}
				continue
			}
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
		if !remade {
			close(r.done)
			continue
		}
		s.goMake(id, r)
	}
	return nil
}

// goMake has the driver make a recorded sandbox apart from whoever asked
// for it, on a context of its own beneath the server's: the making holds
// the sandbox's turn from here until it has ended. The caller holds the
// lock, and the record is the service's.
func (s *Server) goMake(id string, r *record) {
	r.turn <- struct{}{}
	making, stop := context.WithCancel(s.making)
	r.stop = stop
	s.makings.Add(1)
	go s.build(making, id, r, r.wanted)
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
	return wanted, nil
}

// Close stops every making under way and waits for it to stop, and
// then lets go of what its driver holds open, where the driver holds
// anything: a gateway's connection. A sandbox whose making was stopped
// is still recorded as being made, and a server made again over the
// same state makes it again (recover): a making stopped by a server's
// end is no failure. A service is closed by whoever made it once it is
// done with it; nothing is made after.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopMaking()
	s.makings.Wait()
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

	// making is what every sandbox is made under, apart from whoever
	// asked for it: stopMaking ends it, when the server closes. makings
	// are the makings under way, which Close waits for.
	making     context.Context
	stopMaking context.CancelFunc
	makings    sync.WaitGroup

	mu sync.Mutex
	// closed is whether the server is closing: nothing is made after.
	closed  bool
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
	// turn is held while the sandbox is being changed: by its making
	// first, until the driver has made it or failed to.
	turn turn
	// stop stops its making while it is under way on this server, and
	// is nil once that making has ended.
	stop context.CancelFunc
	// done is closed once the making on this server has ended, or at
	// once where there was none (Made).
	done chan struct{}
	// making is whether its driver has not yet made it, nor failed to:
	// kept, so that a server that stops meanwhile leaves the making to
	// the next (recover).
	making bool
	// ending is whether a Delete stopped its making: it is made no
	// further, and is said to be ending until a Delete has ended it.
	ending bool
	// failure is why the driver could not make it, where it could not.
	failure string
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
	if len(req.GetRequestId()) > refusal.RequestIDMost {
		return nil, refuse(connect.CodeInvalidArgument, "REQUEST_ID_INVALID", "a request id is at most 128 bytes")
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
// own (CreateSandbox clones it), and returns at once, the sandbox
// starting. What the driver cannot enforce, and what it finds not well
// formed where it can say so before making anything (driver.Checker), is
// refused here, on the asker's context, and nothing is made. The
// sandbox is recorded as being made before it is: a server that stops
// between the two, or while it is made, makes it again when it starts
// (recover). The making itself is the driver's, apart from whoever
// asked, who may stop waiting for it: it holds the sandbox's turn until
// it ends, and a sandbox the driver could not make is failed, saying
// why, and stays the service's, to be deleted with whatever the driver
// left of it.
func (s *Server) make(ctx context.Context, spec *sandboxv1.SandboxSpec, request string) (*sandboxv1.Sandbox, error) {
	id := newID()
	wanted, err := s.asks(spec, id)
	if err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "SPEC_INVALID", err.Error())
	}
	if err := driver.Admit(s.driver, wanted); err != nil {
		return nil, refused(err)
	}
	if checker, ok := s.driver.(driver.Checker); ok {
		if err := checker.Check(ctx, wanted); err != nil {
			return nil, refused(err)
		}
	}
	r := &record{spec: spec, wanted: wanted, created: time.Now(), request: request, turn: newTurn(), done: make(chan struct{}), making: true}
	if err := s.kept.keep(id, r); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = s.kept.drop(id)
		return nil, connect.NewError(connect.CodeUnavailable, "the sandbox service is closing, and makes nothing more")
	}
	s.records[id] = r
	s.goMake(id, r)
	return s.shown(id, r, driver.Sandbox{State: driver.Starting}), nil
}

// build has the driver make a sandbox, and keeps how that ended: made,
// or failed and why. A making the service stopped, by a Delete or by
// closing, is no failure: one a Delete stopped is ending, and one a
// server's end stopped is made again by the next. It holds the
// sandbox's turn, which it gives back once that is kept.
func (s *Server) build(ctx context.Context, id string, r *record, wanted driver.Sandbox) {
	defer s.makings.Done()
	defer r.turn.give()
	_, failed := s.driver.Apply(ctx, wanted)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer close(r.done)
	// Whether the service stopped it is read under the lock, as a
	// Delete says it is ending and stops it under the lock: read before,
	// a Delete between the two would have a sandbox it stopped failed.
	stopped := ctx.Err() != nil
	r.stop()
	r.stop = nil
	if stopped {
		return
	}
	if failed != nil {
		r.failure = "the driver could not make it: " + failed.Error()
	}
	r.making = false
	if err := s.kept.keep(id, r); err != nil {
		// Kept as being made still, it is made again by the next server,
		// whose driver makes what it does not hold and changes nothing of
		// what it does.
		log.Printf("sandbox: %v", err)
	}
}

// Made waits until the driver has made a sandbox this service made, or
// failed to, or its making was stopped, for as long as the caller waits,
// an image's pull among it: what is waited for once it is made, as that
// it comes to run, is the caller's to bound apart.
func (s *Server) Made(ctx context.Context, id string) error {
	s.mu.Lock()
	r, ok := s.records[id]
	s.mu.Unlock()
	if !ok {
		return refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", "no sandbox has the id asked for")
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// userOf is the user a sandbox's first process runs as, as a driver
// takes it: by number, or empty for the image's own. Zero, root's, is
// the image's own: no sandbox is asked to run as root.
func userOf(id uint32) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(id), 10)
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
		User:        userOf(spec.GetUserId()),
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
	// What a sandbox may reach is said by every spec: no driver's default
	// stands in for it, which would mean anything under one driver and
	// what the rules name under another.
	switch spec.GetEgress().GetMode() {
	case sandboxv1.Egress_MODE_UNSPECIFIED:
		return driver.Sandbox{}, errors.New("a sandbox says what it may reach: its egress has no mode")
	case sandboxv1.Egress_MODE_ALLOW_ALL:
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

// observe returns a sandbox as it is now: starting while it is being
// made, failed where the driver could not make it, whatever the driver
// holds of it, and ending where a Delete stopped its making, until it is
// gone.
func (s *Server) observe(ctx context.Context, id string) (*sandboxv1.Sandbox, error) {
	s.mu.Lock()
	r, ok := s.records[id]
	var making, stopping, ending bool
	var failure string
	if ok {
		making, stopping, ending, failure = r.making, r.stop != nil, r.ending, r.failure
	}
	s.mu.Unlock()
	if !ok {
		return nil, refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", "no sandbox has the id asked for")
	}
	switch {
	case failure != "":
		return s.shown(id, r, driver.Sandbox{State: driver.Failed, Message: failure}), nil
	case ending && stopping:
		return s.shown(id, r, driver.Sandbox{State: driver.Ending}), nil
	case making && !ending:
		return s.shown(id, r, driver.Sandbox{State: driver.Starting}), nil
	}
	now, err := s.driver.Observe(ctx, id)
	if errors.Is(err, driver.ErrNotFound) {
		return nil, refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", fmt.Sprintf("the sandbox %q is gone", id))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	if ending {
		now.State = driver.Ending
	}
	return s.shown(id, r, now), nil
}

// shown is a sandbox of a record as the API says it, where it is now.
func (s *Server) shown(id string, r *record, now driver.Sandbox) *sandboxv1.Sandbox {
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
			Uri:  fmt.Sprintf("%s/sandboxes/%s/ports/%d", s.publicURL, id, port),
		})
	}
	return out
}

func (s *Server) DescribeSandbox(ctx context.Context, req *sandboxv1.DescribeSandboxRequest) (*sandboxv1.DescribeSandboxResponse, error) {
	now, err := s.observe(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	return &sandboxv1.DescribeSandboxResponse{Sandbox: now}, nil
}

func (s *Server) ListSandboxes(ctx context.Context, req *sandboxv1.ListSandboxesRequest) (*sandboxv1.ListSandboxesResponse, error) {
	selected := req.GetLabels()
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
	// One being made is made no further: it is ending from here, kept so
	// before its making is stopped, and its making gives its turn back
	// once the driver has. That is no failure of its making, though this
	// Delete gives up before the turn is had: one sent again ends it.
	s.mu.Lock()
	if r, there := s.records[req.GetSandboxId()]; there && r.stop != nil {
		if !r.ending {
			r.ending = true
			if err := s.kept.keep(req.GetSandboxId(), r); err != nil {
				r.ending = false
				s.mu.Unlock()
				return nil, connect.NewError(connect.CodeInternal, err.Error())
			}
		}
		r.stop()
	}
	s.mu.Unlock()
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
	failure, ending := r.failure, r.ending
	if failure == "" && !ending {
		r.wanted.Paused = paused
	}
	wanted := r.wanted
	s.mu.Unlock()
	// One a Delete stopped the making of is ended, and nothing else.
	if ending {
		return nil, refuse(connect.CodeFailedPrecondition, "SANDBOX_ENDING", fmt.Sprintf("the sandbox %q is ending", id))
	}
	// One the driver could not make is not made now by being paused or
	// resumed: it is deleted, and asked for anew.
	if failure != "" {
		return nil, refuse(connect.CodeFailedPrecondition, "SANDBOX_FAILED", fmt.Sprintf("the sandbox %q could not be made: it is deleted and asked for anew", id))
	}
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
	var now *sandboxv1.Sandbox
	err := poll.Until(ctx, 100*time.Millisecond, func() (bool, error) {
		var err error
		if now, err = s.observe(ctx, req.GetSandboxId()); err != nil {
			return false, err
		}
		return now.GetState() != sandboxv1.Sandbox_STATE_STARTING, nil
	})
	if err != nil {
		return nil, err
	}
	return &sandboxv1.WaitSandboxResponse{Sandbox: now}, nil
}

// execOutput is the most of what a command printed that ExecSandbox
// returns.
const execOutput = 1 << 20

// execLongest is the longest a command ExecSandbox runs may be given:
// a caller that would wait longer starts a service instead.
const execLongest = 10 * time.Minute

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
	now, err := s.observe(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	if now.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		return nil, refuse(connect.CodeFailedPrecondition, "SANDBOX_NOT_RUNNING", fmt.Sprintf("the sandbox %q is not running", req.GetSandboxId()))
	}
	timeout := time.Minute
	if t := req.GetTimeout(); t != nil {
		if timeout = t.AsDuration(); timeout <= 0 || timeout > execLongest {
			return nil, refuse(connect.CodeInvalidArgument, "TIMEOUT_INVALID", "a timeout is more than nothing, and at most ten minutes")
		}
	}
	within, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	exit, output, truncated, err := execer.Exec(within, req.GetSandboxId(), container, req.GetArguments(), execOutput)
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
	return &sandboxv1.ExecSandboxResponse{ExitCode: int32(exit), Output: output, OutputTruncated: truncated}, nil
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
		_ = refusals.Write(w, r, unreached(driver.ErrNotFound))
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
		return nil, fmt.Errorf("no sandbox has the id asked for: %w", driver.ErrNotFound)
	}
	return s.driver.Dial(ctx, id, port)
}

// Tunnel answers a request with a tunnel to a port of a sandbox. A port
// that is not reached is refused by name, in the sandbox service's
// domain, as a call is refused: NOT_FOUND with SANDBOX_NOT_FOUND where
// no sandbox has the id, FAILED_PRECONDITION with PORT_UNREACHED
// otherwise. What the driver said of it is logged here and told to no
// caller: it holds whatever stands between the service and the sandbox
// cared to say. Whoever serves a way of its own to a sandbox's port, in
// a domain of its own, dials (Dial), refuses as it does, and carries
// the connection (Carry).
func (s *Server) Tunnel(w http.ResponseWriter, r *http.Request, id string, port int) {
	inside, err := s.Dial(r.Context(), id, port)
	if err != nil {
		_ = refusals.Write(w, r, unreached(err))
		return
	}
	Carry(w, r, inside)
}

// refusals writes a refusal as the caller's protocol says it.
var refusals = connecthttp.NewErrorWriter()

// unreached is what a caller is told of a port that was not reached.
// FAILED_PRECONDITION, since most often nothing listens there yet, which
// asking again does not mend and starting what listens does: UNAVAILABLE
// would have every client that retries of itself ask the driver again
// and again for a stream to nothing.
func unreached(err error) *connect.Error {
	if errors.Is(err, driver.ErrNotFound) {
		return refuse(connect.CodeNotFound, "SANDBOX_NOT_FOUND", "no sandbox has the id asked for")
	}
	log.Printf("sandbox: a port was not reached: %v", err)
	return refuse(connect.CodeFailedPrecondition, "PORT_UNREACHED", "the port of the sandbox was not reached: nothing answers there, or the sandbox is not running")
}

// Carry answers a request with a tunnel to a connection already made to
// a port inside a sandbox, which it takes and closes: a WebSocket of
// binary messages, whose bytes are the port's. It returns when the
// tunnel has ended, both ways: when either end does, or the request's
// context, which the server's ending ends. Nothing it started is then
// running: each direction is ended and waited for, and only then is the
// connection closed, where the connection can be told to stop (a
// deadline); one that cannot (a stream of a driver's) is closed to end
// what reads it, and that is waited for too. A connection says it
// takes no deadline (os.ErrNoDeadline): one that took a deadline and
// did not heed it would be waited for without end.
func Carry(w http.ResponseWriter, r *http.Request, inside net.Conn) {
	socket, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept has answered the request, and nothing uses the
		// connection yet.
		_ = inside.Close()
		return
	}
	socket.SetReadLimit(message)
	outside := websocket.NetConn(r.Context(), socket, websocket.MessageBinary)
	in, out := make(chan struct{}), make(chan struct{})
	go func() { defer close(in); _, _ = io.Copy(inside, outside) }()
	go func() { defer close(out); _, _ = io.Copy(outside, inside) }()
	select {
	case <-in:
	case <-out:
	case <-r.Context().Done():
	}
	// Whatever still reads or writes the port is told to stop: by a
	// deadline, or, where the connection takes none, by its closing.
	closed := inside.SetDeadline(time.Now()) != nil
	if closed {
		_ = inside.Close()
	}
	// The socket is told it is over, so that the other end reads the end
	// of the port and not a connection that broke; that ends whatever
	// still reads or writes the socket, within the time its closing is
	// given. A request that has ended, as the server's ending ends it,
	// waits for no answer to that: the socket is closed.
	if r.Context().Err() != nil {
		_ = socket.CloseNow()
	} else {
		_ = socket.Close(websocket.StatusNormalClosure, "")
	}
	<-in
	<-out
	if !closed {
		_ = inside.Close()
	}
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
	if given.GetHost() == "" {
		return nil, refuse(connect.CodeInvalidArgument, "CREDENTIAL_HOST_INVALID", "SetCredential is given the host the credential is for")
	}
	if given.GetUsername() != "" && !plain.Username(given.GetUsername()) {
		return nil, refuse(connect.CodeInvalidArgument, "CREDENTIAL_USERNAME_INVALID", "a username is letters, digits and . _ @ -, at most 128")
	}
	if !plain.Secret(given.GetSecret()) {
		return nil, refuse(connect.CodeInvalidArgument, "CREDENTIAL_SECRET_INVALID", "a credential's secret is given, on one line of at most 4096 bytes")
	}
	keeps, err := s.credentialed()
	if err != nil {
		return nil, err
	}
	host, err := driver.ParseEndpoint(given.GetHost())
	if err != nil {
		return nil, refuse(connect.CodeInvalidArgument, "CREDENTIAL_HOST_INVALID", err.Error())
	}
	if err := keeps.Credential(ctx, given.GetName(), host, given.GetUsername(), given.GetSecret()); err != nil {
		switch {
		case errors.Is(err, driver.ErrHostInvalid):
			return nil, refuse(connect.CodeInvalidArgument, "CREDENTIAL_HOST_INVALID", err.Error())
		case errors.Is(err, driver.ErrTaken):
			return nil, refuse(connect.CodeAlreadyExists, "CREDENTIAL_NAME_TAKEN", err.Error())
		}
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
