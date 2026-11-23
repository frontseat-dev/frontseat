package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// Ended calls what Setup returned to stop its signals, giving what they
// still hold five seconds to be exported, and returns why it could not
// be, where it could not: what a program does last.
func Ended(stop func(context.Context) error) error {
	ending, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return stop(ending)
}

// Setup configures the official OTLP exporters. HTTP/protobuf is the standard
// default, gRPC is selectable per signal. With no endpoint or exporter selected,
// a process exports nothing. Endpoint, TLS, credentials, batching, sampling and
// exemplar settings are interpreted by the SDK, not by Frontseat.
// The returned shutdown function must be called even when setup fails partway.
func Setup(ctx context.Context, service string) (*Signals, func(context.Context) error, error) {
	var stops []func(context.Context) error
	stop := func(ctx context.Context) error {
		var errs []error
		for _, f := range stops {
			errs = append(errs, f(ctx))
		}
		return errors.Join(errs...)
	}
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return nil, stop, nil
	}
	var protocols [3]string
	for i, signal := range []string{"TRACES", "METRICS", "LOGS"} {
		p, err := protocol(signal)
		if err != nil {
			return nil, stop, err
		}
		protocols[i] = p
	}
	if protocols == [3]string{} {
		return nil, stop, nil
	}
	r, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(service)), resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, stop, err
	}
	var tp trace.TracerProvider
	var mp metric.MeterProvider
	var lp log.LoggerProvider
	if protocol := protocols[0]; protocol != "" {
		var exporter sdktrace.SpanExporter
		if protocol == "grpc" {
			exporter, err = otlptracegrpc.New(ctx)
		} else {
			exporter, err = otlptracehttp.New(ctx)
		}
		if err != nil {
			return nil, stop, err
		}
		p := sdktrace.NewTracerProvider(sdktrace.WithResource(r), sdktrace.WithBatcher(exporter))
		tp = p
		stops = append(stops, p.Shutdown)
	}
	if protocol := protocols[1]; protocol != "" {
		var exporter sdkmetric.Exporter
		if protocol == "grpc" {
			exporter, err = otlpmetricgrpc.New(ctx)
		} else {
			exporter, err = otlpmetrichttp.New(ctx)
		}
		if err != nil {
			return nil, stop, err
		}
		p := sdkmetric.NewMeterProvider(sdkmetric.WithResource(r), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)))
		mp = p
		stops = append(stops, p.Shutdown)
	}
	if protocol := protocols[2]; protocol != "" {
		var exporter sdklog.Exporter
		if protocol == "grpc" {
			exporter, err = otlploggrpc.New(ctx)
		} else {
			exporter, err = otlploghttp.New(ctx)
		}
		if err != nil {
			return nil, stop, err
		}
		p := sdklog.NewLoggerProvider(sdklog.WithResource(r), sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)))
		lp = p
		stops = append(stops, p.Shutdown)
	}
	s, err := New(tp, mp, lp)
	return s, stop, err
}

// protocol selects an exporter, leaving its configuration to that exporter.
// Frontseat supports OTLP and none; unsupported choices fail explicitly.
func protocol(signal string) (string, error) {
	exporter := os.Getenv("OTEL_" + signal + "_EXPORTER")
	if exporter == "none" {
		return "", nil
	}
	if exporter != "" && exporter != "otlp" {
		return "", fmt.Errorf("OTEL_%s_EXPORTER: supported values are otlp and none", signal)
	}
	if exporter == "" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") == "" {
		return "", nil
	}
	p := os.Getenv("OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL")
	if p == "" {
		p = os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	}
	if p == "" {
		p = "http/protobuf"
	}
	if p != "grpc" && p != "http/protobuf" {
		return "", fmt.Errorf("OTEL_EXPORTER_OTLP_%s_PROTOCOL: supported values are grpc and http/protobuf", signal)
	}
	return p, nil
}
