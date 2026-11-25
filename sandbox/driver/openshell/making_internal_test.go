package openshell

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	client "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/fake"

	"github.com/frontseat-dev/frontseat/core/plain"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
)

// overHeld is a driver over a gateway the SDK holds in memory, which
// keeps a credential for forge.example under the id ada-forge, as this
// driver keeps one.
func overHeld(t *testing.T, gateway client.ClientInterface) *Driver {
	t.Helper()
	at, ok := plain.OriginOf("https", "forge.example", "443")
	if !ok {
		t.Fatal("forge.example is no origin")
	}
	provider, _ := kept("ada-forge")
	if _, err := gateway.Providers().Create(context.Background(), "default", &client.Provider{Name: provider, Type: provider, Spec: client.ProviderSpec{
		Config: map[string]string{"username": "ada", "sent": at.URL(), "id": "ada-forge"},
	}}); err != nil {
		t.Fatal(err)
	}
	return &Driver{config: Config{Workspace: "default", Identity: "held"}, gateway: gateway}
}

// What the gateway would refuse to be asked for, as not well formed, is
// refused before anything of the sandbox is made, so that the sandbox
// service refuses it to whoever asked: a credential kept under no id a
// rule names, or for another host than its rule's, a host named by two
// rules, a program named by no path, a request that is none. Nothing is
// made of any. The control is a sandbox whose rule attaches the
// credential kept for its host, which is taken.
func TestWhatIsNotWellFormedIsRefusedBeforeAnythingIsMade(t *testing.T) {
	gateway := fake.NewClient()
	d := overHeld(t, gateway)
	forge := drivertest.Endpoint(t, "forge.example")
	get := []driver.Request{{Method: "GET", Path: "/acme/code/refs"}}
	ruled := func(rules ...driver.Rule) driver.Sandbox {
		return driver.Sandbox{ID: "sbx-checked", Image: "box", Egress: driver.Egress{Listed: true, Rules: rules}}
	}
	for name, record := range map[string]driver.Sandbox{
		"a credential kept under no id the rule names": ruled(driver.Rule{Host: forge, Requests: get, CredentialID: "bea-forge"}),
		"a credential kept for another host":           ruled(driver.Rule{Host: drivertest.Endpoint(t, "other.example"), Requests: get, CredentialID: "ada-forge"}),
		"a host named by two rules":                    ruled(driver.Rule{Host: forge}, driver.Rule{Host: drivertest.Endpoint(t, "forge.example:443")}),
		"a program named by no path":                   ruled(driver.Rule{Host: forge, Programs: []string{"git"}}),
		"a request that is none":                       ruled(driver.Rule{Host: forge, Requests: []driver.Request{{Method: "CONNECT", Path: "/a"}}}),
	} {
		if err := d.Check(context.Background(), record); !errors.Is(err, driver.ErrInvalid) {
			t.Errorf("%s: %v, want it refused as not well formed", name, err)
		}
	}
	if err := d.Check(context.Background(), ruled(driver.Rule{Host: forge, Requests: get, CredentialID: "ada-forge"})); err != nil {
		t.Errorf("the control: a rule that attaches the credential kept for its host: %v", err)
	}
	nothingMade(t, gateway)
}

// nothingMade fails the test where checking made a sandbox at the
// gateway.
func nothingMade(t *testing.T, gateway client.ClientInterface) {
	t.Helper()
	if held, err := gateway.Sandboxes().ListAll(context.Background(), "default"); err != nil || len(held) != 0 {
		t.Errorf("checking made %d sandboxes (%v)", len(held), err)
	}
}

// What this gateway cannot give a sandbox is refused by name before
// anything of it is made, as a capability it does not serve, so that the
// sandbox service refuses it CAPABILITY_NOT_SERVED: a directory of its
// image written where its sandboxes write what is bound in alone, a
// directory of its host bound in by a compute driver that binds none, a
// volume of another kind. What names no plain path is not well formed.
// Nothing is made of any. The control is each asked of a gateway that
// gives it, which is taken.
func TestWhatTheGatewayCannotGiveIsRefusedByNameBeforeAnythingIsMade(t *testing.T) {
	gateway := fake.NewClient()
	held := overHeld(t, gateway)
	over := func(compute string, private bool) *Driver {
		d := *held
		d.config.Compute, d.config.Private = compute, private
		return &d
	}
	bound := driver.Volume{Name: "work", Kind: driver.Host, Source: t.TempDir(), Path: "/work"}
	sandbox := func(change func(*driver.Sandbox)) driver.Sandbox {
		record := driver.Sandbox{ID: "sbx-checked", Image: "box", Egress: driver.Egress{Block: true}}
		change(&record)
		return record
	}
	for name, c := range map[string]struct {
		refused, control *Driver
		record           driver.Sandbox
	}{
		"a directory of its image written": {over("podman", true), over("podman", false), sandbox(func(s *driver.Sandbox) { s.Writable = []string{"/var/lib/x"} })},
		"a directory of its host bound in": {over("kubernetes", false), over("podman", false), sandbox(func(s *driver.Sandbox) { s.Volumes = []driver.Volume{bound} })},
		"a volume of another kind": {over("podman", false), over("podman", false), sandbox(func(s *driver.Sandbox) {
			s.Volumes = []driver.Volume{{Name: "scratch", Kind: driver.Ephemeral, Path: "/scratch"}}
		})},
	} {
		var unsupported *driver.Unsupported
		if err := c.refused.Check(context.Background(), c.record); !errors.As(err, &unsupported) {
			t.Errorf("%s: %v, want it refused as a capability not served", name, err)
		}
		if name == "a volume of another kind" {
			continue
		}
		if err := c.control.Check(context.Background(), c.record); err != nil {
			t.Errorf("the control: %s, of a gateway that gives it: %v", name, err)
		}
	}
	climbs := bound
	climbs.Path = "/work/../etc"
	if err := over("podman", false).Check(context.Background(), sandbox(func(s *driver.Sandbox) { s.Volumes = []driver.Volume{climbs} })); !errors.Is(err, driver.ErrInvalid) {
		t.Errorf("a volume that names no plain path: %v, want it refused as not well formed", err)
	}
	nothingMade(t, gateway)
}

