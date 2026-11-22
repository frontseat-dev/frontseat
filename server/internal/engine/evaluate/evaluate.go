package evaluate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"runtime"
	"slices"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/internal/yamlnode"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"github.com/frontseat-dev/frontseat/server/internal/pins"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/internal/wire"
)

// Evaluation is what there is to build in a workspace.
type Evaluation struct {
	// Actions are its actions, in the order the repositories, their
	// plugins and the plugins' projects gave them, and after those the
	// host's own.
	Actions []plan.Action
	// Projects are its projects, each with the options it is built with.
	Projects []graph.Project
	// Generated sources are discovered before consumer analysis.
	Generated     []plan.GeneratedSource
	producerReads []observedGeneration
	// Entities are the entities the workspace holds, each with where it
	// lives.
	Entities []record.Located
	// Catalog is the graph of catalog facts, separate from source ownership.
	Catalog     *catalog.Graph
	descriptors []catalog.Descriptor
	// Graph says which project provides what each requires.
	Graph *graph.Graph
	// Releases are what entities say of how they are released, and
	// Listed the plugins each repository lists.
	Releases []config.Release
	Listed   map[string][]string
	// DependsOn is what each entity depends on, as the source says.
	DependsOn map[entity.Ref][]entity.Ref
	// Asked is how many times a plugin was asked, and Again how many
	// times what one had answered before was given again in place of
	// asking it.
	Asked, Again int
	// shared are the shared actions taken so far, by label: where each
	// is in Actions and which project declared it first.
	shared map[model.Label]sharedAt
}

// sharedAt is a shared action as it was first declared.
type sharedAt struct {
	index int
	root  string
}

// share takes a shared action a project declared. The first project to
// declare it gives it to the run; another that declares it alike shares
// it, and one that declares it differently is refused, naming both: the
// two lock one thing differently, and one action cannot be both. It is
// given no project's context: it belongs to none.
func (out *Evaluation) share(repository, plugin, root string, declared *pluginv1.Action) error {
	label := model.Label{Path: repository, Plugin: plugin, Name: declared.GetName()}
	// Its directory is its repository's, where the repository's tool
	// pins are staged: a file of its own there would stand in their place.
	for _, file := range declared.GetSharedFetch().GetFiles() {
		if slices.Contains(pins.Files, path.Clean(file.GetPath())) {
			return &model.Mistake{Message: fmt.Sprintf("plugin %s: %s is given %s, which is where the repository's tool pins are", plugin, label, path.Clean(file.GetPath()))}
		}
	}
	if first, taken := out.shared[label]; taken {
		if proto.Equal(out.Actions[first.index].Declared, declared) {
			return nil
		}
		roots := []string{path.Join(repository, first.root), path.Join(repository, root)}
		sort.Strings(roots)
		return &model.Mistake{Message: fmt.Sprintf("%s is declared differently by the projects at %q and %q: they lock it differently, and one fetch cannot be both", label, roots[0], roots[1])}
	}
	if out.shared == nil {
		out.shared = map[model.Label]sharedAt{}
	}
	out.shared[label] = sharedAt{index: len(out.Actions), root: root}
	out.Actions = append(out.Actions, plan.Action{
		Label:      label,
		Repository: repository,
		Declared:   declared,
	})
	return nil
}

// count counts a plugin's answer: one it gave now, or one given again.
func (e *Evaluation) count(again bool) {
	if again {
		e.Again++
		return
	}
	e.Asked++
}

// Evaluate asks, of each repository, the plugins it lists what there is
// to build in it. A plugin is asked about each directory where a file
// matches its interests, with the options that hold for that directory.
//
// Each repository is read on its own: what one says of how it is built
// does not reach another. The profile is the way the source is to be
// built; a plugin declares the commands of that profile.
//
// The entities are those the workspace holds. What each depends on is
// folded from what the plugins found, and an entity's catalog file is
// held to it.
//
// A mistake in what a repository or an entity says, or two projects that
// provide one coordinate, is a *model.Mistake.
func Evaluate(ctx context.Context, src Source, plugins []*pluginhost.Plugin, answers *Answers, profile typev1.Profile, entities []record.Located) (Evaluation, error) {
	return evaluate(ctx, src, plugins, answers, profile, entities)
}

