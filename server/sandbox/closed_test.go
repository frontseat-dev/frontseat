package sandbox_test

import (
	"context"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"

	connect "connectrpc.com/connect/v2"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
)

// closed is a driver whose sandboxes reach nothing that is not named,
// as OpenShell's do: it keeps what it was asked for.
type closed struct {
	mu    sync.Mutex
	asked map[string]driver.Sandbox
	// kept are the credentials it was given, by id: the host, the
	// username and the secret; inUse the ids it says a sandbox is still
	// let through with.
	kept  map[string][3]string
	inUse map[string]bool
}

func (c *closed) Credential(_ context.Context, id string, host driver.Endpoint, username, secret string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kept == nil {
		c.kept = map[string][3]string{}
	}
	c.kept[id] = [3]string{host.String(), username, secret}
	return nil
}

func (c *closed) Kept(_ context.Context, id string) (driver.Endpoint, string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept, ok := c.kept[id]
	if !ok {
		return driver.Endpoint{}, "", "", driver.ErrNotFound
	}
	host, err := driver.ParseEndpoint(kept[0])
	return host, "someone", "A_STAND_IN", err
}

func (c *closed) Forget(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inUse[id] {
		return driver.ErrInUse
	}
	delete(c.kept, id)
	return nil
}

func (*closed) Name() string { return "closed" }
func (*closed) Capabilities() driver.Capabilities {
	return driver.Capabilities{EgressBlock: true, EgressAllowList: true, EgressRules: true, Credentials: true}
}
func (c *closed) Apply(_ context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if err := driver.Admit(c, record); err != nil {
		return driver.Sandbox{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record.State = driver.Running
	c.asked[record.ID] = record
	return record, nil
}
func (c *closed) Observe(_ context.Context, id string) (driver.Sandbox, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if held, ok := c.asked[id]; ok {
		return held, nil
	}
	return driver.Sandbox{}, driver.ErrNotFound
}
func (c *closed) Delete(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.asked, id)
	return nil
}
func (*closed) Dial(context.Context, string, int) (net.Conn, error) { return nil, driver.ErrNotFound }

// overClosed serves the sandbox API over a driver that lets nothing be
// reached unnamed, and returns the driver and a client of it.
func overClosed(t *testing.T) (*closed, sandboxv1alpha1connect.SandboxServiceClient) {
	t.Helper()
	d := &closed{asked: map[string]driver.Sandbox{}, inUse: map[string]bool{}}
	client, _ := serveOver(t, d)
	return d, client
}

// madeReaching asks for a sandbox of the image box that may reach what
// egress says, and returns it as it was made.
func madeReaching(t *testing.T, client sandboxv1alpha1connect.SandboxServiceClient, egress *sandboxv1.Egress) *sandboxv1.Sandbox {
	t.Helper()
	made, err := client.CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{Image: "box", Egress: egress}})
	if err != nil {
		t.Fatal(err)
	}
	return made.GetSandbox()
}

// Under a driver that lets nothing be reached unnamed, a sandbox that
// says nothing of what it may reach reaches what its rules name and no
// other, and one that asks to reach anything is refused by name and
// not made.
func TestUnderADriverThatLetsNothingBeReachedUnnamedASandboxReachesWhatItLists(t *testing.T) {
	d, client := overClosed(t)
	ctx := context.Background()

	made := madeReaching(t, client, &sandboxv1.Egress{Rules: []*sandboxv1.EgressRule{{Host: "forge.example:443"}}})
	asked := d.asked[made.GetId()]
	if !asked.Egress.Listed || asked.Egress.Block || len(asked.Egress.Rules) != 1 || asked.Egress.Rules[0].Host.String() != "forge.example:443" {
		t.Errorf("the driver was asked for %+v, want only the host the spec lists", asked.Egress)
	}

	_, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{
		Image: "box", Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL},
	}})
	if connect.CodeOf(err) != connect.CodeUnimplemented || !strings.Contains(err.Error(), "a sandbox that reaches anything") {
		t.Errorf("asking to reach anything: %v, want it refused by name", err)
	}
	if len(d.asked) != 1 {
		t.Errorf("the driver holds %d sandboxes, want the one that was admitted", len(d.asked))
	}
}

