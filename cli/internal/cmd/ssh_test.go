package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What the SSH client is told is read by it, and by the shell it starts
// its way with, as written: with this command and the key each in a
// directory whose name holds a space, as the key's own is on macOS
// ("Application Support"), a connection goes by this command and looks
// for that key, told on the command line and told by what is printed for
// ~/.ssh/config. Each is tried by a connection and not by asking the
// client what it made of its options, which expands nothing and runs
// nothing. The control is the key told with no quoting, which the SSH
// client refuses.
func TestTheSSHClientIsToldPathsThatHoldASpace(t *testing.T) {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh client on this machine")
	}
	root := filepath.Join(t.TempDir(), "a directory é")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(root, "id_ed25519")
	self := filepath.Join(root, "frontseat")
	went := filepath.Join(root, "went")
	// This command, as the SSH client starts it: it says it was started,
	// and how, and carries nothing.
	if err := os.WriteFile(self, []byte("#!/bin/sh\necho \"$0 $*\" >> '"+went+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	options, err := sshOptions(self, identity, "")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config")
	printed := "Host frontseat-workspace\n"
	var arguments []string
	for _, option := range options {
		if option[0] == "LogLevel" {
			continue // the test reads what the client says it does
		}
		printed += "  " + option[0] + " " + option[1] + "\n"
		arguments = append(arguments, "-o", option[0]+"="+option[1])
	}
	if err := os.WriteFile(config, []byte(printed), 0o600); err != nil {
		t.Fatal(err)
	}
	for told, arguments := range map[string][]string{
		"on the command line": append(arguments, "-F", "/dev/null"),
		"by ~/.ssh/config":    {"-F", config},
	} {
		_ = os.Remove(went)
		// The connection fails, there being no sandbox: what is read is
		// what the client did on the way.
		said, _ := exec.Command(ssh, append(arguments, "-v", "frontseat-workspace")...).CombinedOutput()
		// The client prints a path as it is where the caller's locale is
		// UTF-8, and each byte beyond ASCII as an octal escape elsewhere.
		if !strings.Contains(string(said), "identity file "+identity+" type") && !strings.Contains(string(said), "identity file "+escapedBeyondASCII(identity)+" type") {
			t.Errorf("told %s, the SSH client looks for another key than %q:\n%s", told, identity, said)
		}
		how, _ := os.ReadFile(went)
		if strings.TrimSpace(string(how)) != self+" workspace ssh --stdio" {
			t.Errorf("told %s, the SSH client went by %q, want this command with its --stdio:\n%s", told, how, said)
		}
	}
	said, err := exec.Command(ssh, "-F", "/dev/null", "-o", "IdentityFile="+identity, "-G", "frontseat-workspace").CombinedOutput()
	if err == nil || !strings.Contains(string(said), "extra arguments") {
		t.Errorf("told the key with no quoting, the SSH client did not refuse it for its space, so the test above shows nothing: %v: %s", err, said)
	}
}

// A path that holds what the SSH client or a shell reads otherwise is
// told to neither: a shell would run what a directory's name holds
// between backticks, and the client would read a % as a token of its
// own. The control is the plain path beside them, which is taken.
func TestAPathAnSSHClientOrAShellReadsOtherwiseIsRefused(t *testing.T) {
	const plain = "/a directory/é/frontseat"
	if _, err := sshOptions(plain, plain, ""); err != nil {
		t.Fatalf("a plain path was refused: %v", err)
	}
	for name, path := range map[string]string{
		"a command between backticks": "/a`touch made`b/frontseat",
		"a command in a dollar":       "/a$(touch made)b/frontseat",
		"a variable":                  "/a$HOME/frontseat",
		"a variable in braces":        "/a${HOME}b/frontseat",
		"a token of the client's":     "/a%hb/frontseat",
		"a percent":                   "/a%b/frontseat",
		"a double quote":              `/a"b/frontseat`,
		"a single quote":              "/a'b/frontseat",
		"a backslash":                 `/a\b/frontseat`,
		"a new line":                  "/a\nb/frontseat",
		"a tab":                       "/a\tb/frontseat",
		"a semicolon":                 "/a;b/frontseat",
		"a tilde":                     "/~b/frontseat",
		"a path that is not whole":    "a/frontseat",
		"nothing":                     "",
	} {
		if _, err := sshOptions(path, plain, ""); err == nil {
			t.Errorf("%s in this command's path was taken", name)
		}
		if _, err := sshOptions(plain, path, ""); err == nil {
			t.Errorf("%s in the key's path was taken", name)
		}
	}
}

// escapedBeyondASCII is a path as the SSH client prints it under a locale
// that is not UTF-8: every byte beyond ASCII as a backslash and three
// octal digits.
func escapedBeyondASCII(path string) string {
	var out strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] < 0x80 {
			out.WriteByte(path[i])
			continue
		}
		fmt.Fprintf(&out, "\\%03o", path[i])
	}
	return out.String()
}
