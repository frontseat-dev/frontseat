package compare_test

import (
	"context"
	"flag"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/ai-openshell/sdk-go/openshell/v1"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testsandbox"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgateway"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/testsupport/testworkload"
)

// cleanup asks for the tests of the comparison's own cleanup, which make
// sandboxes at the real gateway. `mise run test` asks for them; `mise run
// check` needs no containers and does not.
var cleanup = flag.Bool("cleanup", false, "prove that the frontseat comparison fixture releases its sandbox, at the real gateway")

// cleanupAsked skips a cleanup test that was not asked for, and fails one
// that was where no grid answers: passing by not running is no proof.
func cleanupAsked(t *testing.T) {
	t.Helper()
	if !*cleanup {
		t.Skip("give -cleanup to prove the comparison's cleanup at the real gateway")
	}
	_ = testgrid.Address(testrequired.TB(t))
}

// The final warm comparison workspace is removed when its test ends, just as
// a workspace explicitly closed between rounds is. Observe the real gateway
// after the child test's cleanup, independently of its closed control plane.
func TestFrontseatComparisonReleasesItsSandboxWhenTheTestEnds(t *testing.T) {
	cleanupAsked(t)
	d := comparisonGateway(t)
	for _, explicit := range []bool{true, false} {
		name := "when the test ends"
		if explicit {
			name = "explicit close twice"
		}
		t.Run(name, func(t *testing.T) {
			var owned string
			// A failing control must not leave its known fixture running.
			t.Cleanup(func() {
				if owned != "" {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					if err := deleteComparisonSandbox(ctx, d, owned); err != nil {
						t.Error(err)
					}
				}
			})
			t.Run("the benchmark fixture", func(t *testing.T) {
				tl := frontseatInASandbox(t, testworkload.Shape{Modules: 1, Group: 1, Salt: t.Name() + time.Now().Format(time.RFC3339Nano)})
				owned = tl.sandbox
				if owned == "" {
					t.Fatal("the ready workspace did not name its sandbox")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if live, err := comparisonSandboxes(ctx, d, owned); err != nil || len(live) != 1 {
					t.Fatalf("the live fixture must exist before cleanup: %d at the gateway, %v", len(live), err)
				}
				if explicit {
					tl.close()
					tl.close()
					// Seen here, before the test's own cleanup could do it,
					// and in a time of its own: closing may take the first.
					after, done := context.WithTimeout(context.Background(), time.Minute)
					defer done()
					if live, err := comparisonSandboxes(after, d, owned); err != nil || len(live) != 0 {
						t.Fatalf("an explicit close left its sandbox at the gateway: %d there, %v", len(live), err)
					}
				}
			})
			if owned == "" {
				t.Fatal("the fixture did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if live, err := comparisonSandboxes(ctx, d, owned); err != nil || len(live) != 0 {
				t.Fatalf("benchmark cleanup left its sandbox at the gateway: %d there, %v", len(live), err)
			}
		})
	}
}

// The observer uses the gateway's public SDK, independently of the private
// control plane being tested. No server driver or feature test support is imported.
func comparisonGateway(t testing.TB) *sdk.Client {
	t.Helper()
	identity := testgateway.Required(t)
	authority, certificate, key := testgateway.Files(identity)
	client, err := sdk.NewClient(sdk.Config{Address: testgateway.Address, TLS: &sdk.TLSConfig{
		CAFile: authority, CertFile: certificate, KeyFile: key,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
}

const comparisonIdentity = "frontseat.dev/sandbox"

func comparisonSandboxes(ctx context.Context, client *sdk.Client, id string) ([]*sdk.Sandbox, error) {
	if id == "" {
		return nil, fmt.Errorf("the fixture did not record an owned sandbox")
	}
	found, err := client.Sandboxes().ListAll(ctx, "default", sdk.ListOptions{LabelSelector: comparisonIdentity + "=" + id})
	if err != nil {
		return nil, err
	}
	if len(found) > 1 {
		return nil, fmt.Errorf("more than one sandbox carries the fixture's exact identity")
	}
	for _, sandbox := range found {
		if sandbox.Labels[comparisonIdentity] != id {
			return nil, fmt.Errorf("the gateway returned a different sandbox")
		}
	}
	return found, nil
}

// beneath is where the sandbox service beneath the comparison's own
// control plane is asked, once it serves: for the process alone.
var beneath atomic.Value

// workspaceSandbox is the sandbox a workspace of the comparison's lives
// in, as the sandbox service beneath its control plane says, which is
// the id the gateway holds it under: the one the control plane made for
// it, which its status does not say. Empty while there is none.
func workspaceSandbox(ctx context.Context, workspace string) (string, error) {
	at, _ := beneath.Load().(string)
	if at == "" {
		return "", fmt.Errorf("the comparison's control plane does not serve yet")
	}
	listed, err := testsandbox.Client(at).ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{PageSize: 1000, Labels: map[string]string{"frontseat.dev/workspace": workspace}})
	if err != nil || len(listed.GetSandboxes()) == 0 {
		return "", err
	}
	if len(listed.GetSandboxes()) > 1 {
		return "", fmt.Errorf("more than one sandbox carries the workspace %s's name", workspace)
	}
	return listed.GetSandboxes()[0].GetId(), nil
}

// Failed negative controls still delete only their recorded fixture. The SDK
// deletion is asynchronous, so wait for that same gateway record to disappear.
func deleteComparisonSandbox(ctx context.Context, client *sdk.Client, id string) error {
	found, err := comparisonSandboxes(ctx, client, id)
	if err != nil || len(found) == 0 {
		return err
	}
	name := found[0].Name
	if _, err := client.Sandboxes().Delete(ctx, "default", name); err != nil && !sdk.IsNotFound(err) {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := client.Sandboxes().Get(ctx, "default", name); sdk.IsNotFound(err) {
			return nil
		} else if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
