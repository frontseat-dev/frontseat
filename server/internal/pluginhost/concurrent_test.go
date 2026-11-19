package pluginhost_test

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

// A fresh process is essential: another test may already have initialized
// Wazero's package-global version cache before these calls overlap.
func TestPluginHostsCanBeCreatedConcurrentlyFromCold(t *testing.T) {
	if os.Getenv("FRONTSEAT_COLD_HOST_CHILD") != "yes" {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPluginHostsCanBeCreatedConcurrentlyFromCold$", "-test.paniconexit0")
		command.Env = append(os.Environ(), "FRONTSEAT_COLD_HOST_CHILD=yes")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("cold concurrent hosts: %v\n%s", err, out)
		}
		return
	}
	start := make(chan struct{})
	errors := make(chan error, 32)
	var running sync.WaitGroup
	for range 32 {
		running.Go(func() {
			<-start
			host, err := pluginhost.New(context.Background())
			if err == nil {
				err = host.Close(context.Background())
			}
			errors <- err
		})
	}
	close(start)
	running.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
}
