package access_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/build/engine/exec/access"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
)

// ask dials as a daemon's two dials do, the grid's (reapi.Dial) and the
// asset service's, by Options, and asks what is served.
func ask(t *testing.T, config access.Config, address string) (byGrid, byAssets error) {
	t.Helper()
	grid, byGrid := reapi.Dial(t.Context(), reapi.Config{Address: address, Access: config})
	if byGrid == nil {
		grid.Close()
	}
	options, _, err := config.Options()
	if err != nil {
		return byGrid, err
	}
	second, err := grpc.NewClient("passthrough:///"+address, options...)
	if err != nil {
		return byGrid, err
	}
	defer second.Close()
	_, byAssets = repb.NewCapabilitiesClient(second).GetCapabilities(t.Context(), &repb.GetCapabilitiesRequest{})
	return byGrid, byAssets
}

// code is the status of an answer, through what wraps it.
func code(err error) codes.Code { return status.Code(err) }

func token(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// A grid that asks who a daemon is, is told over TLS by the bearer token
// of the file the daemon was given, by both of the daemon's dials alike.
func TestAGridIsAskedOverTLSWithTheTokenOfTheFile(t *testing.T) {
	known := testauthority.New(t)
	grid := &reapitest.Answering{Admits: "Bearer aaa.bbb.ccc", Certificate: &known.Server}
	address := grid.Serve(t)
	config := access.Config{Authorities: known.File, Token: token(t, "aaa.bbb.ccc\n")}
	if err := config.Check(); err != nil {
		t.Fatal(err)
	}
	byGrid, byAssets := ask(t, config, address)
	if byGrid != nil || byAssets != nil {
		t.Fatalf("the grid's dial: %v; the asset service's: %v", byGrid, byAssets)
	}
	if got := grid.Carried(t); len(got) != 1 || got[0] != "Bearer aaa.bbb.ccc" {
		t.Errorf("authorization = %q", got)
	}
	// Controls: with no token the same grid admits neither dial, and one
	// that admits anyone is asked over TLS with nothing said of who asks.
	byGrid, byAssets = ask(t, access.Config{Authorities: known.File}, address)
	if code(byGrid) != codes.Unauthenticated || code(byAssets) != codes.Unauthenticated {
		t.Errorf("with no token: %v, %v; want neither admitted", byGrid, byAssets)
	}
	open := &reapitest.Answering{Certificate: &known.Server}
	byGrid, byAssets = ask(t, access.Config{Authorities: known.File}, open.Serve(t))
	if byGrid != nil || byAssets != nil || len(open.Carried(t)) != 0 {
		t.Errorf("a grid that admits anyone: %v, %v, carrying %q", byGrid, byAssets, open.Carried(t))
	}
}

// A grid whose certificate another authority signed is not the grid.
func TestAGridOfAnotherAuthorityIsRefused(t *testing.T) {
	known, other := testauthority.New(t), testauthority.New(t)
	grid := &reapitest.Answering{Certificate: &other.Server}
	address := grid.Serve(t)
	byGrid, byAssets := ask(t, access.Config{Authorities: known.File, Token: token(t, "aaa.bbb.ccc")}, address)
	if code(byGrid) != codes.Unavailable || code(byAssets) != codes.Unavailable {
		t.Errorf("another authority's grid: %v, %v; want neither reached", byGrid, byAssets)
	}
	if grid.Asked.Load() != 0 {
		t.Error("it was asked all the same")
	}
	// The system's authorities do not know a test's either.
	byGrid, byAssets = ask(t, access.Config{TLS: true}, (&reapitest.Answering{Certificate: &known.Server}).Serve(t))
	if byGrid == nil || byAssets == nil {
		t.Error("the system's authorities took a test's certificate")
	}
}

// The token a call carries is the file's as it is then: one replaced
// while the daemon runs is the one the next call carries.
func TestATokenReplacedIsTheOneTheNextCallCarries(t *testing.T) {
	known := testauthority.New(t)
	grid := &reapitest.Answering{Certificate: &known.Server}
	address := grid.Serve(t)
	file := token(t, "first.token.value")
	options, _, err := access.Config{Authorities: known.File, Token: file}.Options()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient("passthrough:///"+address, options...)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A call is watched, as the daemon's are: what it learns of itself
	// is beside it, and no other call's.
	call := func() error {
		ctx := access.Watch(t.Context())
		_, err := repb.NewCapabilitiesClient(conn).GetCapabilities(ctx, &repb.GetCapabilitiesRequest{})
		return access.Told(ctx, err)
	}
	if err := call(); err != nil || grid.Carried(t)[0] != "Bearer first.token.value" {
		t.Fatalf("first: %v", err)
	}
	// Replaced as a writer must: written beside and moved into place.
	beside := file + ".next"
	if err := os.WriteFile(beside, []byte("second.token.value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(beside, file); err != nil {
		t.Fatal(err)
	}
	if err := call(); err != nil || grid.Carried(t)[0] != "Bearer second.token.value" {
		t.Fatalf("after it was replaced: %v, %q", err, grid.Carried(t))
	}
	// A file that is gone, or holds no token, sends nothing: the call
	// fails here, and the grid is not told the token there was.
	asked := grid.Asked.Load()
	for name, spoil := range map[string]func() error{
		"emptied": func() error { return os.WriteFile(file, nil, 0o600) },
		"gone":    func() error { return os.Remove(file) },
	} {
		if err := spoil(); err != nil {
			t.Fatal(err)
		}
		err := call()
		if status.Code(err) != codes.Unauthenticated || strings.Contains(err.Error(), "second.token.value") {
			t.Errorf("%s: %v; want the call refused here, saying no token", name, err)
		}
		// It is told apart from a grid that did not admit the daemon:
		// nothing was sent for a grid to refuse.
		if !access.Unsent(err) || !strings.Contains(err.Error(), "was not sent to the grid") || !strings.Contains(err.Error(), file) {
			t.Errorf("%s: %v; want it said that nothing was sent, naming the file", name, err)
		}
	}
	if grid.Asked.Load() != asked {
		t.Errorf("the grid was asked %d more times with no token to send", grid.Asked.Load()-asked)
	}
	// Controls: what a grid answers is not said to be unsent, whatever
	// words it chooses, and neither is what a call that read its token
	// was answered, watched or not.
	if access.Unsent(status.Error(codes.Unauthenticated, "nothing was sent to the grid: the grid's own words")) || access.Unsent(nil) {
		t.Error("a grid's refusal was taken for a token that was not sent")
	}
	if err := os.WriteFile(file, []byte("third.token.value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watched := access.Watch(t.Context())
	if told := access.Told(watched, status.Error(codes.Unauthenticated, "nothing was sent to the grid: go away")); access.Unsent(told) || !access.NotAdmitted(told) {
		t.Errorf("a call that sent its token, refused: %v", told)
	}
	if err := call(); err != nil {
		t.Errorf("with a token again: %v", err)
	}
}

// A token is never said in the clear: a daemon given one and no TLS is
// refused before anything is dialled.
func TestATokenIsNotSentInTheClear(t *testing.T) {
	grid := &reapitest.Answering{}
	address := grid.Serve(t)
	config := access.Config{Token: token(t, "aaa.bbb.ccc")}
	if err := config.Check(); err == nil || !strings.Contains(err.Error(), "--grid-ca") || !strings.Contains(err.Error(), "in the clear") {
		t.Errorf("checked: %v; want it refused, naming what is missing", err)
	}
	byGrid, byAssets := ask(t, config, address)
	if byGrid == nil || byAssets == nil || grid.Asked.Load() != 0 {
		t.Errorf("dialled: %v, %v; the grid was asked %d times", byGrid, byAssets, grid.Asked.Load())
	}
	// Control: the same grid is asked in the clear by a daemon given
	// nothing, as the local stack's is.
	byGrid, byAssets = ask(t, access.Config{}, address)
	if byGrid != nil || byAssets != nil || len(grid.Carried(t)) != 0 {
		t.Errorf("in the clear: %v, %v", byGrid, byAssets)
	}
	if err := (access.Config{}).Check(); err != nil {
		t.Error(err)
	}
}

// What the files hold is checked when the daemon starts, and a refusal
// never repeats a token.
func TestWhatTheFilesHoldIsCheckedAtTheStart(t *testing.T) {
	known := testauthority.New(t)
	secret := "sEcReT.tOkEn.vAlUe"
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("no certificate here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	// A token of GitHub's is three parts; one the bound's length and a
	// second line past it.
	long := "a.b." + strings.Repeat("a", 16<<10-4)
	for name, config := range map[string]access.Config{
		// Nobody writes to it: a daemon that waited would not start.
		"a token file that is a pipe":      {Authorities: known.File, Token: pipe},
		"authorities that are a pipe":      {Authorities: pipe},
		"authorities past any":             {Authorities: token(t, strings.Repeat("-----BEGIN CERTIFICATE-----\n", 1<<16))},
		"a token of one part":              {Authorities: known.File, Token: token(t, "sEcReT")},
		"a token of two parts":             {Authorities: known.File, Token: token(t, "sEcReT.tOkEn")},
		"a token of four parts":            {Authorities: known.File, Token: token(t, secret+".more")},
		"a token with an empty part":       {Authorities: known.File, Token: token(t, "sEcReT..vAlUe")},
		"a token padded":                   {Authorities: known.File, Token: token(t, secret+"=")},
		"a token and a line past the most": {Authorities: known.File, Token: token(t, long+"\r\n\nsEcReT second line")},
		"a token of two words":             {Authorities: known.File, Token: token(t, secret+" "+secret)},
		"a token of two lines":             {Authorities: known.File, Token: token(t, secret+"\n"+secret+"\n")},
		"a token with a control":           {Authorities: known.File, Token: token(t, secret+"\x00")},
		"a token that is not ASCII":        {Authorities: known.File, Token: token(t, secret+"é")},
		"an empty token":                   {Authorities: known.File, Token: token(t, " \n")},
		"a token past any token":           {Authorities: known.File, Token: token(t, strings.Repeat(secret, 2000))},
		"a token file that is missing":     {Authorities: known.File, Token: filepath.Join(t.TempDir(), "none")},
		"a token file that is a directory": {Authorities: known.File, Token: t.TempDir()},
		"authorities that are missing":     {Authorities: filepath.Join(t.TempDir(), "none")},
		"authorities with none":            {Authorities: empty},
	} {
		t.Run(name, func(t *testing.T) {
			checked := make(chan error, 1)
			go func() { checked <- config.Check() }()
			var err error
			select {
			case err = <-checked:
			case <-time.After(3 * time.Second):
				t.Fatal("the check waits for what the file never says")
			}
			if err == nil {
				t.Fatal("want it refused")
			}
			if strings.Contains(err.Error(), "sEcReT") {
				t.Errorf("the refusal repeats the token: %v", err)
			}
		})
	}
	// Controls: surrounding space and a line's end are a file's, not the token's.
	for name, content := range map[string]string{"with a line's end": secret + "\r\n", "as it is": secret, "the largest": long, "the parts a token may hold": "aZ09_-.b.c"} {
		if err := (access.Config{Authorities: known.File, Token: token(t, content)}).Check(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := (access.Config{TLS: true}).Check(); err != nil {
		t.Error(err)
	}
}

// A token is told only to a grid known by authorities the daemon was
// named. The system's are chosen by the environment (SSL_CERT_FILE,
// SSL_CERT_DIR): a variable would choose who is told the token.
func TestATokenIsToldOnlyToAGridOfNamedAuthorities(t *testing.T) {
	known := testauthority.New(t)
	file := token(t, "aaa.bbb.ccc\n")
	by := access.Config{TLS: true, Token: file}
	err := by.Check()
	if err == nil || !strings.Contains(err.Error(), "--grid-ca") || !strings.Contains(err.Error(), "--grid-token") || strings.Contains(err.Error(), "aaa.bbb.ccc") {
		t.Errorf("a token, and the system's authorities: %v", err)
	}
	if _, _, err := by.Options(); err == nil {
		t.Error("it would be dialled all the same")
	}
	// Controls: the same token with named authorities, and the system's
	// authorities with no token to tell.
	if err := (access.Config{TLS: true, Authorities: known.File, Token: file}).Check(); err != nil {
		t.Errorf("named authorities: %v", err)
	}
	if err := (access.Config{TLS: true}).Check(); err != nil {
		t.Errorf("no token: %v", err)
	}
}

// A grid is dialled where it is, whatever proxy the environment names:
// a variable would otherwise choose the route to it, as it would the
// authorities it is known by. Where the test runs may refuse the dial
// itself (a grid's action reaches only the loopback, which a proxy is
// never taken to, so the grid is not there): the dial is then the
// daemon's own, to the grid's address, and the proxy is still asked
// nothing.
func TestAnEnvironmentsProxyIsNotTakenToTheGrid(t *testing.T) {
	// Go reads the environment's proxy once a process (net/http's
	// ProxyFromEnvironment), and another test of this one may have read
	// it before this one sets it: it is read in a process of its own.
	if os.Getenv("FRONTSEAT_ACCESS_PROXY_CHILD") != "yes" {
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestAnEnvironmentsProxyIsNotTakenToTheGrid$", "-test.paniconexit0", "-test.v")
		command.Env = append(os.Environ(), "FRONTSEAT_ACCESS_PROXY_CHILD=yes")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	// The grid by an address the environment's proxy is taken to: not
	// the loopback's, which a proxy never is, and yet this machine's.
	known := testauthority.Named(t, "0.0.0.0")
	grid := &reapitest.Answering{Certificate: &known.Server, Admits: "Bearer aaa.bbb.ccc"}
	_, port, err := net.SplitHostPort(grid.Serve(t))
	if err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("0.0.0.0", port)
	proxy := connecting(t)
	t.Setenv("HTTPS_PROXY", "http://"+proxy.address)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	asking := func(options ...grpc.DialOption) error {
		t.Helper()
		conn, err := grpc.NewClient("passthrough:///"+address, options...)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, err = repb.NewCapabilitiesClient(conn).GetCapabilities(t.Context(), &repb.GetCapabilitiesRequest{})
		return err
	}
	config := access.Config{Authorities: known.File, Token: token(t, "aaa.bbb.ccc\n")}
	options, _, err := config.Options()
	if err != nil {
		t.Fatal(err)
	}
	// Reached, or refused at the grid's own address: either way dialled
	// directly.
	direct := func(err error) bool {
		return err == nil || strings.Contains(err.Error(), "dial tcp "+address+":")
	}
	if err := asking(options...); !direct(err) || len(proxy.asked()) != 0 {
		t.Errorf("asked %v of the proxy, answered %v; want the grid dialled where it is", proxy.asked(), err)
	}
	// And so is the grid's own dial.
	dialled, err := reapi.Dial(t.Context(), reapi.Config{Address: address, Access: config})
	if !direct(err) || len(proxy.asked()) != 0 {
		t.Errorf("the grid's dial: asked %v of the proxy, answered %v", proxy.asked(), err)
	}
	if err == nil {
		dialled.Close()
	}
	// Control: dialled otherwise alike, gRPC takes the proxy the
	// environment names.
	pool := x509.NewCertPool()
	pem, err := os.ReadFile(known.File)
	if err != nil || !pool.AppendCertsFromPEM(pem) {
		t.Fatal(err)
	}
	_ = asking(grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: pool})))
	if len(proxy.asked()) == 0 {
		t.Error("the environment's proxy was not taken where nothing said not to: this test would see nothing")
	}
}

// proxying is an HTTP proxy on the loopback that notes what it is asked
// to connect to and connects nothing.
type proxying struct {
	address string
	mu      sync.Mutex
	seen    []string
}

func (p *proxying) asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func connecting(t *testing.T) *proxying {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxying{address: listener.Addr().String()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if request, err := http.ReadRequest(bufio.NewReader(conn)); err == nil {
				p.mu.Lock()
				p.seen = append(p.seen, request.Method+" "+request.Host)
				p.mu.Unlock()
			}
			conn.Close()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	return p
}
