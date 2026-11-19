package compare_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	sdk "github.com/ai-openshell/sdk-go/openshell/v1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/testsupport/testworkload"
	"github.com/google/renameio/v2"
)

const lostCreationRecordEnv = "FRONTSEAT_COMPARE_CLEANUP_RECORD"
const lostCreationModeEnv = "FRONTSEAT_COMPARE_CLEANUP_MODE"
const lostCreationReply = "the test deliberately lost an accepted creation reply"

type lostCreationTransport func(*http.Request) (*http.Response, error)

func (f lostCreationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type lostCreationRecord struct {
	Workspace, Sandbox string
	State              string
	Ready              bool
	// Live is how many sandboxes the gateway itself held under the
	// workspace's exact identity while the workspace was ready.
	Live int
	// Refused says the delayed request met a closed endpoint, and Reason
	// what else became of it; Remaining counts the plane's workspaces after.
	Refused   bool
	Reason    string
	Remaining int
}

// The expected Fatal belongs to a separate test process. That process runs
// the actual comparison helper, including its registered native cleanup. The
// parent observes the real gateway after the process and cleanup have ended.
func TestFrontseatComparisonReleasesASandboxWhoseCreationReplyWasLost(t *testing.T) {
	lostCreation(t, "accepted")
}

// A failed original caller does not prove that its request cannot still arrive.
// The original request is delivered only after cleanup has obtained its list,
// and must meet a closed endpoint. That the same request makes a sandbox
// where the endpoint is open is what the test above shows.
func TestFrontseatComparisonRefusesACreationDeliveredAfterCleanupBegan(t *testing.T) {
	lostCreation(t, "delayed")
}

func lostCreation(t *testing.T, mode string) {
	t.Helper()
	cleanupAsked(t)
	d := comparisonGateway(t)
	path := filepath.Join(t.TempDir(), "owned.json")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestFrontseatComparisonLostCreationChild$", "-test.v", "-test.timeout=4m")
	child.Env = append(os.Environ(), lostCreationRecordEnv+"="+path, lostCreationModeEnv+"="+mode)
	// A failing test, or a child that was killed, must still remove the
	// sandbox the child recorded as its own, and only that one,
	// independently of the child's private control plane. The record is
	// read when the test ends, whatever became of the child.
	t.Cleanup(func() {
		var recorded lostCreationRecord
		raw, err := os.ReadFile(path)
		if err != nil {
			return // the child recorded no sandbox
		}
		if err := json.Unmarshal(raw, &recorded); err != nil {
			t.Errorf("the child's record cannot be read, and what it made may remain: %s: %v", raw, err)
			return
		}
		if recorded.Sandbox == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := deleteComparisonSandbox(ctx, d, recorded.Sandbox); err != nil {
			t.Error(err)
		}
	})
	output, runErr := child.CombinedOutput()
	// The child fails by design, and what it prints says so: it is kept
	// here and said only where this test fails, so that a gate's log holds
	// no failure that did not happen.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the child, whose creation was lost:\n%s", output)
		}
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the child recorded nothing of its creation: %v; child=%v", err, runErr)
	}
	var owned lostCreationRecord
	if err := json.Unmarshal(raw, &owned); err != nil {
		t.Fatalf("invalid owned fixture record %s: %v", raw, err)
	}
	t.Logf("observed original request: %+v", owned)
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), lostCreationReply) {
		t.Fatalf("child did not fail for the injected lost reply: %v", runErr)
	}
	if mode == "delayed" {
		// Anything but a closed endpoint is a failure: a creation that was
		// admitted is the defect, and one that failed otherwise proves nothing.
		if !owned.Refused || owned.Remaining != 0 || owned.Sandbox != "" {
			t.Fatalf("the delayed creation did not meet a closed endpoint and an empty plane: %+v", owned)
		}
		return
	}
	if !owned.Ready || owned.Live != 1 || owned.Workspace == "" || owned.Sandbox == "" {
		t.Fatalf("probe did not establish its real live-resource control: %+v", owned)
	}
	observing, stop := context.WithTimeout(t.Context(), time.Minute)
	defer stop()
	if live, err := comparisonSandboxes(observing, d, owned.Sandbox); err != nil || len(live) != 0 {
		t.Fatalf("comparison setup failure left owned sandbox %s alive: %d at the gateway, %v", owned.Sandbox, len(live), err)
	}
}

