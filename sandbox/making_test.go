package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testsandbox"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// running is a memory driver that runs every command it is asked to,
// and says nothing of it.
type running struct{ *drivertest.Memory }

func (running) Exec(context.Context, string, string, []string, int) (int, []byte, bool, error) {
	return 0, nil, false, nil
}

// makingOver is a sandbox service over a memory driver that runs
// commands, closed when the test ends.
func makingOver(t *testing.T, d *drivertest.Memory) *sandbox.Server {
	t.Helper()
	return makingAt(t, running{d}, t.TempDir())
}

// makingAt is a sandbox service over a driver, keeping its records in a
// state another may have kept them in before, closed when the test ends.
func makingAt(t *testing.T, d driver.Driver, state string) *sandbox.Server {
	t.Helper()
	s, err := sandbox.New(sandbox.Options{Driver: d, PublicURL: "http://sandboxes.example", State: state})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// asking is a request for a sandbox, under a request id or none.
func asking(request string) *sandboxv1.CreateSandboxRequest {
	return &sandboxv1.CreateSandboxRequest{RequestId: request, Spec: &sandboxv1.SandboxSpec{Image: "box", Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL}}}
}

// answered asks for a sandbox, and fails the test where the answer does
// not come within a second: CreateSandbox returns at once.
func answered(t *testing.T, s *sandbox.Server, asked *sandboxv1.CreateSandboxRequest) *sandboxv1.Sandbox {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	made, err := s.CreateSandbox(ctx, asked)
	if err != nil {
		t.Fatalf("CreateSandbox did not answer at once: %v", err)
	}
	return made.GetSandbox()
}

// waiting waits until so many Applies wait on the driver.
func waiting(t *testing.T, d *drivertest.Memory, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := poll.Until(ctx, time.Millisecond, func() (bool, error) { return d.Waiting() == n, nil }); err != nil {
		t.Fatalf("%d Applies wait on the driver, want %d: %v", d.Waiting(), n, err)
	}
}

// notDescribed fails the test where a sandbox that was deleted is
// described.
func notDescribed(t *testing.T, s *sandbox.Server, id string) {
	t.Helper()
	if _, err := s.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: id}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("deleted, it is described: %v", err)
	}
}

// A sandbox is answered for at once, starting, while its driver makes
// it, as a driver does that pulls an image first; it is running once the
// driver has made it. The control is a driver that makes it at once,
// whose sandbox runs as soon.
func TestASandboxIsAnsweredForWhileItsDriverMakesIt(t *testing.T) {
	d := drivertest.NewMemory()
	s := makingOver(t, d)
	if control := testsandbox.Settled(t, s, answered(t, s, asking("")).GetId()); control.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Fatalf("one made at once is %v, want it running", control.GetState())
	}

	let := d.Hold()
	defer let(nil)
	made := answered(t, s, asking(""))
	waiting(t, d, 1)
	if made.GetState() != sandboxv1.Sandbox_STATE_STARTING {
		t.Errorf("answered %v while it is made, want it starting", made.GetState())
	}
	described, err := s.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetId()})
	if err != nil || described.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_STARTING {
		t.Errorf("described as %v (%v) while it is made, want it starting", described.GetSandbox().GetState(), err)
	}
	_, err = s.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: made.GetId(), Arguments: []string{"true"}})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeFailedPrecondition || reason != "SANDBOX_NOT_RUNNING" {
		t.Errorf("a command in one being made: %v, want it refused SANDBOX_NOT_RUNNING", err)
	}
	let(nil)
	if now := testsandbox.Settled(t, s, made.GetId()); now.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Errorf("once made it is %v, want it running", now.GetState())
	}
	if _, err := s.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: made.GetId(), Arguments: []string{"true"}}); err != nil {
		t.Errorf("a command in one made: %v", err)
	}
}

