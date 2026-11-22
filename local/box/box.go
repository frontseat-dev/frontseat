// Package box builds the box image: what a sandbox that holds a daemon
// runs. It is the daemon, the client and the plugins, built from this
// repository as it stands, laid on the sandbox image the local grid's
// workers run (local.Files.Image). Nothing is fetched over the
// network.
//
// It is the one build of the box: `mise run build` (frontseat-local box),
// the local stack (frontseat-local up) and the gate before the tests it
// runs all build it here. Every build is tagged by what it holds
// (frontseat-box:<the first twelve hex digits of its image id>), a tag no
// other build moves, since another content is another tag: whoever runs
// the box by that tag runs what was built, whatever any checkout tags
// frontseat-box:local after.
package box

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/frontseat-dev/frontseat/local"
)

// built is the moment every box image is of: an image is what it holds,
// not when it was made.
var built = time.Unix(0, 0)

// Build is what one build of the box image is given.
type Build struct {
	// Root is this repository's root: where the daemon and the client are
	// built from, and where box is.
	Root string
	// Tag is a name the image is tagged beside the one of its content,
	// for a person to ask for it by (frontseat-box:local); empty, none.
	// It is whatever was built last, from any checkout: nothing that must
	// run this build's box names it.
	Tag string
	// Architecture is the container runtime's: "aarch64" or "arm64" for
	// arm64, anything else for amd64. Empty, the runtime is asked.
	Architecture string
	// Plugins are the wasm modules the daemon loads, by name: the image
	// holds each as plugins/<name>.wasm, and the daemon loads every one.
	Plugins map[string][]byte
	// Log is where the build says what it does; nil says nothing.
	Log io.Writer
}

// Repository is the name every box image is tagged in.
const Repository = "frontseat-box"

