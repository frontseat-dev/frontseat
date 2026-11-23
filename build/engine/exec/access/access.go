// Package access is how a daemon reaches a grid that asks who it is: over
// TLS, known by the system's authorities or by those of a file, and
// admitted by a bearer token read from a file. It is one answer for the
// daemon's two dials, the grid's and its asset service's, which are one
// deployment behind one door.
//
// A daemon given none of it asks in the clear, as it asks the local
// stack's grid on the loopback. A token is never said in the clear: one
// given with no TLS is refused before anything is dialled.
//
// The token is the file's as it is when a call is made, so that one that
// lives for minutes is replaced by whoever was given it and the daemon
// knows nothing of where it comes from. It is a JSON Web Token as it is
// written for a header (RFC 7519): three parts of the URL alphabet with
// a dot between, which is what a grid's parser of them takes.
//
// A writer replaces the file whole, written beside and moved into place.
// One that empties the file and then writes it is read between the two:
// a file read empty, or cut before its third part, holds no token and
// the call it was read for fails here; one cut within its third part is
// the shape of a token and is sent, and the grid refuses it. No call is
// told what an earlier call read.
//
// A file that could not be read when a call was made fails that call
// here, with nothing sent (Unsent): that is not a grid that did not admit
// the daemon (NotAdmitted). Which of the two a call met is the daemon's
// own to know, kept beside the call (Watch) and never read from what a
// grid says: a grid chooses its words. A refusal never repeats what a
// token file held.
//
// A token is told only to a grid known by authorities the daemon was
// named: the system's are chosen by the environment (SSL_CERT_FILE,
// SSL_CERT_DIR), and a variable would choose who is told the token.
//
// The token is presented to its origin alone, as sdk/go/bearer presents
// one over HTTP: gRPC's per-call credentials are of one connection, to
// the address dialled, and a gRPC connection follows no redirect. That
// package is an HTTP client's and holds a token given to it; this one is
// a gRPC connection's and reads its token from a file for each call.
//
// Not served: a certificate of the daemon's own, a server name other than
// the address's, a token the daemon renews itself, a token for a grid
// known only by the system's authorities, and a bearer token that is no
// JSON Web Token.
//
// Not held: what a dial learns of its TLS is one record for all its
// connections (a grid's pool of them), so one connection that was
// answered hides another's refusal. A refusal is not waited for, though
// it may be a server's for a while: a front that shows a certificate of
// its own making until its real one is loaded (ingress-nginx's "Fake
// Certificate"), or that answers handshake_failure until it is ready, has
// the daemon refused at its start, where it would have answered later.
// A server that asks for a certificate of the daemon's and then closes
// the connection without an alert is waited on as a grid not up yet. What
// Go's GODEBUG settings change of TLS and x509 (versions, cipher suites,
// what a certificate may be) shapes the dial, though not the authorities.
package access

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Config says how a grid and its asset service are asked.
type Config struct {
	// TLS asks over TLS, known by the system's authorities.
	TLS bool
	// Authorities is a PEM file of the authorities the grid is known by,
	// in place of the system's. Naming it asks over TLS.
	Authorities string
	// Token is a file holding the bearer token the grid admits the daemon
	// by, read again for every call.
	Token string
}

// secure is whether the grid is asked over TLS.
func (c Config) secure() bool { return c.TLS || c.Authorities != "" }

// clear refuses a token that would be said in the clear, or to a grid
// the daemon was named no authorities of. It reads nothing.
func (c Config) clear() error {
	if c.Token != "" && !c.secure() {
		return errors.New("a grid token is never sent in the clear: give --grid-ca with --grid-token")
	}
	if c.Token != "" && c.Authorities == "" {
		return errors.New("a grid token is told only to a grid known by authorities the daemon was named: give --grid-ca with --grid-token, the system's authorities (--grid-tls) are chosen by the environment")
	}
	return nil
}

// Check says what is wrong with a configuration before anything is
// dialled: a token with no TLS, authorities that are none, a token file
// that holds no token, either of them something other than a file. It is
// asked when a daemon starts and again before each of its dials.
func (c Config) Check() error {
	if err := c.clear(); err != nil {
		return err
	}
	if _, err := c.pool(); err != nil {
		return err
	}
	if c.Token != "" {
		if _, err := read(c.Token); err != nil {
			return err
		}
	}
	return nil
}

