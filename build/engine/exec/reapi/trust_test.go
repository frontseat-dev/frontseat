package reapi_test

import (
	"context"
	"strings"
	"testing"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
)

// results are the grid's results of actions, as any client of the grid
// asks for them: over the Remote Execution API, in the clear, with
// nothing of frontseat's between.
func results(t *testing.T) repb.ActionCacheClient {
	t.Helper()
	conn, err := grpc.NewClient(testgrid.Address(t), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return repb.NewActionCacheClient(conn)
}

// Only a worker says what an action produced. A client that could write
// an action's result would have every build after it take that for what
// the action did: a fetch that "passed" with a tree of the client's own
// making.
func TestAClientCannotSayWhatAnActionProduced(t *testing.T) {
	_, err := results(t).UpdateActionResult(context.Background(), &repb.UpdateActionResultRequest{
		ActionDigest: &repb.Digest{Hash: strings.Repeat("ab", 32), SizeBytes: 140},
		ActionResult: &repb.ActionResult{ExitCode: 0},
	})

	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("a client wrote an action's result: %v, want permission denied", err)
	}
}

// What a worker said of an action it ran is what every client is
// answered, and no client's to replace: the result of an action the
// grid's worker ran is found by the action's digest, a client that
// writes another over it is refused, permission denied, and what is
// found afterwards is still the worker's. The finding is the control of
// the refusal: the cache is one that is read, and that a worker writes.
func TestWhatAWorkerSaidOfAnActionIsFoundAndIsNoClientsToReplace(t *testing.T) {
	ctx := context.Background()
	ran, err := dial(t).Execute(ctx, shell(root(t, map[string]string{"a.txt": "a\n"}), "cat a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if ran.ExitCode != 0 || ran.Cached {
		t.Fatalf("exit %d, cached %v; want it executed by a worker and passed", ran.ExitCode, ran.Cached)
	}
	action := &repb.Digest{Hash: ran.Action.Hash, SizeBytes: ran.Action.Size}
	cache := results(t)
	// found is the exit code the grid keeps for the action.
	found := func() int32 {
		t.Helper()
		kept, err := cache.GetActionResult(ctx, &repb.GetActionResultRequest{ActionDigest: action})
		if err != nil {
			t.Fatalf("the result of an action a worker ran is not found: %v", err)
		}
		return kept.GetExitCode()
	}
	if exit := found(); exit != 0 {
		t.Fatalf("the grid keeps exit %d for the action a worker ran to exit 0", exit)
	}

	_, err = cache.UpdateActionResult(ctx, &repb.UpdateActionResultRequest{ActionDigest: action, ActionResult: &repb.ActionResult{ExitCode: 7}})

	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("a client wrote over what a worker said of an action: %v, want permission denied", err)
	}
	if exit := found(); exit != 0 {
		t.Errorf("after a client wrote exit 7 over it, the grid keeps exit %d for the action, want the worker's 0", exit)
	}
}
