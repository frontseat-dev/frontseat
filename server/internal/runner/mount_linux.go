package runner

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
)

// mountOf says what is mounted at a path, by the kernel's unique id of the
// mount (statx, STATX_MNT_ID_UNIQUE), which it never gives another mount,
// as it does the id the table of mounts writes: a worker started again
// may be given its earlier mount's. A path whose mount is its parent's is
// not mounted; nor is one whose file system no longer answers (a worker
// gone leaves its FUSE mount so). The attributes are not asked of the
// file system, which may be the one that does not answer.
func mountOf(path string) (string, bool, error) {
	here, err := uniqueMount(path)
	if errors.Is(err, unix.ENOTCONN) || errors.Is(err, unix.ENOENT) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	parent, err := uniqueMount(filepath.Dir(path))
	if err != nil {
		return "", false, err
	}
	if here == parent {
		return "", false, nil
	}
	return strconv.FormatUint(here, 10), true, nil
}

// uniqueMount is the unique id of the mount a path is on.
func uniqueMount(path string) (uint64, error) {
	var st unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_DONT_SYNC|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID_UNIQUE, &st); err != nil {
		return 0, err
	}
	if st.Mask&unix.STATX_MNT_ID_UNIQUE == 0 {
		return 0, errors.New("the kernel gives no unique mount id (Linux 6.8 does), by which a slot mounted anew is known")
	}
	return st.Mnt_id, nil
}

// handOver gives the worker the socket a runner made at listen: opened
// without following a link, it is a socket or nothing is done; it is
// given to root alone through what was opened, and linked at endpoint
// from there, so that what was checked is what is linked, whatever is
// put at listen meanwhile.
func handOver(listen, endpoint string) error {
	fd, err := confine.OpenSocket(listen)
	if err != nil {
		return fmt.Errorf("its runner did not make the socket: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.Fchownat(fd, "", 0, 0, unix.AT_EMPTY_PATH); err != nil {
		return err
	}
	// What was opened, by the link /proc holds to it: no path an action
	// names is followed.
	opened := "/proc/self/fd/" + strconv.Itoa(fd)
	if err := unix.Chmod(opened, 0o600); err != nil {
		return err
	}
	return unix.Linkat(unix.AT_FDCWD, opened, unix.AT_FDCWD, endpoint, unix.AT_SYMLINK_FOLLOW)
}