func evaluate(ctx context.Context, src Source, plugins []*pluginhost.Plugin, answers asking, profile typev1.Profile, entities []record.Located) (Evaluation, error) {
	out := Evaluation{Entities: entities, Listed: map[string][]string{}}
	started := map[entity.Ref]config.Service{}
	tests := map[string][]config.TestRuntime{}
	// What each repository's Components say others generate of them.
	consents := map[string][]config.Generated{}
	answers.begin()
	// The tools each repository pins, as a plugin is told of them.
	pinned := map[string]map[string]string{}
	// The labels of the actions catalog files declare: a label is one
	// action's.
	declared := map[model.Label]bool{}
	for _, repository := range src.Repositories() {
		view := repositoryView{source: src, repository: repository}
		configured, pins, err := answers.said(ctx, view)
		if err != nil {
			return out, err
		}
		pinned[repository] = pins
		if runtime := configured.TestRuntimes(); len(runtime) > 0 {
			tests[repository] = runtime
		}
		if said := configured.Generated(); len(said) > 0 {
			consents[repository] = said
		}
		var selected []*pluginhost.Plugin
		for _, name := range configured.Plugins() {
			plugin := pluginhost.Named(plugins, name)
			if plugin == nil {
				return out, &model.Mistake{Repository: repository, File: config.File, Message: fmt.Sprintf("there is no plugin %s", name)}
			}
			if err := configured.Check(name, plugin.Description.GetOptions()); err != nil {
				return out, err
			}
			selected = append(selected, plugin)
		}
		for _, plugin := range selected {
			if !plugin.Description.GetGeneratesSources() {
				continue
			}
			if err := analyze(ctx, view, plugin, answers, configured, profile, pinned[repository], &out); err != nil {
				return out, err
			}
		}
		if err := checkGenerationDiscovery(ctx, src, out); err != nil {
			return out, err
		}
		consumer := repositoryView{source: generatedView{Source: src, generated: out.Generated}, repository: repository}
		for _, plugin := range selected {
			if plugin.Description.GetGeneratesSources() {
				continue
			}
			if err := analyze(ctx, consumer, plugin, answers, configured, profile, pinned[repository], &out); err != nil {
				return out, err
			}
		}
		for _, own := range configured.Actions() {
			action, err := custom(repository, own)
			if err != nil {
				return out, err
			}
			// Two documents that declare one action are refused before
			// anything is resolved: a need would be met by whichever was
			// read last, and one of the two never run.
			if declared[action.Label] {
				return out, &model.Mistake{Repository: own.Repository, File: own.File, Message: fmt.Sprintf("%s is declared by more than one document: an action has one declaration", action.Label)}
			}
			declared[action.Label] = true
			out.Actions = append(out.Actions, action)
		}
		out.Releases = append(out.Releases, configured.Releases()...)
		out.Listed[repository] = configured.Plugins()
		claimed, err := claimants(repository, selected)
		if err != nil {
			return out, err
		}
		for _, written := range configured.Services() {
			written.Repository = repository
			ref := written.Entity
			if other, twice := started[ref]; twice {
				return out, &model.Mistake{Repository: written.Repository, File: written.File, Message: fmt.Sprintf("%s is started twice: here and in %s", ref, path.Join(other.Repository, other.File))}
			}
			// How it is started is what the plugin it names says.
			l := written
			if written.Program == "" {
				l, err = startedAs(ctx, answers, claimed, written, &out)
				if err != nil {
					return out, err
				}
			}
			started[ref] = l
		}
	}
	labels := make(map[model.Label]bool, len(out.Actions))
	if err := distinct(labels, out.Actions); err != nil {
		return out, err
	}
	var err error
	known := make([]catalog.Entity, 0, len(entities))
	for _, entity := range entities {
		known = append(known, catalog.Entity{Ref: entity.Ref, Repository: entity.Repository, Roots: []string{entity.Root}})
	}
	catalogGraph, err := catalog.Assemble(known, out.descriptors)
	if err != nil {
		var conflict *catalog.Conflict
		if errors.As(err, &conflict) {
			rule, reason, help := "catalog/conflicting-ownership", "CONFLICTING_OWNERSHIP", "Place components in distinct source directories and keep each identity in one repository."
			if conflict.Duplicate {
				rule, reason, help = "catalog/duplicate-entity", "DUPLICATE_ENTITY", "Give each catalog entity one descriptor; use distinct namespaces for different entities with the same name."
			}
			err = &model.Mistake{Repository: conflict.Repository, File: conflict.Manifest, Rule: rule, Reason: reason, Message: conflict.Message, Help: help}
		}
		return out, err
	}
	out.Entities = nil
	for _, entity := range catalogGraph.Entities() {
		for _, root := range entity.Roots {
			out.Entities = append(out.Entities, record.Located{Ref: entity.Ref, Repository: entity.Repository, Root: root})
		}
	}
	// A generator writes outside its project only where the Component
	// there says it may, and before anything is handed or bound.
	if err := consented(out, consents); err != nil {
		return out, err
	}
	if out.Graph, err = hand(ctx, generatedView{Source: src, generated: out.Generated}, plugins, answers, profile, pinned, &out); err != nil {
		return out, err
	}
	// Consumer manifests and catalog configuration are protected too, including
	// declarations synthesized without a direct read of their manifest.
	if err := checkGenerationDiscovery(ctx, src, out); err != nil {
		return out, err
	}
	// What each action reads of others is what it needs: resolved among
	// all of them, before generated source adds what its inputs select.
	if err := testRuntimes(&out, tests, pinned); err != nil {
		return out, err
	}
	if err := plan.Resolve(out.Actions); err != nil {
		return out, err
	}
	if err := plan.BindProvided(out.Actions); err != nil {
		return out, err
	}
	if err := plan.BindGenerated(out.Actions, out.Generated); err != nil {
		return out, err
	}
	out.DependsOn = dependsOn(out.Graph, out.Projects, out.Entities, out.Actions)
	out.Catalog = catalogGraph
	if err := services(&out, started); err != nil {
		return out, err
	}
	inStep, err := kept(ctx, src, plugins, answers, out)
	if err != nil {
		return out, err
	}
	if err := distinct(labels, inStep); err != nil {
		return out, err
	}
	out.Actions = append(out.Actions, inStep...)
	return out, nil
}

