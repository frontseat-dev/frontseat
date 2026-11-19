package source_test

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/source"
	"golang.org/x/crypto/ssh"
)

// Every dynamically allocated listener has its own lifetime, even when the
// requests all name the same address with port zero. A single forward is the
// control for the collision exposed by multiple forwards.
func TestReverseForwardedPortsAreReleased(t *testing.T) {
	for _, ending := range []string{"cancel", "disconnect", "shutdown"} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d", ending, count), func(t *testing.T) {
				editor, clients := forwardingEditors(t, 1)
				client := clients[0]
				var forwards []net.Listener
				for range count {
					forward, err := client.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					forwards = append(forwards, forward)
					t.Cleanup(func() { _ = forward.Close() })
					control, err := net.Listen("tcp", forward.Addr().String())
					if control != nil {
						_ = control.Close()
					}
					if !errors.Is(err, syscall.EADDRINUSE) {
						t.Fatalf("forward must own %s before cleanup: %v", forward.Addr(), err)
					}
				}
				switch ending {
				case "cancel":
					for _, forward := range forwards {
						if err := forward.Close(); err != nil {
							t.Fatal(err)
						}
					}
				case "disconnect":
					if err := client.Close(); err != nil {
						t.Fatal(err)
					}
				case "shutdown":
					if err := editor.Close(); err != nil {
						t.Fatal(err)
					}
				}
				for _, forward := range forwards {
					deadline := time.Now().Add(time.Second)
					for {
						reclaimed, err := net.Listen("tcp", forward.Addr().String())
						if err == nil {
							_ = reclaimed.Close()
							break
						}
						if time.Now().After(deadline) {
							t.Fatalf("%s left forwarded port %s occupied: %v", ending, forward.Addr(), err)
						}
						time.Sleep(5 * time.Millisecond)
					}
				}
			})
		}
	}
}

func TestDisconnectDoesNotCloseAnotherEditorsReusedPort(t *testing.T) {
	_, clients := forwardingEditors(t, 2)
	previous, next := clients[0], clients[1]
	first, err := previous.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	address := first.Addr().String()
	if duplicate, err := next.Listen("tcp", address); err == nil {
		_ = duplicate.Close()
		t.Fatal("a port still owned by the first editor was available to another")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := next.Listen("tcp", address)
	if err != nil {
		t.Fatalf("the released port cannot be reused: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := previous.Close(); err != nil {
		t.Fatal(err)
	}
	// Keep checking after the first connection has gone, so its asynchronous
	// cleanup cannot silently close the new owner's listener.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		stolen, err := net.Listen("tcp", address)
		if stolen != nil {
			_ = stolen.Close()
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatalf("disconnecting the previous editor released the next editor's port: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReusedReverseForwardedPortIsReleased(t *testing.T) {
	for _, ending := range []string{"cancel", "disconnect", "shutdown"} {
		t.Run(ending, func(t *testing.T) {
			editor, clients := forwardingEditors(t, 1)
			client := clients[0]
			forward, err := client.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = forward.Close() })
			address := forward.Addr().String()
			for range 100 {
				if err := forward.Close(); err != nil {
					t.Fatal(err)
				}
				replacement, err := client.Listen("tcp", address)
				if err != nil {
					t.Fatalf("cancelled port cannot be reused: %v", err)
				}
				forward = replacement
			}
			// Old accept loops can finish after the replacement starts.
			time.Sleep(150 * time.Millisecond)
			control, err := net.Listen("tcp", address)
			if control != nil {
				_ = control.Close()
			}
			if !errors.Is(err, syscall.EADDRINUSE) {
				t.Fatalf("the reused listener must own its port: %v", err)
			}
			switch ending {
			case "cancel":
				err = forward.Close()
			case "disconnect":
				err = client.Close()
			case "shutdown":
				err = editor.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				reclaimed, err := net.Listen("tcp", address)
				if err == nil {
					_ = reclaimed.Close()
					break
				}
				if !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) {
					t.Fatalf("%s left the reused port occupied: %v", ending, err)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

func forwardingEditors(t *testing.T, count int) (*source.Shell, []*ssh.Client) {
	t.Helper()
	editor, signer := keyedShell(t)
	var clients []*ssh.Client
	for range count {
		clients = append(clients, dialled(t, editor, signer))
	}
	return editor, clients
}
