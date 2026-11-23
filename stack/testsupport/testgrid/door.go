package testgrid

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"
)

// Door is a door in front of the test grid that asks who calls, as a
// grid that is not this machine's own does: stock bb-storage, the one
// the local stack's image holds, over TLS, admitting whoever bears a
// token its key signed for it and relaying to the grid what they ask.
// It is a container of the test's own, of the stack's image the gate
// built (testbox.StackImage), removed when the test ends; the stack is
// not touched.
type Door struct {
	// Address is where the door answers, on the loopback.
	Address string
	// Authorities is a PEM file of the authority its certificate is of.
	Authorities string
	key         *ecdsa.PrivateKey
}

// relayed are the services the door hands on to the grid.
var relayed = []string{
	"build.bazel.remote.execution.v2.Capabilities",
	"build.bazel.remote.execution.v2.ContentAddressableStorage",
	"build.bazel.remote.execution.v2.ActionCache",
	"build.bazel.remote.execution.v2.Execution",
	"google.bytestream.ByteStream",
}

// audience is whom a token the door admits is for: one its key signed
// for another is not admitted.
const audience = "frontseat-test-door"

// storage is where the stack's image holds bb-storage
// (stack/image/Dockerfile).
const storage = "/usr/local/bin/bb_storage"

// NewDoor starts a door in front of the test grid, and skips the test
// where no grid answers or this machine has no container runtime.
func NewDoor(t testing.TB) *Door {
	t.Helper()
	_, port, err := net.SplitHostPort(Address(t))
	if err != nil {
		t.Fatal(err)
	}
	image := testbox.StackImage(t)
	known := testauthority.New(t)
	door := &Door{Authorities: known.File, key: signingKey(t)}
	// Read by the container's user, whoever that is: a test's key, good
	// for an hour, of a server on the loopback.
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{
		"global":                  map[string]any{},
		"maximumMessageSizeBytes": 16 << 20,
		"grpcServers": []any{map[string]any{
			"listenAddresses": []string{":8980"},
			"tls": map[string]any{"serverKeyPair": map[string]any{"files": map[string]any{
				"certificatePath": "/door/tls.crt", "privateKeyPath": "/door/tls.key", "refreshInterval": "3600s",
			}}},
			"authenticationPolicy": map[string]any{"jwt": map[string]any{
				"jwksInline":                           map[string]any{"keys": []any{jwk(&door.key.PublicKey)}},
				"maximumCacheSize":                     100,
				"cacheReplacementPolicy":               "LEAST_RECENTLY_USED",
				"claimsValidationJmespathExpression":   map[string]any{"expression": "payload.aud == '" + audience + "'"},
				"metadataExtractionJmespathExpression": map[string]any{"expression": "`{}`"},
			}},
			"relays": []any{map[string]any{
				"endpoint": map[string]any{"address": net.JoinHostPort("host.docker.internal", port)},
				"services": relayed,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"door.json": config, "tls.crt": known.Certificate, "tls.key": known.Key} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// This container, by the name it is given here, and no other. It is
	// to be removed before it is made: one the runtime made and could not
	// start, or made while its answer was lost, is removed all the same.
	id := testrun.ID(t, "frontseat-test-door")
	t.Cleanup(func() {
		out, err := exec.Command("docker", "rm", "--force", "--volumes", id).CombinedOutput()
		if err != nil && !strings.Contains(string(out), "No such container") {
			t.Errorf("removing the door %s: %v: %s", id, err, out)
		}
	})
	out, err := exec.Command("docker", "run", "--detach", "--pull=never",
		"--name", id, "--label", testrun.Label+"="+testrun.This(),
		"--publish", "127.0.0.1::8980", "--add-host", "host.docker.internal:host-gateway",
		"--volume", dir+":/door:ro", "--entrypoint", storage, "--", image, "/door/door.json").CombinedOutput()
	if err != nil {
		t.Fatalf("starting the door: %v: %s", err, out)
	}
	published, err := exec.Command("docker", "port", id, "8980/tcp").Output()
	if err != nil {
		t.Fatalf("the door's port: %v", err)
	}
	door.Address = strings.TrimSpace(strings.SplitN(string(published), "\n", 2)[0])
	authorities := x509.NewCertPool()
	pem, err := os.ReadFile(known.File)
	if err != nil || !authorities.AppendCertsFromPEM(pem) {
		t.Fatalf("the door's authority: %v", err)
	}
	// It answers once it shows its certificate.
	began := time.Now()
	err = poll.Until(context.Background(), 200*time.Millisecond, func() (bool, error) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", door.Address, &tls.Config{RootCAs: authorities, NextProtos: []string{"h2"}})
		if err == nil {
			conn.Close()
			return true, nil
		}
		if time.Since(began) > 30*time.Second {
			logs, _ := exec.Command("docker", "logs", "--tail", "20", id).CombinedOutput()
			return false, fmt.Errorf("the door did not answer at %s: %v\n%s", door.Address, err, logs)
		}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return door
}

// Token writes a token the door admits to a file, and returns the file.
func (d *Door) Token(t testing.TB) string { return written(t, d.key, audience, 10*time.Minute) }

// Forged writes a token of the same shape that another key signed.
func (d *Door) Forged(t testing.TB) string {
	return written(t, signingKey(t), audience, 10*time.Minute)
}

// Expired writes a token the door's key signed that ran out a minute ago.
func (d *Door) Expired(t testing.TB) string { return written(t, d.key, audience, -time.Minute) }

// Misdirected writes a token the door's key signed for another audience.
func (d *Door) Misdirected(t testing.TB) string {
	return written(t, d.key, "another grid", 10*time.Minute)
}

func signingKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func jwk(key *ecdsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig",
		"x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
		"y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
	}
}

// written signs a token for an audience, good for so long (RFC 7519,
// ES256), and writes it as a writer of one does: the token and a line's
// end.
func written(t testing.TB, key *ecdsa.PrivateKey, audience string, lasts time.Duration) string {
	t.Helper()
	part := func(v any) string {
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(encoded)
	}
	signed := part(map[string]string{"alg": "ES256", "typ": "JWT"}) + "." + part(map[string]any{"sub": "a test's daemon", "aud": audience, "exp": time.Now().Add(lasts).Unix()})
	digest := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(signed+"."+base64.RawURLEncoding.EncodeToString(signature)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}
