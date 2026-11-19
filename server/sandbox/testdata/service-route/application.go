package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// application is the POC repository's small HTTP application, not a service
// manager. PostgreSQL's native client is its explicit runtime dependency,
// supplied by the pinned runtime image. A private socket name makes libpq use
// the granted byte route; authentication still happens at the PostgreSQL server.
func application(upstream, address string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dir, err := os.MkdirTemp("", "application-db-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Symlink(upstream, filepath.Join(dir, ".s.PGSQL.7000")); err != nil {
		return err
	}
	if err := os.Setenv("PGHOST", dir); err != nil {
		return err
	}
	query := func(parent context.Context, statement string) (string, error) {
		call, stop := context.WithTimeout(parent, 5*time.Second)
		defer stop()
		out, err := sqlCommand(call, "", statement).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("database: %w: %s", err, out)
		}
		return strings.TrimSpace(string(out)), nil
	}
	// Readiness is real application readiness: initialization succeeds before
	// there is any listening route. A bad upstream cannot look ready on TCP.
	if _, err := query(ctx, "CREATE TABLE value(value bigint); INSERT INTO value VALUES (0);"); err != nil {
		return err
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(address, 0o666); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /build", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, os.Getenv("POC_BUILD")) })
	mux.HandleFunc("GET /value", func(w http.ResponseWriter, r *http.Request) {
		statement := "SELECT value FROM value;"
		if raw := r.URL.Query().Get("set"); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				http.Error(w, "expected an integer", http.StatusBadRequest)
				return
			}
			statement = "UPDATE value SET value=" + strconv.FormatInt(value, 10) + " RETURNING value;"
		}
		value, err := query(r.Context(), statement)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, value)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	select {
	case err := <-finished:
		cancel()
		server.Close()
		return err
	case <-ctx.Done():
		ending, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		err := server.Shutdown(ending)
		if err != nil {
			server.Close()
		}
		served := <-finished
		if !errors.Is(served, http.ErrServerClosed) {
			return served
		}
		return err
	}
}
