package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/common/confine"
)

// This fixture invokes PostgreSQL's own tools. It implements no SQL parser,
// database wire protocol, provider ABI or public credential interface.
func sql(connection, statement string) error {
	cmd := sqlCommand(context.Background(), connection, statement)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func sqlCommand(ctx context.Context, connection, statement string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "psql", "-X", "-q", "-A", "-t", "-w", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=sqlstate", "--dbname", connection)
	cmd.Stdin = strings.NewReader(statement)
	cmd.WaitDelay = time.Second
	return cmd
}

func postgres() error {
	const data = "/tmp/frontseat-pg"
	init := exec.Command("initdb", "-D", data, "--no-locale", "--encoding=UTF8", "--auth-local=peer", "--auth-host=scram-sha-256")
	if output, err := init.CombinedOutput(); err != nil {
		return fmt.Errorf("initdb: %w: %s", err, output)
	}
	// Only administration inside the provider uses its private Unix socket.
	// The granted socket is a byte relay to TCP, where postgres is refused and
	// every ordinary login must complete native SCRAM authentication.
	if err := os.WriteFile(data+"/pg_hba.conf", []byte("local all postgres peer\nlocal all all reject\nhost all postgres 127.0.0.1/32 reject\nhost all all 127.0.0.1/32 scram-sha-256\n"), 0o600); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := exec.CommandContext(ctx, "postgres", "-D", data,
		"-c", "listen_addresses=127.0.0.1", "-c", "unix_socket_directories=/tmp",
		"-c", "unix_socket_permissions=0700", "-c", "max_connections=24",
		"-c", "log_statement=none", "-c", "log_min_error_statement=panic")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	finished := make(chan error, 2)
	go func() { finished <- cmd.Wait() }()
	go func() {
		finished <- serveContext(ctx, "unix", "/route/service.sock", func(ctx context.Context, downstream net.Conn) {
			upstream, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:5432")
			if err != nil {
				return
			}
			confine.Relay(ctx, downstream, upstream)
		})
	}()
	err := <-finished
	cancel()
	<-finished
	return err
}
