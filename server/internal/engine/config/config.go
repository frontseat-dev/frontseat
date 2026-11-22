// Package config reads what a repository says of how it is built, and
// what holds of it for one directory.
//
// A repository says it in frontseat.yaml at its root: which plugins build
// it, and what it sets of the options those plugins take. A Component may
// set an option too, under spec.frontseat.plugins in its catalog file,
// for the file's directory and what is beneath, once for a directory. The
// nearest setting wins: a Component's over the repository's, the
// repository's over the plugin's own value.
//
// An entity may also have actions of its own, under
// spec.frontseat.actions: the one place a person writes a command. What
// an action reads of other actions is among its inputs, as ":what" or
// "//path:what", and is resolved among the workspace's actions.
//
// All of it is read strictly. A key that is not known, an option a plugin
// does not take, a value of the wrong kind: each is a model.Mistake that names
// the file and what is wrong in it, and nothing is built.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/engine/internal/yamlnode"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// File is the file a repository says how it is built in, at its root.
const File = "frontseat.yaml"

// Reader reads the files of one repository.
type Reader interface {
	Read(ctx context.Context, path string) ([]byte, bool, error)
	Glob(ctx context.Context, directory string, patterns []string) ([]string, error)
}

// Repository is what one repository says of how it is built.
type Repository struct {
	// name is the workspace's name for the repository.
	name string
	// listed are the plugins it lists, in the file's order.
	listed []string
	// repository is what frontseat.yaml sets, by plugin and option.
	repository map[string]map[string]*yaml.Node
	// entities are what catalog files set, each for its directory.
	entities []scope
	// actions are the actions entities have of their own, in the order
	// their files and the files' documents give them.
	actions []Action
	// services are how the repository's entities are started for tests,
	// in the order their files give them.
	services []Service
	// releases are what entities say of how they are released.
	releases []Release
	// tests are the runtime data declared for inferred tests of Components.
	tests []TestRuntime
	// generated are what Components say other Components generate of
	// their directories.
	generated []Generated
	// platforms are the platforms entities say they ship for, each for
	// its directory, the deepest directory last.
	platforms []shipped
}

// shipped is the platforms one catalog file says its entity ships for,
// which hold for its directory and beneath.
type shipped struct {
	file, directory string
	platforms       []string
}

// platform is what a platform is written as: an operating system and
// an architecture, as the tools that build for one name them.
var platform = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)

// readPlatforms reads spec.frontseat.platforms of one document: a list
// of platforms, each once.
func readPlatforms(file string, node *yaml.Node) ([]string, error) {
	mistake := func(line int, format string, a ...any) ([]string, error) {
		return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: ", line) + fmt.Sprintf(format, a...)}
	}
	if node.Kind != yaml.SequenceNode {
		return mistake(node.Line, "spec.frontseat.platforms is a list of the platforms the entity ships for, each <os>/<arch>, as linux/amd64")
	}
	// A list of none is said too: what is beneath ships for no platform,
	// whatever an entity above it says.
	out := []string{}
	for _, item := range node.Content {
		switch {
		case item.Kind != yaml.ScalarNode || item.ShortTag() != "!!str" || !platform.MatchString(item.Value):
			return mistake(item.Line, "spec.frontseat.platforms holds what is no platform: one is written <os>/<arch>, in small letters and digits, as linux/amd64")
		case slices.Contains(out, item.Value):
			return mistake(item.Line, "spec.frontseat.platforms names a platform twice: each is said once")
		}
		out = append(out, item.Value)
	}
	return out, nil
}

// Platforms returns the platforms what is in a directory of the
// repository ships for: what the entity nearest above it says, and
// none where none says any.
func (r *Repository) Platforms(directory string) []string {
	var out []string
	for _, said := range r.platforms {
		if said.directory == "" || said.directory == directory || strings.HasPrefix(directory, said.directory+"/") {
			out = said.platforms
		}
	}
	return out
}

// Releases returns what the repository's entities say of how they are
// released, in the order their files give them.
func (r *Repository) Releases() []Release { return r.releases }

