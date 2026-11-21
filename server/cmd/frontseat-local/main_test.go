package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/testsupport/testauthority"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgateway"
)

// identityOf gives a directory holding a certificate and a key of the test's
// own making, as a gateway's are laid out, the key readable as told.
func identityOf(t *testing.T, key os.FileMode) string {
	t.Helper()
	made := testauthority.New(t)
	authorities, err := os.ReadFile(made.File)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"ca.crt": authorities, "tls.crt": made.Certificate, "tls.key": made.Key} {
		if err := os.WriteFile(filepath.Join(directory, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(directory, "tls.key"), key); err != nil {
		t.Fatal(err)
	}
	return directory
}

// started serves until the test's deadline and returns what serve
// returned; a server that started is stopped and returns nil.
func started(t *testing.T, gateway, identity string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return serve(ctx, "127.0.0.1:0", "", "host.docker.internal:8980", t.TempDir(), gateway, identity, "docker", "", filepath.Join(t.TempDir(), "owner", "token"), nil, nil)
}

// The local server makes its sandboxes with an OpenShell gateway and has
// no other way: asked for none, for one that is not asked over https, or
// for one it has no certificate to be known by, it does not start, and
// says what to name; nor does it start over a gateway that does not
// answer it, one that is not there or one its certificate is not of. The
// control is TestWithTheGatewayThatKnowsItTheServerStarts.
func TestWithoutAGatewayItIsKnownByTheServerDoesNotStart(t *testing.T) {
	sound := identityOf(t, 0o600)
	// A server over TLS whose certificate is of an authority the
	// identity above does not hold: no container is asked.
	another := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(another.Close)
	empty, open := t.TempDir(), identityOf(t, 0o600)
	for directory, mode := range map[string]os.FileMode{empty: 0o700, open: 0o755} {
		if err := os.Chmod(directory, mode); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct{ gateway, identity, says string }{
		"no gateway":                     {"", sound, "name one with --openshell"},
		"a gateway that is not there":    {"https://127.0.0.1:1", sound, "does not answer this server"},
		"a gateway of another authority": {another.URL, sound, "does not answer this server"},
		"a path after the address":       {"https://127.0.0.1:8090/x", sound, "over https"},
		"a question after it":            {"https://127.0.0.1:8090?x=y", sound, "over https"},
		"a port and no host":             {"https://:8090", sound, "over https"},
		"someone named in it":            {"https://someone@127.0.0.1:8090", sound, "over https"},
		"a directory open to others":     {"https://127.0.0.1:8090", open, "is open to others"},
		"a gateway asked in the clear":   {"http://127.0.0.1:8090", sound, "over https"},
		"an address with no scheme":      {"127.0.0.1:8090", sound, "over https"},
		"a scheme and no host":           {"https://", sound, "over https"},
		"another scheme":                 {"grpc://127.0.0.1:8090", sound, "over https"},
		"no certificate":                 {"https://127.0.0.1:8090", "", "--openshell-identity"},
		"no certificate, in capitals":    {"HTTPS://127.0.0.1:8090", "", "--openshell-identity"},
		"a directory that holds none":    {"https://127.0.0.1:8090", empty, "holds no ca.crt"},
		"a key others can read":          {"https://127.0.0.1:8090", identityOf(t, 0o644), "can be read by others"},
	} {
		t.Run(name, func(t *testing.T) {
			err := started(t, c.gateway, c.identity)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("serve = %v, which is to say %q", err, c.says)
			}
		})
	}
}

// Told no directory, the server keeps its state in its own place under
// the state directory a person's programs share, and everything it
// keeps is beneath it.
func TestItsStateIsKeptWhereAPersonsProgramsKeepTheirs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/somewhere")
	if got := kept(); got != "/somewhere/frontseat/local" {
		t.Fatalf("kept() = %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if got := kept(); got != "/home/someone/.local/state/frontseat/local" {
		t.Fatalf("kept() = %s", got)
	}
}

// The control of the refusals above: with the local stack's gateway and
// what it knows a caller by, the server starts and serves until it is
// stopped, also where its clients reach it at another address than it
// listens on.
func TestWithTheGatewayThatKnowsItTheServerStarts(t *testing.T) {
	known := testgateway.Identity(t)
	for _, public := range []string{"", "http://127.0.0.1:7700"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := serve(ctx, "127.0.0.1:0", public, "host.docker.internal:8980", t.TempDir(), testgateway.Address, known, "docker", "", filepath.Join(t.TempDir(), "owner", "token"), nil, nil)
		cancel()
		if err != nil {
			t.Errorf("with the gateway and what it is known by, clients told to reach it at %q, the server did not start: %v", public, err)
		}
	}
}

// Where clients reach the server is an address they can ask: the one it
// listens on, or the one it is told; listening on every address it must
// be told, and what it is told is an address and nothing more.
func TestWhereClientsReachTheServerIsAnAddress(t *testing.T) {
	for _, c := range []struct{ listen, public, want string }{
		{"127.0.0.1:7700", "", "http://127.0.0.1:7700"},
		{"0.0.0.0:7700", "http://127.0.0.1:7700", "http://127.0.0.1:7700"},
		{"0.0.0.0:7700", "https://sandboxes.example", "https://sandboxes.example"},
		{"0.0.0.0:7700", "", ""},
		{":7700", "", ""},
		{"[::]:7700", "", ""},
		{"127.0.0.1:7700", "127.0.0.1:7700", ""},
		{"127.0.0.1:7700", "http://:7700", ""},
		{"127.0.0.1:7700", "http://127.0.0.1:7700/x", ""},
		{"127.0.0.1:7700", "http://someone@127.0.0.1:7700", ""},
		{"127.0.0.1:7700", "ftp://127.0.0.1", ""},
	} {
		got, err := reached(c.listen, c.public)
		if got != c.want || (err == nil) != (c.want != "") {
			t.Errorf("listening on %q, told %q: %q, %v; want %q", c.listen, c.public, got, err, c.want)
		}
	}
	// It is asked before any gateway is: a server told nowhere a client
	// can reach does not start.
	if err := serve(context.Background(), "0.0.0.0:0", "", "host.docker.internal:8980", t.TempDir(), "https://127.0.0.1:1", identityOf(t, 0o600), "docker", "", filepath.Join(t.TempDir(), "owner", "token"), nil, nil); err == nil || !strings.Contains(err.Error(), "--public") {
		t.Errorf("listening on every address and told no other: %v, want --public named", err)
	}
}

// What identityOf writes is an identity a gateway knows: its certificate
// is of the authority ca.crt holds, and a server of that authority that
// asks for its callers' certificates, as a gateway does, admits it, read
// as the gateway's driver reads it (ca.crt, tls.crt, tls.key).
func TestATestsIdentityIsOneAGatewayKnows(t *testing.T) {
	identity := identityOf(t, 0o600)
	authorities := x509.NewCertPool()
	pem, err := os.ReadFile(filepath.Join(identity, "ca.crt"))
	if err != nil || !authorities.AppendCertsFromPEM(pem) {
		t.Fatal(err)
	}
	mine, err := tls.LoadX509KeyPair(filepath.Join(identity, "tls.crt"), filepath.Join(identity, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, each := range r.TLS.PeerCertificates {
			seen = append(seen, each.Subject.CommonName)
		}
	}))
	gateway.TLS = &tls.Config{Certificates: []tls.Certificate{mine}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: authorities}
	gateway.StartTLS()
	t.Cleanup(gateway.Close)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: authorities, Certificates: []tls.Certificate{mine}}}}
	answer, err := client.Get(gateway.URL)
	if err != nil {
		t.Fatalf("a gateway of the identity's authority: %v", err)
	}
	answer.Body.Close()
	if len(seen) != 1 {
		t.Errorf("the gateway was shown %q", seen)
	}
}