// pool is the authorities the grid is known by: those of the file named,
// read whole and bounded, or nil, the system's.
func (c Config) pool() (*x509.CertPool, error) {
	if c.Authorities == "" {
		return nil, nil
	}
	pem, err := held(c.Authorities, authorities)
	if err != nil {
		return nil, fmt.Errorf("the grid's authorities: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("the grid's authorities: %s holds no certificate", c.Authorities)
	}
	return pool, nil
}

// Options returns the options one of the daemon's dials dials with, the
// grid's or its asset service's, after those given, and what is learned
// of the TLS handshakes that dial makes.
//
// Neither takes a proxy from the environment (HTTPS_PROXY, NO_PROXY): a
// variable would choose the route to the grid, as it would choose the
// authorities, and a proxy that refused would read as a grid not up.
func (c Config) Options(first ...grpc.DialOption) ([]grpc.DialOption, *Handshakes, error) {
	if err := c.clear(); err != nil {
		return nil, nil, err
	}
	shaken := &Handshakes{}
	options := append(append([]grpc.DialOption(nil), first...), grpc.WithNoProxy())
	if !c.secure() {
		return append(options, grpc.WithTransportCredentials(insecure.NewCredentials())), shaken, nil
	}
	watching, err := c.credentials(shaken)
	if err != nil {
		return nil, nil, err
	}
	options = append(options, grpc.WithTransportCredentials(watching))
	if c.Token != "" {
		options = append(options, grpc.WithPerRPCCredentials(bearer(c.Token)))
	}
	return options, shaken, nil
}

// credentials is the TLS one dial asks over, noting how each of its
// connections goes on shaken.
func (c Config) credentials(shaken *Handshakes) (credentials.TransportCredentials, error) {
	pool, err := c.pool()
	if err != nil {
		return nil, err
	}
	// gRPC speaks HTTP/2 over TLS, and a server that does not say so
	// (ALPN) is refused by the daemon itself, whatever gRPC's own check is
	// set to (GRPC_ENFORCE_ALPN_ENABLED).
	config := &tls.Config{
		RootCAs: pool,
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.NegotiatedProtocol != "h2" {
				return errNoHTTP2
			}
			return nil
		},
	}
	return watched{TransportCredentials: credentials.NewTLS(config), shaken: shaken}, nil
}

// Handshakes is what one dial learns of its own TLS: whether a handshake
// was refused for what TLS itself said to the daemon, and whether a
// connection it made was answered. It is the daemon's own knowledge,
// never read from what a grid says: a grid that answers chooses its
// words.
type Handshakes struct{ refused, succeeded atomic.Bool }

// Refused is whether the dial's TLS was refused, and none of its
// connections was answered: the grid answers in the clear, shows a
// certificate of other authorities or for another name, does not speak
// HTTP/2 over TLS, or refuses what the daemon offered by an alert of
// refusing, during its handshake or after it (TLS 1.3 says a certificate
// the server requires is missing after the handshake has ended). A
// handshake that ended otherwise, a door that let the connection go,
// reset it, said nothing in time or failed itself (an internal_error
// alert), is not that.
func (h *Handshakes) Refused() bool {
	return h != nil && h.refused.Load() && !h.succeeded.Load()
}

// Said is err, said to be of a server, who at address, that could not
// be asked over TLS as the daemon was told to ask it, where that is what
// the dial's own handshakes say (Refused) and something was sent.
func (h *Handshakes) Said(who, address string, err error) error {
	if err == nil || !h.Refused() || Unsent(err) {
		return err
	}
	return &untrusted{fmt.Errorf("%s at %s could not be asked over TLS as the daemon was told to ask it (--grid-tls, --grid-ca): it answers in the clear or not in HTTP/2, is not known by those authorities or by that name, or refuses what the daemon offered, or asks for a certificate of the daemon's, which it has none of: %w", who, address, err)}
}

// untrusted is the error of a server that was reached and could not be
// asked over TLS as the daemon was told to ask it.
type untrusted struct{ said error }

func (u *untrusted) Error() string { return u.said.Error() }
func (u *untrusted) Unwrap() error { return u.said }

// Untrusted is whether err is of a server that could not be asked over
// TLS as the daemon was told to (Said): asking again would meet the same.
func Untrusted(err error) bool {
	var is *untrusted
	return errors.As(err, &is)
}

// errNoHTTP2 is the daemon's refusal of a server that did not say, as its
// handshake ended, that it speaks HTTP/2 (ALPN h2).
var errNoHTTP2 = errors.New("the server does not speak HTTP/2 over TLS (it named no ALPN protocol h2)")

// watched is TLS that notes on its Handshakes how each of its
// connections went.
type watched struct {
	credentials.TransportCredentials
	shaken *Handshakes
}