// Services returns how the repository's entities are started for tests.
func (r *Repository) Services() []Service { return r.services }

// TestRuntimes returns Component runtime declarations in file/document order.
func (r *Repository) TestRuntimes() []TestRuntime { return r.tests }

// Action is an action of an entity's own, as its catalog file writes it.
type Action struct {
	// File is the catalog file it is written in, relative to the
	// workspace.
	File string
	// Directory is the entity's directory in the repository; empty for
	// the repository's root.
	Directory string
	// Name is its name: lower-case letters, digits and dashes.
	Name string
	// Command is the program it runs and its arguments.
	Command []string
	// Tools are the tools it runs with, each pinned by the repository.
	Tools []string
	// Inputs are what it reads: patterns relative to Directory, and
	// what other actions left, as ":what" or "//path:what".
	Inputs []string
	// Check says it checks the source.
	Check bool
	// Program marks a program, never a finite build action.
	Program bool
	// Fix is the command that puts right what the check finds, run where
	// the checkout is; nothing, where the check has no fix.
	Fix []string
	// Rewrites are patterns, relative to Directory, of the inputs its
	// command may change.
	Rewrites []string
	// Outputs are what it writes, relative to Directory; a directory's
	// ends in a slash.
	Outputs []string
	// Versioned says it is given the version of the release it is built
	// for, where its command names it.
	Versioned bool
	// Package says it makes what the entity ships: each file it leaves
	// is an artifact of a release.
	Package bool
	// Publish says it publishes what a release shipped, where the
	// daemon is; Environment names the variables of where the daemon
	// runs that it is given then.
	Publish     bool
	Environment []string
}

// scope is what one catalog file sets, for its directory and beneath.
type scope struct {
	file, directory string
	plugins         map[string]map[string]*yaml.Node
}

