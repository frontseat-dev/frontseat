// Package testorigin gives a test an origin of crates of its own that
// counts what it is asked: what witnesses that a download is fetched once
// for every repository and daemon of the grid. A daemon is told to rewrite
// crates.io's addresses to it (Rewrites); the grid's asset service then
// fetches from it, and every fetch is counted (Asked). It serves the
// crates in testdata alone, byte for byte as crates.io serves them, so a
// fetch of one reaches nothing beyond this machine.
package testorigin

import (
	"context"
	"embed"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/testlisten"
)

//go:embed testdata/*.crate
var crates embed.FS

// Origin is an origin of crates a test started.
type Origin struct {
	mu    sync.Mutex
	asked map[string]int
	port  int
}

// path is what a crate is served at: what crates.io's address of its
// download is rewritten to.
var path = regexp.MustCompile(`^/crates/([A-Za-z0-9_-]+)/([0-9A-Za-z.+-]+)/download$`)

// Crates starts an origin of the crates in testdata, on a port of this
// machine that is the test's own (testlisten.Own), since the grid's
// asset service reaches it from a container and what it asks is
// counted, and stops it when the test ends, once it has stopped serving.
func Crates(t testing.TB) *Origin {
	t.Helper()
	listener, err := testlisten.Own("tcp")
	if err != nil {
		t.Fatal(err)
	}
	o := &Origin{asked: map[string]int{}, port: listener.Addr().(*net.TCPAddr).Port}
	server := &http.Server{Handler: http.HandlerFunc(o.serve)}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		// What is answering is let finish, so that nothing counts a
		// download once the test has ended.
		ending, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ending)
		_ = server.Close()
		<-served
	})
	return o
}

// serve answers a crate's download, and counts it, whether it holds the
// crate or not.
func (o *Origin) serve(w http.ResponseWriter, r *http.Request) {
	found := path.FindStringSubmatch(r.URL.Path)
	if found == nil {
		http.NotFound(w, r)
		return
	}
	name, version := found[1], found[2]
	o.mu.Lock()
	o.asked[name+"@"+version]++
	o.mu.Unlock()
	content, err := crates.ReadFile("testdata/" + name + "-" + version + ".crate")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	_, _ = w.Write(content)
}

// Asked is how many times a crate's download was asked of the origin.
func (o *Origin) Asked(name, version string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.asked[name+"@"+version]
}

// Rewrites are what a daemon is told to fetch crates.io's downloads from
// this origin by (frontseatd --rewrite), as the grid's asset service
// reaches this machine.
func (o *Origin) Rewrites() map[string]string {
	at := "http://host.docker.internal:" + strconv.Itoa(o.port)
	return map[string]string{`^https://static\.crates\.io/crates/([A-Za-z0-9_-]+)/([0-9A-Za-z.+-]+)/download$`: at + "/crates/$1/$2/download"}
}
