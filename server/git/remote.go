package git

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"

	giturls "github.com/chainguard-dev/git-urls"
)

// Name is the last element of a remote's path, as git names the
// directory it clones one into, or nothing where it names none. A remote
// is read as git reads one, a URL, a host and a path with a colon
// between, or a path (git-urls, which bob reads remotes with).
func Name(remote string) string {
	at, err := giturls.Parse(remote)
	if err != nil {
		return ""
	}
	name := strings.TrimSuffix(path.Base(strings.TrimSuffix(strings.TrimRight(at.Path, "/"), "/.git")), ".git")
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// Why a remote is no remote for a sandbox. None repeats the remote as it
// is written.
var (
	errSecret = errors.New("written with a password, which is not sent on and not repeated here: write the remote without it")
	ErrLocal  = errors.New("a directory of this machine, which no sandbox is given")
	errUnread = errors.New("written in a way that names no repository of a host")
)

// Address is a remote as a sandbox clones it.
type Address struct {
	// Cloned is the remote as a sandbox clones it.
	Cloned string
	// Repository is what repository it is, whatever its spelling: its
	// host in small letters and its path without the ending a clone may
	// or may not write.
	Repository string
	// Converted is whether Cloned is not the remote as it was written.
	Converted bool
}

// Over is a remote as a sandbox clones it. A remote over http or https
// is cloned as it is written, its port with it, less a user it names
// before the host, which is not sent on. One over ssh, written as a URL
// or as a host and a path with a colon between, is cloned over https
// from the same host and path, which is what a forge that serves both
// gives, and is said to be converted so that a person is told. Where it
// is none, the error does not repeat it, since it may hold a password or
// a token.
func Over(written string) (Address, error) {
	if strings.ContainsAny(written, " \t\r\n\x00") {
		return Address{}, errUnread
	}
	var scheme, host, port, path string
	converted := false
	if strings.Contains(written, "://") {
		at, err := url.Parse(written)
		if err != nil {
			return Address{}, errUnread
		}
		if _, secret := at.User.Password(); secret {
			return Address{}, errSecret
		}
		switch at.Scheme {
		case "https", "http":
			// Whoever is named before the host is left out, and so is a
			// token written there: what is sent on names no one.
			scheme, port, converted = at.Scheme, at.Port(), at.User != nil
		case "ssh", "git+ssh", "ssh+git":
			scheme, converted = "https", true
		case "file":
			return Address{}, ErrLocal
		default:
			return Address{}, errUnread
		}
		if at.RawQuery != "" || at.ForceQuery || at.Fragment != "" || at.Opaque != "" {
			return Address{}, errUnread
		}
		host, path = at.Hostname(), at.EscapedPath()
	} else {
		// A host and a path with a colon between, as git reads one, and
		// held to that shape whole before anything reads a part of it:
		// what reads such a remote stops at the first character it does
		// not take, and what it had read by then would be another
		// repository's address.
		shape := scpLike.FindStringSubmatch(written)
		if shape == nil {
			if strings.Contains(written, ":") && !strings.HasPrefix(written, "/") && !strings.HasPrefix(written, ".") {
				return Address{}, errUnread
			}
			return Address{}, ErrLocal
		}
		at, err := giturls.Parse(written)
		if err != nil || at.Scheme != "ssh" || at.Hostname() != shape[1] || strings.TrimLeft(at.Path, "/") != strings.TrimLeft(shape[2], "/") {
			return Address{}, errUnread
		}
		scheme, converted = "https", true
		host, path = shape[1], "/"+strings.TrimLeft(shape[2], "/")
	}
	host = strings.ToLower(host)
	path = strings.TrimRight(path, "/")
	if !named(host) || !plain(path) {
		return Address{}, errUnread
	}
	at := host
	if port != "" {
		at += ":" + port
	}
	return Address{Cloned: scheme + "://" + at + path, Repository: at + strings.TrimSuffix(path, ".git"), Converted: converted}, nil
}

// scpLike is a remote written as a host and a path with a colon between,
// a user before the host or none: the whole of it, each part of the
// characters a host's name and a plainly written path hold.
var scpLike = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+):([A-Za-z0-9._+/-]+)$`)

// named is whether a host is a name: letters, digits, dots and hyphens,
// beginning and ending with a letter or a digit, its last part beginning
// with a letter. An address is none, however it is written (in tens, or
// as 0x7f.1), which no sandbox is let reach by.
func named(host string) bool {
	last := host[strings.LastIndex(host, ".")+1:]
	if host == "" || last == "" || !(last[0] >= 'a' && last[0] <= 'z') {
		return false
	}
	for i, r := range host {
		letter := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !letter && !(strings.ContainsRune(".-", r) && i > 0 && i < len(host)-1) {
			return false
		}
	}
	return true
}

// plain is whether a path is a repository's, written plainly: it begins
// at the root, names something, climbs nowhere and holds nothing a URL
// or a shell would read otherwise.
func plain(path string) bool {
	if !strings.HasPrefix(path, "/") || len(path) < 2 || strings.Contains(path, "//") {
		return false
	}
	for _, part := range strings.Split(path[1:], "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, "~") {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("._-+%", r) {
				return false
			}
		}
	}
	return true
}
