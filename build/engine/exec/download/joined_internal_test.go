package download

import (
	"context"
	"errors"
	"testing"

	remoteasset "github.com/bazelbuild/remote-apis/build/bazel/remote/asset/v1"
	"google.golang.org/grpc"

	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
)

// unwinding is an asset service whose one ask is told to stop and goes
// on a while.
type unwinding struct {
	remoteasset.FetchClient
	call *toolstest.Unwinding
}

func (u unwinding) FetchBlob(ctx context.Context, _ *remoteasset.FetchBlobRequest, _ ...grpc.CallOption) (*remoteasset.FetchBlobResponse, error) {
	return nil, u.call.Hold(ctx)
}

// An ask of the asset service runs on no waiter's context, so that one
// that gives up leaves the others their answer: it goes on with none
// waiting. Closing the client stops it and returns only once it has
// stopped, so that the connection is let go of with nothing asking over
// it; and a client that was closed is asked nothing more, which it says.
// The control is the ask itself, still going before it is let.
func TestClosingAClientStopsWhatIsAskingAndReturnsOnceItHasStopped(t *testing.T) {
	client, err := Dial(t.Context(), Config{Address: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	call := toolstest.NewUnwinding()
	client.service = unwinding{call: call}

	waiter, giveUp := context.WithCancel(t.Context())
	gaveUp := make(chan error, 1)
	go func() {
		_, err := client.Fetch(waiter, "https://example.com/tool.tar.gz", "")
		gaveUp <- err
	}()
	call.Begun(t)
	giveUp()
	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Fatalf("whoever gave up was told %v", err)
	}

	closed := make(chan struct{})
	go func() { _ = client.Close(); close(closed) }()
	call.KeepsBack(t, "closing the client", closed)

	if _, err := client.Fetch(t.Context(), "https://example.com/other.tar.gz", ""); !errors.Is(err, ErrClosed) {
		t.Errorf("a closed client asked for a download answered %v; want %v", err, ErrClosed)
	}
}
