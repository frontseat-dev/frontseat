package download_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	remoteasset "github.com/bazelbuild/remote-apis/build/bazel/remote/asset/v1"
	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/server/internal/exec/access"
	"github.com/frontseat-dev/frontseat/server/internal/exec/download"
	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/server/internal/place"
	"github.com/frontseat-dev/frontseat/server/testsupport/testauthority"
)

// service stands in for an asset service: it answers each URI with the
// digest it was told to, or with a status, and remembers what it was
// asked.
type service struct {
	remoteasset.UnimplementedFetchServer
	mu     sync.Mutex
	asked  []*remoteasset.FetchBlobRequest
	by     map[string]*remoteexecution.Digest
	refuse map[string]*status.Status
}

func (s *service) FetchBlob(_ context.Context, request *remoteasset.FetchBlobRequest) (*remoteasset.FetchBlobResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, request)
	uri := request.GetUris()[0]
	if refused := s.refuse[uri]; refused != nil {
		return &remoteasset.FetchBlobResponse{Status: refused, Uri: uri}, nil
	}
	return &remoteasset.FetchBlobResponse{Status: &status.Status{}, Uri: uri, BlobDigest: s.by[uri]}, nil
}

func (s *service) requests() []*remoteasset.FetchBlobRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*remoteasset.FetchBlobRequest(nil), s.asked...)
}

// serve starts the stand-in and returns a client of it over the rewrites.
func serve(t *testing.T, s *service, rewrites []download.Rewrite) *download.Client {
	t.Helper()
	return serveAs(t, s, "lab", rewrites)
}

// serveAs starts an asset service and returns a client of it, of the
// instance named, over the rewrites.
func serveAs(t *testing.T, s remoteasset.FetchServer, instance string, rewrites []download.Rewrite) *download.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	remoteasset.RegisterFetchServer(server, s)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := download.Dial(t.Context(), download.Config{Address: listener.Addr().String(), Instance: instance, Rewrites: rewrites})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// fetchesItoa fails the test unless the client fetches itoa 1.0.18's
// archive by its sha256, and is answered with content of that digest.
func fetchesItoa(t *testing.T, client *download.Client) {
	t.Helper()
	digest, err := client.Fetch(t.Context(), public, sum)
	if err != nil {
		t.Fatal(err)
	}
	if digest.Hash != sum || digest.Size != 15935 {
		t.Errorf("digest = %+v; want itoa 1.0.18's archive by its sha256", digest)
	}
}

const (
	public  = "https://static.crates.io/crates/itoa/1.0.18/download"
	proxied = "http://artifacts:8080/api/cargo/crates/api/v1/crates/itoa/1.0.18/download"
	sum     = "8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682"
)

var rewrites = []download.Rewrite{{From: `^https://static\.crates\.io/crates/([^/]+)/([^/]+)/download$`, To: "http://artifacts:8080/api/cargo/crates/api/v1/crates/$1/$2/download"}}

// A download is asked of the asset service by the proxy's address for it,
// with its sha256 as the checksum the service must see, and the answer
// names content by that sha256.
func TestADownloadIsAskedOfTheAssetServiceThroughTheProxyByItsChecksum(t *testing.T) {
	s := &service{by: map[string]*remoteexecution.Digest{proxied: {Hash: sum, SizeBytes: 15935}}}
	client := serve(t, s, rewrites)
	fetchesItoa(t, client)
	asked := s.requests()
	if len(asked) != 1 {
		t.Fatalf("asked %d times", len(asked))
	}
	raw, _ := hex.DecodeString(sum)
	request := asked[0]
	switch {
	case request.GetInstanceName() != "lab":
		t.Errorf("instance %q", request.GetInstanceName())
	case len(request.GetUris()) != 1 || request.GetUris()[0] != proxied:
		t.Errorf("uris %v; want the proxy's address alone", request.GetUris())
	case len(request.GetQualifiers()) != 1 || request.GetQualifiers()[0].GetName() != "checksum.sri" || request.GetQualifiers()[0].GetValue() != "sha256-"+base64.StdEncoding.EncodeToString(raw):
		t.Errorf("qualifiers %v", request.GetQualifiers())
	case request.GetDigestFunction() != remoteexecution.DigestFunction_SHA256:
		t.Errorf("digest function %v", request.GetDigestFunction())
	case request.GetTimeout() == nil:
		t.Error("no timeout: the service would take as long as it likes")
	}
}

