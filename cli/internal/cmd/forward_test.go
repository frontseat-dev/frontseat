package cmd

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// boxed is a control plane of the tests that carries a connection to a
// port of a workspace's sandbox, where what listens says back each line
// it is told, after "the box heard", and ends the connection when it is
// told "leave". It keeps what it was asked and what was presented to
// it, and refuses whatever is asked first where it is told to.
type boxed struct {
	// url is where the control plane is asked.
	url string

	mu        sync.Mutex
	asked     []string
	presented []string
	refuses   int
	// over is how many connections it carried have ended, at its end.
	over atomic.Int32
}

// box serves a boxed control plane for as long as the test runs, and
// has the test's commands ask it, presenting the tests' token.
func box(t *testing.T) *boxed {
	t.Helper()
	b := &boxed{}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	b.url = srv.URL
	layOut(t, srv.URL)
	return b
}

func (b *boxed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.asked, b.presented = append(b.asked, r.URL.EscapedPath()), append(b.presented, r.Header.Get("Authorization"))
	refused := b.refuses > 0
	if refused {
		b.refuses--
	}
	b.mu.Unlock()
	if refused {
		http.Error(w, "the test's control plane carries nothing yet", http.StatusServiceUnavailable)
		return
	}
	socket, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer socket.CloseNow()
	defer b.over.Add(1)
	inside := websocket.NetConn(r.Context(), socket, websocket.MessageBinary)
	for heard := bufio.NewScanner(inside); heard.Scan() && heard.Text() != "leave"; {
		if _, err := io.WriteString(inside, "the box heard "+heard.Text()+"\n"); err != nil {
			return
		}
	}
}

func (b *boxed) was() (asked, presented []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.asked...), append([]string(nil), b.presented...)
}

// free is a port of this machine's loopback that nothing listens on now.
func free(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// forwarding runs `workspace forward` with what follows it until the
// test ends it, and returns how to end it: that gives what it printed,
// the code it exits with and why.
func forwarding(t *testing.T, args ...string) func() (string, int, error) {
	t.Helper()
	ctx, interrupt := context.WithCancel(context.Background())
	var printed string
	var code int
	var err error
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		printed, code, err = runAt(t, ctx, "", append([]string{"workspace", "forward"}, args...)...)
	}()
	end := func() (string, int, error) {
		interrupt()
		select {
		case <-ended:
		case <-time.After(soon):
			t.Fatal("workspace forward went on after it was interrupted")
		}
		return printed, code, err
	}
	t.Cleanup(func() { end() })
	return end
}

// soon is how long what a test waits for may take, at most: what ends,
// answers or is refused does so at once, and a machine that is busy is
// given this long. What is left waiting, as a connection no one ends,
// waits for longer than any test.
const soon = 5 * time.Second

// connected is a connection made to an address of this machine, and
// what reads the lines said back on it.
type connected struct {
	net.Conn
	back *bufio.Reader
}

// dialed connects to an address of this machine once something
// listens there; the test ends the connection where it has not.
func dialed(t *testing.T, at string) connected {
	t.Helper()
	for until := time.Now().Add(2 * soon); ; time.Sleep(10 * time.Millisecond) {
		conn, err := net.Dial("tcp", at)
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
			return connected{Conn: conn, back: bufio.NewReader(conn)}
		}
		if time.Now().After(until) {
			t.Fatalf("nothing listens on %s: %v", at, err)
		}
	}
}

// says says a line, and returns the line said back, soon; empty where
// the connection was ended with nothing said, and "(nothing, and the
// connection was left open)" where neither came soon.
func (c connected) says(line string) string {
	_ = c.SetDeadline(time.Now().Add(soon))
	if _, err := io.WriteString(c, line+"\n"); err != nil {
		return ""
	}
	return c.ends()
}

// ends is what is said back next, soon: empty where the connection was
// ended with nothing more said.
func (c connected) ends() string {
	_ = c.SetDeadline(time.Now().Add(soon))
	back, err := c.back.ReadString('\n')
	if timed, ok := err.(net.Error); ok && timed.Timeout() {
		return "(nothing, and the connection was left open)"
	}
	return back
}

// heard connects to an address of this machine once something listens
// there, says a line, and returns the line said back, soon; empty where
// the connection was ended with nothing said.
func heard(t *testing.T, at, line string) string {
	t.Helper()
	conn := dialed(t, at)
	defer conn.Close()
	return conn.says(line)
}

