package local_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/stack/local"
)

// The image every runner sandbox is made of is said once, in the file the
// stack's image holds: the worker advertises it from there, the pool
// makes every sandbox of it from there, and the platform asked of the
// workers is read from it here. An action is of the image it runs in only
// while the three are one.
func TestThePlatformNamesTheImageEveryRunnerSandboxIsMadeOf(t *testing.T) {
	platform, err := local.Embedded.Platform()
	if err != nil {
		t.Fatal(err)
	}
	image := strings.TrimPrefix(platform["container-image"], "docker://")
	if !strings.Contains(image, "@sha256:") {
		t.Fatalf("the platform's image is %q; want one at a digest", platform["container-image"])
	}
	for file, told := range map[string]string{
		"image/rootfs/etc/frontseat/bb/worker.jsonnet":   "{ name: 'container-image', value: 'docker://' + std.extVar('RUNNER_IMAGE') }",
		"image/rootfs/etc/s6-overlay/s6-rc.d/worker/run": "backtick -E runner { cat /etc/frontseat/runner-image }",
		"image/rootfs/etc/s6-overlay/s6-rc.d/pool/run":   "backtick -E runner { cat /etc/frontseat/runner-image }",
	} {
		content, err := ofTheStack(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), told) {
			t.Errorf("%s does not read the image from where it is said: no %q", file, told)
		}
	}
	// The control: what is not an image at a digest is refused.
	for _, wrong := range []string{"", "busybox:1.37", "a@sha256:abc", "a b@sha256:" + strings.Repeat("0", 64)} {
		if _, err := (local.Files{Runner: []byte(wrong)}).Image(); err == nil {
			t.Errorf("%q was taken for an image at a digest", wrong)
		}
	}
}

func TestADaemonIsToldTheGridAndItsWorkersPlatformByItsFlags(t *testing.T) {
	grid, err := local.Embedded.Grid("grid.example:8980")
	if err != nil {
		t.Fatal(err)
	}
	if grid.Address != "grid.example:8980" || grid.Assets != "grid.example:8983" {
		t.Errorf("grid at %q, assets at %q; want the asset service on the grid's host", grid.Address, grid.Assets)
	}
	platform, err := local.Embedded.Platform()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--grid=grid.example:8980", "--assets=grid.example:8983"}
	for _, name := range []string{"ISA", "OSFamily", "container-image"} {
		want = append(want, "--platform="+name+"="+platform[name])
	}
	if told := grid.Arguments(); !slices.Equal(told, want) {
		t.Errorf("the daemon is told %q; want %q", told, want)
	}
}
