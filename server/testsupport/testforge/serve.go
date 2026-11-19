package testforge

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/http/cgi"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/server/testsupport/testlisten"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// Committed makes a directory a git repository with one commit on main
// of what it holds. It is for a directory that is no repository yet: it
// writes a .gitignore that ignores .env, over any the directory had, and
// a second call for one directory fails.
func Committed(t testing.TB, directory string) {
	t.Helper()
	commit(t, directory, false)
}

// Hello makes a directory a repository as Committed does, holding the Go
// module example.com/hello in the directory hello, of a program that does
// nothing, with Go pinned as this repository pins it.
func Hello(t testing.TB, directory string) {
	t.Helper()
	testfiles.Tree(t, directory, map[string]string{
		"hello/go.mod":  "module example.com/hello\n\ngo 1.27\n",
		"hello/main.go": "package main\n\nfunc main() {}\n",
	})
	testpins.Go(t, directory)
	Committed(t, directory)
}

// Serve serves the repositories beneath a directory over HTTP, with
// git's own server, and returns its address as a sandbox reaches it: the
// repository in the directory code is cloned from that address and
// "/code". A file there whose name ends in .yaml is served as it is, as
// a forge serves a catalog file. It is how a test gives a sandbox
// source: a sandbox is given no directory of the machine.
//
// It listens on every address of the machine, with no credential: a
// sandbox reaches the machine at host.docker.internal, which under
// Docker Engine on Linux is the bridge's address and not the loopback.
// So it serves nothing but a test's fixtures, and for the test's length.
// It ends with the test, once what it was answering is answered.
func Serve(t testing.TB, root string) string {
	t.Helper()
	address, close := Start(t, root)
	t.Cleanup(close)
	return address
}

// Start serves a caller-owned fixture directory until close is called.
// Close drains requests and joins the serving goroutine; it must run before
// the directory is removed. The returned values retain no test handle.
func Start(t testing.TB, root string) (address string, close func()) {
	t.Helper()
	return served(t, root, "", "")
}

// ServeGuarded serves as Serve does, to whoever gives a username and a
// password and to no one else: a forge whose repositories are private.
// The password is the test's own making, and guards nothing but its
// fixtures.
func ServeGuarded(t testing.TB, root, username, password string) string {
	t.Helper()
	if password == "" {
		t.Fatal("a guarded forge is given a password")
	}
	address, close := served(t, root, username, password)
	t.Cleanup(close)
	return address
}

func served(t testing.TB, root, username, password string) (string, func()) {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatalf("git --exec-path: %v", err)
	}
	// A sandbox reaches it at host.docker.internal: the port is one no
	// other process of this machine shares.
	listener, err := testlisten.Own("tcp")
	if err != nil {
		t.Fatal(err)
	}
	git := &cgi.Handler{
		Path: filepath.Join(strings.TrimSpace(string(out)), "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	files := http.FileServer(http.Dir(root))
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if password != "" {
			given, secret, ok := r.BasicAuth()
			if !ok || given != username || subtle.ConstantTimeCompare([]byte(secret), []byte(password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="a test's forge"`)
				http.Error(w, "a password is asked", http.StatusUnauthorized)
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, ".yaml") {
			files.ServeHTTP(w, r)
			return
		}
		git.ServeHTTP(w, r)
	})}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	closeServer := func() {
		// What is being answered is answered before the directory it is
		// answered from goes; what does not end in time is closed.
		ending, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ending); err != nil {
			_ = server.Close()
		}
		<-served
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return "http://" + net.JoinHostPort("host.docker.internal", port), closeServer
}
