// Package image reads an image's reference as a service of a test is
// named by: with Docker's own parser (distribution/reference), its
// registry written out, and its digest. It knows nothing of where an
// image is pulled from.
package image

import (
	"errors"
	"strings"

	"github.com/distribution/reference"
)

// Pinned is an image named as nothing but one image can be.
type Pinned struct {
	// Registry is the registry it is named by, as written.
	Registry string
	// Path is its repository within the registry.
	Path string
	// Digest is its digest.
	Digest string
}

// Read reads an image a service is named by. A short name is a guess:
// Podman will not guess a registry, and Docker's guess (docker.io) could
// be another's image; a name with no digest could be another image
// tomorrow; and what is not a reference could be read by a command line
// as its options. An image a build made, named by a label, is not
// started yet. A refusal says what an image's name takes and never the
// name: one may be written with a credential in it.
func Read(written string) (Pinned, error) {
	if written == "" {
		return Pinned{}, errors.New("names no image; an image is named with its registry and digest")
	}
	if strings.HasPrefix(written, "//") {
		return Pinned{}, errors.New("is an image the build makes, which is not started yet; name an image with its registry and digest")
	}
	// The parser's own refusal repeats the name's parts, and is left out.
	named, err := reference.ParseNormalizedNamed(written)
	if err != nil {
		return Pinned{}, errors.New("is not an image's reference, as Docker reads one")
	}
	registry := reference.Domain(named)
	if !strings.HasPrefix(written, registry+"/") {
		return Pinned{}, errors.New("does not name its registry")
	}
	digested, ok := named.(reference.Digested)
	if !ok {
		return Pinned{}, errors.New("is not named by its digest")
	}
	return Pinned{Registry: registry, Path: reference.Path(named), Digest: digested.Digest().String()}, nil
}
