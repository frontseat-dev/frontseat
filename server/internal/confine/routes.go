package confine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Relay preserves both write halves and joins both directions before returning.
// Cancellation and copy failure close both sockets to release blocked reads.
func Relay(ctx context.Context, first, second net.Conn) {
	defer first.Close()
	defer second.Close()
	stop := context.AfterFunc(ctx, func() { first.Close(); second.Close() })
	defer stop()
	copyHalf := func(to, from net.Conn) {
		_, err := io.Copy(to, from)
		if half, ok := to.(interface{ CloseWrite() error }); ok {
			if closeErr := half.CloseWrite(); err == nil {
				err = closeErr
			}
		} else {
			err = errors.New("connection cannot half-close")
		}
		if err != nil {
			to.Close()
			from.Close()
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); copyHalf(first, second) }()
	copyHalf(second, first)
	<-done
}

// runRoutes is the trusted network boundary in one sandbox. Every four argv
// values name a fixed listener and destination; traffic names no destination.
// What follows "--" is what the sandbox runs once every route listens: the
// service, or the runner of the test beside it. It runs as this program's
// child, in the one workload a sandbox holds, and the routes end with it;
// this program ends as it does.
func runRoutes(args []string) error {
	var command []string
	if at := slices.Index(args, "--"); at >= 0 {
		args, command = args[:at], args[at+1:]
		if len(command) == 0 {
			return errors.New("routes are told a command after --, and none is given")
		}
	}
	if len(args)%4 != 0 || len(args) > 4*128 {
		return errors.New("routes are at most 128 listener/destination pairs")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var listeners []net.Listener
	var workers sync.WaitGroup
	defer func() {
		cancel()
		for _, l := range listeners {
			l.Close()
		}
		workers.Wait()
	}()
	capacity := make(chan struct{}, 256)
	errorsOut := make(chan error, 1)
	for i := 0; i < len(args); i += 4 {
		lnet, at, dnet, to := args[i], args[i+1], args[i+2], args[i+3]
		for _, pair := range [][2]string{{lnet, at}, {dnet, to}} {
			switch pair[0] {
			case "tcp":
				host, port, err := net.SplitHostPort(pair[1])
				n, e := strconv.Atoi(port)
				if err != nil || e != nil || host != "127.0.0.1" || n < 1 || n > 65535 {
					return fmt.Errorf("a service route is a loopback TCP port")
				}
			case "unix":
				if !filepath.IsAbs(pair[1]) || filepath.Clean(pair[1]) != pair[1] || !strings.HasPrefix(pair[1], "/frontseat/routes/") {
					return errors.New("a service route is a private Unix socket")
				}
			default:
				return errors.New("a service route uses TCP or Unix sockets")
			}
		}
		if lnet == dnet {
			return errors.New("a service route connects TCP to a Unix socket")
		}
		listener, err := net.Listen(lnet, at)
		if err != nil {
			return err
		}
		listeners = append(listeners, listener)
		if lnet == "unix" {
			if err := os.Chmod(at, 0666); err != nil {
				return err
			}
		}
		workers.Go(func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					select {
					case errorsOut <- err:
					default:
					}
					return
				}
				select {
				case capacity <- struct{}{}:
				case <-ctx.Done():
					conn.Close()
					return
				}
				workers.Go(func() {
					defer func() { <-capacity }()
					upstream, err := dial(ctx, dnet, to)
					if err != nil {
						conn.Close()
						return
					}
					Relay(ctx, conn, upstream)
				})
			}
		})
	}
	// The marker is in a private mount shared only with this service's
	// confiner. It is written after every listener is bound.
	if err := os.WriteFile("/frontseat/ready/listening", []byte("ready\n"), 0644); err != nil {
		return err
	}
	if len(command) == 0 {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errorsOut:
			return err
		}
	}
	return runBeside(ctx, command, errorsOut)
}

// runBeside runs what the sandbox runs beside its routes, as this
// program's child, and ends as it does: its exit status, or 128 and the
// signal that ended it. Asked to end, it asks the command to; where a
// route fails, the command is ended, since nothing reaches it any more.
func runBeside(ctx context.Context, command []string, routeFailed <-chan error) error {
	child := exec.Command(command[0], command[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		return err
	}
	ended := make(chan error, 1)
	go func() { ended <- child.Wait() }()
	select {
	case err := <-ended:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return err
		}
		status := child.ProcessState.Sys().(syscall.WaitStatus)
		if status.Signaled() {
			os.Exit(128 + int(status.Signal()))
		}
		os.Exit(status.ExitStatus())
	case <-ctx.Done():
		_ = child.Process.Signal(syscall.SIGTERM)
	case err := <-routeFailed:
		_ = child.Process.Kill()
		<-ended
		return fmt.Errorf("a route failed, and what it served was ended: %w", err)
	}
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		_ = child.Process.Kill()
		<-ended
	}
	return nil
}

// dial connects a route to where it leads: a loopback port, or the socket
// another sandbox made in a directory it writes, which is reached only
// where it is a socket itself (DialSocket), never through a link it left
// there to something of this sandbox's.
func dial(ctx context.Context, network, to string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if network == "unix" {
		return DialSocket(ctx, to)
	}
	return (&net.Dialer{}).DialContext(ctx, network, to)
}

// DialSocket connects to the unix socket at a path, where that path is a
// socket itself and no link to one: a directory a sandbox writes is where
// such a socket is, and what follows a link there, as connect does, is
// led wherever the sandbox likes (the stack's own Podman, a runner's
// socket). On Linux it is reached by the file it was found as
// (OpenSocket), never by its path again; elsewhere, where nothing a
// sandbox runs runs, the path is looked at, not followed, then connected
// to.
func DialSocket(ctx context.Context, path string) (net.Conn, error) {
	if runtime.GOOS != "linux" {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode().Type() != os.ModeSocket {
			return nil, fmt.Errorf("%s is not a socket", path)
		}
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}
	fd, err := OpenSocket(path)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	// The descriptor's own name leads to the file it was opened as.
	return (&net.Dialer{}).DialContext(ctx, "unix", "/proc/self/fd/"+strconv.Itoa(fd))
}

// OpenSocket opens the socket at a path on Linux as a place, by a
// descriptor the caller closes, without following a link: what is there
// is a socket, or nothing is opened. What is done with the descriptor is
// done to what was checked, whatever is put at the path meanwhile.
func OpenSocket(path string) (int, error) {
	fd, err := syscall.Open(path, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("%s: %w", path, err)
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	if uint32(stat.Mode)&syscall.S_IFMT != syscall.S_IFSOCK {
		syscall.Close(fd)
		return -1, fmt.Errorf("%s is not a socket", path)
	}
	return fd, nil
}

// oPath is Linux's O_PATH, which the syscall package does not name: a
// file opened as a place, named by its descriptor. It is said here, where
// what relays a service's routes reads it, since that source is built
// alone too (a service's program imports it).
const oPath = 0x200000
