package evaluate

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/model"
)

// What a catalog file says is read is read as it is written: a pattern
// of source, held to what a plugin's is, and what another action left,
// each in the order written, beneath the directory it is written in. An
// input set is a project's, and a catalog file declares none. A mistake
// names the file and what was being read, once.
func TestCatalogInputsAreSourceOrWhatAnotherActionLeft(t *testing.T) {
	read, err := readCatalogInputs("lab", "chain", "chain/catalog-info.yaml", "the action report", []string{"src/**", ":go.fetch", "!src/skip/**", "//lib:made/"})
	if err != nil {
		t.Fatal(err)
	}
	if read.source.Directory != "chain" || !slices.Equal(read.source.Patterns, []string{"src/**", "!src/skip/**"}) {
		t.Errorf("source read as %+v", read.source)
	}
	var written []string
	for _, reference := range read.references {
		if reference.Repository != "lab" || reference.File != "chain/catalog-info.yaml" || reference.Plugin != "" {
			t.Errorf("a reference of a catalog file read as %+v", reference)
		}
		written = append(written, reference.Path+" "+reference.Written)
	}
	if !slices.Equal(written, []string{"lab/chain :go.fetch", "lab/lib //lib:made/"}) {
		t.Errorf("references read as %v", written)
	}
	if read, err := readCatalogInputs("lab", "chain", "chain/catalog-info.yaml", "the action report", nil); err != nil || read.source.Patterns != nil || read.references != nil {
		t.Errorf("nothing written read as %+v, %v", read, err)
	}
	for input, says := range map[string]string{
		"../go.mod":       `the action report: its input 2 is not a pattern of files beneath the project`,
		"/../etc/passwd":  `the action report: its input 2 is not a pattern of files beneath the repository's root`,
		"/":               `the action report: its input 2 is not a pattern of files beneath the repository's root`,
		"src/[":           `the action report: its input 2 is not a pattern of files beneath the project`,
		"":                `the action report: its input 2 is not a pattern of files beneath the project`,
		"!":               `the action report: its input 2 is not a pattern of files beneath the project`,
		"=sources":        `the action report: its input 2 names an input set, which a catalog file declares none of`,
		":":               `the action report: the input ":" names nothing after the colon`,
		"//lab/chain":     `the action report: the input "//lab/chain" names a project and nothing of it`,
		"://lab/lib:made": `the action report: the input "://lab/lib:made" names another project after a colon`,
	} {
		_, err := readCatalogInputs("lab", "chain", "chain/catalog-info.yaml", "the action report", []string{"src/**", input})
		var mistake *model.Mistake
		if !errors.As(err, &mistake) || mistake.Where() != "lab/chain/catalog-info.yaml" || !strings.HasPrefix(mistake.Message, says) {
			t.Errorf("%q: %v, want %q of the catalog file", input, err, says)
		}
	}
}

// A pattern written from the repository's root is read from there, a
// fixture of another Component or the contracts; the control is one
// written from the catalog file's directory, which stays beneath it.
func TestACatalogInputFromTheRootIsReadFromTheRepositorysRoot(t *testing.T) {
	read, err := readCatalogInputs("lab", "chain", "chain/catalog-info.yaml", "test.inputs", []string{"fixtures/**", "/api/**/*.proto", "!/api/internal/**"})
	if err != nil {
		t.Fatal(err)
	}
	groups := read.groups()
	if len(groups) != 2 || groups[0].Directory != "chain" || !slices.Equal(groups[0].Patterns, []string{"fixtures/**"}) ||
		groups[1].Directory != "" || !slices.Equal(groups[1].Patterns, []string{"api/**/*.proto", "!api/internal/**"}) {
		t.Fatalf("read as %+v", groups)
	}
	if !groups[1].Accepts("api/build-proto/frontseat/build/v1alpha1/build.proto") || groups[1].Accepts("api/internal/x.proto") || groups[0].Accepts("api/build-proto/x.proto") {
		t.Fatal("a pattern from the root selects what it says, or one from the directory reaches beyond it")
	}
}

// A label written from the root, //path:what, names a project of the
// repository the catalog file is in, wherever the workspace holds that
// repository, as a pattern written /… is read from its root and as
// Bazel reads //… within the repository it is written in. The control
// is the repository at the workspace's top, where the label is the
// project's path as written.
func TestACatalogLabelFromTheRootNamesAProjectOfItsOwnRepository(t *testing.T) {
	for repository, want := range map[string]string{"frontseat": "frontseat/plugins/apko", "": "plugins/apko"} {
		read, err := readCatalogInputs(repository, "server", "server/catalog-info.yaml", "test.inputs", []string{"//plugins/apko:cargo.package/wasm32-wasip1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(read.references) != 1 || read.references[0].Path != want || read.references[0].Rest != "cargo.package/wasm32-wasip1" || read.references[0].Written != "//plugins/apko:cargo.package/wasm32-wasip1" {
			t.Errorf("in the repository %q, read as %+v, want the project %s", repository, read.references, want)
		}
	}
}