// The control: a driver whose sandboxes may reach anything is asked for
// one that does, lists or no lists.
func TestUnderADriverWhoseSandboxesReachAnythingNothingChanges(t *testing.T) {
	client, _ := serve(t)
	ctx := context.Background()
	for name, egress := range map[string]*sandboxv1.Egress{
		"nothing said":    {Rules: []*sandboxv1.EgressRule{{Host: "forge.example:443"}}},
		"anything, asked": {Mode: sandboxv1.Egress_MODE_ALLOW_ALL},
	} {
		if _, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{Image: "box", Egress: egress}}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A host's credential is the driver's to keep, where it keeps any: the
// server hands it over and says nothing of it back; what a sandbox may
// fetch is asked of the driver; a credential still in use is kept, by
// name. A driver that keeps none refuses the credential and a sandbox
// that fetches, each by name, and makes nothing.
func TestACredentialIsKeptByTheDriverAndAFetchIsAskedOfIt(t *testing.T) {
	d, client := overClosed(t)
	ctx := context.Background()

	said, err := client.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: &sandboxv1.Credential{Name: "adas-forge", Host: "forge.example", Username: "someone", Secret: "a-made-up-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if d.kept["adas-forge"] != [3]string{"forge.example:443", "someone", "a-made-up-secret"} {
		t.Errorf("the driver keeps %v under the id, want what was given, the host with its port", d.kept["adas-forge"])
	}
	if strings.Contains(said.String(), "a-made-up-secret") {
		t.Error("the secret was said back")
	}
	for name, request := range map[string]*sandboxv1.Credential{
		"no host":      {Name: "adas-forge", Secret: "a-made-up-secret"},
		"no secret":    {Name: "adas-forge", Host: "forge.example"},
		"no name":      {Host: "forge.example", Secret: "a-made-up-secret"},
		"a name of /":  {Name: "adas/forge", Host: "forge.example", Secret: "a-made-up-secret"},
		"a long name":  {Name: strings.Repeat("a", 64), Host: "forge.example", Secret: "a-made-up-secret"},
		"a capital":    {Name: "Adas-forge", Host: "forge.example", Secret: "a-made-up-secret"},
		"a dash first": {Name: "-adas", Host: "forge.example", Secret: "a-made-up-secret"},
	} {
		if _, err := client.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: request}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("a credential with %s: %v, want it refused", name, err)
		}
	}

	// A rule attaches the credential, and is asked of the driver as it
	// was given.
	made := madeReaching(t, client, &sandboxv1.Egress{Rules: []*sandboxv1.EgressRule{{Host: "forge.example", Credential: "adas-forge", Requests: []*sandboxv1.EgressRequest{{Method: "GET", Path: "/acme/code/refs"}}}}})
	if got := d.asked[made.GetId()].Egress.Rules; len(got) != 1 || got[0].CredentialID != "adas-forge" {
		t.Errorf("the driver was asked for the rules %+v, want the credential attached", got)
	}
	if _, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{
		Image: "box", Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED, Rules: []*sandboxv1.EgressRule{{Host: "forge.example", Credential: "Adas/forge", Requests: []*sandboxv1.EgressRequest{{Method: "GET", Path: "/acme/code/refs"}}}}},
	}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a rule naming no credential's id: %v, want it refused", err)
	}

	d.inUse["adas-forge"] = true
	if _, err := client.DeleteCredential(ctx, &sandboxv1.DeleteCredentialRequest{Name: "adas-forge"}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "still let through") {
		t.Errorf("letting go of a credential in use: %v, want it kept, by name", err)
	}
	d.inUse["adas-forge"] = false
	if _, err := client.DeleteCredential(ctx, &sandboxv1.DeleteCredentialRequest{Name: "adas-forge"}); err != nil || len(d.kept) != 0 {
		t.Errorf("letting go of a credential: %v, and the driver keeps %d", err, len(d.kept))
	}

	// The control: a driver that keeps no credential outside its sandboxes.
	open, _ := serve(t)
	if _, err := open.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: &sandboxv1.Credential{Name: "adas-forge", Host: "forge.example", Secret: "a-made-up-secret"}}); connect.CodeOf(err) != connect.CodeUnimplemented || !strings.Contains(err.Error(), "a credential kept outside its sandboxes") {
		t.Errorf("a credential given to a driver that keeps none: %v, want it refused by name", err)
	}
	if _, err := open.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{
		Image: "box", Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED, Rules: []*sandboxv1.EgressRule{{Host: "forge.example", Credential: "adas-forge", Requests: []*sandboxv1.EgressRequest{{Method: "GET", Path: "/acme/code/refs"}}}}},
	}}); connect.CodeOf(err) != connect.CodeUnimplemented || !strings.Contains(err.Error(), "a credential kept outside its sandboxes") {
		t.Errorf("a sandbox that fetches, under a driver that keeps no credential: %v, want it refused by name", err)
	}
	if listed, err := open.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{}); err != nil || len(listed.GetSandboxes()) != 0 {
		t.Errorf("the refused sandbox was made: %v, %v", listed.GetSandboxes(), err)
	}
}

