package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/internal/yamlnode"
	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// Release is what one entity's catalog file says of how it is released:
// spec.frontseat.release. A setting it does not say is inherited, or is
// the default; which is for whoever resolves it to say.
type Release struct {
	// File is the catalog file it is written in, relative to the
	// workspace.
	File string
	// Entity is the entity that says it.
	Entity entity.Ref
	// Settings are the settings it says, by key, each as it is written.
	Settings map[string]string
}

// The settings of spec.frontseat.release that are read.
const (
	ReleaseGrouping   = "grouping"
	ReleaseTag        = "tag"
	ReleaseInitial    = "initial"
	ReleaseVersioning = "versioning"
	ReleaseBranching  = "branching"
)

// GroupingSynced and GroupingIndependent are what grouping may say.
const (
	GroupingSynced      = "synced"
	GroupingIndependent = "independent"
)

// releaseKeys are the settings read; any other is refused.
var (
	releaseKeys = map[string]bool{ReleaseGrouping: true, ReleaseTag: true, ReleaseInitial: true, ReleaseVersioning: true, ReleaseBranching: true}
	strategy    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*:[a-z0-9][a-z0-9-]*$`)
	placeholder = regexp.MustCompile(`\{[^{}]*\}`)
)

// readRelease reads spec.frontseat.release of one document of a catalog
// file. Each setting is text, written directly, and is checked as far as
// it can be alone: what it means beside other entities' is for whoever
// resolves them.
func readRelease(file string, document, node *yaml.Node) (Release, error) {
	mistake := func(line int, format string, a ...any) (Release, error) {
		return Release{}, &model.Mistake{File: file, Message: fmt.Sprintf(format, a...) + fmt.Sprintf(" (line %d)", line)}
	}
	if node.Kind != yaml.MappingNode {
		return mistake(node.Line, "spec.frontseat.release is a map of settings: %s", keysOf(releaseKeys))
	}
	// The entity is named as the catalog names it, in the grammar every
	// entity reference has: what that refuses is refused here, as it is.
	kind, namespace, name := "", "", ""
	if at := yamlnode.Child(document, "kind"); at != nil {
		kind = at.Value
	}
	if at := yamlnode.Child(yamlnode.Child(document, "metadata"), "name"); at != nil {
		name = at.Value
	}
	if at := yamlnode.Child(yamlnode.Child(document, "metadata"), "namespace"); at != nil {
		namespace = at.Value
	}
	if kind == "" || name == "" {
		return mistake(node.Line, "spec.frontseat.release is said by an entity, which has a kind and a name")
	}
	entity := entity.New(kind, namespace, name)
	if err := entity.Validate(); err != nil {
		return mistake(node.Line, "spec.frontseat.release is said by an entity whose name is none: %v", err)
	}
	out := Release{File: file, Entity: entity, Settings: map[string]string{}}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		switch {
		case !releaseKeys[key.Value]:
			return mistake(key.Line, "spec.frontseat.release.%s is no release setting; it takes %s", key.Value, keysOf(releaseKeys))
		case said.Kind != yaml.ScalarNode || said.ShortTag() != "!!str":
			return mistake(said.Line, "spec.frontseat.release.%s is text", key.Value)
		}
		value := said.Value
		switch key.Value {
		case ReleaseGrouping:
			if value != GroupingSynced && value != GroupingIndependent {
				return mistake(said.Line, "spec.frontseat.release.grouping must be synced or independent, and is %q", value)
			}
		case ReleaseTag:
			if err := Tag(value); err != nil {
				return mistake(said.Line, "spec.frontseat.release.tag %s %v", value, err)
			}
		case ReleaseInitial:
			version, err := semver.StrictNewVersion(value)
			if err != nil {
				return mistake(said.Line, "spec.frontseat.release.initial: %s is no semantic version", value)
			}
			if version.Prerelease() != "" || version.Metadata() != "" {
				return mistake(said.Line, "spec.frontseat.release.initial: %s is a major, a minor and a patch, and nothing after them", value)
			}
		case ReleaseVersioning, ReleaseBranching:
			if !strategy.MatchString(value) {
				return mistake(said.Line, "spec.frontseat.release.%s %s must be <plugin>:<name>", key.Value, value)
			}
		}
		out.Settings[key.Value] = value
	}
	return out, nil
}

// Tag holds a tag pattern to what a tag is spelled from: {version}
// once, and nothing named beside it but {unit}. Whether what it spells
// is a name the version control system takes is that system's to say,
// of the tag itself, when a release is planned.
func Tag(pattern string) error {
	if strings.Count(pattern, "{version}") != 1 {
		return fmt.Errorf("must hold {version} once")
	}
	for _, named := range placeholder.FindAllString(pattern, -1) {
		if named != "{version}" && named != "{unit}" {
			return fmt.Errorf("names %s; a tag names {unit} and {version} and nothing else", named)
		}
	}
	return nil
}
