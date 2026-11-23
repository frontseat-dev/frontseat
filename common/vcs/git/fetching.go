package git

import (
	"fmt"
	"strconv"

	rungit "github.com/frontseat-dev/frontseat/common/git"
	"github.com/frontseat-dev/frontseat/common/vcs"
)

// Cloned implements vcs.Fetching: a remote as git clones it in a
// sandbox (rungit.Over), over https where it is written for ssh. One
// that names anyone but git, as git@host:path and ssh://git@host/path
// do, is vcs.ErrCredentialed.
func (System) Cloned(remote string) (string, error) {
	address, err := rungit.Over(remote)
	if err != nil {
		return "", err
	}
	if address.Named {
		return "", vcs.ErrCredentialed
	}
	return address.Cloned, nil
}

// Requests implements vcs.Fetching: git's smart HTTP protocol reads a
// repository's refs and asks for its objects, by upload-pack; a push
// asks by receive-pack, which is said apart (Pushes).
func (System) Requests(repository string) []vcs.Request {
	return []vcs.Request{
		{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": "git-upload-pack"}},
		{Method: "POST", Path: repository + "/git-upload-pack"},
	}
}

// Pushes implements vcs.Fetching: a push reads the refs the repository
// would take a push to, and sends what it pushes, by receive-pack.
func (System) Pushes(repository string) []vcs.Request {
	return []vcs.Request{
		{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": "git-receive-pack"}},
		{Method: "POST", Path: repository + "/git-receive-pack"},
	}
}

// Programs implements vcs.Fetching: git itself, where its package puts
// it; the helper that speaks HTTP is one it starts.
func (System) Programs() []string { return []string{"/usr/bin/git"} }

// Tells implements vcs.Fetching: the settings git reads from its
// environment.
func (System) Tells() string { return "GIT_CONFIG_" }

// Presenting implements vcs.Fetching. Git reads settings from its
// environment, counted (GIT_CONFIG_COUNT, GIT_CONFIG_KEY_n,
// GIT_CONFIG_VALUE_n): the username is a plain setting of the origin's,
// and its credential helper a line that says the variable's value as
// the password and holds nothing else.
func (System) Presenting(told map[string]string, origin, username, variable string) (map[string]string, error) {
	count := 0
	if already := told["GIT_CONFIG_COUNT"]; already != "" {
		n, err := strconv.Atoi(already)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("git: GIT_CONFIG_COUNT is %q, which is no count", already)
		}
		count = n
	}
	added := map[string]string{}
	set := func(key, value string) {
		index := strconv.Itoa(count)
		count++
		added["GIT_CONFIG_KEY_"+index] = key
		added["GIT_CONFIG_VALUE_"+index] = value
	}
	set("credential."+origin+".username", username)
	// printf, since a shell's echo reads a backslash in the password as
	// an escape, and one ends what it says.
	set("credential."+origin+".helper", `!f() { printf '%s\n' "password=$`+variable+`"; }; f`)
	added["GIT_CONFIG_COUNT"] = strconv.Itoa(count)
	return added, nil
}

// Authoring implements vcs.Fetching: git reads who commits from its
// environment, as the author and as the committer.
func (System) Authoring(name, email string) map[string]string {
	return rungit.Authoring(name, email)
}

var _ vcs.Fetching = System{}
