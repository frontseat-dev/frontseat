// Package secretfile reads a secret a server keeps in a file of its
// own: the file, and the directory it is in, are the server's alone. A
// file another could read, one in a directory another could reach, and
// a link are refused, and what a file holds is never said. The local
// control plane keeps its owner's token so, and an operator gives it the
// token it presents to Backstage so.
package secretfile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Directory holds a directory to one only this server reaches: a
// directory, no link, that neither its group nor others may read, write
// or enter. What says what the directory holds, for a refusal.
func Directory(directory, what string) error {
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("the directory of %s, %s, is open to others or no directory: it is this server's alone (chmod 700)", what, directory)
	}
	return nil
}

// Read returns what a file holds, at most limit bytes, where the file is
// a regular file, no link, that neither its group nor others may read or
// write, in a directory Directory holds. What says what the file holds,
// for a refusal, which never says what is in it.
func Read(file, what string, limit int64) ([]byte, error) {
	if err := Directory(filepath.Dir(file), what); err != nil {
		return nil, err
	}
	there, err := os.Lstat(file)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", what, file, err)
	}
	// What is no regular file, a link or a pipe, is never opened.
	if !there.Mode().IsRegular() {
		return nil, fmt.Errorf("%s in %s is no file of its own (%s): it is this server's alone (chmod 600)", what, file, there.Mode())
	}
	opened, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", what, file, err)
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", what, file, err)
	}
	if !there.Mode().IsRegular() || !os.SameFile(there, info) || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s in %s can be read by others, or is no file of its own (%s): it is this server's alone (chmod 600)", what, file, there.Mode())
	}
	raw, err := io.ReadAll(io.LimitReader(opened, limit))
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", what, file, err)
	}
	return raw, nil
}
