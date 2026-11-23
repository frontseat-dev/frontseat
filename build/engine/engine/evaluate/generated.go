package evaluate

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/build/engine/engine/catalog"
	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
	"github.com/frontseat-dev/frontseat/build/engine/engine/graph"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/build/engine/pins"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// generatedView keeps generated authority out of source analysis and replay.
// It never reads worker outputs; this contract requires committed metadata.
type generatedView struct {
	Source
	generated []plan.GeneratedSource
}

func (v generatedView) covered(repository, file string) bool {
	for _, claim := range v.generated {
		if claim.Covers(repository, file) {
			return true
		}
	}
	return false
}

func (v generatedView) Read(ctx context.Context, repository, file string) ([]byte, bool, error) {
	if v.covered(repository, file) {
		return nil, false, &model.Mistake{Repository: repository, File: file, Message: "analysis cannot read generated source; its metadata must come from committed manifests"}
	}
	return v.Source.Read(ctx, repository, file)
}

func (v generatedView) Digest(ctx context.Context, repository, file string) (string, bool, error) {
	if v.covered(repository, file) {
		_, _, err := v.Read(ctx, repository, file)
		return "", false, err
	}
	return v.Source.Digest(ctx, repository, file)
}

func (v generatedView) Within(ctx context.Context, repository, directory string, patterns []string) ([]string, error) {
	files, err := v.Source.Within(ctx, repository, directory, patterns)
	if err != nil {
		return nil, err
	}
	kept := make([]string, 0, len(files))
	for _, file := range files {
		if !v.covered(repository, file) {
			kept = append(kept, file)
		}
	}
	return kept, nil
}

type observedGeneration struct {
	repository, plugin string
	reads              []pluginhost.Read
}

// checkGenerationDiscovery refuses claims over committed metadata and producer
// observations of generated content. It runs again on cached answers.
func checkGenerationDiscovery(ctx context.Context, src Source, out Evaluation) error {
	catalogs := map[string][]string{}
	for _, claim := range out.Generated {
		protected := append([]string{config.File}, pins.Files...)
		files, found := catalogs[claim.Repository]
		if !found {
			var err error
			files, err = src.Within(ctx, claim.Repository, "", []string{catalog.File, "**/" + catalog.File})
			if err != nil {
				return err
			}
			catalogs[claim.Repository] = files
		}
		protected = append(protected, files...)
		for _, project := range out.Projects {
			if project.Repository == claim.Repository {
				protected = append(protected, project.Manifest)
			}
		}
		for _, file := range protected {
			if claim.Covers(claim.Repository, file) {
				return &model.Mistake{Message: fmt.Sprintf("%s claims committed manifest %s as generated source", claim.Producer, file)}
			}
		}
		for _, observed := range out.producerReads {
			if observed.repository != claim.Repository {
				continue
			}
			for _, read := range observed.reads {
				files := []string{read.Argument}
				if read.Import == "glob" {
					var err error
					files, err = src.Within(ctx, observed.repository, read.Directory, strings.Split(read.Argument, "\x00"))
					if err != nil {
						return err
					}
				}
				for _, file := range files {
					if claim.Covers(observed.repository, file) {
						return &model.Mistake{Message: fmt.Sprintf("plugin %s discovers generation from generated source %s", observed.plugin, file)}
					}
				}
			}
		}
	}
	return nil
}

// consented holds every generated source that goes outside its producer's
// project to what the catalog says: the Component that owns the place it
// goes to must say, in its own catalog file, that the Component owning
// the producer's project generates it (spec.frontseat.generated). A
// claim hides the checkout's files there from every action and is what
// `build --fix` writes there, so a template in one Component is never
// enough to take a directory of another. Each consent is held first to
// be its own Component's to give: a directory of a Component nested in
// it is that Component's.
func consented(out Evaluation, said map[string][]config.Generated) error {
	for _, repository := range slices.Sorted(maps.Keys(said)) {
		for _, consent := range said[repository] {
			owner, found := graph.Owner(out.Entities, path.Join(repository, consent.Directory))
			if !found || owner.Repository != repository || owner.Ref != consent.Entity {
				whose := "no Component's"
				if found {
					whose = owner.Ref.String() + "'s"
				}
				return &model.Mistake{Repository: consent.Repository, File: consent.File, Message: fmt.Sprintf("%s says that %s generates %s, which is not its own directory but %s", consent.Entity, consent.By, path.Join(repository, consent.Directory), whose)}
			}
		}
	}
	for _, claim := range out.Generated {
		at := path.Join(claim.Repository, claim.Directory)
		help := "Generate beneath the project, or have the Component that owns the directory say in its catalog file that the producer's Component generates it: spec.frontseat.generated: [{directory: <relative to that file>, by: component:<name>}]."
		owner, owned := graph.Owner(out.Entities, at)
		producer, produced := graph.Owner(out.Entities, path.Join(claim.Repository, claim.ProjectRoot))
		// No Component beneath the claim but its owner's: a claim over
		// a nested Component's directory would hide and rewrite its files
		// on its owner's word alone.
		for _, entity := range out.Entities {
			// The whole of the primary repository ("." ) holds every
			// Component of it but the one at its root.
			inner := path.Clean(entity.Path())
			if entity.Ref != owner.Ref && inner != at && (at == "." || strings.HasPrefix(inner, at+"/")) {
				return &model.Mistake{Repository: claim.Repository, Rule: "catalog/generated-without-consent", Reason: "GENERATED_WITHOUT_CONSENT", Message: fmt.Sprintf("%s generates %s, which holds %s at %s: a claim never covers another Component's directory", claim.Producer, at, entity.Ref, inner), Help: help}
			}
		}
		// Within its own project and its own Component (or where there
		// are no Components to ask), a generator needs no one's word.
		root := path.Clean(claim.ProjectRoot)
		inProject := root == "." || claim.Directory == root || strings.HasPrefix(claim.Directory, root+"/")
		if inProject && owned == produced && (!owned || owner.Ref == producer.Ref) {
			continue
		}
		if !owned || owner.Repository != claim.Repository || owner.Ref.Kind != entity.KindComponent {
			return &model.Mistake{Repository: claim.Repository, Rule: "catalog/generated-without-consent", Reason: "GENERATED_WITHOUT_CONSENT", Message: fmt.Sprintf("%s generates %s, outside its Component, which no Component owns to say it may", claim.Producer, at), Help: help}
		}
		if !produced || producer.Repository != claim.Repository || producer.Ref.Kind != entity.KindComponent {
			return &model.Mistake{Repository: claim.Repository, Rule: "catalog/generated-without-consent", Reason: "GENERATED_WITHOUT_CONSENT", Message: fmt.Sprintf("%s generates %s, outside its project, and its project belongs to no Component that %s could say generates it", claim.Producer, at, owner.Ref), Help: help}
		}
		given := false
		for _, consent := range said[claim.Repository] {
			if consent.Directory == claim.Directory && consent.Entity == owner.Ref && consent.By == producer.Ref {
				given = true
				break
			}
		}
		if !given {
			return &model.Mistake{Repository: claim.Repository, Rule: "catalog/generated-without-consent", Reason: "GENERATED_WITHOUT_CONSENT", Message: fmt.Sprintf("%s generates %s, outside its own Component, and %s does not say that %s generates it (spec.frontseat.generated in its catalog file)", claim.Producer, at, owner.Ref, producer.Ref), Help: help}
		}
	}
	return nil
}
