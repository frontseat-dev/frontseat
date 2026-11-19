//go:build !windows

package machine

import "github.com/google/renameio/v2"

// keep writes a file whole, in place of the one there was: renameio
// writes it beside and renames it over the old, so that what reads it
// meanwhile reads the one before or this one.
func keep(path string, content []byte) error {
	return renameio.WriteFile(path, content, 0o600)
}