// `workspace forward` listens on this machine's loopback, at the port
// asked for where no other is said, and carries each connection made
// there to that port of the workspace's sandbox, through the control
// plane, presenting the token to it: what is written arrives there and
// what is answered comes back, for one connection after another and
// for several at once: one that stays open keeps no other waiting. It
// says once, on a line, what it listens on and where that leads. A
// connection that cannot be carried is ended, at once, and said on
// standard error, and the command goes on. A connection either end
// ended is ended at the other: the sandbox's when this machine's ends,
// and this machine's, though nothing is said on it, when the sandbox's
// does.
// Interrupted, it ends with 130, at once, saying nothing more: the
// connections that were open are ended, and it listens no longer.
func TestAPortOfThisMachineIsCarriedToAPortOfAWorkspacesSandbox(t *testing.T) {
	b := box(t)
	port := free(t)
	at := "127.0.0.1:" + port
	b.refuses = 1
	end := forwarding(t, "named", port)
	// The first cannot be carried: the control plane refuses it, and it
	// is ended, never left open for whoever made it to give up on.
	if back := heard(t, at, "a word"); back != "" {
		t.Errorf("a connection the control plane did not carry said back %q; want it ended", back)
	}
	// The command goes on: the next is carried, and then several at once.
	if back := heard(t, at, "a word"); back != "the box heard a word\n" {
		t.Errorf("carried to the box, a word came back as %q", back)
	}
	// This machine's end ended it: the sandbox's end is ended too, and
	// is not kept until the command ends.
	for began := time.Now(); b.over.Load() != 1; time.Sleep(10 * time.Millisecond) {
		if time.Since(began) > soon {
			t.Fatalf("a connection this machine's end ended is still open at the sandbox's end: %d have ended there", b.over.Load())
		}
	}
	var several sync.WaitGroup
	for i := range 4 {
		several.Go(func() {
			word := "word " + strconv.Itoa(i)
			if back := heard(t, at, word); back != "the box heard "+word+"\n" {
				t.Errorf("of several at once, %q came back as %q", word, back)
			}
		})
	}
	several.Wait()
	// Two that stay open: the second is carried while the first is, and
	// each goes on.
	first, second := dialed(t, at), dialed(t, at)
	for _, said := range []struct {
		on   connected
		word string
	}{{first, "one"}, {second, "two"}, {first, "one again"}, {second, "two again"}} {
		if back := said.on.says(said.word); back != "the box heard "+said.word+"\n" {
			t.Errorf("of two connections open at once, %q came back as %q", said.word, back)
		}
	}
	// One the sandbox's end ends is ended here, with no one saying
	// anything more on it.
	left := dialed(t, at)
	if back := left.says("a word"); back != "the box heard a word\n" {
		t.Errorf("before the box left, a word came back as %q", back)
	}
	if back := left.says("leave"); back != "" {
		t.Errorf("after the sandbox's end ended the connection, this machine's end read %q; want it ended", back)
	}
	// Interrupted with two connections open: it ends at once, and they do.
	interrupted := time.Now()
	printed, code, err := end()
	if took := time.Since(interrupted); took > soon/2 {
		t.Errorf("interrupted with connections open, it went on for %s", took)
	}
	for name, open := range map[string]connected{"first": first, "second": second} {
		if back := open.ends(); back != "" {
			t.Errorf("after it was interrupted, the %s connection that was open read %q; want it ended", name, back)
		}
	}
	if code != ExitInterrupted || err == nil {
		t.Errorf("interrupted, it ended %d (%v), want %d", code, err, ExitInterrupted)
	}
	if _, said := Failed(canceled(), err); said != "" {
		t.Errorf("interrupted, it says %q, want nothing more", said)
	}
	if want := at + " leads to the port " + port + " of the sandbox of the workspace named; Ctrl-C ends it\n"; printed != want {
		t.Errorf("it printed %q, want %q", printed, want)
	}
	if said := complaints.String(); strings.Count(said, "\n") != 1 || !strings.Contains(said, "503") {
		t.Errorf("of the one connection that was not carried, it said %q", said)
	}
	asked, presented := b.was()
	if len(asked) != 9 {
		t.Fatalf("the control plane was asked %v, want nine connections", asked)
	}
	for i := range asked {
		if asked[i] != "/workspaces/named/ports/"+port || presented[i] != "Bearer "+token {
			t.Errorf("the control plane was asked %s, presented %q", asked[i], presented[i])
		}
	}
	// Nothing of it is left: the address is listened on again.
	again, err := net.Listen("tcp", at)
	if err != nil {
		t.Fatalf("after it ended, %s is still listened on: %v", at, err)
	}
	again.Close()
}

