package backstage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// gateway is an entity as the catalog API answers it.
const gateway = `{"kind":"Component","metadata":{"name":"gateway","namespace":"default","annotations":{"backstage.io/source-location":"url:https://forge.example/acme/platform/tree/main/gateway/"}},"relations":[]}`

// answering is a catalog API that answers each request as answer does,
// for a catalog of the test's with a token and loopback allowed.
func answering(t *testing.T, answer http.HandlerFunc) (*Catalog, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(answer)
	t.Cleanup(server.Close)
	c, err := New(Config{Base: server.URL, Token: "a-secret-token", Loopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return c, server
}

// resolvingGateway resolves gateway, by its whole name, through a catalog.
func resolvingGateway(c *Catalog) error {
	_, err := c.Resolve(context.Background(), "local", []entity.Pattern{{Kind: "component", Namespace: "default", Name: "gateway"}})
	return err
}

// refusedAs holds that an error is a refusal of a reason, and says
// nothing of the token.
func refusedAs(t *testing.T, what string, err error, reason string) {
	t.Helper()
	var refused *Refused
	if !errors.As(err, &refused) || refused.Reason != reason {
		t.Errorf("%s: %v, want it refused %s", what, err, reason)
	}
	if err != nil && strings.Contains(err.Error(), "a-secret-token") {
		t.Errorf("%s: the refusal says the token: %v", what, err)
	}
}

// A redirect is not followed, with the token or without it: it is the
// catalog not answering, and where it led is never asked. The control
// is the same entity answered where it was asked for.
func TestARedirectIsNotFollowed(t *testing.T) {
	led := make(chan string, 1)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		led <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(gateway))
	}))
	t.Cleanup(elsewhere.Close)
	var redirects atomic.Bool
	redirects.Store(true)
	c, _ := answering(t, func(w http.ResponseWriter, r *http.Request) {
		if redirects.Load() {
			http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(gateway))
	})
	refusedAs(t, "a redirect", resolvingGateway(c), Unavailable)
	select {
	case got := <-led:
		t.Errorf("the redirect was followed, presenting %q", got)
	default:
	}
	redirects.Store(false)
	if err := resolvingGateway(c); err != nil {
		t.Errorf("the control, answered where it was asked: %v", err)
	}
}

// An answer of more than 4 MiB is refused; one of 4 MiB exactly is
// read.
func TestAnAnswerOfMoreThanItsLimitIsRefused(t *testing.T) {
	var size atomic.Int64
	size.Store(maxAnswer + 1)
	c, _ := answering(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(gateway + strings.Repeat(" ", int(size.Load())-len(gateway))))
	})
	refusedAs(t, "an answer of 4 MiB and a byte", resolvingGateway(c), Unavailable)
	size.Store(maxAnswer)
	if err := resolvingGateway(c); err != nil {
		t.Errorf("the control, an answer of 4 MiB: %v", err)
	}
}

// A catalog that is slow to answer is not waited for past the request's
// limit; one that answers in time is.
func TestASlowCatalogIsNotWaitedFor(t *testing.T) {
	var slow atomic.Bool
	slow.Store(true)
	c, _ := answering(t, func(w http.ResponseWriter, r *http.Request) {
		if slow.Load() {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
			}
		}
		_, _ = w.Write([]byte(gateway))
	})
	c.limits.request = 200 * time.Millisecond
	c.client = c.made()
	began := time.Now()
	refusedAs(t, "a slow answer", resolvingGateway(c), Unavailable)
	if waited := time.Since(began); waited > 3*time.Second {
		t.Errorf("a slow answer was waited for %v", waited)
	}
	slow.Store(false)
	if err := resolvingGateway(c); err != nil {
		t.Errorf("the control, an answer in time: %v", err)
	}
}