// A sandbox its driver could not make has failed, saying why, and stays
// the service's until it is deleted: it is not made by being resumed.
// The control is TestASandboxIsAnsweredForWhileItsDriverMakesIt, whose
// driver made it.
func TestASandboxItsDriverCouldNotMakeHasFailedSayingWhy(t *testing.T) {
	d := drivertest.NewMemory()
	s := makingOver(t, d)
	let := d.Hold()
	made := answered(t, s, asking(""))
	waiting(t, d, 1)
	let(errors.New("the image could not be pulled"))
	now := testsandbox.Settled(t, s, made.GetId())
	if now.GetState() != sandboxv1.Sandbox_STATE_FAILED || !strings.Contains(now.GetFailure(), "the image could not be pulled") {
		t.Fatalf("it is %v (%q), want it failed, saying why", now.GetState(), now.GetFailure())
	}
	listed, err := s.ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{})
	if err != nil || len(listed.GetSandboxes()) != 1 || listed.GetSandboxes()[0].GetState() != sandboxv1.Sandbox_STATE_FAILED {
		t.Errorf("listed %v (%v), want the one that failed", listed.GetSandboxes(), err)
	}
	_, err = s.ResumeSandbox(context.Background(), &sandboxv1.ResumeSandboxRequest{SandboxId: made.GetId()})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeFailedPrecondition || reason != "SANDBOX_FAILED" {
		t.Errorf("resuming one that failed: %v, want it refused SANDBOX_FAILED", err)
	}
	if d.Holds(made.GetId()) {
		t.Error("resuming one that failed made it")
	}
	if _, err := s.DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()}); err != nil {
		t.Fatal(err)
	}
	notDescribed(t, s, made.GetId())
}

// A sandbox deleted while its driver makes it is made no further: its
// making is stopped, and nothing of it is left, with the driver or the
// service. The control is the same sandbox left alone, which the driver
// makes.
func TestASandboxDeletedWhileItIsMadeLeavesNothing(t *testing.T) {
	d := drivertest.NewMemory()
	s := makingOver(t, d)
	let := d.Hold()
	defer let(nil)
	control := answered(t, s, asking(""))
	made := answered(t, s, asking(""))
	waiting(t, d, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()}); err != nil {
		t.Fatalf("deleting one being made: %v", err)
	}
	waiting(t, d, 1)
	let(nil)
	if now := testsandbox.Settled(t, s, control.GetId()); now.GetState() != sandboxv1.Sandbox_STATE_RUNNING || !d.Holds(control.GetId()) {
		t.Errorf("the control is %v, want it made and running", now.GetState())
	}
	if d.Holds(made.GetId()) {
		t.Error("the driver made one deleted while it was made")
	}
	notDescribed(t, s, made.GetId())
}

// A request sent again while the sandbox it asked for is made, as after
// an answer lost or a caller that stopped waiting, finds that sandbox:
// one is made. The control is another request, which makes another.
func TestARequestSentAgainWhileItsSandboxIsMadeFindsIt(t *testing.T) {
	d := drivertest.NewMemory()
	s := makingOver(t, d)
	let := d.Hold()
	defer let(nil)
	first := answered(t, s, asking("of-x"))
	waiting(t, d, 1)
	again := answered(t, s, asking("of-x"))
	if again.GetId() != first.GetId() || again.GetState() != sandboxv1.Sandbox_STATE_STARTING {
		t.Errorf("sent again it answered %s, %v; want %s, starting", again.GetId(), again.GetState(), first.GetId())
	}
	other := answered(t, s, asking("of-y"))
	waiting(t, d, 2)
	if other.GetId() == first.GetId() {
		t.Error("another request found the first's sandbox")
	}
	let(nil)
	for _, id := range []string{first.GetId(), other.GetId()} {
		if now := testsandbox.Settled(t, s, id); now.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
			t.Errorf("%s is %v, want it running", id, now.GetState())
		}
	}
	listed, err := s.ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{})
	if err != nil || len(listed.GetSandboxes()) != 2 {
		t.Errorf("listed %v (%v), want two", listed.GetSandboxes(), err)
	}
}

