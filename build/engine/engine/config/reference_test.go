package config_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// reference is the page that says everything a repository writes for
// Frontseat, from the repository's root.
const reference = "docs/configuration.md"

// example is one fenced YAML example of a page: the file its first
// line names, in a comment, and the line of the page it begins at.
type example struct {
	file, text string
	line       int
}

// examplesOf returns the fenced YAML examples of a page. Each begins
// with a comment that names the file it is an example of. A fence of
// another language, or one indented, is refused: an example in it would
// be read by no test.
func examplesOf(page string) ([]example, error) {
	var out []example
	lines := strings.Split(page, "\n")
	for i := 0; i < len(lines); i++ {
		if lines[i] != "```yaml" {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				return nil, fmt.Errorf("line %d: an example is fenced ```yaml, at the start of its line", i+1)
			}
			continue
		}
		begins := i + 1
		for i++; i < len(lines) && lines[i] != "```"; i++ {
		}
		if i == len(lines) {
			return nil, fmt.Errorf("line %d: an example that does not end", begins)
		}
		file, named := strings.CutPrefix(lines[begins], "# ")
		if !named || (file != config.File && filepath.Base(file) != "catalog-info.yaml") {
			return nil, fmt.Errorf("line %d: an example begins with a comment naming its file, %s or a catalog-info.yaml", begins+1, config.File)
		}
		out = append(out, example{file: file, text: strings.Join(lines[begins:i], "\n") + "\n", line: begins + 1})
	}
	return out, nil
}

// refusedOf reads each example as a repository would hold it, a catalog
// file beside a frontseat.yaml that lists plugins, and returns what the
// reader refuses of them.
func refusedOf(t *testing.T, examples []example) error {
	t.Helper()
	var refused []error
	for _, each := range examples {
		repository := files{config.File: "plugins: {go: {}, backstage: {}}\n", each.file: each.text}
		if _, err := config.Read(t.Context(), "", repository); err != nil {
			refused = append(refused, fmt.Errorf("the example at line %d: %w", each.line, err))
		}
	}
	return errors.Join(refused...)
}

// Every example of the configuration reference is one the reader
// accepts: a page that showed what is refused would teach a mistake.
// The control is a page whose example says a key that is not taken,
// which the same reading refuses, at the example's line.
func TestEveryExampleOfTheConfigurationReferenceIsRead(t *testing.T) {
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(reference)))
	if err != nil {
		t.Fatal(err)
	}
	examples, err := examplesOf(string(page))
	if err != nil {
		t.Fatalf("%s: %v", reference, err)
	}
	said := map[string]bool{}
	for _, each := range examples {
		said[filepath.Base(each.file)] = true
	}
	if !said[config.File] || !said["catalog-info.yaml"] {
		t.Fatalf("%s holds %d examples, and none of %s or none of a catalog file", reference, len(examples), config.File)
	}
	if err := refusedOf(t, examples); err != nil {
		t.Errorf("%s shows what is refused:\n%v", reference, err)
	}

	control, err := examplesOf("# A page\n\n```yaml\n# catalog-info.yaml\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    platform: [linux/amd64]\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := refusedOf(t, control); err == nil || !strings.Contains(err.Error(), "the example at line 4") || !strings.Contains(err.Error(), "spec.frontseat takes no key platform") {
		t.Errorf("control, an example that says a key that is not taken: %v", err)
	}
	if _, err := examplesOf("```yaml\nplugins: {}\n```\n"); err == nil {
		t.Error("control, an example that does not name its file was taken")
	}
	for _, fenced := range []string{"```yml\n# frontseat.yaml\nplugins: {}\n```\n", "  ```yaml\n  # frontseat.yaml\n  plugins: {}\n  ```\n"} {
		if _, err := examplesOf(fenced); err == nil || !strings.Contains(err.Error(), "fenced ```yaml") {
			t.Errorf("control, an example no test would read was taken: %q: %v", fenced, err)
		}
	}
}

// What the configuration reference says is refused is refused by the
// reader the page describes: who sets a plugin's options, who says how
// it is released, and text written as a number. Each control is the
// same entity written as the page says it may be.
func TestWhatTheConfigurationReferenceRefusesIsRefused(t *testing.T) {
	entity := func(kind, frontseat string) files {
		return files{config.File: "plugins: {go: {}, backstage: {}}\n", "app/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: " + kind + "\nmetadata: {name: app}\nspec:\n  frontseat:\n" + frontseat}
	}
	for _, c := range []struct{ refused, allowed files }{
		{entity("System", "    plugins: {go: {test-shards: 2}}\n"), entity("Component", "    plugins: {go: {test-shards: 2}}\n")},
		{entity("API", "    release: {initial: 1.0.0}\n"), entity("Component", "    release: {initial: 1.0.0}\n")},
		{entity("Resource", "    service: {as: container, user: 70}\n"), entity("Resource", "    service: {as: container, user: \"70\"}\n")},
	} {
		if _, err := config.Read(t.Context(), "", c.refused); err == nil {
			t.Errorf("read, where the page says it is refused: %v", c.refused)
		}
		if _, err := config.Read(t.Context(), "", c.allowed); err != nil {
			t.Errorf("control: %v", err)
		}
	}
}
