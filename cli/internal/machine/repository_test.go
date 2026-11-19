package machine

import "testing"

// A repository is named on the command line by its remote, or by its
// remote, a # and the branch or tag to stand at. A remote is a URL, and
// in a URL a # ends the address: so the first # is where the remote
// ends, and all that follows it is the branch or tag, as it is written.
func TestARepositoryIsNamedByItsRemoteAndWhereToStand(t *testing.T) {
	for written, want := range map[string][2]string{
		"https://example.com/acme/services":              {"https://example.com/acme/services", ""},
		"https://example.com/acme/services#main":         {"https://example.com/acme/services", "main"},
		"https://example.com/acme/services.git#v1.2.3":   {"https://example.com/acme/services.git", "v1.2.3"},
		"https://example.com/acme/services#release/1.2":  {"https://example.com/acme/services", "release/1.2"},
		"ssh://git@example.com/acme/services":            {"ssh://git@example.com/acme/services", ""},
		"ssh://git@example.com:2222/acme/services#main":  {"ssh://git@example.com:2222/acme/services", "main"},
		"git@example.com:acme/services.git":              {"git@example.com:acme/services.git", ""},
		"git@example.com:acme/services.git#main":         {"git@example.com:acme/services.git", "main"},
		"https://someone@example.com/acme/services#main": {"https://someone@example.com/acme/services", "main"},
		"/remotes/services#main":                         {"/remotes/services", "main"},
		// An @ is a remote's own, wherever it is.
		"https://example.com/acme/services@main": {"https://example.com/acme/services@main", ""},
		"/home/me@work/services#main":            {"/home/me@work/services", "main"},
		// A branch may hold what a remote may not: it is taken as written.
		"https://example.com/acme/services#fix#12":       {"https://example.com/acme/services", "fix#12"},
		"https://example.com/acme/services#someone@work": {"https://example.com/acme/services", "someone@work"},
		"https://example.com/acme/services#100%":         {"https://example.com/acme/services", "100%"},
		// A # of a remote's own is written as a URL writes one.
		"file:///remotes/issue%2312#main": {"file:///remotes/issue%2312", "main"},
	} {
		got, err := Repository(written)
		if err != nil {
			t.Errorf("%s: %v", written, err)
			continue
		}
		if got.Remote != want[0] || got.Ref != want[1] {
			t.Errorf("%s is read as the remote %q at %q, want %q at %q", written, got.Remote, got.Ref, want[0], want[1])
		}
	}
	for _, written := range []string{"", "#main", "https://example.com/acme/services#"} {
		if got, err := Repository(written); err == nil {
			t.Errorf("%q was read, as the remote %q at %q", written, got.Remote, got.Ref)
		}
	}
}