// unlooking is a gateway that is not asked after a sandbox a moment
// after it made one: what it is asked after then fails, as a gateway's
// answer that is lost does.
type unlooking struct {
	client.ClientInterface
	sandboxes *unlookingSandboxes
}

func (u unlooking) Sandboxes() client.SandboxInterface { return u.sandboxes }

type unlookingSandboxes struct {
	client.SandboxInterface
	// made is whether it made a sandbox; refuses, whether it makes none.
	made     atomic.Bool
	refuses  bool
	unlooked error
}

func (s *unlookingSandboxes) Create(ctx context.Context, workspace, name string, spec *client.SandboxSpec, opts ...client.CreateOption) (*client.Sandbox, error) {
	if s.refuses {
		return nil, s.unlooked
	}
	made, err := s.SandboxInterface.Create(ctx, workspace, name, spec, opts...)
	s.made.Store(err == nil)
	return made, err
}

func (s *unlookingSandboxes) Get(ctx context.Context, workspace, name string) (*client.Sandbox, error) {
	if s.made.Load() {
		return nil, s.unlooked
	}
	return s.SandboxInterface.Get(ctx, workspace, name)
}

// A sandbox the gateway made is made, though the gateway could not be
// asked after it a moment later: the driver says it is starting, and
// fails nothing of its making. The control is a gateway that makes
// none, whose failure is the making's.
func TestASandboxTheGatewayMadeIsMadeThoughItCouldNotBeAskedAfter(t *testing.T) {
	lost := &client.StatusError{Code: client.ErrorUnavailable, Message: "the gateway's answer was lost"}
	for _, refuses := range []bool{false, true} {
		held := fake.NewClient()
		sandboxes := &unlookingSandboxes{SandboxInterface: held.Sandboxes(), refuses: refuses, unlooked: lost}
		d := overHeld(t, unlooking{ClientInterface: held, sandboxes: sandboxes})
		now, err := d.Apply(context.Background(), driver.Sandbox{ID: "sbx-made", Image: "box", Egress: driver.Egress{Block: true}})
		if refuses {
			if err == nil {
				t.Error("the control: a sandbox the gateway did not make was made")
			}
			continue
		}
		if err != nil || now.State != driver.Starting {
			t.Errorf("a sandbox the gateway made is %v (%v), want it starting", now.State, err)
		}
		if made, err := held.Sandboxes().ListAll(context.Background(), "default"); err != nil || len(made) != 1 {
			t.Errorf("the gateway holds %d sandboxes (%v), want the one made", len(made), err)
		}
	}
}

// unseen is a gateway that answers its first look after a sandbox that
// there is none, as one does whose making another server asked for and
// that is still under way when this one looks.
type unseen struct {
	client.ClientInterface
	sandboxes *unseenSandboxes
}

func (u unseen) Sandboxes() client.SandboxInterface { return u.sandboxes }

type unseenSandboxes struct {
	client.SandboxInterface
	looked atomic.Bool
}

func (s *unseenSandboxes) Get(ctx context.Context, workspace, name string) (*client.Sandbox, error) {
	if s.looked.CompareAndSwap(false, true) {
		return nil, &client.StatusError{Code: client.ErrorNotFound, Message: "not yet"}
	}
	return s.SandboxInterface.Get(ctx, workspace, name)
}

// A sandbox a server before this one asked the gateway for, under the
// same id, and that the gateway made while this one found none, is the
// one made: asked to make it again, the gateway says it holds one of
// that name, which carries this id, and it is taken. The control is one
// of the same name that carries another id, which is not this one and
// is refused, and left as it is.
func TestASandboxTheGatewayMadeForAnEarlierServerIsMadeAgainAsItIs(t *testing.T) {
	for _, of := range []string{"sbx-again", "sbx-another"} {
		held := fake.NewClient()
		d := overHeld(t, unseen{ClientInterface: held, sandboxes: &unseenSandboxes{SandboxInterface: held.Sandboxes()}})
		if _, err := held.Sandboxes().Create(context.Background(), "default", named("sbx-again"), &client.SandboxSpec{Template: &client.SandboxTemplate{Image: "box"}}, client.WithLabels(map[string]string{identity: of})); err != nil {
			t.Fatal(err)
		}
		now, err := d.Apply(context.Background(), driver.Sandbox{ID: "sbx-again", Image: "box", Egress: driver.Egress{Block: true}})
		if of != "sbx-again" {
			if err == nil || !strings.Contains(err.Error(), "that is not sbx-again") {
				t.Errorf("the control: one of the same name and another id is %v (%v), want it refused", now.State, err)
			}
		} else if err != nil {
			t.Errorf("one the gateway made for an earlier server: %v, want it taken", err)
		}
		if made, err := held.Sandboxes().ListAll(context.Background(), "default"); err != nil || len(made) != 1 || made[0].Labels[identity] != of {
			t.Errorf("the gateway holds %v (%v), want the one made before alone", made, err)
		}
	}
}
