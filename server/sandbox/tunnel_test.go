package sandbox_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
)

// through opens a port of a sandbox through a server, as a WebSocket of
// binary messages, and returns all the port says before it closes.
func through(t *testing.T, server, sandbox string, port int) (string, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	socket, response, err := websocket.Dial(ctx, fmt.Sprintf("%s/sandboxes/%s/tunnels/%d", server, sandbox, port), nil)
	if err != nil {
		return "", response, err
	}
	conn := websocket.NetConn(ctx, socket, websocket.MessageBinary)
	defer conn.Close()
	said, err := io.ReadAll(conn)
	return string(said), response, err
}

// What is not HTTP reaches a port of a sandbox as bytes: SSH above all.
// The memory driver's ports say the sandbox's id and close, so that the
// bytes arrived, and that the tunnel ended when the port did, are both
// seen.
func TestAPortOfASandboxIsReachedAsBytesThroughTheServer(t *testing.T) {
	client, srv := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Image: "box", Ports: []int32{2222}})

	said, _, err := through(t, srv.URL, made.GetId(), 2222)

	if err != nil {
		t.Fatalf("the tunnel did not end as the port did: %v", err)
	}
	if strings.TrimSpace(said) != made.GetId() {
		t.Errorf("the port said %q through the tunnel, want the sandbox's id", said)
	}
}

// A server is behind whatever stands in front of it on a platform, and
// what stands there speaks HTTP: the tunnel is HTTP's own upgrade, so
// it passes.
func TestATunnelPassesWhatStandsInFrontOfTheServer(t *testing.T) {
	client, srv := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Image: "box", Ports: []int32{2222}})
	behind, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(httputil.NewSingleHostReverseProxy(behind))
	defer front.Close()

	said, _, err := through(t, front.URL, made.GetId(), 2222)

	if err != nil || strings.TrimSpace(said) != made.GetId() {
		t.Errorf("through what stands in front, the port said %q (%v), want the sandbox's id", said, err)
	}
}

func TestATunnelIsOpenedOnlyToAPortOfASandboxTheServerMade(t *testing.T) {
	client, srv := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Image: "box", Ports: []int32{2222}})

	for name, to := range map[string]struct {
		sandbox string
		port    int
		want    int
	}{
		"a sandbox nobody made":           {"sbx_nobody", 2222, http.StatusNotFound},
		"a port the sandbox never opened": {made.GetId(), 9, http.StatusBadGateway},
	} {
		_, response, err := through(t, srv.URL, to.sandbox, to.port)
		if err == nil || response == nil || response.StatusCode != to.want {
			t.Errorf("%s: %v (%v), want it refused with %d", name, err, response, to.want)
		}
	}
	// And what a port of a sandbox serves over HTTP is still HTTP there.
	response, err := http.Get(fmt.Sprintf("%s/sandboxes/%s/tunnels/2222", srv.URL, made.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode < 400 {
		t.Errorf("a plain request for a tunnel was answered %s, want it refused", response.Status)
	}
}
