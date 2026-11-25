package reapitest

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Answering is a grid on the loopback that answers what it serves, counts
// how often it was asked, and keeps the authorization each call carried,
// of whatever service it was asked.
type Answering struct {
	repb.UnimplementedCapabilitiesServer
	// Code is the status it answers with; OK, that it runs actions.
	Code codes.Code
	// Words, where set, is what it says with Code in place of its own.
	Words string
	// Admits, where set, is the one authorization it admits a call by.
	Admits string
	// Certificate, where set, is what it shows: it answers over TLS.
	Certificate *tls.Certificate
	// ClientAuth is what it asks of a client's certificate over TLS, and
	// Version, where set, the latest TLS it speaks.
	ClientAuth tls.ClientAuthType
	Version    uint16
	// ExecutesNothing, where set, has it answer that it runs no action
	// (no execution capabilities enabled).
	ExecutesNothing bool
	// Also, where set, registers what else it serves: an asset service.
	Also  func(*grpc.Server)
	Asked atomic.Int32

	mu      sync.Mutex
	carried [][]string
}

func (a *Answering) GetCapabilities(ctx context.Context, _ *repb.GetCapabilitiesRequest) (*repb.ServerCapabilities, error) {
	if a.Code != codes.OK {
		words := "the grid's own words"
		if a.Words != "" {
			words = a.Words
		}
		return nil, status.Error(a.Code, words)
	}
	return &repb.ServerCapabilities{ExecutionCapabilities: &repb.ExecutionCapabilities{ExecEnabled: !a.ExecutesNothing, DigestFunction: repb.DigestFunction_SHA256}, CacheCapabilities: &repb.CacheCapabilities{DigestFunctions: []repb.DigestFunction_Value{repb.DigestFunction_SHA256}}}, nil
}

// asked notes a call of any service and admits it or not.
func (a *Answering) asked(ctx context.Context) error {
	a.Asked.Add(1)
	md, _ := metadata.FromIncomingContext(ctx)
	carried := md.Get("authorization")
	a.mu.Lock()
	a.carried = append(a.carried, carried)
	a.mu.Unlock()
	if a.Admits != "" && (len(carried) != 1 || carried[0] != a.Admits) {
		return status.Error(codes.Unauthenticated, "not admitted")
	}
	return nil
}

// Carried is the authorization of the last call, nothing where it bore
// none. It fails the test where the grid was never asked.
func (a *Answering) Carried(t testing.TB) []string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.carried) == 0 {
		t.Fatal("the grid was not asked")
	}
	return a.carried[len(a.carried)-1]
}

// Serve starts the grid and returns its address.
func (a *Answering) Serve(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var options []grpc.ServerOption
	if a.Certificate != nil {
		options = append(options, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{*a.Certificate}, ClientAuth: a.ClientAuth, MaxVersion: a.Version})))
	}
	options = append(options,
		grpc.UnaryInterceptor(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := a.asked(ctx); err != nil {
				return nil, err
			}
			return handler(ctx, request)
		}),
		grpc.StreamInterceptor(func(served any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := a.asked(stream.Context()); err != nil {
				return err
			}
			return handler(served, stream)
		}))
	server := grpc.NewServer(options...)
	repb.RegisterCapabilitiesServer(server, a)
	if a.Also != nil {
		a.Also(server)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	return listener.Addr().String()
}

// Key is the key the grid knows an action by, said without asking the
// grid anything: a grid of the test's own answers only what a client
// asks it as it connects.
func Key(t testing.TB, action exec.Action) string {
	t.Helper()
	grid, err := reapi.Dial(t.Context(), reapi.Config{Address: (&Answering{}).Serve(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { grid.Close() })
	key, err := grid.Key(t.Context(), action)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
