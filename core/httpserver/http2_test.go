package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTP2JoinsAllStreamsBeforeTelemetryCloses(t *testing.T) {
	const count = 8
	listener := listening(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	signals, err := telemetry.New(provider, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, cancelled, released := make(chan struct{}, count), make(chan struct{}, count), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(released) }) }
	defer unblock()
	handler := signals.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("protocol %s, want HTTP/2", r.Proto)
		}
		started <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
		<-released
	}), "test API")
	finished := make(chan error, 1)
	go func() { finished <- Serve(ctx, listener, handler) }()
	transport := &http.Transport{MaxConnsPerHost: 1, Protocols: new(http.Protocols)}
	transport.Protocols.SetUnencryptedHTTP2(true)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	var clients sync.WaitGroup
	for i := 0; i < count; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			response, _ := client.Get(fmt.Sprintf("http://%s/", listener.Addr()))
			if response != nil {
				response.Body.Close()
			}
		}()
	}
	for i := 0; i < count; i++ {
		await(t, started, "HTTP/2 stream admission")
	}
	cancel()
	for i := 0; i < count; i++ {
		await(t, cancelled, "HTTP/2 stream cancellation")
	}
	select {
	case err := <-finished:
		t.Fatalf("Serve returned before stream cleanup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not join streams")
	}
	if got := len(recorder.Ended()); got != count {
		t.Errorf("Serve returned with %d completed HTTP spans, want %d", got, count)
	}
	clients.Wait()
}
