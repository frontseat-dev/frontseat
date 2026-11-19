package backstage

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// source reads where an entity's source is from the URL its catalog
// gives: the repository to clone, the branch or tag to stand at, and
// the directory in it. Why it cannot is said without repeating it. The URL is the one a forge shows for a directory, and
// each forge spells it its own way:
//
//	GitHub, GitLab (old)  <repository>/tree/<ref>/<directory>
//	GitLab                <repository>/-/tree/<ref>/<directory>
//	Gitea, Forgejo        <repository>/src/branch/<ref>/<directory>
//	                      <repository>/src/tag/<ref>/<directory>
//	Bitbucket Cloud       <repository>/src/<ref>/<directory>
//	Azure DevOps          <repository under _git/>?path=/<directory>&version=GB<branch>
//
// A URL with none of these is the repository itself, at the branch its
// remote names. A branch whose name holds a slash cannot be told from a
// directory, and is read as its first segment.
func source(target string) (remote, ref, root string, err error) {
	at, err := url.Parse(target)
	if err != nil || at.Host == "" && at.Scheme != "file" {
		return "", "", "", errors.New("it is no URL with a host")
	}
	if strings.Contains(at.Path, "/_git/") {
		version := at.Query().Get("version")
		ref = strings.TrimPrefix(strings.TrimPrefix(version, "GB"), "GT")
		root = strings.Trim(at.Query().Get("path"), "/")
		at.RawQuery, at.Fragment = "", ""
		return at.String(), ref, root, nil
	}
	for _, marker := range []string{"/-/tree/", "/tree/", "/src/branch/", "/src/tag/", "/src/"} {
		repository, rest, found := strings.Cut(at.Path, marker)
		if !found {
			continue
		}
		ref, root, _ = strings.Cut(rest, "/")
		if ref == "" {
			return "", "", "", fmt.Errorf("it names no branch after %s", strings.Trim(marker, "/"))
		}
		at.Path, at.RawQuery, at.Fragment = repository, "", ""
		return at.String(), ref, strings.Trim(root, "/"), nil
	}
	at.Path, at.RawQuery, at.Fragment = strings.TrimSuffix(at.Path, "/"), "", ""
	return at.String(), "", "", nil
}
