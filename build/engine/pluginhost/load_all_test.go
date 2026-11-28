package pluginhost_test

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// fresh is a module of a digest of its own, which the process has not
// compiled: a custom section of random bytes, which wasm ignores.
func fresh(t *testing.T, name string) []byte {
	t.Helper()
	base, err := testmodule.Plugin(name)
	if err != nil {
		t.Fatal(err)
	}
	return append(append(append([]byte{}, base...), 0, 21, 4, 'l', 'o', 'a', 'd'), rand.Text()[:16]...)
}

// Plugins loaded together are the ones given, in the order given,
// whichever compiled first: a later one of a name is the one asked. The
// largest is given first, so it is not the first done.
func TestPluginsLoadedTogetherAreInTheOrderGiven(t *testing.T) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	names := []string{"backstage", "github", "go", "postgres", "yarn"}
	var modules [][]byte
	for _, name := range names {
		modules = append(modules, fresh(t, name))
	}
	loaded, err := host.LoadAll(ctx, modules)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, plugin := range loaded {
		got = append(got, plugin.Description.GetName())
	}
	if strings.Join(got, " ") != strings.Join(names, " ") {
		t.Fatalf("loaded %v; want %v, in the order given", got, names)
	}
}

// One module that does not load fails them all, by its own refusal, and
// what did load is let go of. The control: the same modules without it
// load.
func TestPluginsLoadedTogetherFailByTheOneThatDoesNotLoad(t *testing.T) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	good := [][]byte{fresh(t, "go"), fresh(t, "github")}
	if _, err := host.LoadAll(ctx, good); err != nil {
		t.Fatalf("the control: %v", err)
	}
	_, err = host.LoadAll(ctx, [][]byte{good[0], []byte("no module"), good[1]})
	if err == nil || !strings.Contains(err.Error(), "compiling") {
		t.Fatalf("a module that is none was not refused as one that does not compile: %v", err)
	}
}