// What the service answered is remembered for the daemon's life, and
// asked again only once a run has let go of what it remembered.
func TestAnAnswerIsRememberedUntilLetGo(t *testing.T) {
	s := &service{by: map[string]*remoteexecution.Digest{proxied: {Hash: sum, SizeBytes: 1}}}
	client := serve(t, s, rewrites)
	for range 3 {
		if _, err := client.Fetch(t.Context(), public, sum); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(s.requests()); n != 1 {
		t.Errorf("asked %d times for one download; want once", n)
	}
	client.Forget()
	if _, err := client.Fetch(t.Context(), public, sum); err != nil {
		t.Fatal(err)
	}
	if n := len(s.requests()); n != 2 {
		t.Errorf("asked %d times after letting go; want twice", n)
	}
	// Another checksum of the same URL is another download.
	other := hex.EncodeToString(sha256.New().Sum(nil))
	s.by[proxied] = &remoteexecution.Digest{Hash: other, SizeBytes: 1}
	if _, err := client.Fetch(t.Context(), public, other); err != nil {
		t.Fatal(err)
	}
	if n := len(s.requests()); n != 3 {
		t.Errorf("asked %d times for a second checksum; want three", n)
	}
}

// A URL no proxy stands in for, and that is no public site's, is refused
// before the service is asked; the service's refusal is told; and content
// that is not what was asked for is refused, whatever the service says.
func TestWhatIsNotADownloadOfTheProxysIsRefused(t *testing.T) {
	wrong := strings.Repeat("0", 64)
	s := &service{
		by:     map[string]*remoteexecution.Digest{proxied: {Hash: wrong, SizeBytes: 1}},
		refuse: map[string]*status.Status{"http://artifacts:8080/api/cargo/crates/api/v1/crates/gone/0.1.0/download": {Code: int32(codes.NotFound), Message: "the registry has no gone 0.1.0"}},
	}
	client := serve(t, s, rewrites)
	for name, try := range map[string]struct{ url, sum, want string }{
		"a URL of the proxy's itself": {proxied, sum, "it is not https"},
		"an address":                  {"https://127.0.0.1:8981/x", sum, "no public site's name"},
		"this machine":                {"https://localhost/x", sum, "no public site's name"},
		"a name of the stack's":       {"https://host.openshell.internal:8980/x", sum, "no public site's name"},
		"a name of no domain":         {"https://storage/x", sum, "no public site's name"},
		"a user":                      {"https://me:secret@example.com/x", sum, "carries a user"},
		"a full-width address":        {"https://１２７.0.0.1/x", sum, "no plain name"},
		"a full-width dot":            {"https://host.docker．internal/x", sum, "no plain name"},
		"what the service refuses":    {"https://static.crates.io/crates/gone/0.1.0/download", sum, "the registry has no gone 0.1.0"},
		"content of another checksum": {public, sum, "whose sha256 is " + wrong},
		"a checksum that is none":     {public, "abc", "is no sha256"},
		"an upper-case checksum":      {public, strings.ToUpper(sum), "is no sha256"},
	} {
		_, err := client.Fetch(t.Context(), try.url, try.sum)
		if err == nil || !strings.Contains(err.Error(), try.want) {
			t.Errorf("%s: %v; want %q", name, err, try.want)
		}
	}
	for _, request := range s.requests() {
		if !strings.HasPrefix(request.GetUris()[0], "http://artifacts:8080/") {
			t.Errorf("the service was asked for %s, which is not the proxy's", request.GetUris()[0])
		}
	}
}

// A URL may hold a password, or a token among what it asks for: one
// that is refused is not repeated to whoever is told of the refusal.
// The control is the same URL without the user, which is asked as it is.
func TestARefusedDownloadsAddressIsNotRepeated(t *testing.T) {
	const asked = "https://example.com/x?sig=tok3n"
	s := &service{by: map[string]*remoteexecution.Digest{asked: {Hash: sum, SizeBytes: 1}}}
	client := serve(t, s, rewrites)
	for name, url := range map[string]string{
		"a user and a password": "https://me:s3cret@example.com/x?sig=tok3n",
		"a host of no site":     "https://tok3n/x?sig=tok3n",
		"a host not plain":      "https://tok3n．internal/x",
		"no URL":                "https://example.com/%zz?sig=tok3n",
		"not https":             "http://example.com/x?sig=tok3n",
	} {
		_, err := client.Fetch(t.Context(), url, sum)
		if err == nil {
			t.Errorf("%s was asked", name)
			continue
		}
		for _, held := range []string{"tok3n", "s3cret"} {
			if strings.Contains(err.Error(), held) {
				t.Errorf("%s: the refusal repeats what it refused: %v", name, err)
			}
		}
	}
	if _, err := client.Fetch(t.Context(), asked, sum); err != nil {
		t.Errorf("the same address with no user: %v", err)
	}
}

// A download a rule proxies is not repeated when the asset service
// refuses it, nor the proxy's address for it, nor when the asker gives
// up: it is said by its site alone. A URL that carries a user is refused
// before any rule is tried, even one written to match it. The control is
// the same download with no user, asked by the proxy's address.
func TestAProxiedDownloadsAddressIsNotRepeated(t *testing.T) {
	const tok3n = "https://static.crates.io/crates/tok3n/1.0.0/download"
	broad := []download.Rewrite{{From: `^https://(?:[^/]*@)?static\.crates\.io/crates/([^/]+)/([^/]+)/download$`, To: "http://artifacts:8080/api/cargo/crates/api/v1/crates/$1/$2/download"}}
	refused := "http://artifacts:8080/api/cargo/crates/api/v1/crates/tok3n/1.0.0/download"
	s := &service{
		by:     map[string]*remoteexecution.Digest{proxied: {Hash: sum, SizeBytes: 1}},
		refuse: map[string]*status.Status{refused: {Code: int32(codes.NotFound), Message: "no such crate"}},
	}
	client := serve(t, s, broad)
	ended, end := context.WithCancel(t.Context())
	end()
	for name, ask := range map[string]struct {
		ctx context.Context
		url string
	}{
		"refused by the service": {t.Context(), tok3n},
		"given up on":            {ended, tok3n},
		"a user a rule matches":  {t.Context(), "https://me:s3cret@static.crates.io/crates/itoa/1.0.18/download"},
	} {
		_, err := client.Fetch(ask.ctx, ask.url, sum)
		if err == nil {
			t.Errorf("%s: answered", name)
			continue
		}
		for _, held := range []string{"tok3n", "s3cret", "artifacts:8080"} {
			if strings.Contains(err.Error(), held) {
				t.Errorf("%s: the error repeats what it was asked: %v", name, err)
			}
		}
		if !strings.Contains(err.Error(), "https://static.crates.io") {
			t.Errorf("%s: %v, want the download said by its site", name, err)
		}
	}
	for _, request := range s.requests() {
		if strings.Contains(request.GetUris()[0], "itoa") {
			t.Errorf("a URL that carries a user was asked as %s", request.GetUris()[0])
		}
	}
	if _, err := client.Fetch(t.Context(), public, sum); err != nil {
		t.Errorf("the same download with no user: %v", err)
	}
}

// A public site's URL no rule maps is asked of the service as it is: no
// proxy is needed to download. The control is a URL a rule maps, asked by
// the proxy's address.
func TestAPublicSitesURLNoRuleMapsIsAskedAsItIs(t *testing.T) {
	direct := "https://example.com/anything.tar.gz"
	s := &service{by: map[string]*remoteexecution.Digest{direct: {Hash: sum, SizeBytes: 1}, proxied: {Hash: sum, SizeBytes: 1}}}
	client := serve(t, s, rewrites)
	for _, url := range []string{direct, public} {
		if _, err := client.Fetch(t.Context(), url, sum); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
	}
	var asked []string
	for _, request := range s.requests() {
		asked = append(asked, request.GetUris()[0])
	}
	if want := []string{direct, proxied}; !slices.Equal(asked, want) {
		t.Errorf("the service was asked for %q, want %q", asked, want)
	}
}

// A rule for some of a site is given before the rule for the rest of
// it, as Bazel's downloader configuration is read from its top: the
// first that matches, in the order given, is taken, whatever order the
// text of the expressions sorts in.
func TestTheFirstRewriteGivenThatMatchesIsTaken(t *testing.T) {
	const some, rest = `^https://github\.com/acme/(.*)$`, `^https://github\.com/(.*)$`
	held := &remoteexecution.Digest{Hash: sum, SizeBytes: 3}
	for name, c := range map[string]struct {
		given []download.Rewrite
		asked string
	}{
		"the rule for some of it first": {[]download.Rewrite{{From: some, To: "http://acme/$1"}, {From: rest, To: "http://mirror/$1"}}, "http://acme/tool.tgz"},
		"the rule for all of it first":  {[]download.Rewrite{{From: rest, To: "http://mirror/$1"}, {From: some, To: "http://acme/$1"}}, "http://mirror/acme/tool.tgz"},
	} {
		t.Run(name, func(t *testing.T) {
			s := &service{by: map[string]*remoteexecution.Digest{"http://acme/tool.tgz": held, "http://mirror/acme/tool.tgz": held}}
			if _, err := serve(t, s, c.given).Fetch(t.Context(), "https://github.com/acme/tool.tgz", sum); err != nil {
				t.Fatal(err)
			}
			if asked := s.requests(); len(asked) != 1 || asked[0].GetUris()[0] != c.asked {
				t.Errorf("asked %v; want %s alone", asked, c.asked)
			}
		})
	}
}

// A rewrite that is no regular expression is refused when the daemon
// starts, not at the first download.
func TestARewriteThatIsNoExpressionIsRefusedAtTheStart(t *testing.T) {
	_, err := download.Dial(t.Context(), download.Config{Address: "127.0.0.1:1", Rewrites: []download.Rewrite{{From: "^https://(", To: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "download rewrite") {
		t.Errorf("err = %v", err)
	}
	if _, err := download.Dial(t.Context(), download.Config{}); err == nil {
		t.Error("no address was accepted")
	}
}

// The unpacking command is frontseat-place of the daemon's form, told
// each archive and its directory as arguments, and runs no shell.
func TestUnpackingIsFrontseatsOwnProgramToldItsArguments(t *testing.T) {
	two := download.Unpacking("a.crate", "vendor/", "b.crate", "vendor/")
	if want := []string{place.Placer, "unpack", "a.crate", "vendor/", "b.crate", "vendor/"}; !slices.Equal(two, want) {
		t.Errorf("command = %q; want %q", two, want)
	}
	if none := download.Unpacking(); !slices.Equal(none, []string{place.Placer, "unpack"}) {
		t.Errorf("the command that places nothing = %q", none)
	}
}

// slow stands in for a service that takes a while to answer, and counts
// what it was asked.
type slow struct {
	remoteasset.UnimplementedFetchServer
	wait  time.Duration
	mu    sync.Mutex
	asked int
}

func (s *slow) FetchBlob(ctx context.Context, request *remoteasset.FetchBlobRequest) (*remoteasset.FetchBlobResponse, error) {
	s.mu.Lock()
	s.asked++
	s.mu.Unlock()
	select {
	case <-time.After(s.wait):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &remoteasset.FetchBlobResponse{Status: &status.Status{}, Uri: request.GetUris()[0], BlobDigest: &remoteexecution.Digest{Hash: sum, SizeBytes: 1}}, nil
}

// Two fetches of one thing at once are one ask of the service, and one
// that is cancelled leaves the other its answer: the ask runs on no
// waiter's context, and each waits only as long as its own lets it.
func TestOneAskServesEveryoneWaitingAndACancelledWaiterLeavesTheOthersTheirAnswer(t *testing.T) {
	s := &slow{wait: 300 * time.Millisecond}
	client := serveAs(t, s, "", rewrites)

	cancelled, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() {
		_, err := client.Fetch(cancelled, public, sum)
		first <- err
	}()
	time.Sleep(50 * time.Millisecond)
	second := make(chan error, 1)
	go func() {
		_, err := client.Fetch(t.Context(), public, sum)
		second <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-first; err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("the cancelled waiter: %v; want its own cancellation", err)
	}
	if err := <-second; err != nil {
		t.Errorf("the other waiter: %v; want the answer", err)
	}
	if s.asked != 1 {
		t.Errorf("the service was asked %d times for one thing asked twice at once", s.asked)
	}
	// The answer is remembered for both.
	if _, err := client.Fetch(t.Context(), public, sum); err != nil || s.asked != 1 {
		t.Errorf("asked again: %v, %d asks", err, s.asked)
	}
}

// An answer that arrives after everything was let go is not remembered:
// what was let go was let go for a reason.
func TestAnAnswerThatArrivesAfterForgettingIsNotRemembered(t *testing.T) {
	s := &slow{wait: 200 * time.Millisecond}
	client := serveAs(t, s, "", rewrites)
	done := make(chan error, 1)
	go func() {
		_, err := client.Fetch(t.Context(), public, sum)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	client.Forget()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(t.Context(), public, sum); err != nil {
		t.Fatal(err)
	}
	if s.asked != 2 {
		t.Errorf("asked %d times; want the answer that came after Forget asked for again", s.asked)
	}
}

// A download with no checksum of the whole is asked of the service with
// none, by the proxy's address, and is answered with whatever digest the
// service found; a checked ask of the same address is another ask, with
// its checksum, and neither answers the other.
func TestAnUncheckedDownloadIsAskedWithNoChecksumAndApartFromACheckedOne(t *testing.T) {
	found := strings.Repeat("c4", 32)
	s := &service{by: map[string]*remoteexecution.Digest{proxied: {Hash: found, SizeBytes: 9}}}
	client := serve(t, s, rewrites)
	for range 2 {
		digest, err := client.Fetch(t.Context(), public, "")
		if err != nil {
			t.Fatal(err)
		}
		if digest.Hash != found || digest.Size != 9 {
			t.Errorf("digest = %+v", digest)
		}
	}
	asked := s.requests()
	if len(asked) != 1 {
		t.Fatalf("asked %d times for one unchecked download; want once", len(asked))
	}
	if len(asked[0].GetQualifiers()) != 0 || asked[0].GetUris()[0] != proxied {
		t.Errorf("the unchecked ask: %v", asked[0])
	}
	// The control: a checked download of the same address is asked for
	// with its checksum, and is not answered with what was unchecked.
	s.mu.Lock()
	s.by[proxied] = &remoteexecution.Digest{Hash: sum, SizeBytes: 15935}
	s.mu.Unlock()
	digest, err := client.Fetch(t.Context(), public, sum)
	if err != nil || digest.Hash != sum {
		t.Fatalf("the checked ask: %+v, %v", digest, err)
	}
	if asked := s.requests(); len(asked) != 2 || len(asked[1].GetQualifiers()) != 1 {
		t.Errorf("the checked ask of an address fetched unchecked: %v", asked)
	}
}

// An unchecked answer that names no content is refused: what the service
// says is placed in an action's root by that name.
func TestAnUncheckedAnswerThatNamesNoContentIsRefused(t *testing.T) {
	for _, hash := range []string{"", "../../etc/passwd", strings.Repeat("C4", 32), strings.Repeat("c4", 31)} {
		s := &service{by: map[string]*remoteexecution.Digest{proxied: {Hash: hash, SizeBytes: 9}}}
		if _, err := serve(t, s, rewrites).Fetch(t.Context(), public, ""); err == nil || !strings.Contains(err.Error(), "names no content") {
			t.Errorf("%q: err = %v; want it refused", hash, err)
		}
	}
}

// The cutting command is frontseat-place of the daemon's form, told
// each part's seven as arguments, and runs no shell; what it does with
// them is place's to show.
func TestCuttingIsFrontseatsOwnProgramToldItsParts(t *testing.T) {
	part := []string{"whole", "0", "1", "sha1", strings.Repeat("1a", 20), "a.part", "https://example.com/a"}
	if got, want := download.Cutting(part...), append([]string{place.Placer, "cut"}, part...); !slices.Equal(got, want) {
		t.Errorf("command = %q; want %q", got, want)
	}
}

// The asset service is asked as its grid is: over TLS, known by the
// authorities the daemon was named, with the token of the file, which a
// daemon given no TLS is refused before anything is dialled. One that
// does not admit the daemon is said so as the grid is, with its status
// kept and the token's file named; a token that could not be read sent
// nothing, and that is the daemon's to know.
func TestTheAssetServiceIsAskedAsItsGridIs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("aaa.bbb.ccc\n")
	_, err := download.Dial(t.Context(), download.Config{Address: "127.0.0.1:1", Access: access.Config{Token: file}})
	if err == nil || !strings.Contains(err.Error(), "never sent in the clear") || strings.Contains(err.Error(), "aaa.bbb.ccc") {
		t.Errorf("a token in the clear: %v", err)
	}

	known := testauthority.New(t)
	sum := strings.Repeat("ab", 32)
	held := &remoteexecution.Digest{Hash: sum, SizeBytes: 3}
	fetching := &service{by: map[string]*remoteexecution.Digest{"http://proxy/one": held, "http://proxy/two": held, "http://proxy/three": held}}
	door := &reapitest.Answering{Certificate: &known.Server, Admits: "Bearer aaa.bbb.ccc", Also: func(server *grpc.Server) { remoteasset.RegisterFetchServer(server, fetching) }}
	rewrites := []download.Rewrite{{From: `^https://example\.com/(.*)$`, To: "http://proxy/$1"}}
	client, err := download.Dial(t.Context(), download.Config{Address: door.Serve(t), Rewrites: rewrites, Access: access.Config{Authorities: known.File, Token: file}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Fetch(t.Context(), "https://example.com/one", sum); err != nil {
		t.Fatalf("with the token of the file: %v", err)
	}
	if carried := door.Carried(t); len(carried) != 1 || carried[0] != "Bearer aaa.bbb.ccc" {
		t.Errorf("carried %q", carried)
	}

	// A token the service does not admit: said as the grid's refusal is.
	write("xxx.yyy.zzz\n")
	_, err = client.Fetch(t.Context(), "https://example.com/two", sum)
	if err == nil || grpcstatus.Code(err) != codes.Unauthenticated || !access.NotAdmitted(err) || !strings.Contains(err.Error(), "the asset service did not admit this daemon") || !strings.Contains(err.Error(), file) || strings.Contains(err.Error(), "xxx.yyy.zzz") {
		t.Errorf("a token that is not admitted: %v", err)
	}
	// A token that cannot be read: nothing was sent, and it is not the
	// service's refusal.
	asked := door.Asked.Load()
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(t.Context(), "https://example.com/three", sum)
	if err == nil || !access.Unsent(err) || access.NotAdmitted(err) || strings.Contains(err.Error(), "did not admit") || !strings.Contains(err.Error(), "was not sent to the grid") || door.Asked.Load() != asked {
		t.Errorf("a token that could not be read: %v", err)
	}
	// Control: with the token back, what was refused is fetched.
	write("aaa.bbb.ccc\n")
	if _, err := client.Fetch(t.Context(), "https://example.com/two", sum); err != nil {
		t.Errorf("with the token back: %v", err)
	}
}

// An asset service that cannot be asked over TLS as the daemon was told,
// one of other authorities, is said so, as its grid is: known by the
// daemon's own handshake, not by anything the service says.
func TestAnAssetServiceThatCannotBeAskedOverTLSIsSaidSo(t *testing.T) {
	known, other := testauthority.New(t), testauthority.New(t)
	sum := strings.Repeat("ab", 32)
	fetching := &service{by: map[string]*remoteexecution.Digest{"http://proxy/one": {Hash: sum, SizeBytes: 3}}}
	rewrites := []download.Rewrite{{From: `^https://example\.com/(.*)$`, To: "http://proxy/$1"}}
	ask := func(address string) error {
		t.Helper()
		client, err := download.Dial(t.Context(), download.Config{Address: address, Rewrites: rewrites, Access: access.Config{Authorities: known.File}})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		_, err = client.Fetch(t.Context(), "https://example.com/one", sum)
		return err
	}
	elsewhere := &reapitest.Answering{Certificate: &other.Server, Also: func(server *grpc.Server) { remoteasset.RegisterFetchServer(server, fetching) }}
	if err := ask(elsewhere.Serve(t)); err == nil || !strings.Contains(err.Error(), "the asset service at") || !strings.Contains(err.Error(), "could not be asked over TLS") {
		t.Errorf("an asset service of other authorities: %v", err)
	}
	// Controls: one that is not there is not said to be of others, and
	// one of the authorities named is asked.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := listener.Addr().String()
	listener.Close()
	if err := ask(gone); err == nil || strings.Contains(err.Error(), "could not be asked over TLS") {
		t.Errorf("an asset service that is not there: %v", err)
	}
	ours := &reapitest.Answering{Certificate: &known.Server, Also: func(server *grpc.Server) { remoteasset.RegisterFetchServer(server, fetching) }}
	if err := ask(ours.Serve(t)); err != nil {
		t.Errorf("an asset service of the authorities named: %v", err)
	}
}
