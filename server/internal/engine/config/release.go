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

// KindDomain and KindSystem are the kinds of the groups a Component is
// released with, as a reference writes a kind.
const (
	KindDomain = "domain"
	KindSystem = "system"
)

// releaseKeys are the settings read; any other is refused, with
// releaseSettings, which names them.
var (
	releaseKeys     = map[string]bool{ReleaseGrouping: true, ReleaseTag: true, ReleaseInitial: true, ReleaseVersioning: true, ReleaseBranching: true, ReleasePrerelease: true, ReleasePrereleaseTag: true}
	releaseSettings = strings.Join(slices.Sorted(maps.Keys(releaseKeys)), ", ")
	strategy        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*:[a-z0-9][a-z0-9-]*$`)
	placeholder     = regexp.MustCompile(`\{[^{}]*\}`)
)

// readRelease reads spec.frontseat.release of one document of a catalog
// file. Each setting is text, written directly, and is checked as far as
// it can be alone: what it means beside other entities' is for whoever
// resolves them.
func readRelease(file string, ref entity.Ref, node *yaml.Node) (Release, error) {
	mistake := func(line int, format string, a ...any) (Release, error) {
		return Release{}, &model.Mistake{File: file, Message: fmt.Sprintf(format, a...) + fmt.Sprintf(" (line %d)", line)}
	}
	if node.Kind != yaml.MappingNode {
		return mistake(node.Line, "spec.frontseat.release is a map of settings: %s", releaseSettings)
	}
	// What is released is a Component, alone or with its group, and a
	// group is a System or a Domain: on any other kind a setting would
	// be read and never used.
	if !slices.Contains([]string{KindDomain, KindSystem, entity.KindComponent}, ref.Kind) {
		return mistake(node.Line, "spec.frontseat.release is said by a Domain, a System or a Component")
	}
	out := Release{File: file, Entity: ref, Settings: map[string]string{}}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		switch {
		case !releaseKeys[key.Value]:
			return mistake(key.Line, "spec.frontseat.release.%s is no release setting; it takes %s", key.Value, releaseSettings)
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
				return mistake(said.Line, "spec.frontseat.release.grouping must be synced or independent")
			}
		case ReleaseTag:
			if err := Tag(value); err != nil {
				return mistake(said.Line, "spec.frontseat.release.tag %v", err)
			}
		case ReleaseInitial:
			version, err := semver.StrictNewVersion(value)
			if err != nil {
				return mistake(said.Line, "spec.frontseat.release.initial is no semantic version: it is a major, a minor and a patch, as 0.1.0")
			}
			if version.Prerelease() != "" || version.Metadata() != "" {
				return mistake(said.Line, "spec.frontseat.release.initial is a major, a minor and a patch, and nothing after them")
			}
		case ReleaseVersioning, ReleaseBranching:
			if !strategy.MatchString(value) {
				return mistake(said.Line, "spec.frontseat.release.%s must be <plugin>:<name>", key.Value)
			}
		case ReleasePrerelease:
			// Read as far as it can be before there is a branch and a
			// commit: what it names is filled, and what it spells of an
			// ordinary branch at an ordinary commit is a prerelease.
			// A branch and a commit may each be written in digits alone,
			// and begin with a zero: a form that makes either a part of
			// its own is no prerelease for those, and is refused for all.
			if _, err := Prerelease(value, "main", "0123abcdef0123abcdef0123abcdef0123abcdef", 1795256100); err != nil {
				return mistake(said.Line, "spec.frontseat.release.prerelease %v", err)
			}
			if _, err := Prerelease(value, "007", "0123456789012345678901234567890123456789", 1795256100); err != nil {
				return mistake(said.Line, "spec.frontseat.release.prerelease %v: a branch or a commit written in digits is a number, which begins with no zero; join it to another part with a dash", err)
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
