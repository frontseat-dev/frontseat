// Package testgateway gives a test the local stack's OpenShell gateway: its
// address, and what it knows a caller by.
package testgateway

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/stack/local"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// Address is where the local stack's gateway answers, on this machine's
// loopback.
var Address = "https://" + Listens

// Listens is the host and port of Address.
var Listens = net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.OpenShell))

// held is the container the gateway runs in, the stack's one, and kept
// where it keeps what it and its callers are known by
// (stack/image/rootfs/etc/openshell/gateway.template.toml).
const (
	held = local.Container
	kept = "/var/lib/frontseat/openshell/tls/"
)

// Identity returns a directory of the test's holding what the gateway is
// known by and knows a caller by: ca.crt, tls.crt and tls.key, copied
// out of its container, readable by the test alone and gone with it. It
// skips the test where no gateway answers, or fails it where the gate
// runs against the stack (testrequired.Missing).
func Identity(t testing.TB) string {
	t.Helper()
	return identity(t, false, t.TempDir())
}

// Required is Identity for a test that was asked to run over the
// gateway: where none answers the test fails, since passing by not
// running is no proof.
func Required(t testing.TB) string {
	t.Helper()
	return identity(t, true, t.TempDir())
}

// RequiredAt copies the required gateway identity into a caller-owned
// directory, for a service whose lifetime extends beyond its first test.
func RequiredAt(t testing.TB, directory string) string {
	t.Helper()
	return identity(t, true, directory)
}

// Files returns where, in a directory Identity, Required or RequiredAt
// returned, the certificate of the gateway's authority is, and the
// certificate and key a caller proves itself with.
func Files(identity string) (authority, certificate, key string) {
	return filepath.Join(identity, "ca.crt"), filepath.Join(identity, "tls.crt"), filepath.Join(identity, "tls.key")
}

func identity(t testing.TB, needed bool, directory string) string {
	t.Helper()
	testrequired.Stack(t)
	conn, err := net.DialTimeout("tcp", Listens, time.Second)
	if err != nil {
		if needed {
			t.Fatalf("no OpenShell gateway answers on %s, and the test was asked to run over one: %v", Listens, err)
		}
		testrequired.Missing(t, "no OpenShell gateway answers on %s: %v", Listens, err)
	}
	_ = conn.Close()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	authority, certificate, key := Files(directory)
	for from, into := range map[string]string{"ca.crt": authority, "client/tls.crt": certificate, "client/tls.key": key} {
		if out, err := exec.Command("docker", "cp", held+":"+kept+from, into).CombinedOutput(); err != nil {
			t.Fatalf("copying %s out of the gateway: %v: %s", from, err, out)
		}
		if err := os.Chmod(into, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

// Runtime runs a command of the container runtime the gateway makes its
// sandboxes with, the stack container's Podman, as the user whose Podman
// it is, and returns what it printed, its error output beside.
func Runtime(ctx context.Context, arguments ...string) ([]byte, error) {
	return local.Podman(ctx, held, arguments...).CombinedOutput()
}
