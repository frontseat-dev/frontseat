package reapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/digest"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// held is directories in memory, by digest.
type heldDirectories map[digest.Digest]*repb.Directory

func (h heldDirectories) store() directoriesOf {
	return directoriesOf{
		read: func(_ context.Context, at digest.Digest) (*repb.Directory, error) {
			if found, there := h[at]; there {
				return found, nil
			}
			return nil, exec.ErrNotKept
		},
		write: func(_ context.Context, directory *repb.Directory) (digest.Digest, error) {
			return h.put(directory), nil
		},
	}
}

func (h heldDirectories) put(directory *repb.Directory) digest.Digest {
	at, err := digest.NewFromMessage(directory)
	if err != nil {
		panic(err)
	}
	h[at] = directory
	return at
}

func file(name, content string, executable bool) *repb.FileNode {
	return &repb.FileNode{Name: name, Digest: digest.NewFromBlob([]byte(content)).ToProto(), IsExecutable: executable}
}

func TestDirectoriesAreMergedOrSayWhereTheyClash(t *testing.T) {
	h := heldDirectories{}
	child := func(name string, directory *repb.Directory) *repb.DirectoryNode {
		return &repb.DirectoryNode{Name: name, Digest: h.put(directory).ToProto()}
	}
	link := func(name, target string) *repb.SymlinkNode { return &repb.SymlinkNode{Name: name, Target: target} }
	for name, c := range map[string]struct {
		a, b  *repb.Directory
		want  *repb.Directory
		clash string
	}{
		"each holds its own": {
			a:    &repb.Directory{Files: []*repb.FileNode{file("c", "c", true), file("a", "a", true)}},
			b:    &repb.Directory{Files: []*repb.FileNode{file("b", "b", true)}},
			want: &repb.Directory{Files: []*repb.FileNode{file("a", "a", true), file("b", "b", true), file("c", "c", true)}},
		},
		"a file the same in both": {
			a:    &repb.Directory{Files: []*repb.FileNode{file("a", "a", true), file("b", "b", false)}},
			b:    &repb.Directory{Files: []*repb.FileNode{file("a", "a", true), file("c", "c", false)}},
			want: &repb.Directory{Files: []*repb.FileNode{file("a", "a", true), file("b", "b", false), file("c", "c", false)}},
		},
		"directories beneath": {
			a:    &repb.Directory{Directories: []*repb.DirectoryNode{child("d", &repb.Directory{Files: []*repb.FileNode{file("a", "a", false)}})}},
			b:    &repb.Directory{Directories: []*repb.DirectoryNode{child("d", &repb.Directory{Files: []*repb.FileNode{file("b", "b", false)}}), child("e", &repb.Directory{})}},
			want: &repb.Directory{Directories: []*repb.DirectoryNode{child("d", &repb.Directory{Files: []*repb.FileNode{file("a", "a", false), file("b", "b", false)}}), child("e", &repb.Directory{})}},
		},
		"links, the same and not": {
			a:    &repb.Directory{Symlinks: []*repb.SymlinkNode{link("l", "t")}},
			b:    &repb.Directory{Symlinks: []*repb.SymlinkNode{link("l", "t"), link("m", "t")}},
			want: &repb.Directory{Symlinks: []*repb.SymlinkNode{link("l", "t"), link("m", "t")}},
		},
		"in no order at all": {
			a:    &repb.Directory{Directories: []*repb.DirectoryNode{child("z", &repb.Directory{})}, Symlinks: []*repb.SymlinkNode{link("y", "t")}},
			b:    &repb.Directory{Directories: []*repb.DirectoryNode{child("a", &repb.Directory{})}, Symlinks: []*repb.SymlinkNode{link("b", "t")}},
			want: &repb.Directory{Directories: []*repb.DirectoryNode{child("a", &repb.Directory{}), child("z", &repb.Directory{})}, Symlinks: []*repb.SymlinkNode{link("b", "t"), link("y", "t")}},
		},
		"a file that differs": {
			a:     &repb.Directory{Files: []*repb.FileNode{file("a", "one", true)}},
			b:     &repb.Directory{Files: []*repb.FileNode{file("a", "two", true)}},
			clash: "a",
		},
		"a file that may be run in one alone": {
			a:     &repb.Directory{Files: []*repb.FileNode{file("a", "a", true)}},
			b:     &repb.Directory{Files: []*repb.FileNode{file("a", "a", false)}},
			clash: "a",
		},
		"a file that differs, beneath": {
			a:     &repb.Directory{Directories: []*repb.DirectoryNode{child("d", &repb.Directory{Directories: []*repb.DirectoryNode{child("e", &repb.Directory{Files: []*repb.FileNode{file("a", "one", false)}})}})}},
			b:     &repb.Directory{Directories: []*repb.DirectoryNode{child("d", &repb.Directory{Directories: []*repb.DirectoryNode{child("e", &repb.Directory{Files: []*repb.FileNode{file("a", "two", false)}})}})}},
			clash: "d/e/a",
		},
		"a file where the other has a directory": {
			a:     &repb.Directory{Files: []*repb.FileNode{file("a", "a", false)}},
			b:     &repb.Directory{Directories: []*repb.DirectoryNode{child("a", &repb.Directory{})}},
			clash: "a",
		},
		"a directory where the other has a file": {
			a:     &repb.Directory{Directories: []*repb.DirectoryNode{child("a", &repb.Directory{})}},
			b:     &repb.Directory{Files: []*repb.FileNode{file("a", "a", false)}},
			clash: "a",
		},
		"a link where the other has a file": {
			a:     &repb.Directory{Files: []*repb.FileNode{file("a", "a", false)}},
			b:     &repb.Directory{Symlinks: []*repb.SymlinkNode{link("a", "t")}},
			clash: "a",
		},
		"links that lead elsewhere": {
			a:     &repb.Directory{Symlinks: []*repb.SymlinkNode{link("l", "t")}},
			b:     &repb.Directory{Symlinks: []*repb.SymlinkNode{link("l", "u")}},
			clash: "l",
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, b := h.put(c.a), h.put(c.b)
			// Either way round: the same directory, or the same clash.
			for _, order := range [][2]digest.Digest{{a, b}, {b, a}} {
				got, err := merge(context.Background(), h.store(), []source{{at: order[0], index: 0}, {at: order[1], index: 1}}, "")
				if c.clash != "" {
					var clash *exec.Clash
					if !errors.As(err, &clash) || clash.Path != c.clash || clash.First != 0 || clash.Second != 1 {
						t.Errorf("merged to %v, %v; want a clash of the two at %s", got, err, c.clash)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if got != h.put(c.want) {
					t.Errorf("merged to %v, want %v", prototext(h[got]), prototext(c.want))
				}
			}
		})
	}
}

// Many directories are read once each and written once, in whatever
// order they come, and a clash names the two that hold it, of however
// many.
func TestManyDirectoriesAreMergedAtOnce(t *testing.T) {
	h := heldDirectories{}
	var sources []source
	want := &repb.Directory{}
	for i := range 100 {
		name := fmt.Sprintf("program%03d", i)
		sources = append(sources, source{at: h.put(&repb.Directory{Files: []*repb.FileNode{file(name, name, true)}}), index: i})
		want.Files = append(want.Files, file(name, name, true))
	}
	reads, writes := 0, 0
	store := h.store()
	counted := directoriesOf{
		read: func(ctx context.Context, at digest.Digest) (*repb.Directory, error) {
			reads++
			return store.read(ctx, at)
		},
		write: func(ctx context.Context, directory *repb.Directory) (digest.Digest, error) {
			writes++
			return store.write(ctx, directory)
		},
	}

	forwards, err := merge(context.Background(), counted, sources, "")

	if err != nil || forwards != h.put(want) {
		t.Fatalf("merged to %v, %v", forwards, err)
	}
	if reads != 100 || writes != 1 {
		t.Errorf("%d reads and %d writes, want each read once and one written", reads, writes)
	}
	slices.Reverse(sources)
	if backwards, err := merge(context.Background(), store, sources, ""); err != nil || backwards != forwards {
		t.Errorf("the other way round merged to %v, %v, want %v", backwards, err, forwards)
	}
	clashing := []source{sources[0], sources[1], {at: h.put(&repb.Directory{Files: []*repb.FileNode{file("other", "other", true)}}), index: 7}, {at: h.put(&repb.Directory{Files: []*repb.FileNode{file("other", "another", true)}}), index: 9}}
	_, err = merge(context.Background(), store, clashing, "beneath")
	var clash *exec.Clash
	if !errors.As(err, &clash) || clash.Path != "beneath/other" || clash.First != 7 || clash.Second != 9 {
		t.Errorf("%v; want a clash at beneath/other of 7 and 9", err)
	}
}

// What a worker's directory says beyond names, digests and whether a
// file may be run makes no difference to what they merge to; a directory
// that lists a name twice, or names a place outside it, is not merged.
func TestWhatADirectoryHoldsIsHeldToBeingOne(t *testing.T) {
	h := heldDirectories{}
	marked := file("a", "a", true)
	marked.NodeProperties = &repb.NodeProperties{UnixMode: wrapperspb.UInt32(0o755)}
	one, two := h.put(&repb.Directory{Files: []*repb.FileNode{marked}, NodeProperties: &repb.NodeProperties{UnixMode: wrapperspb.UInt32(0o700)}}), h.put(&repb.Directory{Files: []*repb.FileNode{file("a", "a", true), file("b", "b", true)}})
	got, err := merge(context.Background(), h.store(), []source{{at: one}, {at: two, index: 1}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if other, err := merge(context.Background(), h.store(), []source{{at: two}, {at: one, index: 1}}, ""); err != nil || other != got {
		t.Errorf("the other way round merged to %v, %v, want %v", other, err, got)
	}
	if got != h.put(&repb.Directory{Files: []*repb.FileNode{file("a", "a", true), file("b", "b", true)}}) {
		t.Errorf("merged to %v, with what is not a name, a digest or whether it may be run", prototext(h[got]))
	}
	for name, bad := range map[string]*repb.Directory{
		"a name twice": {Files: []*repb.FileNode{file("a", "a", true), file("a", "b", true)}},
		"out of it":    {Files: []*repb.FileNode{file("../a", "a", true)}},
		"a path":       {Directories: []*repb.DirectoryNode{{Name: "a/b", Digest: digest.Empty.ToProto()}}},
	} {
		if _, err := merge(context.Background(), h.store(), []source{{at: one}, {at: h.put(bad), index: 1}}, ""); err == nil {
			t.Errorf("%s: merged", name)
		}
	}
}

func prototext(m proto.Message) string { return m.(interface{ String() string }).String() }

// A directory the grid has let go is said to be so, and the run starts
// over as it does for anything else it let go. One directory merged with
// itself asks the grid nothing.
func TestAMergeOfWhatIsNotKeptSaysSo(t *testing.T) {
	h := heldDirectories{}
	there := h.put(&repb.Directory{Files: []*repb.FileNode{file("a", "a", false)}})
	gone := digest.NewFromBlob([]byte("gone"))

	if _, err := merge(context.Background(), h.store(), []source{{at: there}, {at: gone, index: 1}}, ""); !errors.Is(err, exec.ErrNotKept) {
		t.Errorf("merging with what is gone: %v", err)
	}
	if got, err := merge(context.Background(), h.store(), []source{{at: gone}, {at: gone, index: 1}}, ""); err != nil || got != gone {
		t.Errorf("merging what is gone with itself: %v, %v", got, err)
	}
}
