package stack

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/frontseat-dev/frontseat/local/ports"
)

// kernel is the system Init asks on Linux.
type kernel struct{}

func (kernel) pid() int { return os.Getpid() }

func (kernel) chown(path string, owner Owner) error {
	return os.Lchown(path, owner.UID, owner.GID)
}

// remountWritable makes the cgroup file system writable where the
// runtime mounted it read-only, as a container without privilege has it.
func (kernel) remountWritable(path string) error {
	if err := syscall.Access(path+"/cgroup.subtree_control", 2); err == nil {
		return nil
	}
	return syscall.Mount("", path, "", syscall.MS_REMOUNT|syscall.MS_BIND, "")
}

// shareSubtree makes a directory a mount of its own whose mounts are
// shared with the mounts it is bound into.
func (kernel) shareSubtree(path string) error {
	if err := syscall.Mount(path, path, "", syscall.MS_BIND, ""); err != nil {
		return err
	}
	return syscall.Mount("", path, "", syscall.MS_SHARED|syscall.MS_REC, "")
}

func (kernel) exec(program []string) error {
	path, err := exec.LookPath(program[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, program, os.Environ())
}

// ContainerInit is Init as the stack's container runs it: its paths, and the
// users of local/image's Dockerfile.
func ContainerInit() Init {
	return Init{
		Cgroups:   "/sys/fs/cgroup",
		State:     "/var/lib/frontseat",
		Runtime:   "/run/user/1000",
		Frontseat: Owner{UID: 1000, GID: 1000},
		BB:        Owner{UID: 1001, GID: 1001},
		Gateway:   GatewayConfig{Template: "/etc/openshell/gateway.template.toml", Config: "/etc/openshell/gateway.toml", Hosts: "/etc/hosts"},
		Resolver:  Resolver{Config: "/etc/resolv.conf", Forward: "/etc/unbound/forward.conf", Listen: ports.ResolverAddress},
		sys:       kernel{},
	}
}
