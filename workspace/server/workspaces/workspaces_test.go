package workspaces

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/workspace/server/workspaces/sourcestest"
)

// A control plane given what serves stored repositories is composed
// with it, and its gateway is asked to bind the directories that come
// of it into its workspaces' sandboxes. One given none is composed with
// none, so that it refuses a remote workspace by name, and its gateway
// binds nothing of its machine into any sandbox. What it is told
// besides is told the same either way.
func TestAControlPlaneIsComposedWithWhatServesStoredRepositoriesOrNone(t *testing.T) {
	gateway := Gateway{Address: "https://127.0.0.1:8090", Identity: "/an/identity", Runtime: "podman"}
	config := Config{State: "/a/state", Box: "registry.example/acme/box:1", Grid: Grid{Hosts: []string{"grid.example:8980"}, Arguments: []string{"--grid=grid.example:8980"}}}
	held := sourcestest.New("/var/lib/a-store/mounted")
	remote, over := composed("http://127.0.0.1:7700", gateway, config, []Option{WithSources(held)})
	if remote.Sources != held || !over.HostVolumes {
		t.Errorf("given what serves stored repositories, it is composed with %v and its gateway binds directories: %v", remote.Sources, over.HostVolumes)
	}
	local, plain := composed("http://127.0.0.1:7700", gateway, config, nil)
	if local.Sources != nil || plain.HostVolumes {
		t.Errorf("given nothing that serves stored repositories, it is composed with %v and its gateway binds directories: %v", local.Sources, plain.HostVolumes)
	}
	for _, told := range []struct{ got, want string }{
		{local.PublicURL, "http://127.0.0.1:7700"}, {local.State, "/a/state"}, {local.Box, config.Box},
		{strings.Join(local.Grid.Hosts, " "), "grid.example:8980"}, {strings.Join(local.Grid.Arguments, " "), "--grid=grid.example:8980"},
		{plain.Address, gateway.Address}, {plain.Identity, gateway.Identity}, {plain.Runtime, "podman"},
		{remote.State, local.State}, {over.Address, plain.Address},
	} {
		if told.got != told.want {
			t.Errorf("it is told %q, want %q", told.got, told.want)
		}
	}
	if local.Fetching == nil || local.Entities != nil {
		t.Errorf("it is given the version control system %v and the catalog %v, want the one chosen and none", local.Fetching, local.Entities)
	}
}

// A control plane is made for an owner it knows, over a gateway that
// answers it: where its owner's token has no file of its own to be in,
// or its gateway does not answer, none is made. The owner's token is
// minted before the gateway is asked. The control, one that is made over
// the local stack's gateway, is the server's own
// (TestWithTheGatewayThatKnowsItTheServerStarts).
func TestNoControlPlaneIsMadeWithoutAnOwnerOrAGatewayThatAnswers(t *testing.T) {
	nowhere := Gateway{Address: "https://127.0.0.1:1", Identity: t.TempDir(), Runtime: "docker"}
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New("http://127.0.0.1:7700", nowhere, Config{State: t.TempDir(), OwnerToken: filepath.Join(file, "token")}); err == nil || !strings.Contains(err.Error(), "the owner's token") {
		t.Errorf("with its owner's token beneath what is no directory: %v, want it not made, for the token", err)
	}
	token := filepath.Join(t.TempDir(), "owner", "token")
	if _, err := New("http://127.0.0.1:7700", nowhere, Config{State: t.TempDir(), OwnerToken: token}); err == nil || strings.Contains(err.Error(), "the owner's token") {
		t.Errorf("over a gateway that is not there: %v, want it not made, for the gateway", err)
	}
	if minted, err := os.ReadFile(token); err != nil || len(minted) < 64 {
		t.Errorf("its owner's token was not minted before the gateway was asked: %v", err)
	}
}