// A service that closes stops the makings under way and waits for each
// to have stopped; it makes nothing after. The control is a making under
// way before it closes.
func TestAServiceThatClosesStopsItsMakings(t *testing.T) {
	d := drivertest.NewMemory()
	s := makingOver(t, d)
	let := d.Hold()
	defer let(nil)
	answered(t, s, asking(""))
	waiting(t, d, 1)
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing did not stop the making under way")
	}
	if n := d.Waiting(); n != 0 {
		t.Errorf("closed, it left %d makings under way", n)
	}
	if _, err := s.CreateSandbox(context.Background(), asking("")); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("closed, it was asked for a sandbox: %v, want it unavailable", err)
	}
}

// A sandbox whose making a server stopped, as by its end or a crash
// while an image was pulled, is not lost: the server made again over
// the same state makes it, under the same id, and the request that
// asked for it finds it there. The control is a sandbox a Delete
// stopped the making of before the server stopped, which is made by
// no server after.
func TestASandboxWhoseMakingAServerStoppedIsMadeByTheNext(t *testing.T) {
	d := drivertest.NewMemory()
	state := t.TempDir()
	first := makingAt(t, running{d}, state)
	let := d.Hold()
	defer let(nil)
	made := answered(t, first, asking("of-x"))
	deleted := answered(t, first, asking("of-y"))
	waiting(t, d, 2)
	gaveUp, cancel := context.WithCancel(context.Background())
	cancel()
	// Whether it gives up or not, it stops the making, and nothing of it
	// is made after.
	_, _ = first.DeleteSandbox(gaveUp, &sandboxv1.DeleteSandboxRequest{SandboxId: deleted.GetId()})
	waiting(t, d, 1)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	waiting(t, d, 0)

	next := makingAt(t, running{d}, state)
	waiting(t, d, 1)
	let(nil)
	if now := testsandbox.Settled(t, next, made.GetId()); now.GetState() != sandboxv1.Sandbox_STATE_RUNNING || !d.Holds(made.GetId()) {
		t.Fatalf("a sandbox whose making a server stopped is %v after the next started, want it made and running", now.GetState())
	}
	if again := answered(t, next, asking("of-x")); again.GetId() != made.GetId() {
		t.Errorf("the request sent again found %s, want %s", again.GetId(), made.GetId())
	}
	if d.Holds(deleted.GetId()) {
		t.Error("the control: a sandbox a Delete stopped the making of was made by the next server")
	}
	notDescribed(t, next, deleted.GetId())
}

// stubborn is a memory driver whose making waits for the test, whether
// or not its asker still does, and only then finds that it was stopped:
// a provider's does that is pulling an image when it is told to stop.
type stubborn struct {
	running
	// begun is told when a making begins; release lets it go on.
	begun, release chan struct{}
}

func (d stubborn) Apply(ctx context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if !d.Holds(record.ID) {
		d.begun <- struct{}{}
		<-d.release
		if err := ctx.Err(); err != nil {
			return driver.Sandbox{}, err
		}
	}
	return d.Memory.Apply(ctx, record)
}