// Read reads a repository's configuration. A repository with no
// frontseat.yaml does not say how it is built, which is a model.Mistake.
func Read(ctx context.Context, name string, files Reader) (*Repository, error) {
	r := &Repository{name: name, repository: map[string]map[string]*yaml.Node{}}
	raw, found, err := files.Read(ctx, File)
	if err != nil {
		return nil, err
	}
	if !found {
		subject := name
		if subject == "" {
			subject = "the checkout"
		}
		return nil, &model.Mistake{Message: fmt.Sprintf("%s has no %s, which says which plugins build a repository", subject, File)}
	}
	documents, err := parse(r.origin(File), raw, true)
	if err != nil {
		return nil, err
	}
	if len(documents) > 1 {
		return nil, &model.Mistake{File: r.origin(File), Message: "it holds more than one document"}
	}
	if len(documents) == 1 {
		root := documents[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			key, value := root.Content[i], root.Content[i+1]
			if key.Value != "plugins" {
				return nil, &model.Mistake{File: r.origin(File), Message: fmt.Sprintf("line %d: it takes no key %s; a repository lists its plugins under plugins", key.Line, key.Value)}
			}
			if r.listed, r.repository, err = plugins(r.origin(File), value); err != nil {
				return nil, err
			}
		}
	}

	// A testdata directory holds fixtures, to every plugin and here: a
	// catalog file in one is no part of the repository's configuration.
	catalogs, err := files.Glob(ctx, "", []string{catalog.File, "**/" + catalog.File, "!**/testdata/**"})
	if err != nil {
		return nil, err
	}
	for _, file := range catalogs {
		raw, _, err := files.Read(ctx, file)
		if err != nil {
			return nil, err
		}
		documents, err := parse(r.origin(file), raw, false)
		if err != nil {
			return nil, err
		}
		directory := path.Dir(file)
		if directory == "." {
			directory = ""
		}
		for _, document := range documents {
			frontseat := yamlnode.Child(yamlnode.Child(document, "spec"), "frontseat")
			spec, err := yamlnode.ResolvedChild(document, "spec")
			if err != nil {
				return nil, &model.Mistake{File: r.origin(file), Message: err.Error()}
			}
			if spec != nil {
				resolved, err := yamlnode.ResolvedChild(spec, "frontseat")
				if err != nil {
					return nil, &model.Mistake{File: r.origin(file), Message: err.Error()}
				}
				if resolved != nil && frontseat == nil {
					return nil, &model.Mistake{File: r.origin(file), Message: "spec.frontseat must be written directly, not inherited through an alias or merge"}
				}
			}
			if frontseat != nil {
				if frontseat.Kind != yaml.MappingNode {
					return nil, &model.Mistake{File: r.origin(file), Message: "spec.frontseat must be a map"}
				}
				if err := strict(frontseat, true); err != nil {
					return nil, &model.Mistake{File: r.origin(file), Message: err.Error()}
				}
			}
			// What is under spec.frontseat is frontseat's to read, all of
			// it: a key it does not read is a mistake, where a key beside
			// it is Backstage's and none of its business.
			if frontseat != nil && frontseat.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(frontseat.Content); i += 2 {
					key := frontseat.Content[i]
					if key.Value != "actions" && key.Value != "plugins" && key.Value != "service" && key.Value != "release" && key.Value != "test" && key.Value != "generated" && key.Value != "platforms" {
						return nil, &model.Mistake{File: r.origin(file), Message: fmt.Sprintf("line %d: spec.frontseat takes no key %s; it takes actions, generated, platforms, plugins, release, service and test", key.Line, key.Value)}
					}
				}
			}
			if frontseat == nil {
				continue
			}
			// The entity is read once, one way, for every key it says.
			ref, err := entityOf(document, frontseat)
			if err != nil {
				return nil, &model.Mistake{File: r.origin(file), Message: err.Error()}
			}
			if node := yamlnode.Child(frontseat, "test"); node != nil {
				read, err := readTestRuntime(r.origin(file), directory, ref, node)
				if err != nil {
					return nil, err
				}
				for _, before := range r.tests {
					if before.Entity == read.Entity {
						return nil, &model.Mistake{File: read.File, Message: fmt.Sprintf("%s declares test runtime data twice, here and in %s", read.Entity, before.File)}
					}
				}
				r.tests = append(r.tests, read)
			}
			if node := yamlnode.Child(frontseat, "generated"); node != nil {
				read, err := readGenerated(r.origin(file), directory, ref, node)
				if err != nil {
					return nil, err
				}
				for _, said := range read {
					for _, before := range r.generated {
						if before.Directory == said.Directory {
							return nil, &model.Mistake{File: said.File, Message: fmt.Sprintf("%s is said to be generated twice: here and in %s", said.Directory, before.File)}
						}
					}
					r.generated = append(r.generated, said)
				}
			}
			if node := yamlnode.Child(frontseat, "platforms"); node != nil {
				// What ships for a platform is what is built beneath a
				// directory, and a Component alone has one: a System or
				// a Domain is no directory, and says none.
				if ref.Kind != entity.KindComponent {
					return nil, &model.Mistake{File: r.origin(file), Message: fmt.Sprintf("line %d: spec.frontseat.platforms is said by a Component, for what is built beneath its directory", node.Line)}
				}
				read, err := readPlatforms(r.origin(file), node)
				if err != nil {
					return nil, err
				}
				for _, before := range r.platforms {
					if before.directory == directory {
						return nil, &model.Mistake{File: r.origin(file), Message: fmt.Sprintf("line %d: spec.frontseat.platforms is said twice for one directory, here and in %s", node.Line, before.file)}
					}
				}
				r.platforms = append(r.platforms, shipped{file: r.origin(file), directory: directory, platforms: read})
			}
			if node := yamlnode.Child(frontseat, "service"); node != nil {
				service, err := readService(r.origin(file), directory, ref, node)
				if err != nil {
					return nil, err
				}
				r.services = append(r.services, service)
			}
			if node := yamlnode.Child(frontseat, "release"); node != nil {
				release, err := readRelease(r.origin(file), ref, node)
				if err != nil {
					return nil, err
				}
				r.releases = append(r.releases, release)
			}
			if own := yamlnode.Child(frontseat, "actions"); own != nil {
				read, err := actions(r.origin(file), directory, own)
				if err != nil {
					return nil, err
				}
				r.actions = append(r.actions, read...)
			}
			set := yamlnode.Child(frontseat, "plugins")
			if set == nil {
				continue
			}
			// An option holds for what is built beneath a directory, as a
			// platform does, and a Component alone has one: what holds for
			// the whole repository is said in its own file.
			if ref.Kind != entity.KindComponent {
				return nil, &model.Mistake{File: r.origin(file), Message: fmt.Sprintf("line %d: spec.frontseat.plugins is said by a Component, for what is built beneath its directory; what holds for the repository is said in %s", set.Line, File)}
			}
			for _, before := range r.entities {
				if before.directory == directory {
					return nil, &model.Mistake{File: r.origin(file), Message: fmt.Sprintf("line %d: spec.frontseat.plugins is said twice for one directory, here and in %s", set.Line, before.file)}
				}
			}
			_, options, err := plugins(r.origin(file), set)
			if err != nil {
				return nil, err
			}
			r.entities = append(r.entities, scope{file: r.origin(file), directory: directory, plugins: options})
		}
	}
	// The nearest setting wins, so the deepest directory is applied last.
	sort.SliceStable(r.entities, func(i, j int) bool { return len(r.entities[i].directory) < len(r.entities[j].directory) })
	sort.SliceStable(r.platforms, func(i, j int) bool { return len(r.platforms[i].directory) < len(r.platforms[j].directory) })
	return r, nil
}

