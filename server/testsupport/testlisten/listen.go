// Package testlisten gives a test a port of this machine that a container
// reaches at host.docker.internal, and that is the test's own.
//
// A port the kernel hands out is not always one no other process holds.
// A listener on every address over "tcp" is one IPv6 socket that takes
// IPv4 too, and on macOS it binds a port that another process holds on
// every IPv4 address without an error. Under
// OrbStack, a container's host.docker.internal is 0.250.250.254, and
// what it sends there reaches the host at 127.0.0.1: at that port it
// reaches the other process and not the test, which reads it and
// answers nothing.
package testlisten

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// tries is how many ports are asked for before a test is failed.
const tries = 5

// within is how long a connection on the loopback is given to arrive.
const within = time.Second

// errShared is what a listener is refused for when a connection to its
// port reaches another process.
var errShared = errors.New("held by another process too")

// Own listens on every address of this machine, on a port the kernel
// chooses, over network ("tcp" or "tcp4"), and returns the listener
// once a connection to that port at the loopback has arrived at it: a
// port another process also holds is let go and another asked for, and
// after a few the error names the last. The caller closes the listener.
func Own(network string) (net.Listener, error) {
	return own(network, func() int { return 0 })
}

// own is Own, asking for the ports next gives.
func own(network string, next func() int) (net.Listener, error) {
	if network != "tcp" && network != "tcp4" {
		return nil, fmt.Errorf("a port a container reaches is listened on over \"tcp\" or \"tcp4\", and not over %q", network)
	}
	var err error
	for range tries {
		var l net.Listener
		if l, err = owned(network, next()); err == nil {
			return l, nil
		}
	}
	return nil, err
}

// owned listens on a port of every address, and returns the listener
// where a connection to that port at the loopback arrives at it and
// says what was written to it; otherwise it says why it does not.
func owned(network string, port int) (net.Listener, error) {
	l, err := net.Listen(network, "0.0.0.0:"+strconv.Itoa(port))
	if err != nil {
		return nil, fmt.Errorf("listening on port %d of this machine: %w", port, err)
	}
	if err := proven(l, dialled); err != nil {
		return nil, err
	}
	return l, nil
}

// dialled is a connection to an address of this machine.
func dialled(address string) (net.Conn, error) {
	return net.DialTimeout("tcp", address, within)
}

// proven writes a word of its own to the listener's port at each
// loopback it listens on (127.0.0.1, and [::1] where it takes IPv6
// too and the machine has that address) and reads each from the
// listener's own accept, closing the listener where it does not.
// Nothing it opens is left open or running when it returns: the
// connections are closed and the accept is joined.
func proven(l net.Listener, dial func(address string) (net.Conn, error)) (err error) {
	at := l.Addr().(*net.TCPAddr)
	loopbacks := []string{"127.0.0.1"}
	if at.IP.To4() == nil {
		loopbacks = append(loopbacks, "::1")
	}
	// The words written, each to be read from one connection accepted.
	words := map[string]bool{}
	var reached []string
	for _, loopback := range loopbacks {
		address := net.JoinHostPort(loopback, strconv.Itoa(at.Port))
		conn, err := dial(address)
		if loopback == "::1" && (errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.ENETUNREACH)) {
			// The machine has no [::1]: nothing can arrive there, and
			// there is nothing to prove.
			continue
		}
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("port %d of this machine: %w", at.Port, err)
		}
		defer conn.Close()
		word := make([]byte, 16)
		if _, err := rand.Read(word); err != nil {
			_ = l.Close()
			return err
		}
		_ = conn.SetWriteDeadline(time.Now().Add(within))
		if _, err := conn.Write(word); err != nil {
			_ = l.Close()
			return fmt.Errorf("port %d of this machine is %w: a connection to %s could not be written", at.Port, errShared, address)
		}
		words[string(word)] = true
		reached = append(reached, address)
	}
	shared := fmt.Errorf("port %d of this machine is %w: a connection to %s did not reach this listener", at.Port, errShared, strings.Join(reached, " or "))
	expected := len(words)
	accepted := make(chan net.Conn, expected)
	go func() {
		defer close(accepted)
		for range expected {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	defer func() {
		if err != nil {
			// An accept still waiting ends with the listener.
			_ = l.Close()
		}
		for conn := range accepted {
			_ = conn.Close()
		}
	}()
	deadline := time.After(within)
	for len(words) > 0 {
		select {
		case conn, ok := <-accepted:
			if !ok {
				return shared
			}
			_ = conn.SetReadDeadline(time.Now().Add(within))
			heard := make([]byte, 16)
			_, err := io.ReadFull(conn, heard)
			_ = conn.Close()
			if err != nil || !words[string(heard)] {
				return shared
			}
			delete(words, string(heard))
		case <-deadline:
			return shared
		}
	}
	return nil
}
