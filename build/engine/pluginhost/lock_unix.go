//go:build unix

package pluginhost

import (
	"errors"
	"os"
	"syscall"
)

// lockShared holds path's advisory lock shared, waiting for an exclusive
// holder, until release. A lock taken on a file a pruner removed while
// this waited is let go and taken again on the file now at the path.
func lockShared(path string) (release func(), err error) {
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
			_ = f.Close()
			return nil, err
		}
		held, err1 := f.Stat()
		now, err2 := os.Stat(path)
		if err1 == nil && err2 == nil && os.SameFile(held, now) {
			return func() { _ = f.Close() }, nil
		}
		_ = f.Close()
		if err2 != nil && !errors.Is(err2, os.ErrNotExist) {
			return nil, err2
		}
	}
}

// lockExclusive takes path's advisory lock exclusive without waiting:
// held reports that someone else holds it.
func lockExclusive(path string) (release func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() { _ = f.Close() }, false, nil
}