// canceled is a context that was ended, as an interrupted command's is.
func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// own is the addresses of this machine's interfaces that are not of its
// loopback, each as --listen would be given it: the ones a command that
// took any address would listen on. A machine may have none.
func own(t *testing.T) []string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, each := range addresses {
		if network, ok := each.(*net.IPNet); ok && !network.IP.IsLoopback() && !network.IP.IsLinkLocalUnicast() {
			found = append(found, net.JoinHostPort(network.IP.String(), "0"))
		}
	}
	return found
}

// Only an address of this machine's loopback is listened on: one said
// by its number, of 127.0.0.0/8 or ::1, written as IPv6 too, with a
// port or 0 for any. Every other is none: every address and the
// unspecified ones, an address of another network, one of this
// machine's own interfaces, which it could listen on, a name, whatever
// it resolves to, no host, an address of a link with its zone, and a
// port that is none or is a service's name. The controls are the
// loopback's own, ::1 with a zone among them, which is ::1.
func TestOnlyAnAddressOfTheLoopbackIsListenedOn(t *testing.T) {
	for at, loopback := range map[string]bool{
		"127.0.0.1:8080":          true,
		"127.0.0.1:0":             true,
		"127.0.0.1:1":             true,
		"127.0.0.1:65535":         true,
		"127.0.0.2:8080":          true,
		"127.255.255.254:80":      true,
		"[::1]:8080":              true,
		"[::1]:0":                 true,
		"[::ffff:127.0.0.1]:8080": true,
		"0.0.0.0:0":               false,
		"0.0.0.0:8080":            false,
		"[::]:0":                  false,
		"[::]:8080":               false,
		":0":                      false,
		":8080":                   false,
		"192.0.2.1:8080":          false,
		"10.0.0.1:8080":           false,
		"128.0.0.1:8080":          false,
		"[::ffff:192.0.2.1]:8080": false,
		"[::ffff:0.0.0.0]:8080":   false,
		"[2001:db8::1]:8080":      false,
		"[fe80::1%lo0]:8080":      false,
		"[::1%lo0]:8080":          true,
		"localhost:0":             false,
		"localhost:8080":          false,
		"frontseat.example:80":    false,
		"127.0.0.1.example:80":    false,
		"127.1:8080":              false,
		"127.0.0.1":               false,
		"::1":                     false,
		"::1:8080":                false,
		"[::1]":                   false,
		"127.0.0.1:":              false,
		"127.0.0.1:http":          false,
		"127.0.0.1:65536":         false,
		"127.0.0.1:-1":            false,
		"127.0.0.1:080":           false,
		"127.0.0.1:+80":           false,
		"127.0.0.1: 80":           false,
		"127.0.0.1:80:80":         false,
		"":                        false,
	} {
		if got := onLoopback(at); got != loopback {
			t.Errorf("%q is of this machine's loopback: %t, want %t", at, got, loopback)
		}
	}
	for _, at := range own(t) {
		if onLoopback(at) {
			t.Errorf("%q, an address of this machine's own that is not of its loopback, is taken for one", at)
		}
	}
}