// distinct holds a label to one action, whatever declared each: a
// plugin's project, a catalog file, the host. Two of one label are
// refused before anything is resolved by label: a need would be met by
// whichever was read last, and the other never run. A plugin is named
// as it names itself, and may take the name the host gives the actions
// of a catalog file (customPlugin) or its own (catalogPlugin). seen holds the labels
// already taken, and is given those of actions.
func distinct(seen map[model.Label]bool, actions []plan.Action) error {
	for _, action := range actions {
		if seen[action.Label] {
			return &model.Mistake{Message: fmt.Sprintf("%s is declared more than once: a label is one action's", action.Label)}
		}
		seen[action.Label] = true
	}
	return nil
}

// catalogPlugin is the plugin the host's own actions over catalog files
// are named after.
const catalogPlugin = "catalog"

// kept returns the host's own actions: one for each catalog file that
// describes an entity of the workspace, holding what the file says each
// of its entities depends on to what the source says. What the file
// says is what its plugin answered; what it should say instead, the
// plugin is asked (Reconcile), and the host writes the edits it answers,
// keeping the file's comments and other fields, and refuses them where
// the plugin's own reading of the result still disagrees with the source.
func kept(ctx context.Context, src Source, plugins []*pluginhost.Plugin, answers asking, out Evaluation) ([]plan.Action, error) {
	inverse := map[entity.Ref][]entity.Ref{}
	for from, targets := range out.DependsOn {
		if from.Kind != entity.KindComponent {
			continue
		}
		for _, target := range targets {
			inverse[target] = append(inverse[target], from)
		}
	}
	for _, refs := range inverse {
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	}
	described := map[entity.Ref]catalog.Descriptor{}
	for _, descriptor := range out.descriptors {
		described[descriptor.Ref] = descriptor
	}
	byName := map[string]*pluginhost.Plugin{}
	for _, plugin := range plugins {
		byName[plugin.Description.GetName()] = plugin
	}
	// A file that describes several entities is read once and is one
	// action, whose fix is the file as every one of them should have it.
	type file struct {
		descriptor catalog.Descriptor
		raw        []byte
		resolved   []*pluginv1.EntityDependencies
		outOfStep  bool
	}
	var actions []plan.Action
	var files []*file
	at := map[string]int{}
	for _, entry := range out.Entities {
		descriptor, found := described[entry.Ref]
		if !found {
			continue
		}
		shown := path.Join(descriptor.Repository, descriptor.Manifest)
		n, seen := at[shown]
		if !seen {
			raw, found, err := src.Read(ctx, descriptor.Repository, descriptor.Manifest)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			sum := sha256.Sum256(raw)
			n = len(actions)
			at[shown] = n
			actions = append(actions, plan.Action{
				Label:       model.Label{Path: entry.Path(), Plugin: catalogPlugin, Name: "depends-on"},
				Repository:  descriptor.Repository,
				ProjectRoot: entry.Root,
				Declared:    &pluginv1.Action{Name: "depends-on"},
				InStep:      &plan.InStep{File: descriptor.Manifest, From: hex.EncodeToString(sum[:])},
			})
			files = append(files, &file{descriptor: descriptor, raw: raw})
		}
		step, held := actions[n].InStep, files[n]
		says := related(descriptor.Relations, "dependsOn")
		var should []entity.Ref
		for _, other := range out.DependsOn[entry.Ref] {
			if other.Kind == entity.KindComponent {
				should = append(should, other)
			}
		}
		sort.Slice(should, func(i, j int) bool { return should[i].String() < should[j].String() })
		step.Entities = append(step.Entities, plan.Described{Entity: entry.Ref, Says: says, Should: should})
		resolved := &pluginv1.EntityDependencies{Ref: entity.Proto(entry.Ref), Manifest: descriptor.Manifest, DependsOn: entity.Protos(should)}
		held.outOfStep = held.outOfStep || !slices.Equal(says, should)
		// The inverse is held only where the file states it.
		if states(descriptor.Relations, "dependencyOf") {
			dependents, shouldInverse := related(descriptor.Relations, "dependencyOf"), inverse[entry.Ref]
			step.Entities = append(step.Entities, plan.Described{Entity: entry.Ref, Inverse: true, Says: dependents, Should: shouldInverse})
			resolved.DependencyOf = entity.Protos(shouldInverse)
			held.outOfStep = held.outOfStep || !slices.Equal(dependents, shouldInverse)
		}
		held.resolved = append(held.resolved, resolved)
	}
	for n, held := range files {
		if !held.outOfStep {
			continue
		}
		at := manifest{held.descriptor.Repository, held.descriptor.Manifest}
		plugin := byName[held.descriptor.Plugin]
		if plugin == nil {
			return nil, at.mistake("the plugin %s that found its entities is not loaded", held.descriptor.Plugin)
		}
		request := &pluginv1.ReconcileRequest{Context: held.descriptor.Context, Root: held.descriptor.Root, Entities: held.resolved}
		response := new(pluginv1.ReconcileResponse)
		view := repositoryView{source: src, repository: held.descriptor.Repository}
		if _, err := answers.ask(ctx, plugin, view, "Reconcile", request, response); err != nil {
			return nil, mistaken(held.descriptor.Repository, err)
		}
		if err := pluginhost.CheckReconciliation(held.descriptor.Plugin, request, response); err != nil {
			return nil, at.mistake("%v", err)
		}
		fixed := held.raw
		for _, edit := range response.GetEdits() {
			if edit.GetPath() != held.descriptor.Manifest {
				continue
			}
			var err error
			if fixed, err = yamlnode.SetStrings(fixed, int(edit.GetDocument()), edit.GetKeys(), edit.GetValues()); err != nil {
				return nil, at.mistake("%v", err)
			}
		}
		if bytes.Equal(fixed, held.raw) {
			return nil, at.mistake("the plugin %s proposes no change, though the file does not say what the source says", held.descriptor.Plugin)
		}
		// The plugin's own reading of what it proposed must say what the
		// source says: the graph and the rewrite agree, or neither is used.
		again := new(pluginv1.AnalyzeResponse)
		overlay := repositoryView{source: overlaid{Source: src, repository: held.descriptor.Repository, path: held.descriptor.Manifest, content: fixed}, repository: held.descriptor.Repository}
		if _, err := answers.ask(ctx, plugin, overlay, "Analyze", &pluginv1.AnalyzeRequest{Context: held.descriptor.Context, Roots: []string{held.descriptor.Root}}, again); err != nil {
			return nil, mistaken(held.descriptor.Repository, err)
		}
		for _, resolved := range held.resolved {
			if err := agrees(again, resolved); err != nil {
				return nil, at.mistake("the plugin %s's rewrite does not say what the source says: %v", held.descriptor.Plugin, err)
			}
		}
		actions[n].InStep.Fixed = fixed
	}
	// A file that describes no entity that says what it depends on is
	// held to nothing.
	held := actions[:0]
	for _, action := range actions {
		if len(action.InStep.Entities) > 0 {
			held = append(held, action)
		}
	}
	return held, nil
}

