package config

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
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

// The settings of spec.frontseat.release that are read, each by the
// key it is written under; a setting of the prerelease map by
// "prerelease." and its key.
const (
	ReleaseGrouping       = "grouping"
	ReleaseTag            = "tag"
	ReleaseInitialVersion = "initial-version"
	ReleaseVersioning     = "versioning"
	ReleaseBranching      = "branching"
	// ReleasePrereleaseFormat is how a prerelease is spelled after the
	// version's dash, and ReleasePrereleaseTagged whether one is tagged.
	ReleasePrereleaseFormat = "prerelease.format"
	ReleasePrereleaseTagged = "prerelease.tagged"
)

// releasePrerelease is the key of the map of a prerelease's settings.
const releasePrerelease = "prerelease"

// GroupingSynced and GroupingIndependent are what grouping may say.
const (
	GroupingSynced      = "synced"
	GroupingIndependent = "independent"
)

// KindDomain and KindSystem are the kinds of the groups a Component is
// released with, as a reference writes a kind.
const (
	KindDomain = "domain"
	KindSystem = "system"
)

// releaseKeys are the settings read; any other is refused, with
// releaseSettings, which names them.
var (
	releaseKeys     = map[string]bool{ReleaseGrouping: true, ReleaseTag: true, ReleaseInitialVersion: true, ReleaseVersioning: true, ReleaseBranching: true, releasePrerelease: true}
	releaseSettings = strings.Join(slices.Sorted(maps.Keys(releaseKeys)), ", ")
	strategy        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*:[a-z0-9][a-z0-9-]*$`)
	placeholder     = regexp.MustCompile(`\{[^{}]*\}`)
)

// readRelease reads spec.frontseat.release of one document of a catalog
// file. Each setting is text, written directly, and is checked as far as
// it can be alone: what it means beside other entities' is for whoever
// resolves them.
func readRelease(file string, ref entity.Ref, node *yaml.Node) (Release, error) {
	if node.Kind != yaml.MappingNode {
		return Release{}, &model.Mistake{File: file, Message: fmt.Sprintf("spec.frontseat.release is a map of settings: %s (line %d)", releaseSettings, node.Line)}
	}
	// What is released is a Component, alone or with its group, and a
	// group is a System or a Domain: on any other kind a setting would
	// be read and never used.
	if !slices.Contains([]string{KindDomain, KindSystem, entity.KindComponent}, ref.Kind) {
		return Release{}, &model.Mistake{File: file, Message: fmt.Sprintf("spec.frontseat.release is said by a Domain, a System or a Component (line %d)", node.Line)}
	}
	settings, err := readReleaseSettings("spec.frontseat.release", node, true)
	if err != nil {
		return Release{}, &model.Mistake{File: file, Message: err.Error()}
	}
	return Release{File: file, Entity: ref, Settings: settings}, nil
}

// readReleaseSettings reads a map of release settings, as a catalog
// file's release or frontseat.yaml's defaults say them; a refusal names
// each by what it is under (at) and says its line. Grouping is read
// where it may be said: it is each entity's own, and no default.
func readReleaseSettings(at string, node *yaml.Node, grouping bool) (map[string]string, error) {
	mistake := func(line int, format string, a ...any) (map[string]string, error) {
		return nil, fmt.Errorf("%s (line %d)", fmt.Sprintf(format, a...), line)
	}
	if node.Kind != yaml.MappingNode {
		return mistake(node.Line, "%s is a map of settings: %s", at, releaseSettings)
	}
	out := map[string]string{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		switch {
		case !releaseKeys[key.Value]:
			return mistake(key.Line, "%s.%s is no release setting; it takes %s", at, key.Value, releaseSettings)
		case key.Value == ReleaseGrouping && !grouping:
			return mistake(key.Line, "%s.grouping is no default: whether a group is synced is each entity's own", at)
		case key.Value == releasePrerelease:
			if err := readPrerelease(at, said, out); err != nil {
				return nil, err
			}
			continue
		case said.Kind != yaml.ScalarNode || said.ShortTag() != "!!str":
			return mistake(said.Line, "%s.%s is text", at, key.Value)
		}
		value := said.Value
		switch key.Value {
		case ReleaseGrouping:
			if value != GroupingSynced && value != GroupingIndependent {
				return mistake(said.Line, "%s.grouping must be synced or independent", at)
			}
		case ReleaseTag:
			if err := Tag(value); err != nil {
				return mistake(said.Line, "%s.tag %v", at, err)
			}
		case ReleaseInitialVersion:
			version, err := semver.StrictNewVersion(value)
			if err != nil {
				return mistake(said.Line, "%s.initial-version is no semantic version: it is a major, a minor and a patch, as 0.1.0", at)
			}
			if version.Prerelease() != "" || version.Metadata() != "" {
				return mistake(said.Line, "%s.initial-version is a major, a minor and a patch, and nothing after them", at)
			}
		case ReleaseVersioning, ReleaseBranching:
			if !strategy.MatchString(value) {
				return mistake(said.Line, "%s.%s must be <plugin>:<name>", at, key.Value)
			}
		}
		out[key.Value] = value
	}
	return out, nil
}

// readPrerelease reads spec.frontseat.release.prerelease, a map of
// format, how a prerelease is spelled, and tagged, whether one is
// tagged, into settings, each under "prerelease." and its key.
func readPrerelease(at string, node *yaml.Node, settings map[string]string) error {
	wrong := func(line int, format string, a ...any) error {
		return fmt.Errorf("%s (line %d)", fmt.Sprintf(format, a...), line)
	}
	if node.Kind != yaml.MappingNode {
		return wrong(node.Line, "%s.prerelease is a map of format and tagged", at)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "tagged":
			// Whether a prerelease is tagged is true or false, as the
			// YAML reader reads them, and is kept as it reads it.
			var tagged bool
			if said.Kind != yaml.ScalarNode || said.ShortTag() != "!!bool" || said.Decode(&tagged) != nil {
				return wrong(said.Line, "%s.prerelease.tagged is true or false", at)
			}
			settings[ReleasePrereleaseTagged] = strconv.FormatBool(tagged)
		case "format":
			form, ok := text(said)
			if !ok {
				return wrong(said.Line, "%s.prerelease.format is text", at)
			}
			// Read as far as it can be before there is a branch and a
			// commit: what it names is filled, and what it spells of an
			// ordinary branch at an ordinary commit is a prerelease.
			// A branch and a commit may each be written in digits alone,
			// and begin with a zero: a form that makes either a part of
			// its own is no prerelease for those, and is refused for all.
			if _, err := Prerelease(form, "main", "0123abcdef0123abcdef0123abcdef0123abcdef", 1795256100); err != nil {
				return wrong(said.Line, "%s.prerelease.format %v", at, err)
			}
			if _, err := Prerelease(form, "007", "0123456789012345678901234567890123456789", 1795256100); err != nil {
				return wrong(said.Line, "%s.prerelease.format %v: a branch or a commit written in digits is a number, which begins with no zero; join it to another part with a dash", at, err)
			}
			settings[ReleasePrereleaseFormat] = form
		default:
			return wrong(key.Line, "%s.prerelease.%s is no prerelease setting; it takes format and tagged", at, key.Value)
		}
	}
	return nil
}

// Tag holds a tag pattern to what a tag is spelled from: {version}
// once, and nothing named beside it but {unit}. Whether what it spells
// is a name the version control system takes is that system's to say,
// of the tag itself, when a release is planned.
func Tag(pattern string) error {
	if strings.Count(pattern, "{version}") != 1 {
		return errors.New("must hold {version} once")
	}
	for _, named := range placeholder.FindAllString(pattern, -1) {
		if named != "{version}" && named != "{unit}" {
			return errors.New("names something other than {unit} and {version}, which are all a tag names")
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
// refused, by what a form takes and never by what was written.
func Prerelease(form, branch, revision string, committed int64) (string, error) {
	for _, named := range placeholder.FindAllString(form, -1) {
		known := false
		for _, each := range prereleaseFilled {
			known = known || each == named
		}
		if !known {
			return "", fmt.Errorf("names what is not filled: it takes %s", strings.Join(prereleaseFilled, ", "))
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
		return "", errors.New("spells what is no prerelease of a semantic version, whose parts are letters, digits and dashes joined by dots, a part of digits alone beginning with no zero")
	}
	return spelled, nil
}
