package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plugin's name is checked before anything is built.
func TestAPluginNamedAsAPathIsRefusedBeforeAnyWork(t *testing.T) {
	_, err := Build{Root: t.TempDir(), Tag: "frontseat-box:probe", Architecture: "arm64", Plugins: map[string][]byte{"../evil": nil}}.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "a name is a word, not a path") {
		t.Fatalf("a plugin named as a path was not refused first: %v", err)
	}
}

// A box's tag is of what it holds: the image id's first twelve hex
// digits, and nothing that is not an image id is taken for one.
func TestABoxIsTaggedByItsImageID(t *testing.T) {
	tag, err := Of("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil || tag != "frontseat-box:0123456789ab" {
		t.Fatalf("Of = %q, %v; want frontseat-box:0123456789ab", tag, err)
	}
	for _, id := range []string{"", "frontseat-box:local", "sha256:0123", "sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef", "sha512:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"} {
		if tag, err := Of(id); err == nil {
			t.Errorf("Of(%q) = %q; want it refused", id, tag)
		}
	}
}

// A context is named by what it holds: the same files keep one name, and
// a file of the same size and time with other content, or another name,
// gives another.
func TestAContextIsNamedByWhatItHolds(t *testing.T) {
	laid := func(name string, content []byte) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "context")
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
			t.Fatal(err)
		}
		at := filepath.Join(dir, "plugins", name)
		if err := os.WriteFile(at, content, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(at, built, built); err != nil {
			t.Fatal(err)
		}
		named, err := byContent(dir)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Base(named)
	}
	one := laid("go.wasm", []byte("seventeen"))
	if again := laid("go.wasm", []byte("seventeen")); again != one {
		t.Errorf("the same content is named %s and %s", one, again)
	}
	for what, other := range map[string]string{
		"other content of the same size": laid("go.wasm", []byte("eighteen!")),
		"another file's name":            laid("cargo.wasm", []byte("seventeen")),
	} {
		if other == one {
			t.Errorf("%s is named as the first, %s", what, one)
		}
	}
}
