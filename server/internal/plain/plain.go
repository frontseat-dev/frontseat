// Package plain is the one reader of the names that come from outside
// and lead out of a sandbox: a host and the origin it is reached at, a
// path, a program, a parameter, a username, a secret. Each is taken only
// where it is written plainly, so that nothing a URL, a configuration or
// a shell reads would read it as anything else. The control plane holds
// what a person asks and what a version control system answers to it,
// and the gateway's driver what it keeps and what it is asked to let
// through, by these; the sandbox service and a test's services, who a
// sandbox runs as and what of its image it writes. Git's own package
// (server/git) reads a remote by a reader of its own, which the command
// line links: the control plane reads what that reader gives again, by
// these, so that nothing passes one and not the other.
package plain

import (
	"math"
	"net"
	"path"
	"strconv"
	"strings"
)

// Text is whether a text is something, and every character of it one of
// a path written plainly: letters, digits and . _ - + /.
func Text(text string) bool {
	for _, r := range text {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("._-+/", r) {
			return false
		}
	}
	return text != ""
}

// Username is whether a username is one a forge gives: letters, digits
// and . _ @ -, of no great length, and nothing a configuration or a
// shell would read otherwise.
func Username(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("._@-", r) {
			return false
		}
	}
	return true
}

// User is whether a user is named by number, and is neither root's nor
// the number that names no one.
func User(user string) bool {
	uid, err := strconv.ParseUint(user, 10, 32)
	return err == nil && uid != 0 && uid != math.MaxUint32
}

// Absolute is whether a path, a directory or a program, is named
// absolutely and plainly, as path.Clean writes it, and is not the whole
// filesystem.
func Absolute(name string) bool {
	return path.IsAbs(name) && path.Clean(name) == name && name != "/" && !strings.ContainsRune(name, 0)
}

// Asked is whether a path is one a request is let through by: from the
// root, each part written plainly, but that a part after the first may
// be any one part, written "*". What a server names as it makes it, as
// a forge numbers a release, is not known when a rule is written; where
// a rule begins is, and stays said. Nothing else of a pattern is taken:
// not many parts, nor some of a part, and no part climbs.
func Asked(written string) bool {
	if !strings.HasPrefix(written, "/") || strings.Contains(written, "..") || strings.Contains(written, "//") {
		return false
	}
	for i, part := range strings.Split(strings.TrimPrefix(written, "/"), "/") {
		if part == "*" && i > 0 {
			continue
		}
		if !Text(part) {
			return false
		}
	}
	return true
}

// Hostname is whether a host is written as a name is, in small letters:
// labels of letters, digits and hyphens, none beginning or ending with a
// hyphen, the last beginning with a letter, as no address does however
// it is written (in tens, or as 0x7f.1). A sandbox is let reach a name,
// never an address.
func Hostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, each := range labels {
		if each == "" || len(each) > 63 || each[0] == '-' || each[len(each)-1] == '-' {
			return false
		}
		for _, r := range each {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
	}
	first := labels[len(labels)-1][0]
	return first >= 'a' && first <= 'z'
}

// Origin is where something outside a sandbox is reached: over http or
// https, at a host's name and a port.
type Origin struct {
	// Scheme is http or https.
	Scheme string
	// Name is the host's name, in small letters, and Port its port.
	Name string
	Port int
}

// Host is the origin's host as an address names it: name:port.
func (o Origin) Host() string { return net.JoinHostPort(o.Name, strconv.Itoa(o.Port)) }

// Rule is the origin as a sandbox's rule names it, and as a credential
// is kept for it: name:port, over https, or http://name:port, over http.
func (o Origin) Rule() string {
	if o.Scheme == "http" {
		return "http://" + o.Host()
	}
	return o.Host()
}

// Address is the origin as a forge names it: scheme://name, with its
// port where that is not the scheme's own.
func (o Origin) Address() string {
	if o.Port == ports[o.Scheme] {
		return o.Scheme + "://" + o.Name
	}
	return o.URL()
}

// ports are the port of each scheme an origin is reached over, where an
// address names none.
var ports = map[string]int{"http": 80, "https": 443}

// URL is the origin as a URL begins: scheme://name:port.
func (o Origin) URL() string { return o.Scheme + "://" + o.Host() }

// OriginOf reads an origin from its parts, as a URL's parser gives them:
// a scheme, http or https, a host that is a name (Hostname, in any case
// and with or without the dot that ends one), and a port in digits, one
// however it is written (0443 is 443), or none for the scheme's own. The
// second result is whether it is one.
func OriginOf(scheme, host, port string) (Origin, bool) {
	number := ports[scheme]
	if number == 0 {
		return Origin{}, false
	}
	if port != "" {
		var err error
		if number, err = strconv.Atoi(port); err != nil || number < 1 || number > 65535 || strings.Trim(port, "0123456789") != "" {
			return Origin{}, false
		}
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if !Hostname(name) {
		return Origin{}, false
	}
	return Origin{Scheme: scheme, Name: name, Port: number}, true
}

// Ruled reads an origin as a rule names it and a person writes it: a
// name, or a name and a port, over https, with "https://" before it or
// not; or the same after "http://", over http. It is a host and nothing
// more: no user, no path, no address.
func Ruled(written string) (Origin, bool) {
	scheme, rest := "https", strings.TrimPrefix(written, "https://")
	if after, ok := strings.CutPrefix(written, "http://"); ok {
		scheme, rest = "http", after
	}
	host, port := rest, ""
	if at := strings.LastIndexByte(rest, ':'); at >= 0 {
		if host, port = rest[:at], rest[at+1:]; port == "" {
			return Origin{}, false
		}
	}
	return OriginOf(scheme, host, port)
}

// Path is whether a path of a URL is written plainly: it begins at the
// root and names something, every character of it is a plain text's, and
// it climbs nowhere and is spelled one way (no "..", no "//", no "."
// alone between two slashes).
func Path(written string) bool {
	if !strings.HasPrefix(written, "/") || len(written) < 2 || !Text(written) || strings.Contains(written, "..") || strings.Contains(written, "//") {
		return false
	}
	return !strings.Contains(written+"/", "/./")
}

// Program is whether a program is named by its whole path, written
// plainly: one program's, and no pattern of many.
func Program(named string) bool {
	return Absolute(named) && Text(named)
}

// Parameter is whether a parameter of a request and its value are each
// written plainly, and neither holds a path.
func Parameter(name, value string) bool {
	return Text(name) && Text(value) && !strings.Contains(name+value, "/")
}

// Secret is whether a secret is one a credential holds: something, on
// one line, of no great length.
func Secret(secret string) bool {
	return secret != "" && len(secret) <= 4096 && !strings.ContainsAny(secret, "\r\n\x00")
}
