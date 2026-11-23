package local

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"os/exec"
	"testing"

	"github.com/frontseat-dev/frontseat/local/box"
	"github.com/frontseat-dev/frontseat/local/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// A box holds the plugins it was built with, whatever a build before it
// held: two plugins of one size and of different content, each laid out
// at the one moment every box is of, are each read back from their own
// image. The container runtime's builder keeps what it was given of a
// context by each file's size and time, so a plugin that changed and
// kept its size was once built into the box as it was before. The
// control is the first plugin built again, which makes the very image
// it made the first time.
func TestABoxHoldsThePluginsItWasBuiltWithWhateverABuildBeforeItHeld(t *testing.T) {
	testrequired.Stack(t)
	testbox.Runtime(t)
	ctx := context.Background()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	// Of this run's own: no build before it was given either.
	first, second := make([]byte, 4096), make([]byte, 4096)
	_, _ = rand.Read(first)
	_, _ = rand.Read(second)
	built := func(plugin []byte) string {
		t.Helper()
		tag, err := box.Build{Root: root, Plugins: map[string][]byte{"probe": plugin}}.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Its tag is of its content, which holds this run's own plugin:
		// no other build made it, and nothing else runs it.
		t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", tag).Run() })
		return tag
	}
	held := func(tag string) [32]byte {
		t.Helper()
		out, err := exec.Command("docker", "run", "--rm", "--network=none", "--entrypoint", "cat", tag, "/opt/frontseat/plugins/probe.wasm").Output()
		if err != nil {
			t.Fatalf("reading the plugin %s holds: %v", tag, err)
		}
		return sha256.Sum256(out)
	}

	one := built(first)
	two := built(second)

	if got := held(one); got != sha256.Sum256(first) {
		t.Errorf("%s holds %x, want the first plugin, %x", one, got, sha256.Sum256(first))
	}
	if got := held(two); got != sha256.Sum256(second) {
		t.Errorf("%s holds %x, want the second plugin, %x, not the first, %x", two, got, sha256.Sum256(second), sha256.Sum256(first))
	}
	if again := built(bytes.Clone(first)); again != one {
		t.Errorf("the first plugin built again is %s, want the image it made before, %s", again, one)
	}
}
