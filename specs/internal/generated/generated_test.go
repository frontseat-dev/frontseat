package generated_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/specs/internal/generated"
)

// A template writes beneath the buf workspace or beside it in the
// repository, and nowhere outside the repository.
func TestATemplateWritesWithinTheRepository(t *testing.T) {
	template := filepath.Join(t.TempDir(), generated.Contracts, "buf.gen.yaml")
	write := func(out string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(template), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(template, []byte("version: v2\nplugins:\n  - local: protoc-gen-go\n    out: "+out+"\n  - local: protoc-gen-connect-go\n    out: "+out+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for out, want := range map[string]string{"gen/go": filepath.Join("api", "gen", "go"), "../sdk/go/api": filepath.Join("sdk", "go", "api")} {
		write(out)
		if got, err := generated.Outputs(template); err != nil || !slices.Equal(got, []string{want}) {
			t.Errorf("%s: %v, %v; want %s", out, got, err, want)
		}
	}
	for _, out := range []string{"../../elsewhere", "..", "/tmp/out", "''"} {
		write(out)
		if got, err := generated.Outputs(template); err == nil {
			t.Errorf("%s was taken as %v", out, got)
		}
	}
}
