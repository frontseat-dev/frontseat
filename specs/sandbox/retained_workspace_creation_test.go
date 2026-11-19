package sandbox_test

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/sdk/go"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testsandbox"
	. "github.com/onsi/gomega"
	"testing"
)

// cleans a real workspace after its creation reply is lost without ending its neighbour.
func TestRetainedWorkspaceCleansALostCreationReplyAndKeepsItsNeighbour(t *testing.T) {
	spec := newScenario(t)
	at := spec.retainedPlane()
	control := spec.retainedTool(at)
	behind, err := url.Parse(at)
	spec.Expect(err).NotTo(HaveOccurred())
	proxy := httputil.NewSingleHostReverseProxy(behind)
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	var lost atomic.Bool
	proxy.ModifyResponse = func(response *http.Response) error {
		if strings.HasSuffix(response.Request.URL.Path, "/CreateWorkspace") && response.StatusCode == http.StatusOK && lost.CompareAndSwap(false, true) {
			return errors.New("the test deliberately lost a successful creation response")
		}
		return nil
	}
	front := httptest.NewServer(proxy)
	spec.Cleanup(front.Close)
	// What is asked in front of the plane is asked of the plane, as its
	// owner asks it.
	before := planeOf(at)
	before.URL = front.URL
	known(before)
	name := aName()
	var sandboxID string
	// Registered before the shared helper's cleanup: this observes
	// what that cleanup actually left, then retries deletion if needed
	// so a failing probe still cleans its own resources.
	spec.Cleanup(func() {
		defer endedAt(spec.TB, at, name)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := workspacesAt(at).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: name})
		spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound), "the lost-reply workspace remains after its registered cleanup")
		spec.Expect(sandboxID).NotTo(BeEmpty(), "probe must first observe a real ready sandbox")
		_, err = testsandbox.Client(beneathOf(at)).DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: sandboxID})
		spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound), "the real sandbox remains after its registered cleanup")
		kept, err := workspacesAt(at).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: control.Workspace})
		spec.Expect(err).NotTo(HaveOccurred(), "cleanup must not end a different workspace on the same control plane")
		spec.Expect(kept.GetWorkspace().GetName()).To(Equal(control.Workspace))
		spec.Expect(livingIn(at, control.Workspace)).To(Equal(control.Sandbox))
		_, err = testsandbox.Client(beneathOf(at)).DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: control.Sandbox})
		spec.Expect(err).NotTo(HaveOccurred())
	})
	var failures []string
	probe := *spec
	probe.WithT = NewGomega(func(message string, _ ...int) { failures = append(failures, message) }).(*WithT)
	probe.inASandboxOfItsOwn(making{Server: front.URL, Spec: frontseat.Spec{
		Name: name, Entities: entitiesForRetainedTool(),
	}})
	spec.Expect(lost.Load()).To(BeTrue(), "the control plane must accept creation before the reply is lost")
	spec.Expect(failures).To(HaveLen(1), "the creation helper must observe the lost reply as a failure")
	last := spec.ready(context.Background(), workspacesAt(at), name)
	sandboxID = livingIn(at, last.GetName())
	spec.Expect(sandboxID).NotTo(BeEmpty())
	spec.Expect(sandboxID).NotTo(Equal(control.Sandbox))
}
