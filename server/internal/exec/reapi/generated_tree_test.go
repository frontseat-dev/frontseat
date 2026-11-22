package reapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/digest"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

func TestAGeneratedDirectoryListingRefusesMalformedContent(t *testing.T) {
	cases := map[string]*repb.Directory{
		"traversal":            {Files: []*repb.FileNode{file("../escape", "x", false)}},
		"duplicate":            {Files: []*repb.FileNode{file("x", "x", false), file("x", "y", false)}},
		"cross kind duplicate": {Files: []*repb.FileNode{file("x", "x", false)}, Directories: []*repb.DirectoryNode{{Name: "x", Digest: digest.NewFromBlob(nil).ToProto()}}},
		"link":                 {Symlinks: []*repb.SymlinkNode{{Name: "escape", Target: "../../outside"}}},
		"missing digest":       {Files: []*repb.FileNode{{Name: "x"}}},
		"missing child digest": {Directories: []*repb.DirectoryNode{{Name: "x"}}},
	}
	for name, directory := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("malformed CAS directory panicked: %v", p)
				}
			}()
			h := heldDirectories{}
			root := h.put(directory)
			if _, err := list(t.Context(), h.store(), root); err == nil {
				t.Fatal("accepted malformed generated directory")
			}
		})
	}
	t.Run("valid file mode and repeated content", func(t *testing.T) {
		h := heldDirectories{}
		leaf := h.put(&repb.Directory{Files: []*repb.FileNode{file("main.go", "bytes", true)}})
		root := h.put(&repb.Directory{Directories: []*repb.DirectoryNode{{Name: "a", Digest: leaf.ToProto()}, {Name: "b", Digest: leaf.ToProto()}}})
		got, err := list(t.Context(), h.store(), root)
		if err != nil || len(got) != 2 || !got["a/main.go"].Executable || got["a/main.go"] != got["b/main.go"] {
			t.Fatalf("valid listing: %v %v", got, err)
		}
	})
	t.Run("missing metadata", func(t *testing.T) {
		if _, err := list(t.Context(), heldDirectories{}.store(), digest.NewFromBlob([]byte("missing"))); !errors.Is(err, exec.ErrNotKept) {
			t.Fatalf("lost missing-CAS signal: %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		h := heldDirectories{}
		root := h.put(&repb.Directory{})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := list(ctx, h.store(), root); !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	})
	for _, depth := range []int{128, 129} {
		t.Run(fmt.Sprintf("depth %d", depth), func(t *testing.T) {
			h := heldDirectories{}
			root := h.put(&repb.Directory{Files: []*repb.FileNode{file("a.pb.go", "bytes", false)}})
			for range depth {
				root = h.put(&repb.Directory{Directories: []*repb.DirectoryNode{{Name: "d", Digest: root.ToProto()}}})
			}
			got, err := list(t.Context(), h.store(), root)
			if depth == 128 && (err != nil || len(got) != 1) {
				t.Fatalf("allowed depth control: %v %v", got, err)
			}
			if depth == 129 && err == nil {
				t.Fatal("accepted generated directory beyond depth limit")
			}
		})
	}
	t.Run("repeated DAG expansion is bounded", func(t *testing.T) {
		h := heldDirectories{}
		root := h.put(&repb.Directory{Files: []*repb.FileNode{file("a.pb.go", "bytes", false)}})
		for range 17 {
			root = h.put(&repb.Directory{Directories: []*repb.DirectoryNode{{Name: "a", Digest: root.ToProto()}, {Name: "b", Digest: root.ToProto()}}})
		}
		if _, err := list(t.Context(), h.store(), root); err == nil {
			t.Fatal("accepted DAG expansion beyond node limit")
		}
	})
}

// A tool's tree is walked with the links it has within itself; one that
// leads out of the tree is refused, as a listing refuses every link.
func TestATreeIsWalkedWithItsLinksAndRefusesOneThatLeaves(t *testing.T) {
	h := heldDirectories{}
	leaf := h.put(&repb.Directory{Files: []*repb.FileNode{file("gofmt", "bytes", true)}})
	root := h.put(&repb.Directory{
		Directories: []*repb.DirectoryNode{{Name: "1.27.3", Digest: leaf.ToProto()}},
		Symlinks:    []*repb.SymlinkNode{{Name: "1.27", Target: "1.27.3"}},
	})
	files, links, dirs, err := tree(t.Context(), h.store(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !files["1.27.3/gofmt"].Executable || links["1.27"] != "1.27.3" || len(dirs) != 1 || dirs[0] != "1.27.3" {
		t.Fatalf("walked %v %v %v", files, links, dirs)
	}
	for name, directory := range map[string]*repb.Directory{
		"a link out of the tree": {Symlinks: []*repb.SymlinkNode{{Name: "out", Target: "../elsewhere"}}},
		"an absolute link":       {Symlinks: []*repb.SymlinkNode{{Name: "abs", Target: "/usr/bin/true"}}},
		"a link to nowhere":      {Symlinks: []*repb.SymlinkNode{{Name: "empty", Target: ""}}},
		"a malformed name":       {Files: []*repb.FileNode{file("../escape", "x", false)}},
	} {
		h := heldDirectories{}
		if _, _, _, err := tree(t.Context(), h.store(), h.put(directory)); err == nil {
			t.Errorf("%s was accepted in a tree", name)
		}
	}
}

// A materialized tree's links lead within it on the disk, however they
// chain: a chain whose every link looks local but that leads out is
// refused with the tree.
func TestAMaterializedTreesLinksLeadWithinIt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// d leads to the root, e through d back into bin, fine to bin; gofmt
	// looks local link by link and leads out through them.
	for at, to := range map[string]string{"bin/d": "..", "bin/e": "d/bin", "bin/gofmt": "e/" + strings.Repeat("../", 24) + "usr/bin/true", "bin/fine": "../bin"} {
		if err := os.Symlink(filepath.FromSlash(to), filepath.Join(root, filepath.FromSlash(at))); err != nil {
			t.Fatal(err)
		}
	}
	err := linksWithin(root, map[string]string{"bin/d": "..", "bin/e": "d/bin", "bin/gofmt": "x", "bin/fine": "../bin"})
	if err == nil || !strings.Contains(err.Error(), "bin/gofmt") {
		t.Fatalf("a chain of links leading out of the tree was accepted: %v", err)
	}
	if err := linksWithin(root, map[string]string{"bin/d": "..", "bin/e": "d/bin", "bin/fine": "../bin"}); err != nil {
		t.Fatalf("links within the tree were refused: %v", err)
	}
}