// Run builds the image and returns its tag of its content, once that and
// Tag, where one is given, are on it.
func (b Build) Run(ctx context.Context) (string, error) {
	if b.Root == "" {
		return "", fmt.Errorf("a box build names the repository's root")
	}
	goarch, err := goarchOf(ctx, b.Architecture)
	if err != nil {
		return "", err
	}
	say := func(format string, args ...any) {
		if b.Log != nil {
			fmt.Fprintf(b.Log, format+"\n", args...)
		}
	}
	names := make([]string, 0, len(b.Plugins))
	for name := range b.Plugins {
		if name == "" || strings.ContainsAny(name, "/\\.") {
			return "", fmt.Errorf("a plugin is named %q: a name is a word, not a path", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	dir, err := os.MkdirTemp("", "frontseat-box-")
	if err != nil {
		return "", err
	}
	// The directory is this build's own, made just above.
	defer os.RemoveAll(dir)
	laid := filepath.Join(dir, "context")
	if err := os.MkdirAll(filepath.Join(laid, "plugins"), 0o755); err != nil {
		return "", err
	}

	for _, binary := range []string{"frontseatd", "frontseat", "frontseat-runner", "frontseat-local", "frontseat-action"} {
		say("building %s for linux/%s", binary, goarch)
		if err := linux(ctx, b.Root, binary, goarch, filepath.Join(laid, binary)); err != nil {
			return "", err
		}
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(laid, "plugins", name+".wasm"), b.Plugins[name], 0o644); err != nil {
			return "", err
		}
	}
	files, err := local.Read(b.Root)
	if err != nil {
		return "", err
	}
	sandbox, err := files.Image()
	if err != nil {
		return "", err
	}
	// A build that reaches for nothing over the network: the sandbox
	// image is on this machine already, and all else is in the context.
	say("docker build %s on %s", Repository, sandbox)
	return image{
		repository: Repository,
		what:       "the box image",
		dockerfile: filepath.Join(b.Root, "local", "box.Dockerfile"),
		laid:       laid,
		dir:        dir,
		arguments:  []string{"SANDBOX_IMAGE=" + sandbox},
		also:       b.Tag,
		within:     5 * time.Minute,
		say:        say,
	}.build(ctx)
}

// image is one build of an image of this repository, tagged by what it
// holds.
type image struct {
	// repository is the name it is tagged in, what is what it is called
	// where it fails, and dockerfile what it is built by.
	repository, what, dockerfile string
	// laid is its context, laid out, and dir a directory of the build's
	// own, beside it, where the runtime writes the image's id.
	laid, dir string
	// arguments are its build arguments, each NAME=value.
	arguments []string
	// also is a tag it is given beside its tag of its content; empty, none.
	also string
	// within is how long the runtime is given to build it: a runtime that
	// does not answer is reported, not waited on.
	within time.Duration
	say    func(format string, args ...any)
}

// build builds the image and returns its tag of its content.
func (i image) build(ctx context.Context) (string, error) {
	deadline, cancel := context.WithTimeout(ctx, i.within)
	defer cancel()
	// The same context makes the same image, whenever it is built: its
	// files are of one moment, and so is the image. A build that changes
	// nothing then leaves the tag on the image it was on, and whoever
	// holds that image by its id, as a runtime does between resolving a
	// tag and starting a container, still finds it.
	if err := filepath.WalkDir(i.laid, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, built, built)
	}); err != nil {
		return "", err
	}
	// The runtime's builder keeps what it was given of a context by its
	// directory's name and each file's size and time, which are the same
	// for every build: a plugin changed and of the same size would be
	// built as it was before. The context is named by what it holds, so
	// the same content keeps one name, and so one image, and other
	// content is never taken for it.
	named, err := byContent(i.laid)
	if err != nil {
		return "", err
	}
	if err := os.Rename(i.laid, named); err != nil {
		return "", err
	}
	i.laid = named
	// The build is known by the id the runtime writes of it, and is
	// tagged by its content from that: a tag another build may give too
	// could be moved between this one's end and its reading the tag. It
	// is built under a name no other build gives, which holds it until
	// then, and which is taken off after.
	made := filepath.Join(i.dir, "image")
	building := fmt.Sprintf("%s:building-%d-%d", i.repository, os.Getpid(), time.Now().UnixNano())
	arguments := []string{"build",
		"--pull=false",
		// No account of the build is attached: it would say when, and
		// make each build an image of its own.
		"--provenance=false",
		"--file", i.dockerfile,
		"--iidfile", made,
		"--tag", building,
	}
	for _, argument := range i.arguments {
		arguments = append(arguments, "--build-arg", argument)
	}
	// The context is what was laid out, and the id is written beside it,
	// in nothing the image holds.
	command := exec.CommandContext(deadline, "docker", append(arguments, i.laid)...)
	command.Env = append(os.Environ(), "SOURCE_DATE_EPOCH="+strconv.FormatInt(built.Unix(), 10))
	if out, err := command.CombinedOutput(); err != nil {
		if deadline.Err() != nil {
			return "", fmt.Errorf("building %s: the container runtime did not finish within %s\n%s", i.what, i.within, out)
		}
		return "", fmt.Errorf("building %s: %w\n%s", i.what, err, out)
	}
	// The name it was built under is this build's own, and goes with it;
	// the image keeps the tags given it below.
	defer func() {
		_ = exec.CommandContext(context.WithoutCancel(ctx), "docker", "image", "rm", building).Run()
	}()
	read, err := os.ReadFile(made)
	if err != nil {
		return "", fmt.Errorf("building %s: the runtime wrote no id of it: %w", i.what, err)
	}
	tag, err := tagOf(i.repository, strings.TrimSpace(string(read)))
	if err != nil {
		return "", err
	}
	for _, name := range []string{tag, i.also} {
		if name == "" {
			continue
		}
		if out, err := exec.CommandContext(deadline, "docker", "tag", building, name).CombinedOutput(); err != nil {
			return "", fmt.Errorf("tagging %s %s: %w\n%s", i.what, name, err, out)
		}
	}
	i.say("tagged %s", tag)
	return tag, nil
}

