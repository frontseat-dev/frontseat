package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	// ReleasePrerelease is how a prerelease is spelled after the
	// version's dash, and ReleasePrereleaseTag whether one is tagged.
	ReleasePrerelease    = "prerelease"
	ReleasePrereleaseTag = "prerelease-tag"
)

// GroupingSynced and GroupingIndependent are what grouping may say.
const (
	GroupingSynced      = "synced"
	GroupingIndependent = "independent"
)

// releaseKeys are the settings read; any other is refused.
var (
	releaseKeys = map[string]bool{ReleaseGrouping: true, ReleaseTag: true, ReleaseInitial: true, ReleaseVersioning: true, ReleaseBranching: true, ReleasePrerelease: true, ReleasePrereleaseTag: true}
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
		case key.Value == ReleasePrereleaseTag && said.Kind == yaml.ScalarNode && said.ShortTag() == "!!bool":
			// Whether a prerelease is tagged is true or false, as the
			// YAML reader reads them, and is kept as it reads it.
			var tagged bool
			if err := said.Decode(&tagged); err != nil {
				return mistake(said.Line, "spec.frontseat.release.prerelease-tag is true or false")
			}
			out.Settings[key.Value] = strconv.FormatBool(tagged)
			continue
		case key.Value == ReleasePrereleaseTag:
			return mistake(said.Line, "spec.frontseat.release.prerelease-tag is true or false")
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
		case ReleasePrerelease:
			// Read as far as it can be before there is a branch and a
			// commit: what it names is filled, and what it spells of an
			// ordinary branch at an ordinary commit is a prerelease.
			// A branch and a commit may each be written in digits alone,
			// and begin with a zero: a form that makes either a part of
			// its own is no prerelease for those, and is refused for all.
			for _, sample := range [][2]string{{"main", "0123abcdef0123abcdef0123abcdef0123abcdef"}, {"007", "0123456789012345678901234567890123456789"}} {
				if _, err := Prerelease(value, sample[0], sample[1], 1795256100); err != nil {
					return mistake(said.Line, "spec.frontseat.release.prerelease %v: a branch or a commit written in digits is a number, which begins with no zero; join it to another part with a dash", err)
				}
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

// branchAtMost is how much of a branch's name a prerelease is spelled
// with.
const branchAtMost = 64

// prereleaseFilled is what a prerelease's form names to be filled.
var prereleaseFilled = []string{"{branch}", "{timestamp}", "{epoch}", "{sha}"}

// Prerelease spells a prerelease, what follows a version's dash, as a
// form says it of a branch and the commit a release is planned at:
//
//   - {branch} is the branch, every character a semantic version does
//     not take in a prerelease made a dash, as far as its sixty-fourth:
//     feat/greet is feat-greet;
//   - {timestamp} is when the commit was made, in UTC, as
//     20261121101500: always as long, so that by its letters is by its
//     time;
//   - {epoch} is the same time in seconds;
//   - {sha} is the commit, as far as its seventh character.
//
// The time is the commit's and never the clock's: a commit has one
// prerelease wherever it is planned. What the form names beside these,
// and what it spells that is no prerelease of a semantic version, is
// refused, by the form and what it spelled.
func Prerelease(form, branch, revision string, committed int64) (string, error) {
	for _, named := range placeholder.FindAllString(form, -1) {
		known := false
		for _, each := range prereleaseFilled {
			known = known || each == named
		}
		if !known {
			return "", fmt.Errorf("%q names %s, which is not filled: it takes %s", form, named, strings.Join(prereleaseFilled, ", "))
		}
	}
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '-' {
			return r
		}
		return '-'
	}, branch)
	// A branch's name may be longer than a tag's may: as far as its
	// sixty-fourth character says which branch well enough, beside a
	// time, and leaves a tag spelled from it one a repository takes.
	if len(cleaned) > branchAtMost {
		cleaned = cleaned[:branchAtMost]
	}
	sha := revision
	if len(sha) > 7 {
		sha = sha[:7]
	}
	spelled := strings.NewReplacer(
		"{branch}", cleaned,
		"{timestamp}", time.Unix(committed, 0).UTC().Format("20060102150405"),
		"{epoch}", strconv.FormatInt(committed, 10),
		"{sha}", sha,
	).Replace(form)
	// A semantic version's own reader says whether it is a prerelease:
	// parts between dots, each letters, digits and dashes, a number
	// with no zero before it, and nothing after a plus.
	version, err := semver.StrictNewVersion("0.0.0-" + spelled)
	if err != nil || version.Prerelease() != spelled || version.Metadata() != "" {
		return "", fmt.Errorf("%q spells %q for %s, which is no prerelease of a semantic version", form, spelled, branch)
	}
	return spelled, nil
}