func (w watched) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := w.TransportCredentials.ClientHandshake(ctx, authority, raw)
	if err != nil {
		if refusedByTLS(err) {
			w.shaken.refused.Store(true)
		}
		return conn, info, err
	}
	// A connection is answered once the server is heard from: over TLS
	// 1.3 a server refuses what the daemon offered after the handshake has
	// ended. What gRPC asks of the socket beneath is answered as it would
	// be unwatched.
	return heeding(conn, w.shaken), info, nil
}

// heeding is a connection, answering until the server is heard from,
// that gives the socket beneath it where the connection it watches does.
func heeding(conn net.Conn, shaken *Handshakes) net.Conn {
	watched := &answering{Conn: conn, shaken: shaken}
	if socket, ok := conn.(syscall.Conn); ok {
		return answeringSocket{answering: watched, socket: socket}
	}
	return watched
}

// answeringSocket is a connection answering that gives the socket
// beneath it.
type answeringSocket struct {
	*answering
	socket syscall.Conn
}

func (a answeringSocket) SyscallConn() (syscall.RawConn, error) { return a.socket.SyscallConn() }

func (w watched) Clone() credentials.TransportCredentials {
	return watched{TransportCredentials: w.TransportCredentials.Clone(), shaken: w.shaken}
}

// answering is a connection whose TLS has been shaken hands on, until the
// server is heard from: an alert of refusing that ends it before then is
// a refusal of the daemon's TLS.
type answering struct {
	net.Conn
	shaken *Handshakes
	heard  atomic.Bool
}

func (a *answering) Read(b []byte) (int, error) {
	n, err := a.Conn.Read(b)
	if n > 0 && !a.heard.Load() {
		a.heard.Store(true)
		a.shaken.succeeded.Store(true)
	}
	a.ended(err)
	return n, err
}

func (a *answering) Write(b []byte) (int, error) {
	n, err := a.Conn.Write(b)
	a.ended(err)
	return n, err
}

// ended notes a connection refused by an alert before the server was
// heard from.
func (a *answering) ended(err error) {
	if err != nil && !a.heard.Load() && refusedByTLS(err) {
		a.shaken.refused.Store(true)
	}
}

// refusing are the TLS alerts (RFC 8446, section 6.2) by which a server
// refuses what the daemon offered, as asking again would not change:
// handshake_failure, bad_certificate, unsupported_certificate,
// certificate_revoked, certificate_expired, certificate_unknown,
// unknown_ca, access_denied, protocol_version, insufficient_security,
// certificate_required and no_application_protocol. Another alert, an
// internal_error above all, is a server that may yet answer.
var refusing = map[uint64]bool{40: true, 42: true, 43: true, 44: true, 45: true, 46: true, 48: true, 49: true, 70: true, 71: true, 116: true, 120: true}

// refusedByTLS is whether a handshake ended for what TLS itself said, by
// what crypto/tls and crypto/x509 give it: a first record that was not
// TLS, a certificate that was not verified, a server that does not speak
// HTTP/2 there, an alert of refusing from the other side.
func refusedByTLS(err error) bool {
	var (
		header   tls.RecordHeaderError
		verified *tls.CertificateVerificationError
		unknown  x509.UnknownAuthorityError
		host     x509.HostnameError
		invalid  x509.CertificateInvalidError
		op       *net.OpError
	)
	// An alert the other side sent is a *net.OpError crypto/tls makes
	// itself, of its own operation ("remote error"), around the alert's
	// number, of a type of its own.
	alerted := errors.As(err, &op) && op.Op == "remote error" && reflect.ValueOf(op.Err).Kind() == reflect.Uint8 && refusing[reflect.ValueOf(op.Err).Uint()]
	return alerted || errors.Is(err, errNoHTTP2) || errors.As(err, &header) || errors.As(err, &verified) ||
		errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &invalid)
}

// largest is the most a token's file may hold, past any token: one of
// GitHub's is under 2 KiB. authorities is the most a file of authorities
// may: the system's own, all of them, are a quarter of it.
const (
	largest     = 16 << 10
	authorities = 1 << 20
)

// held returns what a file holds, where it is a file and holds no more
// than most. Anything else a path may name, a pipe nobody writes to, a
// device, is not waited for or read.
func held(file string, most int) ([]byte, error) {
	// Opening a pipe to read waits for its writer, unless it is opened so.
	opened, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	is, err := opened.Stat()
	if err != nil {
		return nil, err
	}
	if !is.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a file", file)
	}
	all, err := io.ReadAll(io.LimitReader(opened, int64(most)+1))
	if err != nil {
		return nil, err
	}
	if len(all) > most {
		return nil, fmt.Errorf("%s holds more than %d bytes", file, most)
	}
	return all, nil
}