func TestFrontseatComparisonLostCreationChild(t *testing.T) {
	path := os.Getenv(lostCreationRecordEnv)
	if path == "" {
		t.Skip("subprocess-only setup-failure probe")
	}
	base := http.DefaultTransport
	var injected atomic.Bool
	var delayed atomic.Pointer[http.Request]
	deliveryCtx, stopDelivery := context.WithTimeout(context.Background(), 3*time.Minute+30*time.Second)
	t.Cleanup(stopDelivery)
	writeRecord := func(record lostCreationRecord) error {
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		// Whole or not there: the parent reads it whatever becomes of this process.
		return renameio.WriteFile(path, raw, 0o600)
	}
	// A client of the plane a request was sent to, presenting the token
	// that request presented: the plane's owner's.
	clientAt := func(r *http.Request) workspacev1alpha1connect.WorkspaceServiceClient {
		presented := r.Header.Get("Authorization")
		as := lostCreationTransport(func(asked *http.Request) (*http.Response, error) {
			asked = asked.Clone(asked.Context())
			asked.Header.Set("Authorization", presented)
			return base.RoundTrip(asked)
		})
		return workspacev1alpha1connect.NewWorkspaceServiceClient(connect.NewClient(connecthttp.NewTransport(&http.Client{Transport: as}, r.URL.Scheme+"://"+r.URL.Host)))
	}
	mode := os.Getenv(lostCreationModeEnv)
	if mode != "accepted" && mode != "delayed" {
		t.Fatalf("unknown mode %q", mode)
	}
	var gateway *sdk.Client
	if mode == "accepted" {
		gateway = comparisonGateway(t)
	}
	http.DefaultTransport = lostCreationTransport(func(r *http.Request) (*http.Response, error) {
		if mode == "delayed" && strings.HasSuffix(r.URL.Path, "/CreateWorkspace") && injected.CompareAndSwap(false, true) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			copy := r.Clone(deliveryCtx)
			copy.Body = io.NopCloser(bytes.NewReader(body))
			delayed.Store(copy)
			return nil, errors.New(lostCreationReply)
		}
		response, err := base.RoundTrip(r)
		if err != nil {
			return response, err
		}
		var original *http.Request
		if mode == "delayed" && strings.HasSuffix(r.URL.Path, "/ListWorkspaces") && response.StatusCode == http.StatusOK {
			original = delayed.Swap(nil)
			if original == nil {
				return response, nil
			}
			late, lateErr := base.RoundTrip(original)
			if late != nil {
				defer late.Body.Close()
			}
			if lateErr != nil || late.StatusCode != http.StatusOK {
				// A closed original endpoint is the desired refusal, and
				// nothing else is taken for it. The separate cleanup
				// endpoint must still serve the owned plane.
				record := lostCreationRecord{Refused: errors.Is(lateErr, syscall.ECONNREFUSED)}
				if lateErr != nil {
					record.Reason = lateErr.Error()
				} else {
					record.Reason = late.Status
				}
				remaining, err := clientAt(r).ListWorkspaces(deliveryCtx, &workspacev1.ListWorkspacesRequest{PageSize: 1000})
				if err != nil {
					return nil, err
				}
				record.Remaining = len(remaining.GetWorkspaces())
				if err := writeRecord(record); err != nil {
					return nil, err
				}
				return response, nil
			}
			// The delayed creation was admitted, which is the defect this
			// test is for: what follows records the sandbox it made, so
			// the parent fails by it and removes it.
		} else {
			if mode == "delayed" || !strings.HasSuffix(r.URL.Path, "/CreateWorkspace") || response.StatusCode != http.StatusOK || !injected.CompareAndSwap(false, true) {
				return response, nil
			}
			defer response.Body.Close()
			original = r
		}
		// Wait for the accepted creation using an independent client, and
		// see its sandbox at the gateway itself. This supplies the
		// live-resource control before losing the original reply.
		client := clientAt(original)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		listed, err := client.ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{PageSize: 1000})
		if err != nil || len(listed.GetWorkspaces()) != 1 {
			return nil, fmt.Errorf("live-workspace control: %v, %v", listed, err)
		}
		name := listed.GetWorkspaces()[0].GetName()
		// The workspace is described until it has settled, or, delayed,
		// until its sandbox is known: waiting would end only once it had
		// settled, and the sandbox is wanted as soon as it is there.
		var last *workspacev1.Workspace
		var sandbox string
		for {
			described, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: name})
			if err != nil {
				return nil, err
			}
			last = described.GetWorkspace()
			state := last.GetStatus().GetState()
			id, err := workspaceSandbox(ctx, name)
			if err != nil {
				return nil, err
			}
			if id != "" {
				sandbox = id
				// Preserve the exact owned ID as soon as it is known, so
				// even a later fixture failure can use the parent's fallback.
				if err := writeRecord(lostCreationRecord{Workspace: name, Sandbox: id, State: state.String(), Ready: state == workspacev1.WorkspaceStatus_STATE_READY}); err != nil {
					return nil, err
				}
				if mode == "delayed" {
					break // Sandbox existence, not a clone from the closed forge, is the control.
				}
			}
			if state == workspacev1.WorkspaceStatus_STATE_READY || state == workspacev1.WorkspaceStatus_STATE_FAILED || state == workspacev1.WorkspaceStatus_STATE_PAUSED {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if (mode == "accepted" && last.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY) || sandbox == "" {
			return nil, fmt.Errorf("live-workspace control did not become ready: %v", last)
		}
		if mode == "delayed" {
			return response, nil // cleanup goes on with the empty list it was given.
		}
		live, err := comparisonSandboxes(ctx, gateway, sandbox)
		if err != nil {
			return nil, err
		}
		if err := writeRecord(lostCreationRecord{Workspace: name, Sandbox: sandbox, State: last.GetStatus().GetState().String(), Ready: last.GetStatus().GetState() == workspacev1.WorkspaceStatus_STATE_READY, Live: len(live)}); err != nil {
			return nil, err
		}
		return nil, errors.New(lostCreationReply)
	})
	t.Cleanup(func() { http.DefaultTransport = base })
	_ = frontseatInASandbox(t, testworkload.Shape{Modules: 1, Group: 1, Salt: t.Name() + time.Now().Format(time.RFC3339Nano)})
	t.Fatal("comparison helper unexpectedly accepted its lost creation reply")
}
