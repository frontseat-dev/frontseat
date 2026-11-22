// Package plain says whether a text that leads out of a sandbox is
// written plainly: a path, a parameter or a username that nothing a URL,
// a configuration or a shell reads would read as anything else. The
// control plane holds what a version control system answers to it, and
// the gateway's driver what it keeps and what it is asked to let
// through; the sandbox service and a test's services, who a sandbox runs
// as and what of its image it writes. It says too what of a text from
// outside is shown to a person (Shown).
package plain

import (
	"math"
	"path"
	"strconv"
	"strings"
	"unicode"
)

// Shown is a text as it is shown where a person or a terminal reads it:
// its lines and tabs kept, what is not UTF-8 written U+FFFD, and nothing
// else in it that is acted on and not read: no control character, the
// one-rune escapes above ASCII among them, no format character, as
// those that turn the direction of a line, and no separator of lines or
// paragraphs but the newline.
func Shown(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r), unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp):
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "\uFFFD"))
}

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
