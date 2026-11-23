// Package testowner reads the token the local stack's control plane
// knows the person at this machine by, as the stack lays it out
// (frontseat-stack up, stack), for a test that asks the stack's control plane as that
// person does, and lays a token out for the command line as the stack
// does, for a test that runs it.
package testowner

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
	"github.com/frontseat-dev/frontseat/stack/local"
)

// Stack is where the local stack's control plane answers this machine,
// the one its owner's token is for.
var Stack = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.ControlPlane))

// File is where the local stack lays out its owner's token, as the
// command line reads it (local.TokenFile).
func File(t testing.TB) string {
	t.Helper()
	file, err := local.TokenFile()
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// LayOut makes a directory for the command line to keep what it keeps
// in, as XDG_CONFIG_HOME names one, and lays out there the owner's token
// the file given holds, as the stack lays one out, for the control plane
// at server: "" for the local stack's. A test gives the command line the
// directory, so that it presents that token to that control plane and
// keeps its context and its key apart from this machine's. The file
// given is read, and its token is never said.
func LayOut(t testing.TB, token, server string) string {
	t.Helper()
	config := t.TempDir()
	kept := filepath.Join(config, "frontseat", "local")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	if token != "" {
		raw, err := os.ReadFile(token)
		if err != nil {
			t.Fatalf("the owner's token to lay out: %v", err)
		}
		if err := os.WriteFile(filepath.Join(kept, "token"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if server != "" {
		if err := os.WriteFile(filepath.Join(kept, "server"), []byte(server+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return config
}

// Token is the local stack's owner's token. The test fails where the
// stack laid out none, or one others could read, and where the command
// line is told of another control plane (local.ServerFile), whose token
// it then is; the token is never said.
func Token(t testing.TB) string {
	t.Helper()
	token, err := held()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// held is the token laid out for the local stack's control plane.
func held() (string, error) {
	server, err := local.ServerFile()
	if err != nil {
		return "", err
	}
	named, err := os.ReadFile(server)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if at := strings.TrimSpace(string(named)); at != "" && at != Stack {
		return "", fmt.Errorf("%s names another control plane than the local stack's, and the token beside it is for that one", server)
	}
	file, err := local.TokenFile()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("the local stack laid out no owner's token (mise start lays it out): %w", err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("the local stack's owner's token %s can be read by others, or not at all: %v", file, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// Client is an HTTP client that presents a token, as a bearer's, to the
// origin of the address given and to no other, following no redirect:
// the SDK's own way (sdk/go/bearer), since the token may be the person's
// at this machine.
// An origin that is no URL is presented nothing.
func Client(token, origin string) *http.Client {
	at, err := url.Parse(origin)
	if err != nil {
		at = nil
	}
	return bearer.NewClient(nil, token, at)
}