// related returns the components a descriptor's relations of one type
// name, sorted as references are written.
func related(relations []catalog.DeclaredRelation, kind string) []entity.Ref {
	var out []entity.Ref
	for _, relation := range relations {
		if relation.Type == kind && relation.Target.Kind == entity.KindComponent {
			out = append(out, relation.Target)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// states reports whether a descriptor has any relation of a type: the
// file states the field, whatever it names.
func states(relations []catalog.DeclaredRelation, kind string) bool {
	for _, relation := range relations {
		if relation.Type == kind {
			return true
		}
	}
	return false
}

// agrees checks a plugin's reading of a rewritten file against what the
// source says of one entity: its components depended on, and depended
// on by where the file states it.
func agrees(answer *pluginv1.AnalyzeResponse, want *pluginv1.EntityDependencies) error {
	for _, analysis := range answer.GetAnalyses() {
		for _, entry := range analysis.GetEntities() {
			if !proto.Equal(entry.GetRef(), want.GetRef()) {
				continue
			}
			var declared []catalog.DeclaredRelation
			for _, relation := range entry.GetRelations() {
				declared = append(declared, catalog.DeclaredRelation{Type: relation.GetType(), Target: entity.FromProto(relation.GetTarget())})
			}
			if got, should := related(declared, "dependsOn"), sorted(want.GetDependsOn()); !slices.Equal(got, should) {
				return fmt.Errorf("%s would depend on %v, not %v", entity.FromProto(want.GetRef()), got, should)
			}
			if want.DependencyOf != nil {
				if got, should := related(declared, "dependencyOf"), sorted(want.GetDependencyOf()); !slices.Equal(got, should) {
					return fmt.Errorf("%s would be a dependency of %v, not %v", entity.FromProto(want.GetRef()), got, should)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("%s is no longer described", entity.FromProto(want.GetRef()))
}

// sorted is references as the host sorts them.
func sorted(refs []*typev1.EntityRef) []entity.Ref {
	out := make([]entity.Ref, 0, len(refs))
	for _, ref := range refs {
		out = append(out, entity.FromProto(ref))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// overlaid is a source with one file as a rewrite would leave it: what
// a plugin is asked to read back, before anything is written.
type overlaid struct {
	Source
	repository, path string
	content          []byte
}

func (o overlaid) Read(ctx context.Context, repository, path string) ([]byte, bool, error) {
	if repository == o.repository && path == o.path {
		return o.content, true, nil
	}
	return o.Source.Read(ctx, repository, path)
}

func (o overlaid) Digest(ctx context.Context, repository, path string) (string, bool, error) {
	if repository == o.repository && path == o.path {
		sum := sha256.Sum256(o.content)
		return hex.EncodeToString(sum[:]), true, nil
	}
	return o.Source.Digest(ctx, repository, path)
}

// tools returns the versions of the tools a repository pins, by tool, as
// a plugin is told of them.
func tools(ctx context.Context, repository string, files config.Reader) (map[string]string, error) {
	raw, found, err := files.Read(ctx, pins.Files[0])
	if err != nil || !found {
		return nil, err
	}
	versions, err := pins.Versions(raw)
	if err != nil {
		return nil, &model.Mistake{Repository: repository, File: pins.Files[0], Message: err.Error()}
	}
	return versions, nil
}

// hand gives each action that asks for it what its project requires of
// the workspace's other projects, where the action said. It then
// asks the action's plugin to arrange it: to write what its tool must be
// told so that it finds what was handed. Two projects that provide one
// coordinate are a *model.Mistake. It returns what provides what.
func hand(ctx context.Context, src Source, plugins []*pluginhost.Plugin, answers asking, profile typev1.Profile, pinned map[string]map[string]string, out *Evaluation) (*graph.Graph, error) {
	resolved, err := graph.Resolve(out.Projects)
	if err != nil {
		return nil, err
	}
	projects := map[model.ProjectRef]graph.Project{}
	for _, project := range out.Projects {
		projects[project.Ref()] = project
	}
	// Actions of a project that were handed the same things are arranged
	// together: the plugin is asked once for them.
	type arranging struct {
		project graph.Project
		handed  []*pluginv1.Handoff
		actions []int
	}
	type arrangementKey struct {
		project model.ProjectRef
		handed  string
	}
	var order []arrangementKey
	together := map[arrangementKey]*arranging{}
	for i, action := range out.Actions {
		project := projects[model.NewProjectRef(action.Repository, action.ProjectRoot, action.Label.Plugin)]
		dependencies := pluginhost.Dependencies(action.Declared.GetInputs())
		if len(dependencies) == 0 {
			continue
		}
		var handed []*pluginv1.Handoff
		selections, err := selectedHandoffs(resolved, project, dependencies)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", action.Label, err)
		}
		for _, selection := range selections {
			each := selection.handoff
			if len(each.Provide.Sources) == 0 && len(each.Provide.Artifacts) == 0 {
				continue
			}
			into, err := handoffPlacement(project, each, selection.into)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", action.Label, err)
			}
			var artifacts []plan.Read
			for _, produced := range each.Provide.Artifacts {
				artifacts = append(artifacts, plan.Read{From: model.Label{Path: each.From.Path(), Plugin: each.From.Plugin, Name: produced.Action}, Repository: each.From.Repository, ProjectRoot: each.From.Root, Output: produced.Output, Optional: produced.Optional})
			}
			out.Actions[i].Handed = append(out.Actions[i].Handed, plan.Handed{
				Repository: each.From.Repository,
				Root:       each.From.Root,
				Patterns:   each.Provide.Sources,
				Generated:  each.Provide.Generated,
				Artifacts:  artifacts,
				Into:       into,
			})
			handed = append(handed, &pluginv1.Handoff{Coordinate: wire.Coordinate(each.Provide.Coordinate), Into: into})
		}
		// A project with nothing handed is left as it is, unless its
		// plugin asks to arrange it for every run: its tool needs files
		// written for the workspace the project is in, which may hold
		// the project alone.
		if len(handed) == 0 && !project.AlwaysArranged {
			continue
		}
		if err := plan.CheckHanded(out.Actions[i]); err != nil {
			return nil, err
		}
		encoded, err := proto.Marshal(&pluginv1.ArrangeRequest{Handed: handed})
		if err != nil {
			return nil, err
		}
		key := arrangementKey{project: project.Ref(), handed: string(encoded)}
		if together[key] == nil {
			together[key] = &arranging{project: project, handed: handed}
			order = append(order, key)
		}
		together[key].actions = append(together[key].actions, i)
	}
	// Each is asked on its own, several at once, and what was answered is
	// taken in the order the groups come in.
	asked := make([]*pluginhost.Plugin, len(order))
	requests := make([]*pluginv1.ArrangeRequest, len(order))
	answered := make([]*pluginv1.ArrangeResponse, len(order))
	given := make([]bool, len(order))
	for n, key := range order {
		group := together[key]
		if asked[n] = pluginhost.Named(plugins, group.project.Plugin); asked[n] == nil {
			continue
		}
		options := map[string]*typev1.OptionValue{}
		for _, option := range group.project.Options {
			options[option.Name] = option.Value
		}
		requests[n] = &pluginv1.ArrangeRequest{
			Context: &pluginv1.Context{Profile: profile, Options: options, Tools: pinned[group.project.Repository], Platforms: group.project.Platforms},
			Root:    group.project.Root,
			Handed:  group.handed,
		}
		for _, i := range group.actions {
			requests[n].Actions = append(requests[n].Actions, out.Actions[i].Name())
		}
	}
	failed := make([]error, len(order))
	_ = each(len(order), func(n int) error {
		if asked[n] == nil {
			return nil
		}
		group := together[order[n]]
		answered[n] = &pluginv1.ArrangeResponse{}
		var err error
		if given[n], err = answers.ask(ctx, asked[n], repositoryView{source: src, repository: group.project.Repository}, "Arrange", requests[n], answered[n]); err != nil {
			failed[n] = mistaken(group.project.Repository, err)
		}
		return nil
	})
	for n, key := range order {
		if asked[n] == nil {
			continue
		}
		if failed[n] != nil {
			return nil, failed[n]
		}
		group := together[key]
		out.count(given[n])
		if err := pluginhost.CheckArrangement(asked[n].Description.GetName(), requests[n].GetRoot(), answered[n]); err != nil {
			return nil, err
		}
		for _, i := range group.actions {
			for _, file := range answered[n].GetFiles() {
				out.Actions[i].Arranged = append(out.Actions[i].Arranged, plan.Arranged{Path: file.GetPath(), Content: file.GetContent(), Executable: file.GetExecutable()})
			}
		}
	}
	return resolved, nil
}

// customPlugin is the plugin an entity's own actions are named after.
const customPlugin = "custom"

// custom is an entity's own action, as the host addresses it.
func custom(repository string, own config.Action) (plan.Action, error) {
	at := path.Join(repository, own.Directory)
	what := "the action " + own.Name
	reading, err := readCatalogInputs(repository, own.Directory, own.File, what, own.Inputs)
	if err != nil {
		return plan.Action{}, err
	}
	action := plan.Action{
		Label:       model.Label{Path: at, Plugin: customPlugin, Name: own.Name},
		Repository:  repository,
		ProjectRoot: own.Directory,
		Sources:     reading.ofOwn(),
		References:  reading.references,
		Declared: &pluginv1.Action{
			Name:      own.Name,
			Command:   &pluginv1.Command{Arguments: own.Command},
			Tools:     own.Tools,
			Versioned: own.Versioned,
			Kind:      &pluginv1.Action_Build{Build: &pluginv1.Build{}},
		},
	}
	// It is of the one kind it says; what only a check says, the
	// configuration refused of any other where it was written.
	switch own.Kind {
	case config.KindCheck:
		check := &pluginv1.Check{Rewrites: own.Rewrites}
		if len(own.Fix) > 0 {
			check.Fix = &pluginv1.Command{Arguments: own.Fix}
		}
		action.Declared.Kind = &pluginv1.Action_Check{Check: check}
	case config.KindPackage:
		action.Declared.Kind = &pluginv1.Action_Package{Package: &pluginv1.Package{}}
	}
	if err := pluginhost.CheckRewrites(what, own.Rewrites); err != nil {
		return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: err.Error()}
	}
	if err := pluginhost.CheckFix(what, action.Declared); err != nil {
		return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: err.Error()}
	}
	if err := pluginhost.CheckOutputs(what, own.Outputs); err != nil {
		return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: err.Error()}
	}
	for _, output := range own.Outputs {
		action.Declared.Outputs = append(action.Declared.Outputs, &pluginv1.Output{Path: output})
	}
	// It is named as it is written, whatever its kind: the name in the
	// file is the name on the command line.
	if own.Kind == config.KindProgram {
		action.Declared.Kind = &pluginv1.Action_Program{Program: &pluginv1.Program{}}
		if err := pluginhost.CheckProgram(action.Declared); err != nil {
			return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: err.Error()}
		}
	}
	if own.Kind == config.KindPublish {
		// What it reads is nothing: it is given its unit's artifacts.
		if len(own.Inputs) > 0 {
			return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: what + " publishes and says what it reads: it is given its unit's artifacts, and reads nothing else"}
		}
		action.Declared.Kind = &pluginv1.Action_Publish{Publish: &pluginv1.Publish{Environment: own.InheritEnvironment}}
		if err := pluginhost.CheckPublish(what, action.Declared); err != nil {
			return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: err.Error()}
		}
	}
	if err := pluginhost.CheckVersion(what, action.Declared); err != nil {
		return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: err.Error()}
	}
	if err := pluginhost.CheckPackage(what, action.Declared); err != nil {
		return plan.Action{}, &model.Mistake{Repository: own.Repository, File: own.File, Message: err.Error()}
	}
	return action, nil
}

