package evaluate

import (
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

func starting(name string, as ...string) *pluginhost.Plugin {
	return &pluginhost.Plugin{Description: &pluginv1.DescribeResponse{Name: name, Services: as}}
}

// A name is one plugin's: two of a repository that claim it are refused
// by name, in either order, beside the same two claiming different ones.
func TestANameTwoPluginsOfARepositoryServiceIsRefused(t *testing.T) {
	claimed, err := claimants("repo", []*pluginhost.Plugin{starting("postgres", "postgres"), starting("container", "container"), starting("go")})
	if err != nil || len(claimed) != 2 || claimed["postgres"].Description.GetName() != "postgres" {
		t.Fatalf("two plugins that start different things: %v, %v", claimed, err)
	}
	for _, order := range [][]*pluginhost.Plugin{
		{starting("postgres", "postgres"), starting("timescale", "postgres")},
		{starting("timescale", "postgres"), starting("postgres", "postgres")},
	} {
		_, err := claimants("repo", order)
		if err == nil || !strings.Contains(err.Error(), "both start postgres") || !strings.Contains(err.Error(), "timescale") {
			t.Fatalf("two plugins that start one name: %v", err)
		}
	}
}

// What a service names that nothing services is refused before anything
// is asked, with what the repository's plugins do service, or that none
// does.
func TestAServiceAsWhatNoPluginStartsNamesWhatIs(t *testing.T) {
	written := config.Service{File: "catalog-info.yaml", Line: 9, Namespace: "default", Name: "db", As: "mainframe"}
	_, err := startedAs(t.Context(), &Answers{}, map[string]*pluginhost.Plugin{"postgres": starting("postgres", "postgres"), "container": starting("container", "container")}, written, &Evaluation{})
	if err == nil || !strings.Contains(err.Error(), "started as mainframe, which no plugin of the repository starts: container, postgres") {
		t.Fatalf("got %v", err)
	}
	_, err = startedAs(t.Context(), &Answers{}, nil, written, &Evaluation{})
	if err == nil || !strings.Contains(err.Error(), "none does") {
		t.Fatalf("got %v", err)
	}
}