// --listen says another address of this machine's loopback, a port of
// its own among it, and the workspace last opened is the one asked
// where none is named. An address that is not this machine's loopback
// is refused, as that and by those words, never by what listening there
// came to: a port of a sandbox is not led to the network by this
// command, and an address of this machine's own interfaces, which it
// could listen on, is refused as any. So is a port that is none, and
// more than a name and a port; each before anything is listened on or
// asked. The help says that the loopback is every program's of this
// machine. The control is the loopback, by either of its addresses.
func TestAPortIsCarriedFromTheLoopbackAlone(t *testing.T) {
	b := box(t)
	asked, _ := b.was()
	if len(asked) != 0 {
		t.Fatal(asked)
	}
	const noLoopback, noPort, noName, noCount = "is no address of this machine's loopback", "is no port: one is a number from 1 to 65535", "is no workspace's name", "a port is named, after the workspace where one is"
	refused := []struct {
		args []string
		as   string
	}{
		{[]string{"named", "8080", "--listen", "0.0.0.0:0"}, noLoopback},
		{[]string{"named", "8080", "--listen", "[::]:0"}, noLoopback},
		{[]string{"named", "8080", "--listen", ":0"}, noLoopback},
		{[]string{"named", "8080", "--listen", "192.0.2.1:8080"}, noLoopback},
		{[]string{"named", "8080", "--listen", "[::ffff:192.0.2.1]:0"}, noLoopback},
		{[]string{"named", "8080", "--listen", "localhost:0"}, noLoopback},
		{[]string{"named", "8080", "--listen", "frontseat.example:80"}, noLoopback},
		{[]string{"named", "8080", "--listen", "127.0.0.1"}, noLoopback},
		{[]string{"named", "8080", "--listen", "127.0.0.1:http"}, noLoopback},
		{[]string{"named", "8080", "--listen", "127.0.0.1:65536"}, noLoopback},
		{[]string{"named", "0"}, noPort},
		{[]string{"named", "65536"}, noPort},
		{[]string{"named", "080"}, noPort},
		{[]string{"named", "+80"}, noPort},
		{[]string{"named", "80x"}, noPort},
		{[]string{"named", "-1"}, "unknown shorthand flag"},
		{[]string{"named", "080", "--listen", "127.0.0.1:0"}, noPort},
		{[]string{"named", "+80", "--listen", "127.0.0.1:0"}, noPort},
		{[]string{"named", "80x", "--listen", "127.0.0.1:0"}, noPort},
		{[]string{"named", "65536", "--listen", "127.0.0.1:0"}, noPort},
		{[]string{"Named", "8080"}, noName},
		{[]string{"8080", "named"}, noPort},
		{[]string{"named"}, noPort},
		{[]string{}, noCount},
		{[]string{"named", "8080", "more"}, noCount},
	}
	// Every address of this machine's own that is not of its loopback:
	// it could be listened on, and is not.
	for _, at := range own(t) {
		refused = append(refused, struct {
			args []string
			as   string
		}{[]string{"named", "8080", "--listen", at}, noLoopback})
	}
	for _, c := range refused {
		// One that is taken, and listens, is ended and not waited for.
		briefly, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		printed, code, err := runAt(t, briefly, "", append([]string{"workspace", "forward"}, c.args...)...)
		cancel()
		if err == nil || code != ExitUsage || printed != "" || !strings.Contains(err.Error(), c.as) {
			t.Errorf("workspace forward %v: %q, %d, %v; want it refused as written wrongly, saying %q", c.args, printed, code, err, c.as)
		}
	}
	if !strings.Contains(forwardCmd.Long, "any\nprogram of this machine, of any of its users, reaches the sandbox's\nport through it") || !strings.Contains(forwardCmd.Long, "as with ssh -L") {
		t.Errorf("the help of workspace forward does not say whose the listener is:\n%s", forwardCmd.Long)
	}
	if asked, _ := b.was(); len(asked) != 0 {
		t.Errorf("for what was refused, the control plane was asked %v", asked)
	}
	// The control: the loopback's addresses, and the workspace last
	// opened.
	lastOpened(t, b.url, "opened")
	at := "127.0.0.1:" + free(t)
	end := forwarding(t, "8080", "--listen", at)
	if back := heard(t, at, "a word"); back != "the box heard a word\n" {
		t.Errorf("of the workspace last opened, a word came back as %q", back)
	}
	if printed, _, _ := end(); !strings.HasSuffix(printed, "of the workspace opened; Ctrl-C ends it\n") {
		t.Errorf("of the workspace last opened, it printed %q", printed)
	}
	if asked, _ := b.was(); len(asked) != 1 || asked[0] != "/workspaces/opened/ports/8080" {
		t.Errorf("of the workspace last opened, the control plane was asked %v", asked)
	}
	for _, loopback := range []string{"127.0.0.1", "127.0.0.2", "[::1]"} {
		at := loopback + ":" + free(t)
		probe, err := net.Listen("tcp", at)
		if err != nil {
			// A machine without this address of the loopback.
			t.Logf("this machine listens on no %s: %v", at, err)
			continue
		}
		probe.Close()
		end := forwarding(t, "named", "8080", "--listen", at)
		if back := heard(t, at, "a word"); back != "the box heard a word\n" {
			t.Errorf("listening on %s, a word came back as %q", at, back)
		}
		printed, code, _ := end()
		if want := at + " leads to the port 8080 of the sandbox of the workspace named; Ctrl-C ends it\n"; printed != want || code != ExitInterrupted {
			t.Errorf("listening on %s, it printed %q and ended %d", at, printed, code)
		}
	}
	if asked, _ := b.was(); len(asked) < 2 || asked[1] != "/workspaces/named/ports/8080" {
		t.Errorf("the control: the control plane was asked %v", asked)
	}
}
