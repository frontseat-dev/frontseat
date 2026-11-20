package reapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/client"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/fakes"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/filemetadata"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	bspb "google.golang.org/genproto/googleapis/bytestream"
	oppb "google.golang.org/genproto/googleapis/longrunning"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// Cancellation is part of the Executor contract even when it arrives as a
// transport status or an ExecuteResponse. Its caller need not speak gRPC.
func TestGridErrorsKeepTheirCancellationMeaning(t *testing.T) {
	for _, operation := range []string{"upload", "execute", "executing", "merge"} {
		for _, code := range []codes.Code{codes.OK, codes.Canceled, codes.DeadlineExceeded, codes.Internal, codes.Unauthenticated, codes.PermissionDenied} {
			t.Run(operation+"/"+code.String(), func(t *testing.T) {
				grid := answeringGrid(t, operation, code)
				var err error
				if operation == "merge" {
					// Two different directories force a read. The fake CAS holds
					// both so the success control proves this is a valid merge.
					var all []exec.Held
					for _, name := range []string{"one", "two"} {
						at, writeErr := grid.client.WriteProto(t.Context(), &repb.Directory{
							Symlinks: []*repb.SymlinkNode{{Name: name, Target: "target"}},
						})
						if writeErr != nil {
							t.Fatal(writeErr)
						}
						all = append(all, exec.Held{Directory: true, Digest: exec.Digest{Hash: at.Hash, Size: at.Size}})
					}
					_, err = grid.Merge(t.Context(), all)
				} else {
					_, err = grid.Execute(t.Context(), exec.Action{
						Label: "//test:compile", Root: t.TempDir(), Inputs: []string{"."}, Arguments: []string{"tool"},
					})
				}
				if status.Code(err) != code {
					t.Errorf("status = %v, want %s: %v", status.Code(err), code, err)
				}
				if got := errors.Is(err, context.Canceled); got != (code == codes.Canceled) {
					t.Errorf("cancellation = %t: %v", got, err)
				}
				if got := errors.Is(err, context.DeadlineExceeded); got != (code == codes.DeadlineExceeded) {
					t.Errorf("deadline = %t: %v", got, err)
				}
				if code != codes.OK && (err == nil || !strings.Contains(err.Error(), "grid test error")) {
					t.Errorf("lost grid error detail: %v", err)
				}
				// A grid that stops admitting a daemon while it builds, its
				// token having run out, says so as one that never admitted
				// it does; no other failure is said to be that.
				// What a worker was refused while it ran an action comes in
				// the grid's answer for the action ("execute"), not as the
				// call's own status ("executing"): the daemon was admitted,
				// and is not told to renew a token.
				refused := (code == codes.Unauthenticated || code == codes.PermissionDenied) && operation != "execute"
				if said := err != nil && strings.Contains(err.Error(), "did not admit this daemon"); said != refused || NotAdmitted(err) != refused {
					t.Errorf("said not admitted = %t, want %t: %v", said, refused, err)
				}
				if operation == "execute" && (code == codes.Unauthenticated || code == codes.PermissionDenied) && (!strings.Contains(err.Error(), "where it ran") || strings.Contains(err.Error(), "--grid-token")) {
					t.Errorf("a worker's refusal: %v", err)
				}
			})
		}
	}
}

// The SDK supplies the CAS, cache and capabilities implementation. Only the
// response under test is supplied here, through the real gRPC client path.
func answeringGrid(t *testing.T, operation string, code codes.Code) *Grid {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refuse := func(method string) error {
		if code != codes.OK && (operation == "upload" && strings.HasSuffix(method, "/FindMissingBlobs") || operation == "merge" && method == "/google.bytestream.ByteStream/Read" || operation == "executing" && strings.HasSuffix(method, "/Execute")) {
			return fmt.Errorf("wrapped grid failure: %w", status.Error(code, "grid test error"))
		}
		return nil
	}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := refuse(info.FullMethod); err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := refuse(info.FullMethod); err != nil {
				return err
			}
			return handler(srv, stream)
		}),
	)
	cas, cache := fakes.NewCAS(), fakes.NewActionCache()
	repb.RegisterContentAddressableStorageServer(server, cas)
	bspb.RegisterByteStreamServer(server, cas)
	repb.RegisterActionCacheServer(server, cache)
	repb.RegisterCapabilitiesServer(server, fakes.NewExec(t, cache, cas))
	answer := codes.OK
	if operation == "execute" {
		answer = code
	}
	repb.RegisterExecutionServer(server, executionAnswer{code: answer})
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	c, err := client.NewClient(t.Context(), "instance", client.DialParams{Service: listener.Addr().String(), NoSecurity: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Retrier = nil
	t.Cleanup(func() { _ = c.Close() })
	return &Grid{client: c, files: filemetadata.NewNoopCache()}
}

type executionAnswer struct {
	repb.UnimplementedExecutionServer
	code codes.Code
}

func (a executionAnswer) Execute(_ *repb.ExecuteRequest, stream repb.Execution_ExecuteServer) error {
	response, err := anypb.New(&repb.ExecuteResponse{
		Result: &repb.ActionResult{}, Status: status.New(a.code, "grid test error").Proto(),
	})
	if err != nil {
		return err
	}
	return stream.Send(&oppb.Operation{Done: true, Result: &oppb.Operation_Response{Response: response}})
}
