package life

import (
	"context"
	"os"
	"path/filepath"

	"github.com/frontseat-dev/frontseat/build/daemon/build"
	"github.com/frontseat-dev/frontseat/build/daemon/settings"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/common/vcs/chosen"
)

// Seen is how everything a build of a directory is made from looked at
// one moment (Look): what a command that builds again when something
// changed holds one moment against another by. It is the engine's own
// look at the engine's own listing (source.Looks), and reads no file
// for what it holds.
type Seen = source.Looks

// Look looks at what a build of a directory, by the daemon these
// settings make, is made from, as it is now: the files of the
// directory's repositories, as a build lists them; the workspace's
// manifest, which says which repositories there are; and what the
// daemon itself is made of (Parts), since one made of anything else is
// another daemon, whose builds may be others.
//
// What cannot be read of the directory or of the settings is not what
// a look fails on: a manifest that says the workspace is not whole, or
// settings a person is writing, are looked at as the files they are,
// and the build that is asked for then says what is wrong with them.
func Look(ctx context.Context, directory, told string) (*Seen, error) {
	beside := []string{filepath.Join(directory, filepath.FromSlash(manifest.File))}
	parts, err := Parts(told)
	if err != nil {
		parts = []string{told}
	}
	beside = append(beside, parts...)
	var tree source.Tree
	if alone, err := Alone(directory); err == nil {
		if tree, err = build.Source(directory, alone, chosen.System()); err != nil {
			tree = source.Tree{}
		}
	}
	return source.Look(ctx, tree, beside)
}

// Parts names the files a daemon of a file of settings is made of, as
// Read reads them: the settings, every plugin module they load, the
// authorities they name, if any, and this program's own file.
func Parts(told string) ([]string, error) {
	raw, err := settings.Bytes(told)
	if err != nil {
		return nil, err
	}
	read, err := settings.Parse(raw, told)
	if err != nil {
		return nil, err
	}
	parts := append([]string{told}, read.Plugins...)
	if read.Grid.Authorities != "" {
		parts = append(parts, read.Grid.Authorities)
	}
	program, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return append(parts, program), nil
}