// byContent is the name, beside laid, of a context that holds what laid
// does: "context-" and a digest of each of its paths, in order, and of
// each file's mode and content.
func byContent(laid string) (string, error) {
	digest := sha256.New()
	err := filepath.WalkDir(laid, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(laid, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%q %v\n", filepath.ToSlash(rel), info.Mode())
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%d\n", len(content))
		_, err = digest.Write(content)
		return err
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(laid), "context-"+hex.EncodeToString(digest.Sum(nil))), nil
}

// Of is the tag of the box image of an id (sha256:<64 hex digits>): the
// repository's name and the first twelve hex digits of the id, which is
// a digest of what the image holds.
func Of(id string) (string, error) { return tagOf(Repository, id) }

// tagOf is the tag of an image of a repository by its id.
func tagOf(repository, id string) (string, error) {
	hex, is := strings.CutPrefix(id, "sha256:")
	if !is || len(hex) != 64 || strings.Trim(hex, "0123456789abcdef") != "" {
		return "", fmt.Errorf("the container runtime named the image it built %q, which is no image id", id)
	}
	return repository + ":" + hex[:12], nil
}

// PluginsIn reads each <name>.wasm of a directory, by name. What a
// desktop or a writer leaves there, a name beginning with a dot, is no
// plugin and is not put in the image.
func PluginsIn(plugins string) (map[string][]byte, error) {
	entries, err := os.ReadDir(plugins)
	if err != nil {
		return nil, err
	}
	modules := map[string][]byte{}
	for _, entry := range entries {
		name, isModule := strings.CutSuffix(entry.Name(), ".wasm")
		if !isModule || entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(plugins, entry.Name()))
		if err != nil {
			return nil, err
		}
		modules[name] = content
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("%s holds no <name>.wasm: build the plugins first", plugins)
	}
	return modules, nil
}

// Answers returns where the container runtime does not answer, and why.
func Answers(ctx context.Context) error {
	_, err := goarchOf(ctx, "")
	return err
}

// goarchOf is the Go name of the container runtime's architecture, as
// given ("aarch64" or "arm64" for arm64, anything else for amd64), or
// asked of the runtime where none is given.
func goarchOf(ctx context.Context, architecture string) (string, error) {
	if architecture == "" {
		out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.Architecture}}").Output()
		if err != nil {
			return "", fmt.Errorf("asking the container runtime its architecture: %w", err)
		}
		architecture = strings.TrimSpace(string(out))
	}
	if architecture == "aarch64" || architecture == "arm64" {
		return "arm64", nil
	}
	return "amd64", nil
}

// programs are the programs the box holds, by their command's name, each
// with its package: cmd/<binary> of the Component that owns it.
var programs = map[string]string{
	"frontseat":        "cli/cmd/frontseat",
	"frontseatd":       "server/cmd/frontseatd",
	"frontseat-runner": "server/cmd/frontseat-runner",
	"frontseat-local":  "server/cmd/frontseat-local",
	"frontseat-action": "server/cmd/frontseat-action",
}

// linux builds a program of this repository, by its command's name, for
// Linux on an architecture: static, as everything the box holds is.
func linux(ctx context.Context, root, binary, goarch, out string) error {
	program, known := programs[binary]
	if !known {
		return fmt.Errorf("%s is no program the box holds", binary)
	}
	// Without the checkout's path in it: two checkouts of one source
	// build one box. Without the symbol table and debugging information
	// either, a third of each program: a panic still names its functions.
	compile := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./"+program)
	compile.Dir = root
	compile.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
	if output, err := compile.CombinedOutput(); err != nil {
		return fmt.Errorf("building %s for linux/%s: %w\n%s", binary, goarch, err, output)
	}
	return nil
}

