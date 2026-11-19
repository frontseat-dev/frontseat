package stack

import (
	"slices"
	"strings"
	"testing"
)

func TestTheContainerIsGivenWhatNestingNeedsAndNothingOfThisMachine(t *testing.T) {
	arguments := Container{Name: "n", Image: "i", Volume: "v"}.Arguments("/p.json")
	for _, refused := range []string{"--privileged", "/var/run/docker.sock", "--mount"} {
		for _, argument := range arguments {
			if strings.Contains(argument, refused) {
				t.Errorf("the container is given %q", argument)
			}
		}
	}
	for _, volume := range []string{"v:/var/lib/frontseat"} {
		if !slices.Contains(arguments, volume) {
			t.Errorf("the container is not given its volume %s", volume)
		}
	}
	// This machine is reached by one name on every system, as a test's
	// origin is (testorigin): Linux's runtimes name it only when told.
	if !slices.Contains(arguments, "host.docker.internal:host-gateway") {
		t.Errorf("the container is not told how this machine is named: %q", arguments)
	}
	if slices.Contains(arguments, "--publish") {
		t.Errorf("a container told to publish nothing publishes %q", arguments)
	}
	// The control: one told to publish publishes on the loopback alone.
	published := Container{Name: "n", Image: "i", Volume: "v", Publish: true}.Arguments("/p.json")
	for i, argument := range published {
		if argument == "--publish" && !strings.HasPrefix(published[i+1], "127.0.0.1:") {
			t.Errorf("a port is published as %q, beyond this machine", published[i+1])
		}
	}
	if !slices.Contains(published, "--publish") {
		t.Error("a container told to publish its ports publishes none")
	}
}