// What the catalog's address leads to is held when it is dialled: an
// address of no one host's is never dialled, and this machine only
// where the operator said so. The control is this machine, said so.
func TestACatalogIsDialledOnlyWhereItMayBe(t *testing.T) {
	for _, base := range []string{"https://169.254.169.254", "https://[fe80::1]:443", "https://0.0.0.0:443", "https://224.0.0.1"} {
		c, err := New(Config{Base: base, Token: "a-secret-token", Loopback: true})
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		err = resolvingGateway(c)
		refusedAs(t, base, err, Unavailable)
		if err == nil || !strings.Contains(err.Error(), "may not be at") {
			t.Errorf("%s: %v, want it refused as an address the catalog may not be at", base, err)
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(gateway)) }))
	t.Cleanup(server.Close)
	unsaid, err := New(Config{Base: server.URL, Token: "a-secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	unsaid.trusted = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	unsaid.client = unsaid.made()
	err = resolvingGateway(unsaid)
	refusedAs(t, "this machine, unsaid", err, Unavailable)
	if err == nil || !strings.Contains(err.Error(), "may not be at") {
		t.Errorf("this machine, unsaid: %v, want it refused as an address the catalog may not be at", err)
	}
	said, err := New(Config{Base: server.URL, Token: "a-secret-token", Loopback: true})
	if err != nil {
		t.Fatal(err)
	}
	said.trusted = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	said.client = said.made()
	if err := resolvingGateway(said); err != nil {
		t.Errorf("the control, this machine said: %v", err)
	}
}

// The token is presented to the catalog's origin alone. A source
// location elsewhere is read, never asked, and no request leaves the
// origin; the control is the origin, presented the token.
func TestTheTokenIsPresentedToTheCatalogAlone(t *testing.T) {
	asked := make(chan string, 1)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked <- r.URL.Path }))
	t.Cleanup(elsewhere.Close)
	presenting := make(chan string, 4)
	c, _ := answering(t, func(w http.ResponseWriter, r *http.Request) {
		presenting <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(strings.Replace(gateway, "https://forge.example", elsewhere.URL, 1)))
	})
	if err := resolvingGateway(c); err != nil {
		t.Fatal(err)
	}
	if presented := <-presenting; presented != "Bearer a-secret-token" || len(presenting) != 0 {
		t.Errorf("the catalog was presented %q, and asked %d more times", presented, len(presenting))
	}
	select {
	case at := <-asked:
		t.Errorf("the source location was asked, at %s", at)
	default:
	}
}

// A catalog is asked over https, and over http only at this machine
// where the operator said so; an address with a user, a query or a
// fragment is none. A refusal never says the address's secret.
func TestACatalogsAddressIsHeld(t *testing.T) {
	for _, base := range []string{"http://backstage.example.com", "http://10.0.0.1", "https://someone:a-password@backstage.example.com", "https://backstage.example.com?x=y", "https://backstage.example.com#f", "ftp://backstage.example.com", "backstage.example.com", ""} {
		for _, loopback := range []bool{false, true} {
			if _, err := New(Config{Base: base, Loopback: loopback}); err == nil {
				t.Errorf("%q (loopback %v) was taken", base, loopback)
			} else if strings.Contains(err.Error(), "a-password") {
				t.Errorf("the refusal of %q says its password: %v", base, err)
			}
		}
	}
	if _, err := New(Config{Base: "http://127.0.0.1:7007"}); err == nil {
		t.Error("http to this machine was taken unsaid")
	}
	for _, base := range []string{"http://127.0.0.1:7007", "http://[::1]:7007", "http://localhost:7007", "https://backstage.example.com", "https://backstage.example.com/portal/"} {
		if _, err := New(Config{Base: base, Loopback: true}); err != nil {
			t.Errorf("the control %q: %v", base, err)
		}
	}
}

// The catalog's answers are refused by what they say: refused for want
// of a token, saying whether one was sent and never what it is, and
// anything but an answer as unavailable.
func TestACatalogsRefusalSaysWhetherATokenWasSent(t *testing.T) {
	c, _ := answering(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	})
	err := resolvingGateway(c)
	refusedAs(t, "a 401", err, Unavailable)
	if err == nil || !strings.Contains(err.Error(), "a token was presented") {
		t.Errorf("a 401 does not say a token was sent: %v", err)
	}
	c.token = ""
	c.client = c.made()
	if err := resolvingGateway(c); err == nil || !strings.Contains(err.Error(), "no token was presented") {
		t.Errorf("a 401 does not say no token was sent: %v", err)
	}
	broken, _ := answering(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	refusedAs(t, "a 500", resolvingGateway(broken), Unavailable)
}
