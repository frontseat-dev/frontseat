package download_test

import (
	"strings"
	"testing"

	remoteasset "github.com/bazelbuild/remote-apis/build/bazel/remote/asset/v1"
	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/build/engine/exec/download"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
)

// gridAssets is a client of the grid's own asset service, which fetches
// a public site's URL as it is.
func gridAssets(t *testing.T) *download.Client {
	t.Helper()
	client, err := download.Dial(t.Context(), download.Config{Address: testgrid.Assets(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// gridAssetsConnection is a connection to the grid's own asset service,
// for what the client does not ask.
func gridAssetsConnection(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///"+testgrid.Assets(t), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// The grid's own asset service fetches a crate from crates.io and
// answers with content whose digest is the sha256 asked for; asked for
// content with another sha256, it keeps nothing and refuses. The control
// is what proves the check: a service that did not check would answer
// the wrong checksum with the crate all the same.
func TestTheAssetServiceFetchesByChecksumAndRefusesAnother(t *testing.T) {
	client := gridAssets(t)
	fetchesItoa(t, client)
	wrong := sum[:63] + "0"
	_, err := client.Fetch(t.Context(), public, wrong)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "checksum") {
		t.Errorf("the wrong checksum: %v; want a refusal that names the checksum", err)
	}
}

// Nothing pushes an asset to the grid's service: what it remembers it
// fetched and checked itself. A push, even of content the store holds,
// is refused.
func TestNothingPushesAnAssetToTheService(t *testing.T) {
	conn := gridAssetsConnection(t)
	_, err := remoteasset.NewPushClient(conn).PushBlob(t.Context(), &remoteasset.PushBlobRequest{
		Uris:       []string{"https://static.crates.io/crates/itoa/1.0.18/download"},
		Qualifiers: []*remoteasset.Qualifier{{Name: "checksum.sri", Value: "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}},
		BlobDigest: &remoteexecution.Digest{Hash: strings.Repeat("0", 64), SizeBytes: 1},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("a push was answered %v; want it refused", err)
	}
}

// What the service is asked for with no checksum is kept under a key no
// checked request reads: a daemon asking by checksum is answered from
// the origin, and refused where the origin differs, whatever was asked
// before without one. The control: the unchecked request is answered.
func TestAnUncheckedRequestNeverAnswersACheckedOne(t *testing.T) {
	conn := gridAssetsConnection(t)
	unchecked, err := remoteasset.NewFetchClient(conn).FetchBlob(t.Context(), &remoteasset.FetchBlobRequest{Uris: []string{public}})
	if err != nil || unchecked.GetBlobDigest().GetHash() != sum {
		t.Fatalf("the unchecked request: %v, %v; want itoa's archive", unchecked.GetBlobDigest(), err)
	}
	client := gridAssets(t)
	// The service's own refusal, of content it fetched again from the
	// origin; had it answered from the unchecked entry, the client would
	// have refused the digest itself, in other words.
	if _, err := client.Fetch(t.Context(), public, strings.Repeat("1", 64)); err == nil || !strings.Contains(err.Error(), "did not match checksum.sri") {
		t.Errorf("a checked request after an unchecked one: %v; want the service's refusal of what it fetched", err)
	}
}