// origin is a file of the repository as the workspace names it.
func (r *Repository) origin(file string) string { return path.Join(r.name, file) }

// Plugins returns the plugins the repository lists, in its order.
func (r *Repository) Plugins() []string { return r.listed }

// Actions returns the actions entities of the repository have of their
// own.
func (r *Repository) Actions() []Action { return r.actions }

// named is what an action's name is made of.
var named = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// actions reads a mapping of an entity's own actions.
func actions(file, directory string, node *yaml.Node) ([]Action, error) {
	if node.Kind != yaml.MappingNode {
		return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: actions is a map of actions, each by its name", node.Line)}
	}
	var out []Action
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, body := node.Content[i], node.Content[i+1]
		if !named.MatchString(name.Value) {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: an action's name is lower-case letters, digits and dashes, beginning with a letter or a digit", name.Line)}
		}
		if body.Kind != yaml.MappingNode {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: the action %s is a map: its command, and what it reads and writes", body.Line, name.Value)}
		}
		action := Action{File: file, Directory: directory, Name: name.Value}
		for j := 0; j+1 < len(body.Content); j += 2 {
			key, value := body.Content[j], body.Content[j+1]
			var list *[]string
			switch key.Value {
			case "description":
				// What it does, said for whoever reads the file.
				if value.Kind != yaml.ScalarNode || value.ShortTag() != "!!str" {
					return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: description is text", value.Line)}
				}
				continue
			case "command":
				list = &action.Command
			case "tools":
				list = &action.Tools
			case "inputs":
				list = &action.Inputs
			case "outputs":
				list = &action.Outputs
			case "rewrites":
				list = &action.Rewrites
			case "fix":
				list = &action.Fix
			case "environment":
				list = &action.Environment
			case "check", "program", "versioned", "package", "publish":
				if value.Kind != yaml.ScalarNode || value.ShortTag() != "!!bool" {
					return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: %s is true or false", value.Line, key.Value)}
				}
				var enabled bool
				if err := value.Decode(&enabled); err != nil {
					return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: %s is true or false", value.Line, key.Value)}
				}
				switch key.Value {
				case "program":
					action.Program = enabled
				case "versioned":
					action.Versioned = enabled
				case "package":
					action.Package = enabled
				case "publish":
					action.Publish = enabled
				default:
					action.Check = enabled
				}
				continue
			default:
				return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: an action takes no key %s; it takes check, command, description, environment, fix, inputs, outputs, package, program, publish, rewrites, tools, versioned", key.Line, key.Value)}
			}
			read, wrong := textList(value)
			switch {
			case wrong == value:
				return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: %s is a list", value.Line, key.Value)}
			case wrong != nil:
				return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: %s is a list of text", wrong.Line, key.Value)}
			}
			*list = read
		}
		if len(action.Command) == 0 {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: the action %s says no command", name.Line, name.Value)}
		}
		if len(action.Environment) > 0 && !action.Publish {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: the action %s says an environment, which only an action that publishes is given", name.Line, name.Value)}
		}
		out = append(out, action)
	}
	return out, nil
}

