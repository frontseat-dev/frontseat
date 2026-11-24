package local

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/stack/image"
)

// Files are the stack's files that say what it is.
type Files struct {
	// Runner is stack/image/rootfs/etc/frontseat/runner-image: the image
	// every runner sandbox of the grid is made of, at its digest.
	Runner []byte
}

// runnerImage is where the image the grid's actions run in is said,
// beneath the repository's root: once, in the file the stack's image
// holds (/etc/frontseat/runner-image), which its worker advertises and
// its pool makes every runner sandbox of.
const runnerImage = "stack/image/rootfs/etc/frontseat/runner-image"

// Read reads the stack's files from the repository at root.
func Read(root string) (Files, error) {
	runner, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(runnerImage)))
	if err != nil {
		return Files{}, err
	}
	return Files{Runner: runner}, nil
}

// Workers returns how many workers the stack's grid has once it is up,
// as the stack's image says: one for each action it runs at once, and
// one for each test with services it runs beside them.
func Workers() (int, error) {
	total := 0
	for name, said := range map[string][]byte{"slots": image.Slots, "services": image.Services} {
		count, err := strconv.Atoi(strings.TrimSpace(string(said)))
		if err != nil || count < 0 {
			return 0, fmt.Errorf("the stack's image says %q for its %s, which is no count", strings.TrimSpace(string(said)), name)
		}
		total += count
	}
	return total, nil
}

// Container is the local stack's one container's name.
const Container = "frontseat-stack"

// Confiners is the directory of the stack's container each form of the
// confiner (frontseat-confine) is installed into, which every runner
// sandbox holds at /bb, where its runner (frontseat-runner serve) runs
// an action only by one of them.
const Confiners = "/var/lib/frontseat/bb"

// Image returns the image every runner sandbox of the grid is made of, at
// its digest: what a worker advertises, and what the box is laid on.
func (f Files) Image() (string, error) {
	image := strings.TrimSpace(string(f.Runner))
	if _, digest, pinned := strings.Cut(image, "@sha256:"); !pinned || len(digest) != 64 || strings.ContainsAny(image, " \t\n") {
		return "", fmt.Errorf("%s names %q, which is not an image at a digest", runnerImage, image)
	}
	return image, nil
}

// Platform returns what an action asks of the grid's workers: the
// properties the stack's worker.jsonnet advertises. The image is among them,
// so an action is of the image it runs in.
func (f Files) Platform() (map[string]string, error) {
	image, err := f.Image()
	if err != nil {
		return nil, err
	}
	return map[string]string{"OSFamily": "linux", "ISA": ISA(runtime.GOARCH), "container-image": "docker://" + image}, nil
}

// ISA is the grid's name of a Go architecture: what a worker of it
// advertises (GRID_ISA) and an action asks for. Empty for one the grid
// has no workers of.
func ISA(goarch string) string {
	return map[string]string{"arm64": "aarch64", "amd64": "x86-64"}[goarch]
}

// Grid is what a box's supervisor on this machine is told of the
// stack's grid.
type Grid struct {
	// Address is the grid's, host:port, as a build daemon reaches it.
	Address string
	// Assets is the asset service's, host:port, on the grid's host.
	Assets string
	// Platform is what an action asks of the grid's workers.
	Platform map[string]string
}

// Grid returns what a box's supervisor is told of the grid at an
// address, as a sandbox reaches it: its workers' platform, and the
// asset service on the same host, which fetches a download from where
// it names.
func (f Files) Grid(address string) (Grid, error) {
	platform, err := f.Platform()
	if err != nil {
		return Grid{}, err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return Grid{}, fmt.Errorf("the grid's address %q: %w", address, err)
	}
	return Grid{Address: address, Assets: net.JoinHostPort(host, strconv.Itoa(ports.Assets)), Platform: platform}, nil
}

// Arguments are the flags that tell a box's supervisor of the grid a
// build in its workspace runs on (frontseat-box --help), which it writes
// where a build there reads its settings: --grid and --assets, where
// each is known, and a --platform for each property, as name=value, in
// order. Each is one argument, written with its "=", so that no value is
// read as a flag.
func (g Grid) Arguments() []string {
	var arguments []string
	if g.Address != "" {
		arguments = append(arguments, "--grid="+g.Address)
	}
	if g.Assets != "" {
		arguments = append(arguments, "--assets="+g.Assets)
	}
	for _, name := range sorted(g.Platform) {
		arguments = append(arguments, "--platform="+name+"="+g.Platform[name])
	}
	return arguments
}

// Hosts are where a build reaches the grid, each host:port: the grid,
// and its asset service, where each is known.
func (g Grid) Hosts() []string {
	var hosts []string
	for _, host := range []string{g.Address, g.Assets} {
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// sorted is a map's keys, in order.
func sorted(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Checkout returns the checkout of Frontseat a program is started at, as
// mise starts the stack's program, the gate and what prepares the
// plugins: its working directory, which holds go.work, and never one
// above it.
func Checkout() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		return "", fmt.Errorf("%s holds no go.work: run this at the root of a checkout of frontseat", root)
	}
	return root, nil
}
