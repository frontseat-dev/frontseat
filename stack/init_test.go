package stack

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// asked records what Init asks of the system, and asks nothing of the
// kernel: a cgroup file system is a directory of files to it.
type asked struct {
	owned   map[string]Owner
	shared  []string
	execed  []string
	refuses string
}

func (a *asked) pid() int { return 1 }
func (a *asked) chown(path string, owner Owner) error {
	if a.owned == nil {
		a.owned = map[string]Owner{}
	}
	a.owned[path] = owner
	return nil
}
func (a *asked) remountWritable(string) error { return nil }
func (a *asked) shareSubtree(path string) error {
	if a.refuses == "share" {
		return errors.New("refused")
	}
	a.shared = append(a.shared, path)
	return nil
}
func (a *asked) exec(program []string) error { a.execed = program; return nil }

func initIn(t *testing.T, has string) (Init, *asked) {
	t.Helper()
	root := t.TempDir()
	cgroups := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(cgroups, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cgroups, "cgroup.controllers"), []byte(has), 0o644); err != nil {
		t.Fatal(err)
	}
	// What a kernel holds in a cgroup that only root is to write.
	if err := os.MkdirAll(filepath.Join(cgroups, "frontseat"), 0o755); err != nil {
		t.Fatal(err)
	}
	// What a kernel makes in every cgroup: the files that place processes,
	// and one that limits it, which only root is to write.
	for _, file := range []string{"cgroup.procs", "cgroup.threads", "memory.max"} {
		if err := os.WriteFile(filepath.Join(cgroups, "frontseat", file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	template := filepath.Join(root, "gateway.toml")
	if err := os.WriteFile(template, []byte("[openshell.drivers.podman]\nhost_gateway_ip       = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sys := &asked{}
	return Init{
		Gateway:   GatewayConfig{Template: template, Config: filepath.Join(root, "run", "frontseat", "gateway.toml"), Hosts: filepath.Join(root, "hosts")},
		Cgroups:   cgroups,
		State:     filepath.Join(root, "state"),
		Runtime:   filepath.Join(root, "run", "user", "1000"),
		Frontseat: Owner{1000, 1000},
		BB:        Owner{1001, 1001},
		sys:       sys,
	}, sys
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestInitGivesFrontseatACgroupSubtreeWithTheControllersTheKernelHas(t *testing.T) {
	i, sys := initIn(t, "cpuset cpu io memory hugetlb pids rdma misc")
	if err := i.Run([]string{"/init"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(i.Cgroups, "init", "cgroup.procs")); got != "1" {
		t.Errorf("init's process went to %q, want itself (1) moved out of the root", got)
	}
	for _, at := range []string{i.Cgroups, filepath.Join(i.Cgroups, "frontseat")} {
		if got := read(t, filepath.Join(at, "cgroup.subtree_control")); got != "+cpu +memory +pids" {
			t.Errorf("%s gives its children %q, want +cpu +memory +pids", at, got)
		}
	}
	for _, sub := range []string{"frontseat", "frontseat/podman", "frontseat/runner", "frontseat/cgroup.procs", "frontseat/cgroup.subtree_control"} {
		if owner := sys.owned[filepath.Join(i.Cgroups, sub)]; owner != i.Frontseat {
			t.Errorf("%s is owned by %v, want frontseat %v", sub, owner, i.Frontseat)
		}
	}
	// What limits frontseat stays root's, as systemd's delegation leaves it.
	if owner, owned := sys.owned[filepath.Join(i.Cgroups, "frontseat", "memory.max")]; owned {
		t.Errorf("frontseat's memory.max is given to %v: it would raise its own limit", owner)
	}
	if !slices.Equal(sys.execed, []string{"/init"}) {
		t.Errorf("it became %q, want /init", sys.execed)
	}
}

func TestInitGivesOnlyTheControllersTheKernelHas(t *testing.T) {
	// The control: a kernel without the memory controller is not told to
	// give it, which it would refuse.
	i, _ := initIn(t, "cpu pids")
	if err := i.Run([]string{"/init"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(i.Cgroups, "cgroup.subtree_control")); got != "+cpu +pids" {
		t.Errorf("the root gives %q, want +cpu +pids", got)
	}
}

func TestInitLaysOutTheStateForWhoKeepsEachPart(t *testing.T) {
	i, sys := initIn(t, "cpu memory pids")
	// A volume used before holds what its owners left: Podman's store
	// holds files of frontseat's subordinate ids, which are not to change.
	kept := filepath.Join(i.State, "containers", "storage", "overlay", "layer", "bin", "su")
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, nil, 0o4755); err != nil {
		t.Fatal(err)
	}
	if err := i.Run([]string{"/init"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]*Owner{
		"storage-cas/persistent_state": &i.BB,
		"containers":                   &i.Frontseat,
		"openshell":                    &i.Frontseat,
		"controlplane":                 &i.Frontseat,
		"worker":                       nil,
		"bb":                           nil,
	} {
		at := filepath.Join(i.State, path)
		if _, err := os.Stat(at); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		owner, owned := sys.owned[at]
		if want == nil && owned {
			t.Errorf("%s is given to %v, and is root's", path, owner)
		}
		if want != nil && owner != *want {
			t.Errorf("%s is owned by %v, want %v", path, owner, *want)
		}
	}
	for path, owner := range sys.owned {
		if strings.HasPrefix(path, filepath.Join(i.State, "containers")+string(filepath.Separator)) {
			t.Errorf("%s, within Podman's store, was given to %v", path, owner)
		}
	}
	// The control: the store's own directory is frontseat's.
	if owner := sys.owned[filepath.Join(i.State, "containers")]; owner != i.Frontseat {
		t.Errorf("Podman's store is owned by %v, want frontseat", owner)
	}
	info, err := os.Stat(filepath.Join(i.State, "worker"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 || info.Mode()&os.ModeSticky != 0 {
		t.Errorf("the worker's directory is %v, want root's alone to write (0755)", info.Mode())
	}
	if !slices.Equal(sys.shared, []string{filepath.Join(i.State, "worker")}) {
		t.Errorf("shared %q, want the worker's directory alone", sys.shared)
	}
	if owner := sys.owned[i.Runtime]; owner != i.Frontseat {
		t.Errorf("the runtime directory is owned by %v, want frontseat", owner)
	}
	// frontseat passes through what is above its runtime directory: one
	// made private kept its Podman out of its own (lstat /run/user/1000:
	// permission denied).
	above, err := os.Stat(filepath.Dir(i.Runtime))
	if err != nil {
		t.Fatal(err)
	}
	if above.Mode().Perm()&0o005 != 0o005 {
		t.Errorf("%s is %v: frontseat cannot pass through it", filepath.Dir(i.Runtime), above.Mode())
	}
}

func TestInitBecomesNothingWhereItCouldNotLayTheStateOut(t *testing.T) {
	i, sys := initIn(t, "cpu memory pids")
	sys.refuses = "share"
	err := i.Run([]string{"/init"})
	if err == nil || !strings.Contains(err.Error(), "laying out") {
		t.Fatalf("got %v, want the layout refused", err)
	}
	if sys.execed != nil {
		t.Errorf("it became %q though the state was not laid out", sys.execed)
	}
}

func TestInitIsToldWhatToBecome(t *testing.T) {
	i, _ := initIn(t, "cpu")
	if err := i.Run(nil); err == nil {
		t.Fatal("init with no program ran")
	}
}

// A sandbox's names of this machine lead where the container runtime says
// this machine is (host.docker.internal), and where it says nothing, or
// says the container's own loopback, they lead where the image's
// configuration says.
func TestInitSaysWhereASandboxsNamesOfThisMachineLead(t *testing.T) {
	for hosts, want := range map[string]string{
		"127.0.0.1 localhost\n0.250.250.254\thost.docker.internal\n":                    `host_gateway_ip = "0.250.250.254"`,
		"192.168.65.254 gateway.docker.internal host.docker.internal # the runtime's\n": `host_gateway_ip = "192.168.65.254"`,
		"127.0.0.1 localhost\n":                                                `host_gateway_ip       = "127.0.0.1"`,
		"127.0.0.1 localhost host.docker.internal\n":                           `host_gateway_ip       = "127.0.0.1"`,
		"fdc4::254 host.docker.internal\n0.250.250.254 host.docker.internal\n": `host_gateway_ip = "0.250.250.254"`,
	} {
		i, _ := initIn(t, "cpu memory pids")
		if err := os.WriteFile(i.Gateway.Hosts, []byte(hosts), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := i.Run([]string{"/init"}); err != nil {
			t.Fatal(err)
		}
		if got := read(t, i.Gateway.Config); !strings.Contains(got, want+"\n") {
			t.Errorf("with hosts %q the gateway is told %q, want %s", hosts, got, want)
		}
	}
}