// Option is an option as it holds for one directory.
type Option struct {
	// Name is the option's name.
	Name string
	// Value is its value there.
	Value *typev1.OptionValue
	// Origin is the file that sets it, relative to the workspace; empty
	// where nothing does and the value is the plugin's own.
	Origin string
}

// Check holds everything set of a plugin, anywhere in the repository, to
// the options the plugin declared.
func (r *Repository) Check(plugin string, declared []*pluginv1.Option) error {
	if _, err := r.settle(plugin, declared, nil); err != nil {
		return err
	}
	for _, entity := range r.entities {
		if _, err := r.settle(plugin, declared, &entity); err != nil {
			return err
		}
	}
	for _, entity := range r.entities {
		for _, name := range slices.Sorted(maps.Keys(entity.plugins)) {
			if !slices.Contains(r.listed, name) {
				return &model.Mistake{File: entity.file, Message: fmt.Sprintf("it sets options of the %s plugin, which the repository does not list in %s", name, File)}
			}
		}
	}
	return nil
}

// Options returns every option a plugin declared, each as it holds for a
// directory of the repository, sorted by name.
func (r *Repository) Options(plugin string, declared []*pluginv1.Option, directory string) ([]Option, error) {
	held := map[string]Option{}
	for _, option := range declared {
		held[option.GetName()] = Option{Name: option.GetName(), Value: option.GetDefault()}
	}
	set, err := r.settle(plugin, declared, nil)
	if err != nil {
		return nil, err
	}
	for name, option := range set {
		held[name] = option
	}
	for _, entity := range r.entities {
		if entity.directory != "" && entity.directory != directory && !strings.HasPrefix(directory, entity.directory+"/") {
			continue
		}
		set, err := r.settle(plugin, declared, &entity)
		if err != nil {
			return nil, err
		}
		for name, option := range set {
			held[name] = option
		}
	}
	out := make([]Option, 0, len(held))
	for _, option := range held {
		out = append(out, option)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// settle reads what one place sets of a plugin, the repository's file or
// an entity's, against what the plugin declared.
func (r *Repository) settle(plugin string, declared []*pluginv1.Option, entity *scope) (map[string]Option, error) {
	file, set := r.origin(File), r.repository[plugin]
	if entity != nil {
		file, set = entity.file, entity.plugins[plugin]
	}
	out := map[string]Option{}
	// The first by name is the one refused, whenever it is read.
	for _, name := range slices.Sorted(maps.Keys(set)) {
		node := set[name]
		var option *pluginv1.Option
		for _, d := range declared {
			if d.GetName() == name {
				option = d
			}
		}
		if option == nil {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: the %s plugin takes no option %s%s", node.Line, plugin, name, taken(declared))}
		}
		if entity != nil && !option.GetEntitySettable() {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: %s is set for a repository, in %s, and not by a Component", node.Line, name, File)}
		}
		value, err := valueOf(option, node)
		if err != nil {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: %s", node.Line, err)}
		}
		out[name] = Option{Name: name, Value: value, Origin: file}
	}
	return out, nil
}

// taken says which options a plugin takes, for whoever misspelt one.
func taken(declared []*pluginv1.Option) string {
	if len(declared) == 0 {
		return "; it takes none"
	}
	names := make([]string, 0, len(declared))
	for _, option := range declared {
		names = append(names, option.GetName())
	}
	sort.Strings(names)
	return "; it takes " + strings.Join(names, ", ")
}

// valueOf reads a value as the kind its option takes.
func valueOf(option *pluginv1.Option, node *yaml.Node) (*typev1.OptionValue, error) {
	name := option.GetName()
	scalar := func(tag string) bool { return node.Kind == yaml.ScalarNode && node.ShortTag() == tag }
	switch option.GetKind() {
	case pluginv1.Option_KIND_STRING:
		value, ok := text(node)
		if !ok {
			return nil, fmt.Errorf("%s is text", name)
		}
		return &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: value}}, nil
	case pluginv1.Option_KIND_BOOL:
		// YAML writes true as true, True or TRUE: what was meant is read
		// from the value, not from how it was spelt.
		var on bool
		if !scalar("!!bool") || node.Decode(&on) != nil {
			return nil, fmt.Errorf("%s is true or false", name)
		}
		return &typev1.OptionValue{Value: &typev1.OptionValue_BoolValue{BoolValue: on}}, nil
	case pluginv1.Option_KIND_INT:
		number, ok := whole[int64](node)
		if !ok {
			return nil, fmt.Errorf("%s is a whole number", name)
		}
		return &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: number}}, nil
	case pluginv1.Option_KIND_STRING_LIST:
		items, wrong := textList(node)
		switch {
		case wrong == node:
			return nil, fmt.Errorf("%s is a list", name)
		case wrong != nil:
			return nil, fmt.Errorf("%s is a list of text", name)
		}
		return &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{Values: items}}}, nil
	}
	return nil, fmt.Errorf("the plugin does not say what kind of value %s takes", name)
}

