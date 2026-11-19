package container_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/driver/container"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

func TestFurtherContainersWaitForTheMainStartupGate(t *testing.T) {
	d := container.Driver{}
	for _, allowed := range []bool{true, false} {
		name := "ready"
		if !allowed {
			name = "refused"
		}
		t.Run(name, func(t *testing.T) {
			s, host := runnerSandbox(t, "frontseat-test-startup-"+name, true, "")
			s.Volumes[0].ReadOnly = false
			s.Containers[0].Volumes = s.Volumes
			s.Containers[0].Arguments = []string{"sh", "-c", "test -f /work/ready || exit 1; touch /work/started; sleep 600"}
			command := []string{"touch", "/work/ready"}
			if !allowed {
				command = []string{"false"}
			}
			s.BeforeContainers = &driver.StartupGate{Arguments: command, Timeout: 5 * time.Second}
			// The id is the test's own (runnerSandbox), so what it ends is
			// what it asked for.
			t.Cleanup(func() { d.Delete(context.Background(), s.ID) })
			_, err := d.Apply(t.Context(), drivertest.Labelled(s))
			if !allowed {
				if err == nil || !strings.Contains(err.Error(), "startup gate") {
					t.Fatalf("failed gate was ignored: %v", err)
				}
				if _, err := os.Stat(filepath.Join(host, "started")); !os.IsNotExist(err) {
					t.Fatalf("dependent started after a failed gate: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(host, "ready")); err != nil {
				t.Fatalf("the startup gate did not run: %v", err)
			}
			if exit, out := execIn(t, d, s.ID, "web", "test", "-f", "/work/started"); exit != 0 {
				t.Fatalf("dependent did not start after readiness: %d %s", exit, out)
			}
		})
	}
}
