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

	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
)

// Only a worker says what an action produced. A client that could write
// an action's result would have every build after it take that for what
// the action did: a fetch that "passed" with a tree of the client's own
// making.
func TestAClientCannotSayWhatAnActionProduced(t *testing.T) {
	conn, err := grpc.NewClient(testgrid.Address(t), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = repb.NewActionCacheClient(conn).UpdateActionResult(context.Background(), &repb.UpdateActionResultRequest{
		ActionDigest: &repb.Digest{Hash: strings.Repeat("ab", 32), SizeBytes: 140},
		ActionResult: &repb.ActionResult{ExitCode: 0},
	})

	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("a client wrote an action's result: %v, want permission denied", err)
	}
}