// plugins reads a mapping of plugins to what is set of each.
func plugins(file string, node *yaml.Node) ([]string, map[string]map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: plugins is a map of plugins, each to what it sets", node.Line)}
	}
	var names []string
	set := map[string]map[string]*yaml.Node{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, options := node.Content[i], node.Content[i+1]
		names = append(names, name.Value)
		set[name.Value] = map[string]*yaml.Node{}
		switch {
		case options.Kind == yaml.ScalarNode && options.ShortTag() == "!!null":
		case options.Kind == yaml.MappingNode:
			for j := 0; j+1 < len(options.Content); j += 2 {
				set[name.Value][options.Content[j].Value] = options.Content[j+1]
			}
		default:
			return nil, nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: what is set of the %s plugin is a map of its options", options.Line, name.Value)}
		}
	}
	return names, set, nil
}

// parse rejects duplicate keys throughout. Aliases are forbidden in Frontseat
// configuration; catalog-owned fields follow their ecosystem's YAML semantics.
func parse(file string, raw []byte, noAliases bool) ([]*yaml.Node, error) {
	if len(raw) > yamlnode.MaxDocumentBytes {
		return nil, &model.Mistake{File: file, Message: "configuration exceeds 4 MiB"}
	}
	var documents []*yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var document yaml.Node
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return documents, nil
		}
		if err != nil {
			return nil, &model.Mistake{File: file, Message: err.Error()}
		}
		if len(document.Content) == 0 {
			continue
		}
		root := document.Content[0]
		if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
			continue
		}
		if root.Kind != yaml.MappingNode {
			return nil, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: a document is a map", root.Line)}
		}
		if err := strict(root, noAliases); err != nil {
			return nil, &model.Mistake{File: file, Message: err.Error()}
		}
		documents = append(documents, root)
	}
}

func strict(node *yaml.Node, noAliases bool) error {
	if noAliases && (node.Kind == yaml.AliasNode || node.Anchor != "") {
		return fmt.Errorf("line %d: anchors and aliases are not read; write the value out", node.Line)
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if seen[key.Value] {
				return fmt.Errorf("line %d: %s is set twice", key.Line, key.Value)
			}
			seen[key.Value] = true
		}
	}
	for _, each := range node.Content {
		if err := strict(each, noAliases); err != nil {
			return err
		}
	}
	return nil
}
