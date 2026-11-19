// Package testowner reads the token the local stack's control plane
// knows the person at this machine by, as the stack lays it out
// (frontseat-local up, local/stack), for a test that asks the stack's control plane as that
// person does, and lays a token out for the command line as the stack
// does, for a test that runs it.
package testowner

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/local/ports"
)

// Stack is where the local stack's control plane answers this machine,
// the one its owner's token is for.
var Stack = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.ControlPlane))

// File is where the local stack lays out its owner's token, as the
// command line reads it: frontseat/local/token in the configuration
// directory of the XDG base directory specification.
func File(t testing.TB) string {
	t.Helper()
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "frontseat", "local", "token")
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
// stack laid out none, or one others could read; the token is never said.
func Token(t testing.TB) string {
	t.Helper()
	file := File(t)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the local stack laid out no owner's token (mise start lays it out): %v", err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the local stack's owner's token %s can be read by others, or not at all: %v", file, err)
	}
	return strings.TrimSpace(string(raw))
}

// Client is an HTTP client that presents a token with every request, as
// a bearer's.
func Client(token string) *http.Client {
	return &http.Client{Transport: bearer(token)}
}

type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	carried := r.Clone(r.Context())
	carried.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(carried)
}
