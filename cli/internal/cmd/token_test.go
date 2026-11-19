package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// token is a token of the tests' local stack.
const token = "746f6b656e2d6f662d7468652d636f6d6d616e642d6c696e65732d7465737473"

// The token laid out beside the control plane the command line asks is
// presented to it, by every command that asks it, and to no other
// origin: not to a workspace's daemon the control plane says is
// elsewhere. A token another could read is refused, without being said.
// The controls are a daemon of the control plane's own origin, which is
// presented it, and a machine with no token, whose commands present
// none.
func TestTheTokenIsPresentedToTheControlPlaneAlone(t *testing.T) {
	var mu sync.Mutex
	var presented []string
	recording := func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			presented = append(presented, r.Host+" "+r.Header.Get("Authorization"))
			mu.Unlock()
			handler.ServeHTTP(w, r)
		})
	}
	taken := func() []string {
		mu.Lock()
		defer mu.Unlock()
		got := presented
		presented = nil
		return got
	}
	// A daemon elsewhere, and one beneath the control plane.
	builds := connect.NewServer()
	buildv1alpha1connect.RegisterBuildServiceHandler(builds, &daemonOf{})
	daemon := http.NewServeMux()
	connecthttp.Mount(daemon, builds)
	elsewhere := httptest.NewServer(recording(daemon))
	t.Cleanup(elsewhere.Close)
	var daemonAt string
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, keeping{daemon: func() string { return daemonAt }})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	mux.Handle("/workspaces/named/build/", http.StripPrefix("/workspaces/named/build", daemon))
	srv := httptest.NewServer(recording(mux))
	t.Cleanup(srv.Close)
	recorded := srv.URL
	file := layOut(t, recorded)
	plane := recorded[len("http://"):]

	if _, _, err := runAt(t, context.Background(), "", "workspace", "list"); err != nil {
		t.Fatal(err)
	}
	if got := taken(); len(got) != 1 || got[0] != plane+" Bearer "+token {
		t.Errorf("the control plane was presented %q, want the token", got)
	}
	daemonAt = recorded + "/workspaces/named/build"
	if _, _, err := runAt(t, context.Background(), "", "builds", "--workspace", "named"); err != nil {
		t.Fatal(err)
	}
	if got := taken(); len(got) != 2 || got[1] != plane+" Bearer "+token {
		t.Errorf("a daemon beneath the control plane was presented %q, want the token", got)
	}
	daemonAt = elsewhere.URL
	if _, _, err := runAt(t, context.Background(), "", "builds", "--workspace", "named"); err != nil {
		t.Fatal(err)
	}
	if got := taken(); len(got) != 2 || got[1] != elsewhere.URL[len("http://"):]+" " {
		t.Errorf("a daemon elsewhere was presented %q, want nothing", got)
	}

	// Inside a workspace's sandbox, the daemon beside is presented no
	// token, though one is laid out.
	if _, _, err := runAt(t, context.Background(), elsewhere.URL, "builds"); err != nil {
		t.Fatal(err)
	}
	if got := taken(); len(got) != 1 || got[0] != elsewhere.URL[len("http://"):]+" " {
		t.Errorf("the daemon beside was presented %q, want nothing", got)
	}

	// A token another could read is refused, and is not said.
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "list"); err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("a token another could read: %v, want it refused without being said", err)
	}
	if got := taken(); len(got) != 0 {
		t.Errorf("with a token another could read, the control plane was asked: %q", got)
	}

	// The control: no token, none presented.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "list"); err != nil {
		t.Fatal(err)
	}
	if got := taken(); len(got) != 1 || got[0] != plane+" " {
		t.Errorf("with no token, the control plane was presented %q", got)
	}
	for at, here := range map[string]bool{"http://127.0.0.1:7700": true, "http://[::1]:1": true, "http://localhost:1": true, "https://cp.example.com": false, "http://127.0.0.1.example.com": false} {
		if local(at) != here {
			t.Errorf("%s is said to be of this machine: %v, want %v", at, local(at), here)
		}
	}
}

// layOut lays out the owner's token where the command line finds it,
// beneath a root of the test's own, for the control plane at server,
// and returns the token's file.
func layOut(t *testing.T, server string) string {
	t.Helper()
	kept := serverAt(t, server)
	file := filepath.Join(kept, "token")
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// outside is where the tests' command lines run by default: outside any
// workspace's sandbox.
func outside() (bool, error) { return false, nil }

// TestMain has every command line of the tests keep what it keeps
// beneath a root of their own, where it finds no token of this
// machine's, so that none is presented to the tests' servers; a test
// that wants one lays out its own.
func TestMain(m *testing.M) {
	config, err := os.MkdirTemp("", "frontseat-cli-tests-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", config); err != nil {
		panic(err)
	}
	// And runs outside any workspace's sandbox, whatever the machine the
	// tests run on is; a test that runs inside one says so (runAt).
	inside = outside
	code := m.Run()
	// The directory is the tests' own, made just above.
	_ = os.RemoveAll(config)
	os.Exit(code)
}
