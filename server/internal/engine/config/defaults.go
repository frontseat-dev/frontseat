package config

import (
	"fmt"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// Defaults is what frontseat.yaml says every Component of the
// repository has unless it says otherwise (defaults), under the keys an
// entity says each by. An entity that says one overrides it: its
// platforms replace the default's; its tests are given the default's
// tools and its own, and the default's variables with its own over
// them; and a release setting it, its System or its Domain says wins
// over the default's.
type Defaults struct {
	// File is where they are said, relative to the workspace; empty
	// where nothing is.
	File string
	// Platforms are what is built ships for where no entity says; nil
	// where they are not said.
	Platforms []string
	// Test is what every Component's tests are given.
	Test TestDefaults
	// Release is each release setting by its key, as an entity's
	// Settings hold it. Grouping is each entity's own, and is no default.
	Release map[string]string
}

// TestDefaults is what every Component's tests are given.
type TestDefaults struct {
	Tools       []string
	Environment map[string]string
}

// Defaults returns what frontseat.yaml says every Component has unless
// it says otherwise.
func (r *Repository) Defaults() Defaults { return r.defaults }

// readDefaults reads frontseat.yaml's defaults: a map of platforms,
// test and release, each read as an entity's is.
func readDefaults(file string, node *yaml.Node) (Defaults, error) {
	if node.Kind != yaml.MappingNode {
		return Defaults{}, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: defaults is a map of platforms, release and test", node.Line)}
	}
	out := Defaults{File: file}
	var err error
	for at := 1; at < len(node.Content); at += 2 {
		key, said := node.Content[at-1], node.Content[at]
		switch key.Value {
		case "platforms":
			out.Platforms, err = readPlatforms(file, "defaults.platforms", said)
		case "release":
			if out.Release, err = readReleaseSettings("defaults.release", said, false); err != nil {
				err = &model.Mistake{File: file, Message: err.Error()}
			}
		case "test":
			out.Test, err = readTestDefaults(file, said)
		default:
			return Defaults{}, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: defaults takes no key %s; it takes platforms, release and test", key.Line, key.Value)}
		}
		if err != nil {
			return Defaults{}, err
		}
	}
	return out, nil
}

// readTestDefaults reads defaults.test: the tools and the variables
// every Component's tests are given.
func readTestDefaults(file string, node *yaml.Node) (TestDefaults, error) {
	mistake := func(err error) (TestDefaults, error) {
		return TestDefaults{}, &model.Mistake{File: file, Message: err.Error()}
	}
	if node.Kind != yaml.MappingNode {
		return mistake(fmt.Errorf("line %d: defaults.test is a map of environment and tools", node.Line))
	}
	var out TestDefaults
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		var err error
		switch key.Value {
		case "tools":
			out.Tools, err = testList("defaults.test.tools", said)
		case "environment":
			out.Environment, err = testEnvironment("defaults.test.environment", said)
		default:
			err = fmt.Errorf("line %d: defaults.test takes no key %s; it takes environment and tools", key.Line, key.Value)
		}
		if err != nil {
			return mistake(err)
		}
	}
	return out, nil
}
