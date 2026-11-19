package stack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// runtime stands in for the container runtime: it answers what the
// stack asks of it from what it holds, and keeps, in order, what it was
// told to do.
type runtime struct {
	t *testing.T
	// from is the checkout the stack's container was started from, empty
	// where there is none.
	from string
	// done is what was done, in order: "rm", "run <image>", "load <box>".
	done []string
	// ran is what the container was made with.
	ran []string
}

func (r *runtime) docker(_ context.Context, arguments ...string) (string, error) {
	line := strings.Join(arguments, " ")
	switch {
	case line == "info --format {{.Architecture}}":
		return "aarch64\n", nil
	case arguments[0] == "ps":
		return r.from + "\n", nil
	case line == "rm --force "+Name:
		r.done = append(r.done, "rm")
		r.from = ""
		return "", nil
	case arguments[0] == "run":
		r.done = append(r.done, "run "+arguments[len(arguments)-1])
		r.ran = arguments
		return "", nil
	case arguments[0] == "exec" && arguments[2] == "test":
		return "", nil
	case arguments[0] == "exec" && arguments[2] == "cat":
		return "the owner's token\n", nil
	}
	r.t.Fatalf("the stack asked the runtime what it was not expected to: docker %s", line)
	return "", nil
}

// checkouts are each test's checkout, by the test.
var checkouts sync.Map

// checkout is the test's checkout: one of its own, the same however many
// stacks it makes of it, from which the stand-in runtime builds nothing.
func checkout(t *testing.T) string {
	t.Helper()
	if dir, ok := checkouts.Load(t); ok {
		return dir.(string)
	}
	dir := t.TempDir()
	checkouts.Store(t, dir)
	t.Cleanup(func() { checkouts.Delete(t) })
	return dir
}

// stackOf is a checkout's stack over a runtime that stands in, whose
// box and image are built as the tags given.
func stackOf(t *testing.T, from string) (*runtime, Stack, *[]string) {
	t.Helper()
	root := checkout(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	r := &runtime{t: t, from: from}
	built := &[]string{}
	s := Stack{Root: root, docker: r.docker,
		build: func(context.Context) (string, error) {
			*built = append(*built, "box")
			return "frontseat-box:a11111111111", nil
		},
		image: func(_ context.Context, box string) (string, error) {
			*built = append(*built, "image on "+box)
			return "frontseat-local:b22222222222", nil
		},
		load: func(_ context.Context, box string) error {
			r.done = append(r.done, "load "+box)
			return nil
		},
	}
	return r, s, built
}

// A stack started from this checkout runs the image built from it, on
// the box built from it, which its Podman is given: the container is made
// anew of that image, with its one volume, its ports on this machine
// alone, and the checkout it was started from.
func TestAStackRunsTheImageAndBoxOfItsCheckout(t *testing.T) {
	r, s, built := stackOf(t, "")
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"box", "image on frontseat-box:a11111111111"}; !slices.Equal(*built, want) {
		t.Errorf("built %q, want %q", *built, want)
	}
	if want := []string{"rm", "run frontseat-local:b22222222222", "load frontseat-box:a11111111111"}; !slices.Equal(r.done, want) {
		t.Errorf("did %q, want %q", r.done, want)
	}
	here, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, told := range []string{Volume + ":/var/lib/frontseat", checkoutLabel + "=" + here, "127.0.0.1:7700:7700"} {
		if !slices.Contains(r.ran, told) {
			t.Errorf("the container was not made with %q: %q", told, r.ran)
		}
	}
}

// A stack another checkout started is refused before anything is built:
// it runs what that checkout built. The control is a stack this checkout
// started, which is made anew.
func TestAStackAnotherCheckoutStartedIsRefusedBeforeAnythingIsBuilt(t *testing.T) {
	r, s, built := stackOf(t, "/elsewhere")
	err := s.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/elsewhere") {
		t.Fatalf("a stack another checkout started was not refused: %v", err)
	}
	if len(*built) != 0 || len(r.done) != 0 {
		t.Errorf("a refused stack built %q and did %q", *built, r.done)
	}
	here, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	r, s, _ = stackOf(t, here)
	if err := s.Up(context.Background()); err != nil {
		t.Fatalf("this checkout's own stack was refused: %v", err)
	}
	if len(r.done) == 0 || r.done[0] != "rm" {
		t.Errorf("this checkout's stack was not made anew: %q", r.done)
	}
}

// A stack another checkout started while this one built is not removed:
// the stack is asked again just before it is made anew.
func TestAStackAnotherCheckoutStartedDuringTheBuildIsLeftAsItIs(t *testing.T) {
	r, s, _ := stackOf(t, "")
	built := s.image
	s.image = func(ctx context.Context, box string) (string, error) {
		r.from = "/elsewhere"
		return built(ctx, box)
	}
	err := s.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/elsewhere") {
		t.Fatalf("a stack another checkout started meanwhile was not refused: %v", err)
	}
	if slices.Contains(r.done, "rm") {
		t.Errorf("the other checkout's stack was removed: %q", r.done)
	}
}

// The owner's token is laid out where the command line finds it, that
// person's alone, and what named another server is not left beside it.
func TestTheOwnersTokenIsLaidOutForTheCommandLineAlone(t *testing.T) {
	_, s, _ := stackOf(t, "")
	kept := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "frontseat", "local")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "server"), []byte("https://another.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	laid, err := os.ReadFile(filepath.Join(kept, "token"))
	if err != nil || string(laid) != "the owner's token\n" {
		t.Fatalf("the token laid out is %q, %v", laid, err)
	}
	for at, want := range map[string]os.FileMode{kept: 0o700, filepath.Join(kept, "token"): 0o600} {
		if info, err := os.Stat(at); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s is %v, want %v alone: %v", at, info.Mode().Perm(), want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(kept, "server")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("what named another server is left beside the stack's token: %v", err)
	}
}

// A relative XDG_CONFIG_HOME is ignored, as the specification says and
// the command line ignores it: the token is laid out beneath the home's
// .config, where the command line reads it, and nothing beneath the
// relative directory. The control is an absolute one, which is used.
func TestARelativeConfigurationDirectoryIsIgnoredAsTheCommandLineIgnoresIt(t *testing.T) {
	_, s, _ := stackOf(t, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "frontseat", "local", "token")); err != nil {
		t.Errorf("the token is not where the command line reads it: %v", err)
	}
	if _, err := os.Stat("relative"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stack laid out beneath a relative XDG_CONFIG_HOME: %v", err)
	}
	absolute := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", absolute)
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(absolute, "frontseat", "local", "token")); err != nil {
		t.Errorf("the control: the token is not beneath an absolute XDG_CONFIG_HOME: %v", err)
	}
}

// Down brings the stack down by its name, from any checkout, and keeps
// its volume.
func TestDownRemovesTheContainerAndKeepsItsVolume(t *testing.T) {
	r, s, _ := stackOf(t, "/elsewhere")
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rm"}; !slices.Equal(r.done, want) {
		t.Fatalf("down did %q, want %q", r.done, want)
	}
}