// mistaken turns what a plugin says is wrong with a file of a repository
// into its author's mistake: the build fails, as it does for a mistake
// in the source, and says where. What a plugin says of no file is the
// plugin's own failure and stays one.
func mistaken(repository string, err error) error {
	var said *pluginhost.Error
	if !errors.As(err, &said) {
		return err
	}
	bound, bindErr := said.InRepository(repository)
	if bindErr != nil {
		return bindErr
	}
	if bound.Detail.GetSourceLocation() == nil {
		return bound
	}
	return &model.Mistake{Message: bound.Error(), Cause: bound}
}

// analyze asks one plugin about one repository: about each directory
// that holds a file the plugin looks for, on its own.
func analyze(ctx context.Context, view repositoryView, plugin *pluginhost.Plugin, answers asking, configured *config.Repository, profile typev1.Profile, pinned map[string]string, out *Evaluation) error {
	name := plugin.Description.GetName()
	declared := plugin.Description.GetOptions()
	// Discover roots against the original snapshot, so a claimed manifest cannot
	// silently hide a whole project. Reads of claimed paths are refused below.
	discovery := view
	if generated, ok := view.source.(generatedView); ok {
		discovery.source = generated.Source
	}
	files, err := discovery.Glob(ctx, "", plugin.Description.GetInterests())
	if err != nil {
		return err
	}
	// The plugin is asked about each directory on its own, so that what
	// it answered for one holds whatever becomes of another: a change in
	// one module has it asked about that module and no other. It is
	// asked about several at once, and what it found is taken in the
	// order the directories come in, whichever answer came first.
	roots := directories(files)
	requests := make([]*pluginv1.AnalyzeRequest, len(roots))
	for i, root := range roots {
		options, err := configured.Options(name, declared, directory(root))
		if err != nil {
			return err
		}
		held := map[string]*typev1.OptionValue{}
		for _, option := range options {
			held[option.Name] = option.Value
		}
		requests[i] = &pluginv1.AnalyzeRequest{Context: &pluginv1.Context{Profile: profile, Options: held, Tools: pinned, Platforms: configured.Platforms(directory(root))}, Roots: []string{root}}
	}
	answered := make([]*pluginv1.AnalyzeResponse, len(roots))
	given := make([]bool, len(roots))
	observed := make([][]pluginhost.Read, len(roots))
	err = each(len(roots), func(i int) (err error) {
		answered[i] = &pluginv1.AnalyzeResponse{}
		given[i], observed[i], err = answers.askObserved(ctx, plugin, view, "Analyze", requests[i], answered[i])
		return err
	})
	if err != nil {
		return mistaken(view.repository, err)
	}
	for i, answer := range answered {
		out.count(given[i])
		if err := pluginhost.CheckAnalysis(name, requests[i].GetRoots(), answer); err != nil {
			return err
		}
		if plugin.Description.GetGeneratesSources() {
			out.producerReads = append(out.producerReads, observedGeneration{repository: view.repository, plugin: name, reads: observed[i]})
		}
		for _, analysis := range answer.GetAnalyses() {
			for _, entry := range analysis.GetEntities() {
				descriptor := catalog.Descriptor{Entity: catalog.Entity{Ref: entity.FromProto(entry.GetRef()), Repository: view.repository, Manifest: entry.GetManifest(), Roots: entry.GetRoots()}, Plugin: name, Root: analysis.GetRoot(), Context: requests[i].GetContext()}
				for _, relation := range entry.GetRelations() {
					descriptor.Relations = append(descriptor.Relations, catalog.DeclaredRelation{Type: relation.GetType(), InverseType: relation.GetInverseType(), Target: entity.FromProto(relation.GetTarget())})
				}
				out.descriptors = append(out.descriptors, descriptor)
			}
			for _, project := range analysis.GetProjects() {
				options, err := configured.Options(name, declared, directory(project.GetRoot()))
				if err != nil {
					return err
				}
				sets := map[string][]*pluginv1.Input{}
				declaredSets := map[string]*pluginv1.InputSet{}
				for _, set := range project.GetInputSets() {
					sets[set.GetName()] = set.GetInputs()
					declaredSets[set.GetName()] = set
				}
				for _, generated := range project.GetGeneratedSources() {
					if !plugin.Description.GetGeneratesSources() {
						return fmt.Errorf("plugin %s declares generated sources without its capability", name)
					}
					// Where it goes: beneath its project, or a destination
					// outside it, which only the Component there lets it
					// go to (consented).
					at := generated.GetDestination()
					if at == "" {
						at = path.Join(project.GetRoot(), generated.GetDirectory())
					}
					out.Generated = append(out.Generated, plan.GeneratedSource{
						Repository: view.repository, ProjectRoot: project.GetRoot(),
						Directory: at, Output: generated.GetOutput(), Patterns: generated.GetPatterns(),
						Producer: model.Label{Path: path.Join(view.repository, project.GetRoot()), Plugin: name, Name: generated.GetAction()},
					})
				}
				found := graph.Project{
					Repository: view.repository,
					Root:       project.GetRoot(),
					Plugin:     name,
					Kind:       project.GetKind(),
					Name:       project.GetName(),
					Manifest:   project.GetManifest(),
					Options:    options,
					Platforms:  configured.Platforms(directory(project.GetRoot())),

					AlwaysArranged: project.GetAlwaysArranged(),
				}
				optional := map[string]map[string]bool{}
				if slices.ContainsFunc(project.GetProvides(), func(p *pluginv1.Provision) bool { return len(p.GetArtifacts()) > 0 }) {
					for _, declared := range project.GetActions() {
						for _, output := range declared.GetOutputs() {
							if output.GetOptional() {
								if optional[declared.GetName()] == nil {
									optional[declared.GetName()] = map[string]bool{}
								}
								optional[declared.GetName()][output.GetPath()] = true
							}
						}
					}
				}
				for _, provide := range project.GetProvides() {
					provided := graph.Provide{Coordinate: wire.ReadCoordinate(provide.GetCoordinate())}
					for _, artifact := range provide.GetArtifacts() {
						provided.Artifacts = append(provided.Artifacts, graph.Produced{Action: artifact.GetAction(), Output: artifact.GetOutput(), Optional: optional[artifact.GetAction()][artifact.GetOutput()]})
					}
					if set := provide.GetSources(); set != "" {
						resolved, err := pluginhost.ResolveInputs([]*pluginv1.Input{pluginhost.SetInput(set)}, sets)
						if err != nil {
							return fmt.Errorf("plugin %s: %s: what it hands: %w", name, project.GetRoot(), err)
						}
						provided.Sources, _ = pluginhost.SplitInputs(resolved)
						if provided.Generated, err = pluginhost.GeneratedInputs([]*pluginv1.Input{pluginhost.SetInput(set)}, declaredSets); err != nil {
							return err
						}
					}
					found.Provides = append(found.Provides, provided)
				}
				for _, requirement := range project.GetRequires() {
					found.Requires = append(found.Requires, graph.Requirement{
						Coordinate: wire.ReadCoordinate(requirement.GetCoordinate()),
						File:       requirement.GetOrigin().GetPath(),
						Line:       requirement.GetOrigin().GetLine(),
						Version:    requirement.GetVersion(),
					})
				}
				out.Projects = append(out.Projects, found)
				for _, declared := range project.GetActions() {
					if declared.GetSharedFetch() != nil {
						if err := out.share(view.repository, name, project.GetRoot(), declared); err != nil {
							return err
						}
						continue
					}
					inputs, err := pluginhost.ResolveInputs(declared.GetInputs(), sets)
					if err != nil {
						return fmt.Errorf("plugin %s: %s: action %s: %w", name, project.GetRoot(), declared.GetName(), err)
					}
					generatedInputs, err := pluginhost.GeneratedInputs(declared.GetInputs(), declaredSets)
					if err != nil {
						return err
					}
					at := path.Join(view.repository, project.GetRoot())
					// What it names of other actions, ":what", is the
					// plugin's own: an action of the project, or a shared
					// one, which is the repository's whichever project
					// declared it. It is resolved among the workspace's
					// actions (plan.Resolve); the patterns stay inputs.
					patterns, named := pluginhost.SplitInputs(inputs)
					var references []plan.Reference
					if len(named) > 0 {
						references = make([]plan.Reference, 0, len(named))
					}
					for _, input := range named {
						reference, err := model.ParseReference(view.repository, project.GetRoot(), input)
						if err != nil {
							return fmt.Errorf("plugin %s: %s: action %s: %w", name, project.GetRoot(), declared.GetName(), err)
						}
						references = append(references, plan.Reference{Reference: reference, Plugin: name, Shared: view.repository})
					}
					out.Actions = append(out.Actions, plan.Action{
						Sources:        []pattern.Group{{Directory: project.GetRoot(), Patterns: patterns, Generated: generatedInputs}},
						References:     references,
						WorkspaceFiles: project.GetWorkspaceFiles(),
						Label:          model.Label{Path: at, Plugin: name, Name: declared.GetName()},
						Repository:     view.repository,
						ProjectRoot:    project.GetRoot(),
						Declared:       declared,
						Context:        requests[i].GetContext(),
					})
				}
			}
		}
	}
	return nil
}

