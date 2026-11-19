package reapi

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A worker that has no frontseat-action of the daemon's form is named as
// that, from what bb_runner says when it cannot start the program; what
// it says of another program, or of this one failing otherwise, or under
// another code, is not taken for it. The message is the one the local
// grid gave (2026-11-13) for a form no worker had.
func TestAWorkerWithoutTheDaemonsConfinerIsNamed(t *testing.T) {
	said := "Failed to run command: Failed to start process: fork/exec /bb/frontseat-action-0123abcd: no such file or directory"
	if confiner, lacks := lacking(status.New(codes.InvalidArgument, said)); !lacks || confiner != "/bb/frontseat-action-0123abcd" {
		t.Errorf("a missing confiner was taken for %q, %v", confiner, lacks)
	}
	for way, other := range map[string]*status.Status{
		"another program":        status.New(codes.InvalidArgument, "Failed to run command: Failed to start process: fork/exec /usr/bin/go: no such file or directory"),
		"the confiner otherwise": status.New(codes.InvalidArgument, "Failed to run command: Failed to start process: fork/exec /bb/frontseat-action-0123abcd: permission denied"),
		"under another code":     status.New(codes.Internal, said),
		"saying nothing":         status.New(codes.InvalidArgument, ""),
	} {
		if confiner, lacks := lacking(other); lacks {
			t.Errorf("%s was taken for a missing confiner: %q", way, confiner)
		}
	}
}