// A sandbox whose making a Delete stopped is ending, and has not failed,
// though the Delete gave up before it was gone: the service stopped the
// making, which is no failure of the sandbox's. A Delete sent again ends
// it. The control is the same sandbox left alone, which is made.
func TestASandboxADeleteStoppedTheMakingOfIsEndingAndNotFailed(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		d := stubborn{running: running{drivertest.NewMemory()}, begun: make(chan struct{}, 1), release: make(chan struct{})}
		s := makingAt(t, d, t.TempDir())
		made := answered(t, s, asking(""))
		<-d.begun
		if !deleted {
			close(d.release)
			if now := testsandbox.Settled(t, s, made.GetId()); now.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
				t.Errorf("the control: left alone, it is %v, want it running", now.GetState())
			}
			continue
		}
		gaveUp, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := s.DeleteSandbox(gaveUp, &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()}); err == nil {
			t.Fatal("a Delete that did not wait for the making ended it")
		}
		described, err := s.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetId()})
		if err != nil || described.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_ENDING {
			t.Errorf("while its stopped making ends, it is %v (%v), want it ending", described.GetSandbox().GetState(), err)
		}
		close(d.release)
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.Made(ctx, made.GetId()); err != nil {
			t.Fatal(err)
		}
		stop()
		described, err = s.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetId()})
		if described.GetSandbox().GetState() == sandboxv1.Sandbox_STATE_FAILED {
			t.Errorf("once its making stopped, it has failed: %q", described.GetSandbox().GetFailure())
		} else if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("once its making stopped, it is described: %v", err)
		}
		if _, err := s.DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()}); err != nil {
			t.Fatalf("sent again, the Delete: %v", err)
		}
		notDescribed(t, s, made.GetId())
		if listed, err := s.ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{}); err != nil || len(listed.GetSandboxes()) != 0 {
			t.Errorf("deleted, it is listed: %v (%v)", listed.GetSandboxes(), err)
		}
	}
}

// obstinate is a memory driver whose making waits for the test and then
// makes the sandbox though it was told to stop, as a provider's does
// whose making had gone too far to be stopped.
type obstinate struct {
	running
	// begun is told when a making begins; release lets one go on.
	begun, release chan struct{}
}

func (d obstinate) Apply(ctx context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if !d.Holds(record.ID) {
		d.begun <- struct{}{}
		<-d.release
		return d.Memory.Apply(context.WithoutCancel(ctx), record)
	}
	return d.Memory.Apply(ctx, record)
}

// A sandbox a Delete stopped the making of, which its driver made all
// the same, is ending whatever its driver says of it, and is neither
// paused nor resumed: it is refused SANDBOX_ENDING. A Delete sent again
// ends it. The control is one made and left alone, which runs, and is
// paused and resumed.
func TestASandboxThatIsEndingThoughItsDriverHoldsItIsEndingAndNotChanged(t *testing.T) {
	d := obstinate{running: running{drivertest.NewMemory()}, begun: make(chan struct{}, 1), release: make(chan struct{}, 1)}
	s := makingAt(t, d, t.TempDir())
	ctx := context.Background()

	control := answered(t, s, asking(""))
	<-d.begun
	d.release <- struct{}{}
	if now := testsandbox.Settled(t, s, control.GetId()); now.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Fatalf("the control is %v, want it running", now.GetState())
	}
	if paused, err := s.PauseSandbox(ctx, &sandboxv1.PauseSandboxRequest{SandboxId: control.GetId()}); err != nil || paused.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_PAUSED {
		t.Errorf("the control is paused as %v (%v), want it paused", paused.GetSandbox().GetState(), err)
	}
	if resumed, err := s.ResumeSandbox(ctx, &sandboxv1.ResumeSandboxRequest{SandboxId: control.GetId()}); err != nil || resumed.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Errorf("the control is resumed as %v (%v), want it running", resumed.GetSandbox().GetState(), err)
	}

	made := answered(t, s, asking(""))
	<-d.begun
	gaveUp, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.DeleteSandbox(gaveUp, &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()}); err == nil {
		t.Fatal("a Delete that did not wait for the making ended it")
	}
	d.release <- struct{}{}
	waited, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if err := s.Made(waited, made.GetId()); err != nil {
		t.Fatal(err)
	}
	if !d.Holds(made.GetId()) {
		t.Fatal("its driver did not make it")
	}
	if now, err := s.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetId()}); err != nil || now.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_ENDING {
		t.Errorf("its driver holding it, it is %v (%v), want it ending", now.GetSandbox().GetState(), err)
	}
	for call, changed := range map[string]func() error{
		"paused": func() error {
			_, err := s.PauseSandbox(ctx, &sandboxv1.PauseSandboxRequest{SandboxId: made.GetId()})
			return err
		},
		"resumed": func() error {
			_, err := s.ResumeSandbox(ctx, &sandboxv1.ResumeSandboxRequest{SandboxId: made.GetId()})
			return err
		},
	} {
		err := changed()
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeFailedPrecondition || reason != "SANDBOX_ENDING" {
			t.Errorf("ending, it is %s: %v, want it refused SANDBOX_ENDING", call, err)
		}
	}
	if _, err := s.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()}); err != nil {
		t.Fatalf("sent again, the Delete: %v", err)
	}
	notDescribed(t, s, made.GetId())
	if d.Holds(made.GetId()) {
		t.Error("deleted, its driver holds it still")
	}
}

