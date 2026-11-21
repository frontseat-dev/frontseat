package controlplane

import (
	"context"
	"slices"
	"strings"
	"testing"

	credentialv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/forge"
)

// let is what a sandbox's rule for a host lets through, each request on
// a line, with the programs it is for and the credential it carries.
func let(rules []*sandboxv1.EgressRule, host string) string {
	for _, rule := range rules {
		if rule.GetHost() != host {
			continue
		}
		var asked []string
		for _, request := range rule.GetRequests() {
			asked = append(asked, request.GetMethod()+" "+request.GetPath())
		}
		return strings.Join(rule.GetPrograms(), ",") + " by " + rule.GetCredential() + ": " + strings.Join(asked, "; ")
	}
	return "no rule"
}

// A workspace made to push may publish the releases of the repositories
// it pushes to, and of no other: its sandbox is let through for what
// their forge asks of a release, by the program that asks it, with its
// owner's credential for the host asked. GitHub is asked at hosts of its
// own, each a rule; a forge asked where its repositories are fetched is
// let through by the rule there is for it. The controls are a workspace
// not made to push, a repository of a forge nobody told of, and a
// repository a catalog named and the workspace was not asked for by.
func TestAWorkspaceMadeToPushMayPublishItsRepositorysReleasesAndNoOther(t *testing.T) {
	s := over(t)
	for _, host := range []string{"github.com:443", "forge.example:443"} {
		s.keeps("local", host)
	}
	// GitHub's credential, as it is kept for the hosts GitHub is asked
	// at beside its own; and one its owner gave for one of those hosts
	// themselves, which is another credential, and not the one used.
	s.mu.Lock()
	for _, beside := range []string{"api.github.com:443", "uploads.github.com:443"} {
		s.kept[besideID("local", "github.com:443", beside)] = beside
	}
	s.mu.Unlock()
	s.keeps("local", "api.github.com:443")
	known, err := forge.GitHub("https://forge.example")
	if err != nil {
		t.Fatal(err)
	}
	client, _, _ := served(t, s, t.TempDir(), func(c *Config) { c.Releasing = known })
	ctx := context.Background()
	made := func(name string, push bool, remotes ...string) []*sandboxv1.EgressRule {
		t.Helper()
		spec := of(remotes...)
		spec.Push = push
		if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: spec}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return s.sandbox(s.of(waited(t, client, name).GetName())).GetEgress().GetRules()
	}
	fetch := "GET /acme/code/refs; POST /acme/code/a-fetch"
	releases := func(under, above string) string {
		return "GET " + under + "/repos/acme/code/releases; POST " + under + "/repos/acme/code/releases; PATCH " + under + "/repos/acme/code/releases/*" + above
	}
	// GitHub: its repositories are fetched from one host and its
	// releases asked of two others.
	github := made("to-github", true, "https://github.com/acme/code")
	for host, want := range map[string]string{
		"github.com:443":         "/usr/bin/a-fetcher by " + credentialID("local", "github.com:443") + ": " + fetch + "; POST /acme/code/a-push",
		"api.github.com:443":     forge.Program + " by " + besideID("local", "github.com:443", "api.github.com:443") + ": " + releases("", ""),
		"uploads.github.com:443": forge.Program + " by " + besideID("local", "github.com:443", "uploads.github.com:443") + ": POST /repos/acme/code/releases/*/assets",
	} {
		if got := let(github, host); got != want {
			t.Errorf("a workspace that pushes to GitHub is let through to %s as\n%s\nwant\n%s", host, got, want)
		}
	}
	// A forge of its kind, told of: asked where it is fetched, by the
	// rule there is for that host, for both programs.
	told := made("to-a-forge", true, "https://forge.example/acme/code")
	if got, want := let(told, "forge.example:443"), "/usr/bin/a-fetcher,"+forge.Program+" by "+credentialID("local", "forge.example:443")+": "+fetch+"; POST /acme/code/a-push; "+releases("/api/v3", "; POST /api/uploads/repos/acme/code/releases/*/assets"); got != want {
		t.Errorf("a workspace that pushes to a forge told of is let through as\n%s\nwant\n%s", got, want)
	}
	// Controls: not made to push; a forge nobody told of.
	if got, want := let(made("fetching", false, "https://github.com/acme/code"), "github.com:443"), "/usr/bin/a-fetcher by "+credentialID("local", "github.com:443")+": "+fetch; got != want {
		t.Errorf("a workspace not made to push is let through as\n%s\nwant\n%s", got, want)
	}
	if rules := made("fetching-more", false, "https://github.com/acme/code"); let(rules, "api.github.com:443") != "no rule" || let(rules, "uploads.github.com:443") != "no rule" {
		t.Errorf("a workspace not made to push reaches a forge's releases: %v", rules)
	}
	other := made("to-another", true, "https://other.example/acme/code")
	if got := let(other, "other.example:443"); strings.Contains(got, "releases") || strings.Contains(got, forge.Program) {
		t.Errorf("a repository of a forge nobody told of is let through as %s", got)
	}
	// A workspace that also fetches from a host its forge is asked at
	// for a release is refused: that host's rule is the fetch's, with
	// whatever credential its owner gave for it, and no release is
	// asked through it.
	both := of("https://github.com/acme/code", "https://api.github.com/acme/else")
	both.Push = true
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "both", Spec: both}); err == nil || !strings.Contains(err.Error(), "which a repository of this workspace is fetched from") {
		t.Errorf("a workspace that fetches from a host its forge is asked at: %v, want it refused", err)
	}
	// A control plane told of no forge lets none be published to.
	none, _, _ := served(t, s, t.TempDir(), nil)
	spec := of("https://github.com/acme/code")
	spec.Push = true
	if _, err := none.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "no-forge", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if rules := s.sandbox(s.of(waited(t, none, "no-forge").GetName())).GetEgress().GetRules(); let(rules, "api.github.com:443") != "no rule" {
		t.Errorf("a control plane told of no forge lets a workspace reach one: %v", rules)
	}
}

