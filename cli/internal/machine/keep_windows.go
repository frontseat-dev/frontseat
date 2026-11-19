package machine

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// keep writes a file whole, in place of the one there was, where
// renameio does not serve (its WriteFile is not built for Windows): the
// file is written beside the old one, made to last, and renamed over it,
// which Windows does in one step (MoveFileEx, replacing what is there).
// A file another command has open, as one reading the context does,
// cannot be replaced while it is: the rename is tried again for as long
// as such a read takes, and fails if it still cannot be done.
//
// Unproven on Windows: nothing in this repository runs it there.
func keep(path string, content []byte) (err error) {
	beside, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(beside.Name())
		}
	}()
	if _, err = beside.Write(content); err != nil {
		beside.Close()
		return err
	}
	if err = beside.Sync(); err != nil {
		beside.Close()
		return err
	}
	if err = beside.Close(); err != nil {
		return err
	}
	for tries := 0; ; tries++ {
		err = os.Rename(beside.Name(), path)
		if err == nil || !held(err) || tries == 50 {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// held says whether a file could not be replaced because another has it
// open: access denied, or a sharing violation.
func held(err error) bool {
	const sharingViolation = syscall.Errno(32)
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, sharingViolation)
}