// InstallConfiner installs the confiner, frontseat-action, as this
// repository builds it, where the local stack's runner sandboxes find it
// (local.Confiners, in the stack's container), beside the forms already
// there and never over one (confine.Install), as the stack's installer
// does with its box's. It copies the program into the stack's container
// and runs its own install there, and returns the path a worker runs it
// by.
func InstallConfiner(ctx context.Context, root string) (string, error) {
	goarch, err := goarchOf(ctx, "")
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "frontseat-action-")
	if err != nil {
		return "", err
	}
	// The directory is this call's own, made just above.
	defer os.RemoveAll(dir)
	if err := linux(ctx, root, "frontseat-action", goarch, filepath.Join(dir, "frontseat-action")); err != nil {
		return "", err
	}
	// Into a place of the container's that is this call's own, named by
	// this process; it is installed from there and removed after.
	carried := fmt.Sprintf("/tmp/frontseat-action-%d-%d", os.Getpid(), time.Now().UnixNano())
	if out, err := exec.CommandContext(ctx, "docker", "cp", filepath.Join(dir, "frontseat-action"), local.Container+":"+carried).CombinedOutput(); err != nil {
		return "", fmt.Errorf("carrying frontseat-action into the stack (%s): %w: %s", local.Container, err, strings.TrimSpace(string(out)))
	}
	// What it carried in is its own, under the name made above.
	defer func() {
		_ = exec.CommandContext(context.WithoutCancel(ctx), "docker", "exec", local.Container, "rm", "-f", "--", carried).Run()
	}()
	var said strings.Builder
	install := exec.CommandContext(ctx, "docker", "exec", local.Container, carried, "install", local.Confiners)
	install.Stderr = &said
	out, err := install.Output()
	if err != nil {
		return "", fmt.Errorf("installing frontseat-action in %s: %w: %s", local.Confiners, err, strings.TrimSpace(said.String()))
	}
	// Where a runner sandbox runs it: each holds local.Confiners at /bb
	// (runner.Pool), and its runner runs nothing else.
	return path.Join("/bb", path.Base(strings.TrimSpace(string(out)))), nil
}

// Give hands an image to the stack's Podman (local.Container's), as
// frontseat, whose Podman it is: the box, which each workspace's sandbox
// runs.
func Give(ctx context.Context, image string) error {
	return GiveTo(ctx, local.Container, image)
}

// GiveTo hands an image to the Podman of a container of the stack's
// image, as Give does to the stack's own: a test's stack beside it.
func GiveTo(ctx context.Context, container, image string) error {
	podman := []string{"exec", "-i", "-u", "frontseat", "-e", "XDG_RUNTIME_DIR=/run/user/1000", "-e", "HOME=/home/frontseat", container, "podman"}
	// The stack's Podman answers once the container has laid out what is
	// frontseat's, which a container just started has yet to do.
	for tries := 0; exec.CommandContext(ctx, "docker", append(podman, "info", "--format", "{{.Host.Arch}}")...).Run() != nil; tries++ {
		if tries >= 120 {
			return fmt.Errorf("the stack's Podman did not answer in two minutes: docker logs %s", container)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	save := exec.CommandContext(ctx, "docker", "save", image)
	load := exec.CommandContext(ctx, "docker", append(podman, "load", "-q")...)
	piped, err := save.StdoutPipe()
	if err != nil {
		return err
	}
	load.Stdin = piped
	// Each says what went wrong into its own buffer: the two run at once.
	var saving, loading bytes.Buffer
	save.Stderr, load.Stderr = &saving, &loading
	if err := save.Start(); err != nil {
		return err
	}
	loaded := load.Run()
	// What load did not read is read by no one: save is told so, rather
	// than writing on into a pipe no one empties.
	_ = piped.Close()
	saved := save.Wait()
	if err := errors.Join(saved, loaded); err != nil {
		return fmt.Errorf("handing %s to the stack's Podman: %w: %s", image, err, strings.TrimSpace(saving.String()+" "+loading.String()))
	}
	return nil
}