// contended is a memory driver whose first making waits for the test
// and then fails, whatever it was told meanwhile, and whose name, which
// the service reads under its lock as it answers a request, is said
// only once the test lets it, while naming is set: the test holds the
// service's lock so, and has what waits for it wait in the order the
// test asks.
type contended struct {
	running
	// begun is told when the first making begins; fail is what it fails
	// with, once the test says it.
	begun chan struct{}
	fail  chan error
	first *atomic.Bool
	// naming, where it is set, is told the name is asked, and the name
	// is said once it is closed.
	naming *atomic.Pointer[chan struct{}]
	named  chan struct{}
}

func (d contended) Name() string {
	if held := d.naming.Load(); held != nil {
		d.named <- struct{}{}
		<-*held
	}
	return d.running.Name()
}

func (d contended) Apply(ctx context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if d.first.CompareAndSwap(true, false) {
		d.begun <- struct{}{}
		return driver.Sandbox{}, <-d.fail
	}
	return d.Memory.Apply(ctx, record)
}

// goroutine is the id of the goroutine that calls it.
func goroutine() string {
	buf := make([]byte, 64)
	header, _, _ := strings.Cut(string(buf[:runtime.Stack(buf, false)]), " [")
	return strings.TrimPrefix(header, "goroutine ")
}

// stack is a goroutine as runtime.Stack writes it: its id, what it
// waits on, the functions it is in, innermost first, and what started
// it in which goroutine.
type stack struct {
	id, status  string
	functions   []string
	creator, in string
}

// blockedIn is whether it waits as the status says, its innermost
// function of neither the runtime nor sync being the one given, which
// called sync through the function given as via, and nothing of sync
// where via is empty. The runtime's own frames, which a traceback shows
// or not as GOTRACEBACK says, are passed over.
func (g stack) blockedIn(status, via, function string) bool {
	if !strings.HasPrefix(g.status, status) {
		return false
	}
	previous := ""
	for _, f := range g.functions {
		if strings.HasPrefix(f, "runtime.") {
			continue
		}
		if strings.HasPrefix(f, "sync.") || strings.HasPrefix(f, "internal/sync.") {
			previous = f
			continue
		}
		return f == function && previous == via
	}
	return false
}

// stacks are every goroutine's.
func stacks() []stack {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for n == len(buf) {
		buf = make([]byte, 2*len(buf))
		n = runtime.Stack(buf, true)
	}
	var out []stack
	for _, written := range strings.Split(string(buf[:n]), "\n\n") {
		lines := strings.Split(strings.TrimSpace(written), "\n")
		header, ok := strings.CutPrefix(lines[0], "goroutine ")
		if !ok {
			continue
		}
		var g stack
		g.id, g.status, _ = strings.Cut(strings.TrimSuffix(header, "]:"), " [")
		for i := 1; i < len(lines); i += 2 {
			if created, ok := strings.CutPrefix(lines[i], "created by "); ok {
				g.creator, g.in, _ = strings.Cut(created, " in goroutine ")
				break
			}
			function := lines[i]
			if open := strings.LastIndex(function, "("); open > 0 && strings.HasSuffix(function, ")") {
				function = function[:open]
			}
			g.functions = append(g.functions, function)
		}
		out = append(out, g)
	}
	return out
}

