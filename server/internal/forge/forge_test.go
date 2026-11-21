package forge_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/forge"
)

func asked(hosts []forge.Host) []string {
	var out []string
	for _, host := range hosts {
		for _, request := range host.Requests {
			out = append(out, host.Origin+" "+request.Method+" "+request.Path)
		}
	}
	return out
}

// Publishing a release of a repository asks its forge for that
// repository's releases and nothing else of the forge: to list them, to
// make one, to change one, and to add a file to one, whose number the
// forge gives as it makes it. GitHub is asked at two hosts of its own;
// a forge of its kind told of by its address is asked at that address.
// The control is a repository of a forge nobody told of, which asks
// nothing.
func TestPublishingAsksAForgeForItsRepositorysReleasesAlone(t *testing.T) {
	known, err := forge.GitHub("http://forge.example:8080", "https://code.acme.example")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		origin, repository string
		want               []string
	}{
		"github": {"https://github.com", "/acme/code", []string{
			"https://api.github.com GET /repos/acme/code/releases",
			"https://api.github.com POST /repos/acme/code/releases",
			"https://api.github.com PATCH /repos/acme/code/releases/*",
			"https://uploads.github.com POST /repos/acme/code/releases/*/assets",
		}},
		"github, as git names a repository": {"https://github.com", "/acme/code.git", []string{
			"https://api.github.com GET /repos/acme/code/releases",
			"https://api.github.com POST /repos/acme/code/releases",
			"https://api.github.com PATCH /repos/acme/code/releases/*",
			"https://uploads.github.com POST /repos/acme/code/releases/*/assets",
		}},
		"one of its kind, told of": {"http://forge.example:8080", "/acme/code", []string{
			"http://forge.example:8080 GET /api/v3/repos/acme/code/releases",
			"http://forge.example:8080 POST /api/v3/repos/acme/code/releases",
			"http://forge.example:8080 PATCH /api/v3/repos/acme/code/releases/*",
			"http://forge.example:8080 POST /api/uploads/repos/acme/code/releases/*/assets",
		}},
		"another told of": {"https://code.acme.example", "/team/app", []string{
			"https://code.acme.example GET /api/v3/repos/team/app/releases",
			"https://code.acme.example POST /api/v3/repos/team/app/releases",
			"https://code.acme.example PATCH /api/v3/repos/team/app/releases/*",
			"https://code.acme.example POST /api/uploads/repos/team/app/releases/*/assets",
		}},
	} {
		hosts, is := known.Releases(c.origin, c.repository)
		if !is || !slices.Equal(asked(hosts), c.want) {
			t.Errorf("%s: publishing asks %v (a forge known: %v), want %v", name, asked(hosts), is, c.want)
		}
	}
	// Refused: a forge nobody told of, GitHub over http, and what names
	// no repository of a forge of this kind, which is an owner and a
	// name.
	for name, c := range map[string][2]string{
		"a forge nobody told of":  {"https://forge.other.example", "/acme/code"},
		"github over http":        {"http://github.com", "/acme/code"},
		"github's own other host": {"https://api.github.com", "/acme/code"},
		"no owner":                {"https://github.com", "/code"},
		"more than a name":        {"https://github.com", "/acme/code/more"},
		"a part that is any":      {"https://github.com", "/acme/*"},
		"a path that climbs":      {"https://github.com", "/acme/.."},
		"no repository":           {"https://github.com", ""},
	} {
		if hosts, is := known.Releases(c[0], c[1]); is || len(hosts) != 0 {
			t.Errorf("%s: publishing asks %v", name, asked(hosts))
		}
	}
	// One that knows GitHub alone knows no other.
	if _, is := forge.GitHubAlone().Releases("http://forge.example:8080", "/acme/code"); is {
		t.Error("a forge nobody told of was taken for GitHub")
	}
}

// A person's credential for a forge serves the hosts the forge is asked
// at: GitHub's two beside the one its repositories are fetched from. A
// forge asked at its own address has no other.
func TestACredentialForAForgeServesTheHostsItIsAskedAt(t *testing.T) {
	known, err := forge.GitHub("http://forge.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	for origin, want := range map[string]string{
		"https://github.com":          "https://api.github.com https://uploads.github.com",
		"http://forge.example:8080":   "",
		"https://forge.other.example": "",
		"https://api.github.com":      "",
	} {
		if got := strings.Join(known.Beside(origin), " "); got != want {
			t.Errorf("a credential for %s serves %q beside it, want %q", origin, got, want)
		}
	}
}

// A forge is told of by its address as that address is compared: one
// written any other way would be taken for no forge and never said to
// be, so it is refused when it is told. The control is one written so.
func TestAForgeIsToldOfByItsAddressAsItIsCompared(t *testing.T) {
	for _, address := range []string{
		"https://Code.Acme.example", "https://code.acme.example/", "https://code.acme.example:443", "http://forge.example:80",
		"https://code.acme.example/api/v3", "code.acme.example", "ssh://code.acme.example", "https://someone@code.acme.example",
		"https://192.0.2.7", "https://code.acme.example.", "https://code.acme.example?x=1", "", "https://",
		"https://github.com", "https://api.github.com", "https://uploads.github.com",
	} {
		if known, err := forge.GitHub("https://sound.example", address); err == nil || known != nil {
			t.Errorf("%q was taken for a forge's address", address)
		}
	}
	for _, address := range []string{"https://code.acme.example", "https://code.acme.example:8443", "http://forge.example:8080", "http://forge.example"} {
		if _, err := forge.GitHub(address); err != nil {
			t.Errorf("%q was refused: %v", address, err)
		}
	}
}
