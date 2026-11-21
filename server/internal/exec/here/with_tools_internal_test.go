package here

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A fix's environment is its own, with what its check's tools and
// repository set and without what the repository unsets; a tool that is
// not on the path it is given is refused, beside a control that is.
func TestAFixIsGivenWhatItsCheckWasGivenAndNothingItUnsets(t *testing.T) {
	install := filepath.Join(t.TempDir(), "installs", "go")
	if err := os.MkdirAll(filepath.Join(install, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	installs := map[string]string{"go": install}
	own := []string{"PATH=/bin", "HOME=/h", "GONE=inherited"}
	got, err := withTools(own, []string{"PATH=" + filepath.Join(install, "bin") + ":/bin", "SET=set", "GONE"}, installs)
	if err != nil {
		t.Fatalf("the control was refused: %v", err)
	}
	for _, want := range []string{"HOME=/h", "SET=set", "PATH=" + filepath.Join(install, "bin") + ":/bin"} {
		if !slices.Contains(got, want) {
			t.Errorf("the fix's environment %v lacks %s", got, want)
		}
	}
	if slices.ContainsFunc(got, func(line string) bool { return line == "GONE=inherited" || line == "GONE" }) {
		t.Errorf("what the repository unsets is set: %v", got)
	}
	if _, err := withTools(own, []string{"PATH=/bin"}, installs); err == nil {
		t.Error("a tool that is not on the fix's path was taken")
	}
}