// awaited waits until one goroutine is as what is said of it, and
// returns it.
func awaited(t *testing.T, what string, is func(stack) bool) stack {
	t.Helper()
	waited, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	var found stack
	if err := poll.Until(waited, time.Millisecond, func() (bool, error) {
		for _, g := range stacks() {
			if is(g) {
				found = g
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatalf("no goroutine %s: %v", what, err)
	}
	return found
}

// A sandbox whose making a Delete stopped is not failed though its
// driver's failure came back before the Delete stopped it, and the
// service took its lock to keep that after the Delete had: whether the
// making was stopped is read under the lock, as the Delete says so under
// it. The service's lock is held by a request for another sandbox whose
// driver's name is held, while the Delete waits for it first and the
// making's end after. The control is the same failure with no Delete,
// which has failed, saying why.
func TestASandboxADeleteStoppedAsItsMakingFailedIsNotFailed(t *testing.T) {
	test := goroutine()
	for _, deleted := range []bool{false, true} {
		d := contended{running: running{drivertest.NewMemory()}, begun: make(chan struct{}), fail: make(chan error), first: &atomic.Bool{}, naming: &atomic.Pointer[chan struct{}]{}, named: make(chan struct{})}
		d.first.Store(true)
		s := makingAt(t, d, t.TempDir())
		made := answered(t, s, asking(""))
		<-d.begun
		if !deleted {
			d.fail <- errors.New("the image could not be pulled")
			if now := testsandbox.Settled(t, s, made.GetId()); now.GetState() != sandboxv1.Sandbox_STATE_FAILED || !strings.Contains(now.GetFailure(), "the image could not be pulled") {
				t.Errorf("the control: it is %v, %q, want it failed, saying why", now.GetState(), now.GetFailure())
			}
			continue
		}
		// The lock is held, by another request, until the test lets go.
		held := make(chan struct{})
		var once sync.Once
		let := func() { once.Do(func() { close(held) }) }
		// A test that fails before it lets go lets go as it ends, so
		// that what waits ends and the service closes.
		t.Cleanup(func() {
			select {
			case d.fail <- errors.New("the test ended"):
			default:
			}
			let()
		})
		d.naming.Store(&held)
		other := make(chan struct{})
		go func() {
			defer close(other)
			_, _ = s.CreateSandbox(context.Background(), asking(""))
		}()
		<-d.named
		d.naming.Store(nil)
		// The Delete waits for the lock first, and gives up after it has
		// said the sandbox is ending.
		deleting := make(chan struct{})
		go func() {
			defer close(deleting)
			gaveUp, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = s.DeleteSandbox(gaveUp, &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()})
		}()
		const (
			lock   = "sync.(*Mutex).Lock"
			server = "github.com/frontseat-dev/frontseat/sandbox.(*Server)."
		)
		awaited(t, "of this test's Delete waits for the lock", func(g stack) bool {
			return g.creator == "github.com/frontseat-dev/frontseat/sandbox_test."+t.Name() && g.in == test && g.blockedIn("sync.Mutex.Lock", lock, server+"DeleteSandbox")
		})
		// The making's failure comes back, and its end waits after: the
		// making is the one goroutine whose driver waits for the test.
		making := awaited(t, "makes the sandbox the test fails", func(g stack) bool {
			const apply = "github.com/frontseat-dev/frontseat/sandbox_test.contended.Apply"
			at := slices.Index(g.functions, apply)
			return g.blockedIn("chan receive", "", apply) && at+1 < len(g.functions) && g.functions[at+1] == server+"build"
		})
		d.fail <- errors.New("the image could not be pulled")
		awaited(t, "that made the sandbox waits for the lock", func(g stack) bool {
			return g.id == making.id && g.blockedIn("sync.Mutex.Lock", lock, server+"build")
		})
		let()
		<-other
		<-deleting
		waited, stop := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.Made(waited, made.GetId()); err != nil {
			t.Fatal(err)
		}
		stop()
		now, err := s.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetId()})
		if now.GetSandbox().GetState() == sandboxv1.Sandbox_STATE_FAILED {
			t.Errorf("a Delete stopped its making, and it has failed: %q", now.GetSandbox().GetFailure())
		} else if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("once its making stopped, it is described: %v", err)
		}
	}
}

