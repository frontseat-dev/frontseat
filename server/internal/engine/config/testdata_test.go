package config_test

import (
	"context"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

// A catalog file beneath a testdata directory is a fixture, as every
// plugin reads it, and no part of the repository's configuration: one
// that would be refused is passed by there. The control is the same file
// beside it, which is read, and refused.
func TestACatalogFileBeneathTestdataIsAFixture(t *testing.T) {
	refused := "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: fixture\nspec:\n  type: service\n  frontseat:\n    plugin:\n      tool:\n        strict: true\n"
	at := func(path string) error {
		_, err := config.Read(context.Background(), "repo", files{config.File: "plugins:\n  tool:\n", path: refused})
		return err
	}
	if err := at("service/testdata/fixture/catalog-info.yaml"); err != nil {
		t.Errorf("a catalog file beneath testdata was read: %v", err)
	}
	if err := at("service/fixture/catalog-info.yaml"); err == nil {
		t.Error("the control: a catalog file beside it was not read")
	}
}