// directory is a directory of a repository as configuration names it:
// the root is the empty path.
func directory(root string) string {
	if root == "." {
		return ""
	}
	return root
}

// directories returns the directories that hold files, each once, sorted.
func directories(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		dir := path.Dir(f)
		if dir == "." {
			dir = ""
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

// repositoryView is one repository of a source, as a plugin reads it.
type repositoryView struct {
	source     Source
	repository string
}

var (
	_ pluginhost.Source = repositoryView{}
	_ pluginhost.Known  = repositoryView{}
)

func (v repositoryView) Read(ctx context.Context, p string) ([]byte, bool, error) {
	return v.source.Read(ctx, v.repository, p)
}

func (v repositoryView) Digest(ctx context.Context, p string) (string, bool, error) {
	return v.source.Digest(ctx, v.repository, p)
}

func (v repositoryView) Glob(ctx context.Context, directory string, patterns []string) ([]string, error) {
	return v.source.Within(ctx, v.repository, directory, patterns)
}

// each does something for each of a number of things, several at once,
// and returns the error of the first of them, in their order, that had
// one. None is given up for another's error: which error is reported
// does not depend on which was first to fail.
func each(n int, do func(i int) error) error {
	errs := make([]error, n)
	var wait sync.WaitGroup
	at := make(chan int)
	for range min(n, runtime.GOMAXPROCS(0)) {
		wait.Go(func() {
			for i := range at {
				errs[i] = do(i)
			}
		})
	}
	for i := range n {
		at <- i
	}
	close(at)
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
