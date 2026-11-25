package openshell

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
)

// binding are the compute drivers of a gateway that bind a directory of
// their host into a sandbox, each told a mount as Docker's engine is.
var binding = map[string]bool{"podman": true, "docker": true}

// volumesOf is what volumes a gateway of a compute driver takes: a host
// volume where it binds one, and none otherwise.
func volumesOf(compute string) []driver.VolumeKind {
	if binding[compute] {
		return []driver.VolumeKind{driver.Host}
	}
	return nil
}

// mounts is what the gateway's compute driver is told of a sandbox's
// volumes, keyed by its name, as OpenShell's driver configuration is: a
// bind of each host directory, read-only unless the volume says it is
// written. Nil where there is none. A volume is held to a plain absolute
// path on each side, which is what is bound and where: the host's
// directory is the caller's to name, and a path that climbs or is
// spelled two ways names something else than it says.
func mounts(compute string, volumes []driver.Volume) (map[string]any, error) {
	if len(volumes) == 0 {
		return nil, nil
	}
	if !binding[compute] {
		return nil, &driver.Unsupported{Driver: "openshell", Capability: fmt.Sprintf("a directory of its host bound in, over the compute driver %q, which binds none", compute)}
	}
	// As a protobuf Struct holds it, which is what the gateway is sent:
	// a list of any.
	var bound []any
	for _, v := range volumes {
		if v.Kind != driver.Host {
			return nil, &driver.Unsupported{Driver: "openshell", Capability: "a volume of another kind than the host's, the one kind the gateway binds"}
		}
		for _, path := range []string{v.Source, v.Path} {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return nil, fmt.Errorf("openshell: a volume names what is no plain absolute path: %w", driver.ErrInvalid)
			}
		}
		source, err := beneath(v.Source, v.Subpath)
		if err != nil {
			return nil, fmt.Errorf("openshell: a volume's subpath: %w: %w", err, driver.ErrInvalid)
		}
		bound = append(bound, map[string]any{"type": "bind", "source": source, "target": v.Path, "read_only": v.ReadOnly})
	}
	return map[string]any{compute: map[string]any{"mounts": bound}}, nil
}

// beneath is the directory of a host volume a subpath names, reached from
// the volume's source as it is written: each directory on the way is one,
// and no link, since the gateway's driver follows a link it binds, and
// one would take the bind elsewhere than the volume. The gateway binds
// what this machine holds, so it is checked here, as the gateway's host
// has it. No subpath is the source itself.
func beneath(source, subpath string) (string, error) {
	if subpath == "" {
		return source, nil
	}
	if !filepath.IsLocal(subpath) || filepath.Clean(subpath) != subpath {
		return "", errors.New("it is no plain path beneath its volume")
	}
	at := source
	for _, step := range strings.Split(subpath, string(filepath.Separator)) {
		at = filepath.Join(at, step)
		// What is said of a step names no path: the subpath and the
		// volume's source are the caller's, and either may hold a token.
		info, err := os.Lstat(at)
		var unread *fs.PathError
		if errors.As(err, &unread) {
			return "", fmt.Errorf("a step of it cannot be read: %w", unread.Err)
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", errors.New("a step of it is no directory: a subpath is reached through directories alone")
		}
	}
	return at, nil
}
