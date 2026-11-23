package box

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/frontseat-dev/frontseat/stack/local"
)

// StackRepository is the name every image of the local stack is tagged in.
const StackRepository = "frontseat-local"

// Stack is one build of the local stack's image (stack/image): the stack
// as one container, built on a box, whose programs it runs and which its
// Podman runs for each workspace. Unlike the box, it reaches the network:
// Wolfi's packages and the downloads its Dockerfile names by checksum.
type Stack struct {
	// Root is this repository's root, where stack/image is.
	Root string
	// Box is the box it is built on, by its tag of its content (Build.Run).
	Box string
	// Tag is a name the image is tagged beside the one of its content;
	// empty, none.
	Tag string
	// Architecture is the container runtime's, as for Build; empty, the
	// runtime is asked.
	Architecture string
	// Log is where the build says what it does; nil says nothing.
	Log io.Writer
}

// Run builds the stack's image and returns its tag of its content.
func (s Stack) Run(ctx context.Context) (string, error) {
	if s.Root == "" || s.Box == "" {
		return "", fmt.Errorf("a stack build names the repository's root and the box it is built on")
	}
	goarch, err := goarchOf(ctx, s.Architecture)
	if err != nil {
		return "", err
	}
	say := func(format string, args ...any) {
		if s.Log != nil {
			fmt.Fprintf(s.Log, format+"\n", args...)
		}
	}
	dir, err := os.MkdirTemp("", "frontseat-local-image-")
	if err != nil {
		return "", err
	}
	// The directory is this build's own, made just above.
	defer os.RemoveAll(dir)
	laid := filepath.Join(dir, "context")
	source := filepath.Join(s.Root, "stack", "image")
	if err := os.CopyFS(laid, os.DirFS(source)); err != nil {
		return "", err
	}
	// What the context holds is what the repository holds: a file made
	// executable there (an s6 run file) is executable in the image.
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		return os.Chmod(filepath.Join(laid, rel), info.Mode().Perm())
	}); err != nil {
		return "", err
	}
	say("docker build %s on %s", StackRepository, s.Box)
	return image{
		repository: StackRepository,
		what:       "the stack's image",
		dockerfile: filepath.Join(source, "Dockerfile"),
		laid:       laid,
		dir:        dir,
		// The architecture is said: a stage is chosen by it, and a builder
		// need not say it of itself.
		arguments: s.arguments(goarch),
		also:      s.Tag,
		// The first build fetches Wolfi's packages and the downloads.
		within: 20 * time.Minute,
		say:    say,
	}.build(ctx)
}

// arguments are the stack's build arguments: the box, and the
// architecture as the builder and the grid name it.
func (s Stack) arguments(goarch string) []string {
	return []string{"BOX=" + s.Box, "TARGETARCH=" + goarch, "ISA=" + local.ISA(goarch)}
}
