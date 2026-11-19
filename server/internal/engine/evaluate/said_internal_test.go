package evaluate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
)

// counted is a source of one repository, held in memory, that counts
// the files read for their content.
type counted struct {
	files map[string]string
	read  int
}

func (c *counted) Repositories() []string { return []string{""} }

func (c *counted) Read(_ context.Context, _, path string) ([]byte, bool, error) {
	c.read++
	content, found := c.files[path]
	return []byte(content), found, nil
}

func (c *counted) Digest(_ context.Context, _, path string) (string, bool, error) {
	content, found := c.files[path]
	if !found {
		return "", false, nil
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:]), true, nil
}

func (c *counted) Within(_ context.Context, _, directory string, patterns []string) ([]string, error) {
	return pluginhosttest.Files(c.files).Within(directory, patterns), nil
}

const componentFile = "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: a\nspec:\n  frontseat:\n    actions:\n      check:\n        command: [\"true\"]\n"

// What a repository says of how it is built is read once, and kept for
// as long as the files that say it are the same: a daemon does not read
// and parse every catalog file of a workspace for every build. A file
// that changes, and one that comes, have it read again.
func TestWhatARepositorySaysIsReadAgainOnlyWhenItChanges(t *testing.T) {
	ctx := context.Background()
	source := &counted{files: map[string]string{"frontseat.yaml": "plugins: {}\n", "a/catalog-info.yaml": componentFile, "mise.toml": "[tools]\ngo = \"1.27.3\"\n"}}
	answers := &Answers{}
	said := func() (actions int, tools map[string]string) {
		t.Helper()
		answers.begin()
		repository, pinned, err := answers.said(ctx, repositoryView{source: source, repository: ""})
		if err != nil {
			t.Fatal(err)
		}
		return len(repository.Actions()), pinned
	}

	if actions, tools := said(); actions != 1 || tools["go"] != "1.27.3" || source.read == 0 {
		t.Fatalf("read %d files, found %d actions and tools %v; want the repository read", source.read, actions, tools)
	}

	source.read = 0
	if actions, tools := said(); actions != 1 || tools["go"] != "1.27.3" || source.read != 0 {
		t.Errorf("with nothing changed, read %d files, found %d actions and tools %v; want nothing read and the same said", source.read, actions, tools)
	}

	source.files["a/catalog-info.yaml"] = componentFile + "      more:\n        command: [\"true\"]\n"
	if actions, _ := said(); actions != 2 || source.read == 0 {
		t.Errorf("with a catalog file changed, read %d files and found %d actions; want it read again, and both its actions", source.read, actions)
	}

	source.read = 0
	source.files["b/catalog-info.yaml"] = componentFile
	if actions, _ := said(); actions != 3 || source.read == 0 {
		t.Errorf("with a catalog file come, read %d files and found %d actions; want it read, and its action", source.read, actions)
	}

	source.read = 0
	source.files["mise.toml"] = "[tools]\ngo = \"1.28.0\"\n"
	if _, tools := said(); tools["go"] != "1.28.0" {
		t.Errorf("with the pins changed, tools are %v; want what they pin now", tools)
	}
}
