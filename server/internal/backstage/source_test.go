package backstage

import "testing"

func TestASourceLocationIsReadAsItsForgeSpellsIt(t *testing.T) {
	cases := []struct {
		forge, location   string
		remote, ref, root string
	}{
		{"GitHub", "https://github.com/acme/platform/tree/main/gateway/", "https://github.com/acme/platform", "main", "gateway"},
		{"GitHub, a nested directory", "https://github.com/acme/platform/tree/release/services/gateway", "https://github.com/acme/platform", "release", "services/gateway"},
		{"GitHub, the repository's root", "https://github.com/acme/platform/tree/main/", "https://github.com/acme/platform", "main", ""},
		{"GitLab", "https://gitlab.com/acme/group/platform/-/tree/main/gateway", "https://gitlab.com/acme/group/platform", "main", "gateway"},
		{"GitLab, the old spelling", "https://gitlab.example.com/acme/platform/tree/main/gateway", "https://gitlab.example.com/acme/platform", "main", "gateway"},
		{"Gitea, a branch", "https://gitea.example.com/acme/platform/src/branch/main/gateway", "https://gitea.example.com/acme/platform", "main", "gateway"},
		{"Gitea, a tag", "https://gitea.example.com/acme/platform/src/tag/v1/gateway", "https://gitea.example.com/acme/platform", "v1", "gateway"},
		{"Bitbucket Cloud", "https://bitbucket.org/acme/platform/src/main/gateway/", "https://bitbucket.org/acme/platform", "main", "gateway"},
		{"Azure DevOps", "https://dev.azure.com/acme/shop/_git/platform?path=/gateway&version=GBmain", "https://dev.azure.com/acme/shop/_git/platform", "main", "gateway"},
		{"a repository and nothing more", "https://github.com/acme/platform/", "https://github.com/acme/platform", "", ""},
		{"a repository on this machine", "file:///remotes/platform/tree/main/gateway/", "file:///remotes/platform", "main", "gateway"},
	}
	for _, c := range cases {
		remote, ref, root, err := source(c.location)
		if err != nil {
			t.Errorf("%s: %v", c.forge, err)
			continue
		}
		if remote != c.remote || ref != c.ref || root != c.root {
			t.Errorf("%s: %s is read as %s at %q in %q; want %s at %q in %q", c.forge, c.location, remote, ref, root, c.remote, c.ref, c.root)
		}
	}
}

func TestASourceLocationThatSaysNoWhereIsRefused(t *testing.T) {
	for _, location := range []string{"gateway", "https://github.com/acme/platform/tree/"} {
		if _, _, _, err := source(location); err == nil {
			t.Errorf("%q was read as a source", location)
		}
	}
}
