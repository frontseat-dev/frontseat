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
	"testing"
	"time"

	"github.com/coder/websocket"
)

// boxed is a control plane of the tests that carries a connection to a
// port of a workspace's sandbox, where what listens says back each line
// it is told, after "the box heard". It keeps what it was asked and
// what was presented to it, and refuses whatever is asked first where
// it is told to.
type boxed struct {
	// url is where the control plane is asked.
	url string

	mu        sync.Mutex
	asked     []string
	presented []string
	refuses   int
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
	inside := websocket.NetConn(r.Context(), socket, websocket.MessageBinary)
	for heard := bufio.NewScanner(inside); heard.Scan(); {
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
		case <-time.After(30 * time.Second):
			t.Fatal("workspace forward went on after it was interrupted")
		}
		return printed, code, err
	}
	t.Cleanup(func() { end() })
	return end
}

// heard connects to an address of this machine once something listens
// there, says a line, and returns the line said back; empty where the
// connection was ended with nothing said.
func heard(t *testing.T, at, line string) string {
	t.Helper()
	var conn net.Conn
	for until := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var err error
		if conn, err = net.Dial("tcp", at); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("nothing listens on %s: %v", at, err)
		}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(conn, line+"\n"); err != nil {
		return ""
	}
	back, _ := bufio.NewReader(conn).ReadString('\n')
	return back
}

// `workspace forward` listens on this machine's loopback, at the port
// asked for where no other is said, and carries each connection made
// there to that port of the workspace's sandbox, through the control
// plane, presenting the token to it: what is written arrives there and
// what is answered comes back, for one connection after another and
// for several at once. It says once, on a line, what it listens on and
// where that leads. A connection that cannot be carried is ended and
// said on standard error, and the command goes on. Interrupted, it
// ends with 130, saying nothing more, and listens no longer.
func TestAPortOfThisMachineIsCarriedToAPortOfAWorkspacesSandbox(t *testing.T) {
	b := box(t)
	port := free(t)
	at := "127.0.0.1:" + port
	b.refuses = 1
	end := forwarding(t, "named", port)
	// The first cannot be carried: the control plane refuses it.
	if back := heard(t, at, "a word"); back != "" {
		t.Errorf("a connection the control plane did not carry said back %q", back)
	}
	// The command goes on: the next is carried, and then several at once.
	if back := heard(t, at, "a word"); back != "the box heard a word\n" {
		t.Errorf("carried to the box, a word came back as %q", back)
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
	printed, code, err := end()
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
	if len(asked) != 6 {
		t.Fatalf("the control plane was asked %v, want six connections", asked)
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

// --listen says another address of this machine's loopback, a port of
// its own among it, and the workspace last opened is the one asked
// where none is named. An address that is not this machine's loopback
// is refused: a port of a sandbox is not led to the network by this
// command. So is a port that is none, and more than a name and a port;
// each before anything is listened on or asked. The control is the
// loopback, by either of its addresses.
func TestAPortIsCarriedFromTheLoopbackAlone(t *testing.T) {
	b := box(t)
	asked, _ := b.was()
	if len(asked) != 0 {
		t.Fatal(asked)
	}
	for _, args := range [][]string{
		{"named", "8080", "--listen", "0.0.0.0:0"},
		{"named", "8080", "--listen", "[::]:0"},
		{"named", "8080", "--listen", ":0"},
		{"named", "8080", "--listen", "192.0.2.1:8080"},
		{"named", "8080", "--listen", "localhost:0"},
		{"named", "8080", "--listen", "frontseat.example:80"},
		{"named", "8080", "--listen", "127.0.0.1"},
		{"named", "8080", "--listen", "127.0.0.1:http"},
		{"named", "8080", "--listen", "127.0.0.1:65536"},
		{"named", "0"},
		{"named", "65536"},
		{"named", "080"},
		{"named", "+80"},
		{"named", "80x"},
		{"named", "-1"},
		{"named", "080", "--listen", "127.0.0.1:0"},
		{"named", "+80", "--listen", "127.0.0.1:0"},
		{"named", "80x", "--listen", "127.0.0.1:0"},
		{"named", "65536", "--listen", "127.0.0.1:0"},
		{"Named", "8080"},
		{"8080", "named"},
		{"named"},
		{},
		{"named", "8080", "more"},
	} {
		// One that is taken, and listens, is ended and not waited for.
		briefly, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		printed, code, err := runAt(t, briefly, "", append([]string{"workspace", "forward"}, args...)...)
		cancel()
		if err == nil || code != ExitUsage || printed != "" {
			t.Errorf("workspace forward %v: %q, %d, %v; want it refused as written wrongly", args, printed, code, err)
		}
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
