package testlisten

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// held listens at address (a host and port 0) as a process does that
// lets others share its port (SO_REUSEADDR and SO_REUSEPORT), as Logi
// Options+ does on every IPv4 address at 59869, and accepts and holds
// what reaches it, answering nothing. It returns the port.
func held(t *testing.T, network, address string) int {
	t.Helper()
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var failed error
		if err := raw.Control(func(fd uintptr) {
			// x/sys names both on every system this is built for: package
			// syscall has no SO_REUSEPORT on linux/amd64.
			for _, option := range []int{unix.SO_REUSEADDR, unix.SO_REUSEPORT} {
				if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, option, 1); err != nil {
					failed = err
				}
			}
		}); err != nil {
			return err
		}
		return failed
	}}
	l, err := config.Listen(context.Background(), network, address)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var conns []net.Conn
		defer func() {
			for _, conn := range conns {
				_ = conn.Close()
			}
		}()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conns = append(conns, conn)
		}
	}()
	t.Cleanup(func() { _ = l.Close(); <-done })
	return l.Addr().(*net.TCPAddr).Port
}

// at asks for the ports given, one after another.
func at(ports ...int) func() int {
	return func() int {
		next := ports[0]
		if len(ports) > 1 {
			ports = ports[1:]
		}
		return next
	}
}

// refused says the refusal names the port, and which refusal it is:
// where the system lets a second listener bind a port held on every
// IPv4 address (macOS), the proof refuses it; where it does not
// (Linux), the listening does.
func refused(t *testing.T, err error, port int) {
	t.Helper()
	if err == nil {
		t.Fatalf("a listener on port %d, which another holds, was taken for this test's own", port)
	}
	if !strings.Contains(err.Error(), "port "+strconv.Itoa(port)+" ") {
		t.Fatalf("the refusal does not name the port %d: %v", port, err)
	}
	switch runtime.GOOS {
	case "darwin":
		if !errors.Is(err, errShared) {
			t.Fatalf("on %s the proof refuses the port, and this refused it: %v", runtime.GOOS, err)
		}
	case "linux":
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatalf("on %s the listening refuses the port, and this refused it: %v", runtime.GOOS, err)
		}
	default:
		t.Skipf("which refusal %s gives is not known: %v", runtime.GOOS, err)
	}
}

func TestAPortAnotherProcessHoldsOnEveryIPv4AddressIsRefusedByItsNumber(t *testing.T) {
	port := held(t, "tcp4", "0.0.0.0:0")
	l, err := owned("tcp", port)
	if err == nil {
		_ = l.Close()
	}
	refused(t, err, port)
}

func TestAPortAnotherProcessHoldsAtTheIPv6LoopbackIsRefusedByItsNumber(t *testing.T) {
	if probe, err := net.Listen("tcp6", "[::1]:0"); err != nil {
		t.Skipf("this machine has no [::1] for another process to hold: %v", err)
	} else {
		_ = probe.Close()
	}
	port := held(t, "tcp6", "[::1]:0")
	l, err := owned("tcp", port)
	if err == nil {
		_ = l.Close()
	}
	refused(t, err, port)
}

func TestAnotherPortIsAskedForWhenOneIsHeld(t *testing.T) {
	port := held(t, "tcp4", "0.0.0.0:0")
	l, err := own("tcp", at(port, 0))
	if err != nil {
		t.Fatalf("a free port was not asked for after %d: %v", port, err)
	}
	defer l.Close()
	if got := l.Addr().(*net.TCPAddr).Port; got == port {
		t.Fatalf("the listener is on the held port %d", port)
	}
}

func TestAPortOfItsOwnIsReachedAtItsListener(t *testing.T) {
	for _, network := range []string{"tcp", "tcp4"} {
		t.Run(network, func(t *testing.T) {
			l, err := Own(network)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			// Reached again, as the proof reaches it, now the listener is
			// given out.
			if err := proven(l, dialled); err != nil {
				t.Fatalf("a listener Own gave out on port %d is not reached: %v", l.Addr().(*net.TCPAddr).Port, err)
			}
		})
	}
}

// withoutIPv6 dials as a machine does whose [::1] fails as given, and
// as this one does otherwise.
func withoutIPv6(failed error) func(string) (net.Conn, error) {
	return func(address string) (net.Conn, error) {
		if host, _, _ := net.SplitHostPort(address); host == "::1" {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: failed}}
		}
		return dialled(address)
	}
}

// unshared is a listener on every address at a port no other process of
// this machine holds on a loopback, proven as the machine dials: a port
// the system hands out may be one another process took at 127.0.0.1
// alone, which is what proving is for, and no fault of what is tested
// with it. A machine that hands out ten such in a row fails the test.
func unshared(t *testing.T) net.Listener {
	t.Helper()
	for range 10 {
		l, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		if proven(l, dialled) == nil {
			return l
		}
		_ = l.Close()
	}
	t.Fatal("ten ports in a row were held by another process too")
	return nil
}

// A machine without [::1] has nothing there to prove; any other failure
// to reach the port there is a refusal. The control is the same
// listener proven as the machine dials, which it is before each case.
func TestALoopbackTheMachineDoesNotHaveIsNotProven(t *testing.T) {
	for _, missing := range []error{syscall.EADDRNOTAVAIL, syscall.ENETUNREACH} {
		l := unshared(t)
		if l.Addr().(*net.TCPAddr).IP.To4() != nil {
			_ = l.Close()
			t.Skip("this machine listens on every address over IPv4 alone, and dials no [::1]")
		}
		if err := proven(l, withoutIPv6(missing)); err != nil {
			t.Errorf("a machine whose [::1] fails with %v was refused: %v", missing, err)
		}
		_ = l.Close()
	}
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := proven(l, withoutIPv6(syscall.ECONNREFUSED)); err == nil || !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("a [::1] that refused the connection was let pass: %v", err)
	}
}

func TestOnlyANetworkOfTCPIsListenedOn(t *testing.T) {
	for _, network := range []string{"unix", "udp", "tcp6", ""} {
		l, err := Own(network)
		if err == nil {
			_ = l.Close()
			t.Fatalf("a listener over %q was given out", network)
		}
		if !strings.Contains(err.Error(), strconv.Quote(network)) {
			t.Fatalf("the refusal of %q does not name it: %v", network, err)
		}
	}
}