// A person's credential for a forge is kept for the hosts the forge is
// asked at too, under names of its own, and is let go of with them: it
// is given once, and listed once, as the host it was given for. One the
// person gave for one of those hosts themselves is another credential:
// it is neither replaced nor let go of with the forge's. And one still
// in use is let go of in no part.
func TestACredentialForAForgeIsKeptForTheHostsItIsAskedAt(t *testing.T) {
	s := over(t)
	_, credentials := servedToPeopleWith(t, s, func(c *Config) { c.Releasing = forge.GitHubAlone() })
	ctx := context.Background()
	set := func(host string) {
		t.Helper()
		if _, err := credentials(owner).SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: &credentialv1.Credential{Host: host, Username: "ada", Secret: "adas-secret"}}); err != nil {
			t.Fatal(err)
		}
	}
	// Under which names credentials are kept, each with its host.
	kept := func() []string {
		s.mu.Lock()
		defer s.mu.Unlock()
		named := map[string]string{
			credentialID("ada", "github.com:443"):                       "github",
			credentialID("ada", "api.github.com:443"):                   "api, given",
			credentialID("ada", "forge.example:443"):                    "forge",
			besideID("ada", "github.com:443", "api.github.com:443"):     "api, beside github",
			besideID("ada", "github.com:443", "uploads.github.com:443"): "uploads, beside github",
		}
		var out []string
		for name := range s.kept {
			out = append(out, named[name])
		}
		slices.Sort(out)
		return out
	}
	// A credential she gave for GitHub's own other host, before.
	set("api.github.com")
	set("github.com")
	set("forge.example")
	if got, want := kept(), []string{"api, beside github", "api, given", "forge", "github", "uploads, beside github"}; !slices.Equal(got, want) {
		t.Errorf("credentials are kept as %v, want %v", got, want)
	}
	hosts, err := listedFor(t, credentials(owner))
	if want := []string{"api.github.com:443", "forge.example:443", "github.com:443"}; err != nil || !slices.Equal(hosts, want) {
		t.Errorf("Ada's credentials are listed as %v (%v), want %v", hosts, err, want)
	}
	// In use, it is let go of in no part.
	s.mu.Lock()
	s.inUse[credentialID("ada", "github.com:443")] = true
	s.mu.Unlock()
	if _, err := credentials(owner).DeleteCredential(ctx, &credentialv1.DeleteCredentialRequest{Host: "github.com"}); err == nil || !strings.Contains(err.Error(), "still let through") {
		t.Errorf("letting go of a credential in use: %v, want it refused", err)
	}
	if got, want := kept(), []string{"api, beside github", "api, given", "forge", "github", "uploads, beside github"}; !slices.Equal(got, want) {
		t.Errorf("after a refused letting go, credentials are kept as %v, want %v", got, want)
	}
	s.mu.Lock()
	delete(s.inUse, credentialID("ada", "github.com:443"))
	s.mu.Unlock()
	if _, err := credentials(owner).DeleteCredential(ctx, &credentialv1.DeleteCredentialRequest{Host: "github.com"}); err != nil {
		t.Fatal(err)
	}
	// What she gave for the other host herself is hers still.
	if got, want := kept(), []string{"api, given", "forge"}; !slices.Equal(got, want) {
		t.Errorf("after GitHub's is let go of, credentials are kept as %v, want %v", got, want)
	}
}
