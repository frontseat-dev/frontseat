package build

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/fakes"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

type connectionStats struct{ opened, closed atomic.Int32 }

func (s *connectionStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (s *connectionStats) HandleRPC(context.Context, stats.RPCStats) {}
func (s *connectionStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (s *connectionStats) HandleConn(_ context.Context, event stats.ConnStats) {
	switch event.(type) {
	case *stats.ConnBegin:
		s.opened.Add(1)
	case *stats.ConnEnd:
		s.closed.Add(1)
	}
}
func TestPublicConstructorReleasesOwnedConnections(t *testing.T) {
	for _, mode := range []string{"bad-plugin", "bad-history", "success"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			tracker := &connectionStats{}
			srv := grpc.NewServer(grpc.StatsHandler(tracker))
			t.Cleanup(srv.Stop)
			cas, cache := fakes.NewCAS(), fakes.NewActionCache()
			repb.RegisterCapabilitiesServer(srv, fakes.NewExec(t, cache, cas))
			go srv.Serve(listener)
			config := Config{Workspace: t.TempDir(), Mounted: true, Builds: t.TempDir(), Grid: Grid{Address: listener.Addr().String()}, Plugins: [][]byte{module}}
			if mode == "bad-plugin" {
				config.Plugins = [][]byte{[]byte("invalid wasm")}
			}
			if mode == "bad-history" {
				file := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				config.Builds = file
			}
			daemon, err := New(t.Context(), config)
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(daemon.Close)
				if tracker.opened.Load() == 0 || tracker.closed.Load() != 0 {
					t.Fatalf("live connection control: opened %d closed %d", tracker.opened.Load(), tracker.closed.Load())
				}
				var group sync.WaitGroup
				for range 8 {
					group.Go(daemon.Close)
				}
				group.Wait()
				again, err := New(t.Context(), config)
				if err != nil {
					t.Fatalf("history was not released: %v", err)
				}
				t.Cleanup(again.Close)
				again.Close()
			} else {
				if err == nil {
					daemon.Close()
					t.Fatal("invalid configuration accepted")
				}
				t.Log(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for tracker.opened.Load() == 0 || tracker.closed.Load() != tracker.opened.Load() {
				if time.Now().After(deadline) {
					t.Fatalf("connections opened %d closed %d", tracker.opened.Load(), tracker.closed.Load())
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