// A sandbox its driver could not make has failed for good: a server
// made again over the same state finds it failed, saying why, and not
// as whatever its driver holds. The control is one its driver made,
// which the next server finds running.
func TestASandboxThatFailedHasFailedForTheNextServerToo(t *testing.T) {
	d := drivertest.NewMemory()
	state := t.TempDir()
	first := makingAt(t, running{d}, state)
	control := testsandbox.Settled(t, first, answered(t, first, asking("")).GetId())
	let := d.Hold()
	made := answered(t, first, asking(""))
	waiting(t, d, 1)
	let(errors.New("the image could not be pulled"))
	if now := testsandbox.Settled(t, first, made.GetId()); now.GetState() != sandboxv1.Sandbox_STATE_FAILED {
		t.Fatalf("it is %v, want it failed", now.GetState())
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next := makingAt(t, running{d}, state)
	now, err := next.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetId()})
	if err != nil || now.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_FAILED || !strings.Contains(now.GetSandbox().GetFailure(), "the image could not be pulled") {
		t.Errorf("to the next server it is %v, %q (%v), want it failed, saying why", now.GetSandbox().GetState(), now.GetSandbox().GetFailure(), err)
	}
	if again, err := next.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: control.GetId()}); err != nil || again.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Errorf("the control: to the next server the one made is %v (%v), want it running", again.GetSandbox().GetState(), err)
	}
}

// checking is a memory driver that finds a sandbox labelled malformed
// not well formed before it makes anything of it, as a gateway's driver
// does a rule that attaches a credential kept for another host.
type checking struct{ running }

func (checking) Check(_ context.Context, record driver.Sandbox) error {
	if record.Labels["malformed"] != "" {
		return fmt.Errorf("%w: the test's driver takes nothing labelled malformed", driver.ErrInvalid)
	}
	if record.Labels["unserved"] != "" {
		return &driver.Unsupported{Driver: "memory", Capability: "anything labelled unserved"}
	}
	return nil
}

// What its driver finds not well formed before making anything is
// refused to whoever asked, SPEC_INVALID, and what it finds it cannot
// give, CAPABILITY_NOT_SERVED; nothing is made or listed: no sandbox
// that failed stands for either. The control is the same sandbox well
// formed, answered for as starting.
func TestWhatItsDriverFindsNotWellFormedIsRefusedWhenAskedFor(t *testing.T) {
	d := drivertest.NewMemory()
	s := makingAt(t, checking{running{d}}, t.TempDir())
	malformed := asking("")
	malformed.Spec.Labels = map[string]string{"malformed": "yes"}
	_, err := s.CreateSandbox(context.Background(), malformed)
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "SPEC_INVALID" {
		t.Errorf("a sandbox its driver finds not well formed: %v, want it refused SPEC_INVALID", err)
	}
	unserved := asking("")
	unserved.Spec.Labels = map[string]string{"unserved": "yes"}
	_, err = s.CreateSandbox(context.Background(), unserved)
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeUnimplemented || reason != "CAPABILITY_NOT_SERVED" {
		t.Errorf("a sandbox its driver cannot give: %v, want it refused CAPABILITY_NOT_SERVED", err)
	}
	if listed, err := s.ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{}); err != nil || len(listed.GetSandboxes()) != 0 {
		t.Errorf("one refused is listed: %v (%v)", listed.GetSandboxes(), err)
	}
	if made := answered(t, s, asking("")); made.GetState() != sandboxv1.Sandbox_STATE_STARTING {
		t.Errorf("the control: a well formed one is answered %v, want it starting", made.GetState())
	}
}
