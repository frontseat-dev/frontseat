// A probe fixture, not a Frontseat runner: echo supplies observable service
// identity, proxy copies bytes between a private loopback and one granted Unix
// socket, and check exercises the route with binary data. No destination comes
// from the data being forwarded.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/common/confine"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if os.Getuid() == 0 {
		return fmt.Errorf("probe consumer/service must not run as root")
	}
	args := os.Args[1:]
	if len(args) != 3 {
		return fmt.Errorf("expected mode and two arguments")
	}
	switch args[0] {
	case "without-db":
		for _, name := range []string{"PGHOST", "PGHOSTADDR", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE", "PGSERVICE", "PGSERVICEFILE", "PGSSLCERT", "PGSSLKEY"} {
			if _, set := os.LookupEnv(name); set {
				return fmt.Errorf("unexpected database variable %s", name)
			}
		}
		return nil
	case "application":
		return application(args[1], args[2])
	case "postgres":
		return postgres()
	case "sql":
		return sql(args[1], args[2])
	case "echo":
		return serve("unix", args[1], func(ctx context.Context, conn net.Conn) {
			conn.Write([]byte(args[2] + "\n"))
			io.Copy(conn, conn)
		})
	case "halfclose":
		return serve("unix", args[1], func(ctx context.Context, conn net.Conn) {
			conn.Write([]byte(args[2] + "\n"))
			conn.(*net.UnixConn).CloseWrite()
			payload, err := io.ReadAll(io.LimitReader(conn, 256*1024+1))
			if err == nil && len(payload) > 0 {
				os.WriteFile("/tmp/receipt", payload, 0o600)
			}
		})
	case "proxy":
		return serve("tcp", args[2], func(ctx context.Context, downstream net.Conn) {
			upstream, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", args[1])
			if err != nil {
				fmt.Fprintln(os.Stderr, "upstream:", err)
				return
			}
			confine.Relay(ctx, downstream, upstream)
		})
	case "check":
		return check(args[1], args[2])
	case "after-eof":
		network := "tcp"
		if args[1][0] == '/' {
			network = "unix"
		}
		conn, err := net.DialTimeout(network, args[1], 5*time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		hello, err := io.ReadAll(io.LimitReader(conn, int64(len(args[2])+2)))
		if err != nil || string(hello) != args[2]+"\n" {
			return fmt.Errorf("server-first EOF: %v, greeting %q", err, hello)
		}
		_, err = io.Copy(conn, bytes.NewReader(halfClosePayload()))
		return err
	case "receipt":
		payload, err := os.ReadFile("/tmp/receipt")
		if err != nil || !bytes.Equal(payload, halfClosePayload()) {
			return fmt.Errorf("server receipt: %v, bytes=%d", err, len(payload))
		}
		return nil
	case "dial":
		conn, err := net.DialTimeout(args[1], args[2], 3*time.Second)
		if err != nil {
			return err
		}
		return conn.Close()
	case "write":
		return os.WriteFile(args[1], []byte(args[2]), 0o600)
	case "resolve":
		addresses, err := net.LookupIP(args[1])
		if err != nil {
			return err
		}
		for _, address := range addresses {
			if v4 := address.To4(); v4 != nil {
				fmt.Println(v4.String())
				return nil
			}
		}
		return fmt.Errorf("no IPv4 address")
	default:
		return fmt.Errorf("unknown probe mode")
	}
}

func halfClosePayload() []byte {
	return bytes.Repeat([]byte{0, 1, 2, 3}, 65536)
}

func serve(network, address string, handle func(context.Context, net.Conn)) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return serveContext(ctx, network, address, handle)
}

func serveContext(parent context.Context, network, address string, handle func(context.Context, net.Conn)) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listener, err := net.Listen(network, address)
	if err != nil {
		return err
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	if network == "unix" {
		if err := os.Chmod(address, 0o666); err != nil {
			return err
		}
	}
	var clients sync.WaitGroup
	defer func() {
		cancel()
		listener.Close()
		clients.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		clients.Add(1)
		go func() {
			defer clients.Done()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			handle(ctx, conn)
		}()
	}
}

func check(address, identity string) error {
	network := "tcp"
	if len(address) > 0 && address[0] == '/' {
		network = "unix"
	}
	conn, err := net.DialTimeout(network, address, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	hello := make([]byte, len(identity)+1)
	if _, err := io.ReadFull(conn, hello); err != nil {
		return fmt.Errorf("service greeting: %w", err)
	}
	if string(hello) != identity+"\n" {
		return fmt.Errorf("reached a different service")
	}
	payload := make([]byte, 256*1024)
	if _, err := rand.Read(payload); err != nil {
		return err
	}
	written := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, bytes.NewReader(payload))
		switch conn := conn.(type) {
		case *net.TCPConn:
			conn.CloseWrite()
		case *net.UnixConn:
			conn.CloseWrite()
		}
		written <- err
	}()
	received, readErr := io.ReadAll(io.LimitReader(conn, int64(len(payload)+1)))
	conn.Close()
	writeErr := <-written
	if readErr != nil || writeErr != nil || !bytes.Equal(payload, received) {
		return fmt.Errorf("binary roundtrip: read=%v write=%v bytes=%d", readErr, writeErr, len(received))
	}
	fmt.Println("service="+identity, "bytes=262144 uid="+fmt.Sprint(os.Getuid()))
	return nil
}
