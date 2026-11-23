package conformance

import (
	"context"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// Load gives a test the real Wasm plugin and joins its lifetime to the test.
func Load(t testing.TB, module []byte) *pluginhost.Plugin {
	t.Helper()
	host, err := pluginhost.New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	plugin, err := host.Load(t.Context(), module)
	if err != nil {
		t.Fatal(err)
	}
	return plugin
}

// Plugin gives a test the plugin of this checkout by that name, e.g.
// "go", loaded as Load loads it.
func Plugin(t testing.TB, name string) *pluginhost.Plugin {
	t.Helper()
	module, err := testmodule.Plugin(name)
	if err != nil {
		t.Fatal(err)
	}
	return Load(t, module)
}

// SDKExample runs a published SDK example through the same host as a plugin.
func SDKExample(t testing.TB, name string) *pluginhost.Plugin {
	t.Helper()
	module, err := testmodule.SDKExample(name)
	if err != nil {
		t.Fatal(err)
	}
	return Load(t, module)
}
