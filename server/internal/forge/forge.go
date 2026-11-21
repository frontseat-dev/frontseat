// Package forge is what the control plane knows of a forge beyond what
// its version control system asks of one: how a release is published
// there. It says which hosts publishing asks and which requests of
// each, for the gateway of a sandbox to hold a publisher to, and makes
// none of them. What a forge asks is said here and nowhere else.
package forge

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/plain"
)

// Request is one request publishing asks: a method, and a path a part
// of which that is "*" is any one part, as a forge numbers a release as
// it makes it.
type Request struct {
	Method, Path string
}

// Host is a host publishing asks, and what it asks of it.
type Host struct {
	// Origin is the host as it is reached: "https://api.github.com".
	Origin string
	// Requests are the requests asked of it.
	Requests []Request
}

// Releasing is how a forge's releases are published.
type Releasing interface {
	// Releases are the hosts publishing a release of a repository asks,
	// and the requests of each: a repository by the host it is fetched
	// from, as "https://github.com", and its path there, as
	// "/acme/code". It asks of that repository's releases alone. It is
	// false of a repository of no forge it knows.
	Releases(origin, repository string) ([]Host, bool)
	// Beside are the hosts a credential for a forge's host serves
	// beside it: those the forge is asked at that are not where its
	// repositories are fetched from.
	Beside(origin string) []string
}

// Program is the program that asks them, where a box holds it: the one
// a sandbox's rules let ask.
const Program = "/opt/frontseat/bin/frontseat-publish"

// github is GitHub, and the forges of its kind someone told of.
type github struct {
	// told are the forges of its kind, each by its address.
	told []string
}

// GitHub knows GitHub, and the forges of its kind at the addresses
// given, each as "https://code.acme.example": a forge that answers as
// GitHub does, at its own address, beneath /api/v3 and /api/uploads.
//
// An address is told as it is compared: a scheme, http or https, a
// host's name in small letters, and its port where that is not the
// scheme's own; nothing after it. One written any other way would be
// taken for no forge and never said to be, so it is refused here, as is
// GitHub's own, which is known without being told.
func GitHub(told ...string) (Releasing, error) {
	for _, address := range told {
		at, err := url.Parse(address)
		switch {
		case err != nil || (at.Scheme != "http" && at.Scheme != "https") || at.Hostname() == "" || at.User != nil || at.Path != "" || at.RawQuery != "" || at.Fragment != "":
			return nil, fmt.Errorf("%q is no forge's address: one is written as https://code.acme.example, with a port where it is not the scheme's own and nothing after it", address)
		case at.Hostname() != strings.ToLower(at.Hostname()) || !plain.Text(at.Hostname()) || net.ParseIP(at.Hostname()) != nil || strings.HasSuffix(at.Hostname(), "."):
			return nil, fmt.Errorf("%q names a forge by no host's name in small letters", address)
		case at.Port() == map[string]string{"http": "80", "https": "443"}[at.Scheme]:
			return nil, fmt.Errorf("%q names the port its scheme has already: write it without", address)
		case address == githubOrigin || slices.Contains([]string{githubAPI, githubUploads}, address):
			return nil, fmt.Errorf("%q is GitHub's own, which is known without being told", address)
		case address != at.Scheme+"://"+at.Host:
			return nil, fmt.Errorf("%q is no forge's address: one is written as https://code.acme.example", address)
		}
	}
	return github{told: slices.Clone(told)}, nil
}

// GitHubAlone knows GitHub and no other forge.
func GitHubAlone() Releasing { return github{} }

const (
	githubOrigin  = "https://github.com"
	githubAPI     = "https://api.github.com"
	githubUploads = "https://uploads.github.com"
)

func (g github) Releases(origin, repository string) ([]Host, bool) {
	api, uploads := githubAPI, githubUploads
	apiPath, uploadsPath := "", ""
	switch {
	case origin == githubOrigin:
	case slices.Contains(g.told, origin):
		api, uploads, apiPath, uploadsPath = origin, origin, "/api/v3", "/api/uploads"
	default:
		return nil, false
	}
	// A repository of this kind of forge is an owner and a name, each
	// said: git names it with ".git" after it or without.
	owner, name, two := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(repository, "/"), ".git"), "/")
	if !strings.HasPrefix(repository, "/") || !two || !part(owner) || !part(name) {
		return nil, false
	}
	of := "/repos/" + owner + "/" + name + "/releases"
	asked := []Request{{"GET", apiPath + of}, {"POST", apiPath + of}, {"PATCH", apiPath + of + "/*"}}
	added := Request{"POST", uploadsPath + of + "/*/assets"}
	if api == uploads {
		return []Host{{Origin: api, Requests: append(asked, added)}}, true
	}
	return []Host{{Origin: api, Requests: asked}, {Origin: uploads, Requests: []Request{added}}}, true
}

// part is whether text is one part of a repository's path, said: no
// part that is any, and none that climbs.
func part(text string) bool {
	return text != "" && text != "." && text != ".." && !strings.ContainsAny(text, "/*") && plain.Text(text)
}

func (github) Beside(origin string) []string {
	if origin == githubOrigin {
		return []string{githubAPI, githubUploads}
	}
	return nil
}
