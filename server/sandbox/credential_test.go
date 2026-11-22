package sandbox_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

// keeping is a driver that holds its sandboxes in memory and keeps
// credentials, each under an id for one host, as a driver that keeps
// them outside its sandboxes says what it refuses.
type keeping struct {
	*drivertest.Memory
	mu   sync.Mutex
	kept map[string]driver.Endpoint
}

func (k *keeping) Capabilities() driver.Capabilities {
	caps := k.Memory.Capabilities()
	caps.Credentials = true
	return caps
}

func (k *keeping) Credential(_ context.Context, id string, host driver.Endpoint, _, _ string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if there, ok := k.kept[id]; ok && there != host {
		return fmt.Errorf("%w: %s", driver.ErrTaken, id)
	}
	k.kept[id] = host
	return nil
}

func (k *keeping) Forget(_ context.Context, id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.kept, id)
	return nil
}

func (k *keeping) Kept(_ context.Context, id string) (driver.Endpoint, string, string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	host, ok := k.kept[id]
	if !ok {
		return driver.Endpoint{}, "", "", driver.ErrNotFound
	}
	return host, "", "CRED", nil
}

// Each way a credential is not one SetCredential keeps is refused by a
// reason of its own, in the sandbox service's domain: the name, the
// host, the username, the secret, and a name kept for another host. The
// control is the credential kept as given, and given again for its host.
func TestACredentialThatIsNotOneIsRefusedByWhatIsWrong(t *testing.T) {
	client, _ := serveOver(t, &keeping{Memory: drivertest.NewMemory(), kept: map[string]driver.Endpoint{}})
	ctx := context.Background()
	set := func(c *sandboxv1.Credential) error {
		_, err := client.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: c})
		return err
	}
	good := func() *sandboxv1.Credential {
		return &sandboxv1.Credential{Name: "forge", Host: "forge.example", Username: "someone", Secret: "s3cret"}
	}
	if err := set(good()); err != nil {
		t.Fatalf("the control: %v", err)
	}
	if err := set(good()); err != nil {
		t.Fatalf("the control, given again for its host: %v", err)
	}
	for name, c := range map[string]struct {
		change func(*sandboxv1.Credential)
		code   connect.Code
		reason string
	}{
		"no name":             {func(c *sandboxv1.Credential) { c.Name = "" }, connect.CodeInvalidArgument, "CREDENTIAL_NAME_INVALID"},
		"no host":             {func(c *sandboxv1.Credential) { c.Host = "" }, connect.CodeInvalidArgument, "CREDENTIAL_HOST_INVALID"},
		"a host that is none": {func(c *sandboxv1.Credential) { c.Host = "forge.example/acme" }, connect.CodeInvalidArgument, "CREDENTIAL_HOST_INVALID"},
		"a username":          {func(c *sandboxv1.Credential) { c.Username = "some one" }, connect.CodeInvalidArgument, "CREDENTIAL_USERNAME_INVALID"},
		"no secret":           {func(c *sandboxv1.Credential) { c.Secret = "" }, connect.CodeInvalidArgument, "CREDENTIAL_SECRET_INVALID"},
		"a secret of lines":   {func(c *sandboxv1.Credential) { c.Secret = "s3\ncret" }, connect.CodeInvalidArgument, "CREDENTIAL_SECRET_INVALID"},
		"a long secret":       {func(c *sandboxv1.Credential) { c.Secret = strings.Repeat("s", 4097) }, connect.CodeInvalidArgument, "CREDENTIAL_SECRET_INVALID"},
		"another host":        {func(c *sandboxv1.Credential) { c.Host = "elsewhere.example" }, connect.CodeAlreadyExists, "CREDENTIAL_NAME_TAKEN"},
	} {
		asked := good()
		c.change(asked)
		err := set(asked)
		if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != c.code || reason != c.reason || domain != refusal.Sandbox {
			t.Errorf("%s: %v (%s, %s), want %v and %s", name, err, reason, domain, c.code, c.reason)
		}
	}
}

// A request id longer than 128 bytes is refused, as every Create
// refuses one; the control is one of 128 bytes, which makes a sandbox.
func TestARequestIDLongerThanACreateTakesIsRefused(t *testing.T) {
	client, _ := serveOver(t, drivertest.NewMemory())
	ask := func(id string) error {
		_, err := client.CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{RequestId: id, Spec: &sandboxv1.SandboxSpec{Image: "box"}})
		return err
	}
	if err := ask(strings.Repeat("r", 128)); err != nil {
		t.Fatalf("the control: %v", err)
	}
	err := ask(strings.Repeat("r", 129))
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "REQUEST_ID_INVALID" || domain != refusal.Sandbox {
		t.Errorf("%v (%s, %s), want it refused REQUEST_ID_INVALID", err, reason, domain)
	}
}
