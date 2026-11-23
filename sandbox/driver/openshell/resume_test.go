package openshell_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	sdk "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// Capture a failed fixture before conformance cleanup deletes its diagnostics.
// This adds no retry and changes no conformance assertion or production behavior.
type failureCapture struct {
	driver.Driver
	t       *testing.T
	gateway *sdk.Client
}

// A running supervisor is not proof that the daemon it starts has survived
// startup. Try both an immediate pause and a pause after the daemon answers.
func TestAResumedOpenShellBoxAnswersThroughItsRestartedDaemon(t *testing.T) {
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
		return d.Dial(ctx, id, buildPort)
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
			t.Fatalf("daemon failed after running: %+v", got)
		}
		select {
		case <-deadline.Done():
			t.Fatalf("daemon did not answer: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func captureFailures(t *testing.T, d driver.Driver) driver.Driver {
	t.Helper()
	return &failureCapture{Driver: d, t: t, gateway: gatewayClient(t)}
}

func (d *failureCapture) Observe(ctx context.Context, id string) (driver.Sandbox, error) {
	got, err := d.Driver.Observe(ctx, id)
	if got.State == driver.Failed {
		capture, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer stop()
		if record, e := d.gateway.Sandboxes().Get(capture, "default", id); e == nil {
			status, _ := json.Marshal(record.Status)
			d.t.Logf("failed sandbox %s status: %s", id, status)
		} else {
			d.t.Logf("failure status capture: %v", e)
		}
		if logs, e := d.gateway.Sandboxes().GetLogs(capture, "default", id, sdk.WithLogLines(100)); e == nil {
			data, _ := json.Marshal(logs)
			d.t.Logf("failed sandbox %s logs: %s", id, data)
		} else {
			d.t.Logf("failure log capture: %v", e)
		}
		// The gateway exposes its supervisor's trace, not the workload's
		// stderr. Read only this fixture's supervisor before it is deleted.
		if logged, e := supervisorLog(capture, d.gateway, id); e != nil {
			d.t.Logf("failure supervisor capture: %v", e)
		} else {
			d.t.Logf("failed sandbox %s supervisor: %s", id, logged)
		}
	}
	return got, err
}
