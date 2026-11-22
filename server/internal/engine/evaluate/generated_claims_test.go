package evaluate

import (
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"testing"
)

func TestAGeneratedClaimInvalidatesAMissingFileRead(t *testing.T) {
	src := &counted{files: map[string]string{}}
	original := repositoryView{source: src}
	recorder := &pluginhost.Recorder{Source: original}
	if _, found, err := recorder.Read(t.Context(), "gen/optional.pb.go"); err != nil || found {
		t.Fatalf("missing control: %v %v", found, err)
	}
	if same, err := pluginhost.Same(t.Context(), original, recorder.Reads()); err != nil || !same {
		t.Fatalf("unchanged absence control: %v %v", same, err)
	}
	view := repositoryView{source: generatedView{Source: src, generated: []plan.GeneratedSource{{Directory: "gen", Patterns: []string{"**/*.pb.go"}}}}}
	if _, _, err := view.Read(t.Context(), "gen/optional.pb.go"); err == nil {
		t.Fatal("direct forbidden-read control unexpectedly allowed")
	}
	if same, err := pluginhost.Same(t.Context(), view, recorder.Reads()); err == nil && same {
		t.Fatal("cache replay accepts a missing-file observation whose direct read is now forbidden")
	}
}

func TestGeneratedAuthorityCannotCoverEntityConfiguration(t *testing.T) {
	src := &counted{files: map[string]string{"frontseat.yaml": "plugins: {}\n", "gen/catalog-info.yaml": componentFile}}
	configured, _, err := new(Answers).said(t.Context(), repositoryView{source: src})
	if err != nil || len(configured.Actions()) != 1 {
		t.Fatalf("configuration control: %v, %v", configured, err)
	}
	for _, c := range []struct {
		dir, pattern string
		refuse       bool
	}{
		{".", "frontseat.yaml", true},
		{"gen", "catalog-info.yaml", true},
		{"gen", "**/*.pb.go", false},
	} {
		t.Run(c.pattern, func(t *testing.T) {
			out := Evaluation{Generated: []plan.GeneratedSource{{Directory: c.dir, Patterns: []string{c.pattern}}}}
			err := checkGenerationDiscovery(t.Context(), src, out)
			if c.refuse && err == nil {
				t.Fatal("accepted generated authority over committed configuration already used by evaluation")
			}
			if !c.refuse && err != nil {
				t.Fatal(err)
			}
		})
	}
}
