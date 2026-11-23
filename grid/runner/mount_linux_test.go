package runner

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A mount point is mounted, by an id the kernel never gives another; a
// directory on its parent's mount is not, nor a path not there.
func TestAMountIsKnownByTheKernelsUniqueID(t *testing.T) {
	id, is, err := mountOf("/proc")
	if err != nil && strings.Contains(err.Error(), "no unique mount id") {
		t.Skip(err)
	}
	if err != nil || !is || id == "" {
		t.Fatalf("/proc is mounted at %q (%v, %v); want a mount", id, is, err)
	}
	again, _, _ := mountOf("/proc")
	if again != id {
		t.Errorf("/proc's mount read %q, then %q", id, again)
	}
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "absent")} {
		if _, is, err := mountOf(path); err != nil || is {
			t.Errorf("%s is said mounted (%v, %v)", path, is, err)
		}
	}
}

// A runner's socket is handed over as what was checked: given to root,
// 0600, and linked at the endpoint. A link put at listen, or a file that
// is no socket, is refused, and what the link leads to is left as it is.
func TestARunnersSocketIsHandedOverAsWhatWasChecked(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("handing a socket to root is root's to do")
	}
	dir, err := os.MkdirTemp("/tmp", "hand")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chown(dir, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	listen, endpoint := filepath.Join(dir, "listen"), filepath.Join(dir, "endpoint")
	listener, err := net.Listen("unix", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Lchown(listen, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if err := handOver(listen, endpoint); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(endpoint)
	if err != nil || info.Mode().Type() != os.ModeSocket || info.Mode().Perm() != 0o600 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Errorf("the endpoint is %v (%v), want root's socket, 0600", info, err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, plant := range map[string]func(string) error{
		"a link": func(at string) error { return os.Symlink(victim, at) },
		"a file": func(at string) error { return os.WriteFile(at, nil, 0o644) },
	} {
		at, to := filepath.Join(dir, "planted"), filepath.Join(dir, "planted-endpoint")
		if err := plant(at); err != nil {
			t.Fatal(err)
		}
		if err := handOver(at, to); err == nil {
			t.Errorf("%s at listen was handed over", name)
		}
		if _, err := os.Lstat(to); err == nil {
			t.Errorf("%s at listen was linked at the endpoint", name)
		}
		os.Remove(at)
	}
	if info, err := os.Stat(victim); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("what the link led to was changed: %v %v", info, err)
	}
}
