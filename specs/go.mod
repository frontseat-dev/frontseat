module github.com/frontseat-dev/frontseat/specs

go 1.27.3

require (
	connectrpc.com/connect/v2 v2.0.2
	github.com/ai-openshell/sdk-go v0.1.10
	github.com/bazelbuild/remote-apis v0.0.0-20260331222004-becdd8f9ff81
	github.com/frontseat-dev/frontseat/build/daemon v0.0.0
	github.com/frontseat-dev/frontseat/common/told v0.0.0
	github.com/frontseat-dev/frontseat/common/vcs v0.0.0
	github.com/frontseat-dev/frontseat/core v0.0.0
	github.com/frontseat-dev/frontseat/stack v0.0.0
	github.com/frontseat-dev/frontseat/sandbox v0.0.0
	github.com/frontseat-dev/frontseat/sdk/go v0.0.0
	github.com/frontseat-dev/frontseat/testsupport v0.0.0
	github.com/frontseat-dev/frontseat/workspace/box v0.0.0
	github.com/frontseat-dev/frontseat/workspace/server v0.0.0
	github.com/go-git/go-git/v5 v5.19.3
	github.com/google/renameio/v2 v2.0.2
	github.com/onsi/gomega v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/proto/otlp v1.11.1
	go.yaml.in/yaml/v4 v4.0.0
	golang.org/x/crypto v0.58.0
	google.golang.org/genproto/googleapis/api v0.0.0-20261104224025-d1e00e9f33ab
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261019180522-ce908d29299b
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	cloud.google.com/go/longrunning v1.0.0 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/bazelbuild/remote-apis-sdks v0.0.0-20261104145708-67e564a2db07 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.4 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chainguard-dev/git-urls v1.0.2 // indirect
	github.com/charmbracelet/x/conpty v0.2.0 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/frontseat-dev/frontseat/build/engine v0.0.0 // indirect
	github.com/frontseat-dev/frontseat/common/confine v0.0.0 // indirect
	github.com/frontseat-dev/frontseat/common/git v0.0.0 // indirect
	github.com/frontseat-dev/frontseat/common/record v0.0.0 // indirect
	github.com/frontseat-dev/frontseat/common/runner v0.0.0 // indirect
	github.com/frontseat-dev/ssh v0.4.4-0.20261107091341-205332a4a986 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.9.2 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang/glog v1.2.5 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.31.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/pires/go-proxyproto v0.12.0 // indirect
	github.com/pkg/xattr v0.4.12 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.72.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.23.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.23.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/log v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/oauth2 v0.38.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/bytestream v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
)
