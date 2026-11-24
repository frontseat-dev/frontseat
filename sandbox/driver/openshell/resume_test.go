package openshell_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgateway"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// Capture a failed fixture before conformance cleanup deletes its diagnostics.
// This adds no retry and changes no conformance assertion or production behavior.
type failureCapture struct {
	driver.Driver
	t       *testing.T
	gateway *sdk.Client
	// captured are the sandboxes whose failure was captured: once each.
	captured map[string]bool
}

// A running sandbox is not proof that the box's supervisor has survived
// startup. Try both an immediate pause and a pause after it answers.
func TestAResumedOpenShellBoxAnswersThroughItsRestartedSupervisor(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(fmt.Sprintf("pause_after_answer=%t", ready), func(t *testing.T) {
			native := driven(t)
			d := captureFailures(t, native)
			record := box(t, fmt.Sprintf("frontseat-resume-%x", time.Now().UnixNano()))
			ctx, stop := context.WithTimeout(t.Context(), 2*time.Minute)
			defer stop()
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
				defer stop()
				if err := d.Delete(cleanup, record.ID); err != nil {
					t.Errorf("cleanup: %v", err)
				}
			})
			if _, err := d.Apply(ctx, record); err != nil {
				t.Fatal(err)
			}
			drivertest.Await(t, d, record.ID, driver.Running)
			if ready {
				answerAfterResume(t, ctx, d, record.ID)
				code, output, _, err := native.Exec(ctx, record.ID, "", []string{"touch", "/work/kept-before-pause"}, 1024)
				if err != nil || code != 0 {
					t.Fatalf("write before pause: %d %v %s", code, err, output)
				}
			}
			record.Paused = true
			if _, err := d.Apply(ctx, record); err != nil {
				t.Fatal(err)
			}
			drivertest.Await(t, d, record.ID, driver.Paused)
			record.Paused = false
			if _, err := d.Apply(ctx, record); err != nil {
				t.Fatal(err)
			}
			drivertest.Await(t, d, record.ID, driver.Running)
			answerAfterResume(t, ctx, d, record.ID)
			if ready {
				code, output, _, err := native.Exec(ctx, record.ID, "", []string{"test", "-f", "/work/kept-before-pause"}, 1024)
				if err != nil || code != 0 {
					t.Fatalf("file after resume: %d %v %s", code, err, output)
				}
			}
		})
	}
}

func answerAfterResume(t *testing.T, ctx context.Context, d driver.Driver, id string) {
	testrequired.Stack(t)
	t.Helper()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return d.Dial(ctx, id, answeringPort)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	deadline, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	for {
		request, err := http.NewRequestWithContext(deadline, http.MethodGet, "http://sandbox/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if err == nil {
				return
			}
		}
		if got, observed := d.Observe(deadline, id); observed == nil && got.State == driver.Failed {
			t.Fatalf("the box failed after running: %+v", got)
		}
		select {
		case <-deadline.Done():
			t.Fatalf("the box's supervisor did not answer: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func captureFailures(t *testing.T, d driver.Driver) driver.Driver {
	t.Helper()
	return &failureCapture{Driver: d, t: t, gateway: gatewayClient(t), captured: map[string]bool{}}
}

func (d *failureCapture) Observe(ctx context.Context, id string) (driver.Sandbox, error) {
	got, err := d.Driver.Observe(ctx, id)
	if got.State == driver.Failed && !d.captured[id] {
		d.captured[id] = true
		capture, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer stop()
		// The sandbox as the gateway holds it, found as the driver finds
		// it: what it says of it, and what it kept of what it printed.
		held, e := heldAs(capture, d.gateway, id)
		if e != nil || len(held) == 0 {
			d.t.Logf("failure status capture: the gateway holds %d sandboxes of %s (%v)", len(held), id, e)
		}
		for _, sandbox := range held {
			status, _ := json.Marshal(sandbox.Status)
			d.t.Logf("failed sandbox %s status: %s", id, status)
			if logs, e := d.gateway.Sandboxes().GetLogs(capture, "default", sandbox.Name, sdk.WithLogLines(100)); e == nil {
				data, _ := json.Marshal(logs)
				d.t.Logf("failed sandbox %s logs: %s", id, redacted(data))
			} else {
				d.t.Logf("failure log capture: %v", e)
			}
		}
		// Its containers, as the runtime holds them: which ended, how,
		// and when. The gateway says only that one did.
		if names, e := containersOf(capture, d.gateway, id); e != nil {
			d.t.Logf("failure container capture: %v", e)
		} else {
			for _, name := range names {
				said, e := testgateway.Runtime(capture, "inspect", "--format", "{{.Name}} {{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} started={{.State.StartedAt}} finished={{.State.FinishedAt}} error={{.State.Error}}", "--", name)
				d.t.Logf("failed sandbox %s container: %s (%v)", id, strings.TrimSpace(string(said)), e)
			}
		}
		// The gateway exposes its supervisor's trace, not the workload's
		// stderr. Read only this fixture's supervisor before it is deleted.
		if logged, e := supervisorLog(capture, d.gateway, id); e != nil {
			d.t.Logf("failure supervisor capture: %v", e)
		} else {
			d.t.Logf("failed sandbox %s supervisor: %s", id, logged)
		}
		// And what it is said to be from then on, for half a minute:
		// whether a sandbox said to have failed stays so. Nothing is
		// decided by it: what was observed is returned as it was.
		last := ""
		for began := time.Now(); time.Since(began) < 30*time.Second; time.Sleep(200 * time.Millisecond) {
			later, e := d.Driver.Observe(capture, id)
			if now := fmt.Sprintf("%v %q %v", later.State, later.Message, e); now != last {
				d.t.Logf("failed sandbox %s, %s on: %s", id, time.Since(began).Round(time.Millisecond), now)
				last = now
			}
		}
	}
	return got, err
}