// What a sandbox may reach is asked of the driver as it was asked of the
// server, rule by rule: the host, the programs, the requests, and whether
// the host's credential is attached. The server names no repository and
// no program of its own in it. A rule that is no rule is refused, and a
// credential that is kept is described without its secret.
func TestWhatASandboxMayReachIsAskedOfTheDriverRuleByRule(t *testing.T) {
	d, client := overClosed(t)
	ctx := context.Background()

	const forge, grid, catalog = "forge.example", "grid.example:8980", "http://catalog.example"
	fetcher, daemon := []string{"/usr/bin/a-fetcher"}, []string{"/opt/a/daemon"}
	const refs, pack = "/acme/code/info/refs", "/acme/code/a-service"
	service := map[string]string{"service": "a-service"}
	made := madeReaching(t, client, &sandboxv1.Egress{Rules: []*sandboxv1.EgressRule{
		{Host: forge, Programs: fetcher, Credential: "adas-forge", Requests: []*sandboxv1.EgressRequest{{Method: "GET", Path: refs, Query: service}, {Method: "POST", Path: pack}}},
		{Host: grid, Programs: daemon},
		{Host: catalog},
	}})
	// Each host is asked for as the driver port reads it, port and all.
	want := []driver.Rule{
		{Host: driver.Endpoint{Name: "forge.example", Port: 443}, Programs: fetcher, CredentialID: "adas-forge", Requests: []driver.Request{{Method: "GET", Path: refs, Query: service}, {Method: "POST", Path: pack}}},
		{Host: driver.Endpoint{Name: "grid.example", Port: 8980}, Programs: daemon},
		{Host: driver.Endpoint{Name: "catalog.example", Port: 80, Clear: true}},
	}
	if got := d.asked[made.GetId()].Egress.Rules; !reflect.DeepEqual(got, want) {
		t.Errorf("the driver was asked for the rules\n%+v\nwant\n%+v", got, want)
	}
	if spec := made.GetSpec().GetEgress().GetRules(); len(spec) != 3 || spec[0].GetCredential() != "adas-forge" {
		t.Errorf("the sandbox's record says its rules are %v", spec)
	}
	for what, rule := range map[string]*sandboxv1.EgressRule{
		"a rule of no host":         {Programs: []string{"/usr/bin/a-fetcher"}},
		"a request of no method":    {Host: "forge.example", Requests: []*sandboxv1.EgressRequest{{Path: "/a"}}},
		"a request of no path":      {Host: "forge.example", Requests: []*sandboxv1.EgressRequest{{Method: "GET"}}},
		"a program that is no path": {Host: "forge.example", Programs: []string{""}},
		"a host that is an address": {Host: "192.0.2.7"},
		"a host of no port":         {Host: "forge.example:"},
	} {
		_, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{Image: "box", Egress: &sandboxv1.Egress{Rules: []*sandboxv1.EgressRule{rule}}}})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want it refused as not well formed", what, err)
		}
	}

	// A credential that is kept is described: whose it is and where its
	// stand-in is found, and never its secret.
	if _, err := client.DescribeCredential(ctx, &sandboxv1.DescribeCredentialRequest{Name: "beas-forge"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a credential that was never given: %v, want it not found", err)
	}
	if _, err := client.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: &sandboxv1.Credential{Name: "beas-forge", Host: "forge.example", Username: "someone", Secret: "a-made-up-secret"}}); err != nil {
		t.Fatal(err)
	}
	described, err := client.DescribeCredential(ctx, &sandboxv1.DescribeCredentialRequest{Name: "beas-forge"})
	if err != nil || described.GetCredential().GetName() != "beas-forge" || described.GetCredential().GetHost() != "forge.example:443" || described.GetCredential().GetUsername() != "someone" || described.GetCredential().GetVariable() != "A_STAND_IN" {
		t.Errorf("the credential is described as %v (%v)", described, err)
	}
	if strings.Contains(described.String(), "a-made-up-secret") {
		t.Error("its secret was said back")
	}
	if _, err := client.DescribeCredential(ctx, &sandboxv1.DescribeCredentialRequest{}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a credential of no id: %v, want it refused", err)
	}
	// The control: a driver that keeps none says so, by name.
	open, _ := serve(t)
	if _, err := open.DescribeCredential(ctx, &sandboxv1.DescribeCredentialRequest{Name: "beas-forge"}); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("a credential asked after under a driver that keeps none: %v, want it refused by name", err)
	}
}