// read returns the token a file holds: what is left of it without the
// space and line's end around it, three parts of the URL alphabet with a
// dot between. Anything else it holds, a second word or line, a part
// that is missing, is no token, and is not repeated.
func read(file string) (string, error) {
	all, err := held(file, largest)
	if err != nil {
		return "", fmt.Errorf("the grid token: %w", err)
	}
	token := strings.Trim(string(all), " \t\r\n")
	if token == "" {
		return "", fmt.Errorf("the grid token: %s holds none", file)
	}
	parts, begun := 1, false
	for i := 0; i < len(token); i++ {
		switch c := token[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			begun = true
		case c == '.' && begun && parts < 3:
			parts, begun = parts+1, false
		default:
			return "", fmt.Errorf("the grid token: %s holds what is no token: one is three parts of letters, digits, - and _, with a dot between", file)
		}
	}
	if parts != 3 || !begun {
		return "", fmt.Errorf("the grid token: %s holds what is no token: one is three parts of letters, digits, - and _, with a dot between", file)
	}
	return token, nil
}

// bearer is the token of a file, said with every call and only over TLS.
type bearer string

var _ credentials.PerRPCCredentials = bearer("")

// GetRequestMetadata reads the file for each call: measured at
// BenchmarkTheTokenIsReadForACall, beside a call that crosses a network.
func (b bearer) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	token, err := read(string(b))
	if err != nil {
		// Nothing is sent, the last token read included: a grid would
		// otherwise be told one its writer had taken away. The call fails
		// with the status of one the grid did not admit, and whoever
		// watches the call is told it never got there.
		if watched, ok := ctx.Value(watching{}).(*watch); ok {
			watched.unread.CompareAndSwap(nil, &err)
		}
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	return map[string]string{"authorization": "Bearer " + token}, nil
}

func (bearer) RequireTransportSecurity() bool { return true }

// watching is the key a call's watch is kept under in its context.
type watching struct{}

// watch is what one call of the daemon's learns here of itself: the first
// token read that failed for it.
type watch struct{ unread atomic.Pointer[error] }

// Watch returns a context for one call of the daemon's to a grid, however
// many requests it makes: a token that cannot be read for any of them is
// noted beside the call, where Told finds it. Each call has its own, so
// calls made at once learn nothing of each other.
func Watch(ctx context.Context) context.Context {
	if _, ok := ctx.Value(watching{}).(*watch); ok {
		return ctx
	}
	return context.WithValue(ctx, watching{}, &watch{})
}

// unsent is the error of a call that failed here, for a token that could
// not be read. A call may ask a grid several things at once, each reading
// the token for itself: what could not read it sent nothing, and another
// of the same call may have been answered, which is what is kept.
type unsent struct{ unread, answered error }

func (u *unsent) Error() string {
	return "a token could not be read for this call, and what needed it was not sent to the grid: " + u.unread.Error() + ": " + u.answered.Error()
}
func (u *unsent) Unwrap() error { return u.answered }

// Told returns what a watched call answered, said to have sent nothing
// where the call's own token could not be read: known by what the call
// noted of itself, and not by anything a grid said.
func Told(ctx context.Context, err error) error {
	watched, ok := ctx.Value(watching{}).(*watch)
	if err == nil || !ok {
		return err
	}
	if unread := watched.unread.Load(); unread != nil && !Unsent(err) {
		return &unsent{unread: *unread, answered: err}
	}
	return err
}

// Unsent is whether a call failed here, for a token that could not be
// read, with nothing sent to the grid (Told).
func Unsent(err error) bool {
	var is *unsent
	return errors.As(err, &is)
}

// NotAdmitted is whether a grid, or its asset service, answered that it
// does not admit whoever asks: it is up, and asking again says nothing
// new. A call that sent nothing was never answered, and is not one.
func NotAdmitted(err error) bool {
	code := status.Code(err)
	return err != nil && (code == codes.Unauthenticated || code == codes.PermissionDenied) && !Unsent(err)
}

// Refused says of an answer that who gave it, the grid or its asset
// service, did not admit the daemon, where that is what it answered: at
// the start, or while the daemon builds, its token having run out. The
// answer's own status is kept, and the token's file is named and never
// what it holds.
func (c Config) Refused(who string, err error) error {
	if !NotAdmitted(err) {
		return err
	}
	by := "it was given no token to be told who asks by (--grid-token)"
	if c.Token != "" {
		by = "it is told who asks by the token " + c.Token + " holds (--grid-token), which whoever was given it renews"
	}
	return fmt.Errorf("%s did not admit this daemon; %s: %w", who, by, err)
}