// A refusal of the sandbox service repeats nothing of what it refused:
// an id, a name, a selector, a rule's credential, a container's name, an
// image and a volume's subpath are each the caller's to write, and any
// may hold a token, as may a volume's name, which a driver that does
// not give its kind refuses. The control is the sandbox each refusal
// changes, asked for as it is, which is made.
func TestARefusalOfASandboxRepeatsNothingItRefused(t *testing.T) {
	const refusedValue = "V4lue_of adas"
	_, client := overClosed(t)
	ctx := context.Background()
	asked := func(change func(*sandboxv1.SandboxSpec)) error {
		spec := &sandboxv1.SandboxSpec{Image: "box", Egress: &sandboxv1.Egress{Rules: []*sandboxv1.EgressRule{{Host: "forge.example:443"}}}}
		change(spec)
		_, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: spec})
		return err
	}
	if err := asked(func(*sandboxv1.SandboxSpec) {}); err != nil {
		t.Fatalf("the control: %v", err)
	}
	refusals := map[string]error{
		"an image that is none": asked(func(s *sandboxv1.SandboxSpec) { s.Image = refusedValue }),
		"a rule's credential that is none": asked(func(s *sandboxv1.SandboxSpec) {
			s.Egress.Rules[0].Credential = refusedValue
		}),
		"a container's name that is none": asked(func(s *sandboxv1.SandboxSpec) {
			s.Containers = []*sandboxv1.Container{{Name: refusedValue, Image: "box"}}
		}),
		"a volume of a kind the driver does not give": asked(func(s *sandboxv1.SandboxSpec) {
			s.Volumes = []*sandboxv1.Volume{{Name: refusedValue, Kind: sandboxv1.Volume_KIND_HOST, Source: "/srv", Path: "/srv"}}
		}),
		"a volume's subpath that climbs": asked(func(s *sandboxv1.SandboxSpec) {
			s.Volumes = []*sandboxv1.Volume{{Name: refusedValue, Kind: sandboxv1.Volume_KIND_HOST, Source: "/srv", Path: "/srv", Subpath: "../" + refusedValue}}
		}),
	}
	_, refusals["an id no sandbox has"] = client.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: refusedValue})
	_, refusals["a selector that is none"] = client.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{LabelSelector: refusedValue})
	_, refusals["a credential's name nothing is kept by"] = client.DescribeCredential(ctx, &sandboxv1.DescribeCredentialRequest{Name: "cred-of-" + strings.ToLower(refusedValue[:5])})
	for what, err := range refusals {
		if err == nil {
			t.Errorf("%s was taken", what)
		} else if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(refusedValue[:5])) {
			t.Errorf("the refusal of %s repeats what it refused: %v", what, err)
		}
	}
}
