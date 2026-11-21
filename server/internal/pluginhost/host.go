package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"io"
	"slices"
	"sync"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"google.golang.org/protobuf/proto"
)

// The plugin ABIs this host loads.
const (
	MinABI = 23
	MaxABI = 23
)

// memoryLimitPages bounds a module's memory, in 64 KiB pages: 256 MiB.
const memoryLimitPages = 4096

// Host loads plugins.
type Host struct {
	rt wazero.Runtime
}

// Wazero 1.12 initializes its process-wide version cache during runtime
// construction without synchronization (internal/version.GetWazeroVersion).
// This package owns every runtime; serialize construction, while each host's
// compilation and execution remain independent.
var runtimeCreation sync.Mutex

func newRuntime(ctx context.Context) wazero.Runtime {
	runtimeCreation.Lock()
	defer runtimeCreation.Unlock()
	return wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(memoryLimitPages).
		WithCloseOnContextDone(true))
}

// New returns a host. Close it when done.
func New(ctx context.Context) (*Host, error) {
	rt := newRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("pluginhost: %w", err)
	}
	if err := serveImports(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("pluginhost: %w", err)
	}
	return &Host{rt: rt}, nil
}

// Close releases the host and every plugin it loaded.
func (h *Host) Close(ctx context.Context) error { return h.rt.Close(ctx) }

// Plugin is a loaded module.
type Plugin struct {
	// Description is what the module said of itself when it was loaded.
	Description *pluginv1.DescribeResponse
	// Digest is the SHA-256 of the module's bytes.
	Digest string

	rt       wazero.Runtime
	compiled wazero.CompiledModule
}

// The ABI's exports, each with the shape the ABI document gives it.
var exports = []struct {
	name            string
	params, results []api.ValueType
}{
	{"frontseat_abi", nil, []api.ValueType{api.ValueTypeI32}},
	{"frontseat_alloc", []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}},
	{"frontseat_free", []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, nil},
	{"frontseat_call", []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI64}},
}

// conforms holds a compiled module to the ABI's exports: each is there,
// with the parameters and the results the ABI gives it, and the module
// has a memory. A module is somebody else's code, so this is checked
// before any of it runs: what is called afterwards returns what the
// host reads.
func conforms(compiled wazero.CompiledModule) error {
	functions := compiled.ExportedFunctions()
	for _, want := range exports {
		got, exported := functions[want.name]
		if !exported {
			return fmt.Errorf("it exports no %s", want.name)
		}
		if !slices.Equal(got.ParamTypes(), want.params) || !slices.Equal(got.ResultTypes(), want.results) {
			return fmt.Errorf("its %s is not the ABI's: it takes %d values and returns %d, and the ABI's takes %d and returns %d", want.name, len(got.ParamTypes()), len(got.ResultTypes()), len(want.params), len(want.results))
		}
	}
	if len(compiled.ExportedMemories()) == 0 {
		return errors.New("it exports no memory")
	}
	return nil
}

// Load compiles a module, holds it to the ABI and asks it what it is. A
// module that does not export the ABI, or speaks one the host does not
// load, is refused.
func (h *Host) Load(ctx context.Context, module []byte) (*Plugin, error) {
	sum := sha256.Sum256(module)
	digest := hex.EncodeToString(sum[:])
	compiled, err := h.rt.CompileModule(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: compiling %s: %w", digest[:12], err)
	}
	if err := conforms(compiled); err != nil {
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf("pluginhost: %s is not a plugin: %w", digest[:12], err)
	}
	p := &Plugin{Digest: digest, rt: h.rt, compiled: compiled}
	instance, err := p.instantiate(ctx)
	if err != nil {
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf("pluginhost: instantiating %s: %w", digest[:12], err)
	}
	got, err := instance.ExportedFunction("frontseat_abi").Call(ctx)
	_ = instance.Close(ctx)
	if err != nil {
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf("pluginhost: %s: %w", digest[:12], err)
	}
	if v := uint32(got[0]); v < MinABI || v > MaxABI {
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf("pluginhost: %s speaks plugin ABI %d, and this host loads %d to %d", digest[:12], v, MinABI, MaxABI)
	}
	description := &pluginv1.DescribeResponse{}
	if _, err := p.Call(ctx, nil, "Describe", &pluginv1.DescribeRequest{HostAbiMin: MinABI, HostAbiMax: MaxABI}, description); err != nil {
		_ = compiled.Close(ctx)
		return nil, err
	}
	if err := checkDescription(description); err != nil {
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf("pluginhost: %s: %w", digest[:12], err)
	}
	p.Description = description
	return p, nil
}

// instantiate returns an instance of the module: its own memory, with
// nothing in it of any call before.
func (p *Plugin) instantiate(ctx context.Context) (api.Module, error) {
	config := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithStdout(io.Discard).
		WithStderr(io.Discard)
	return p.rt.InstantiateModule(ctx, p.compiled, config)
}

// Close releases the module.
func (p *Plugin) Close(ctx context.Context) error { return p.compiled.Close(ctx) }

// ErrUnsupported is returned for a method the module does not serve.
var ErrUnsupported = errors.New("pluginhost: the plugin does not serve the method")

// Call asks the plugin one method of the contract's PluginService and
// decodes its answer into response. While it answers, the plugin reads
// through src, and Call returns everything it read: what the answer
// depends on.
//
// Each call is made on an instance of its own. Nothing a call leaves is
// there for the next, so an answer depends on the request and on what
// was read and on nothing else; calls do not wait for each other; and a
// call whose caller has gone ends alone, the plugin still answering.
func (p *Plugin) Call(ctx context.Context, src Source, method string, request, response proto.Message) (reads []Read, err error) {
	ctx, span := telemetry.Start(ctx, "plugin", attribute.String("plugin", p.Description.GetName()), attribute.String("method", method))
	defer func() {
		span.SetAttributes(attribute.Int("reads", len(reads)))
		telemetry.End(span, err)
	}()
	in, err := proto.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", method, err)
	}
	envelope, err := proto.Marshal(&pluginv1.Call{Method: method, Request: in})
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", method, err)
	}
	state := &callState{source: src}
	ctx = context.WithValue(ctx, callKey{}, state)

	instance, err := p.instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", method, err)
	}
	defer instance.Close(context.WithoutCancel(ctx))
	alloc, free, call := instance.ExportedFunction("frontseat_alloc"), instance.ExportedFunction("frontseat_free"), instance.ExportedFunction("frontseat_call")

	ptr, err := writeInto(ctx, instance, alloc, envelope)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", method, err)
	}
	packed, err := call.Call(ctx, uint64(ptr), uint64(len(envelope)))
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: the module trapped: %w", method, err)
	}
	_, _ = free.Call(ctx, uint64(ptr), uint64(len(envelope)))
	if state.err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", method, state.err)
	}
	rptr, rsize := uint32(packed[0]>>32), uint32(packed[0])
	raw, ok := instance.Memory().Read(rptr, rsize)
	if !ok {
		return nil, fmt.Errorf("pluginhost: %s: the module answered outside its memory", method)
	}
	reply := &pluginv1.Reply{}
	if err := proto.Unmarshal(raw, reply); err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", method, err)
	}
	switch r := reply.GetResult().(type) {
	case *pluginv1.Reply_Response:
		if err := proto.Unmarshal(r.Response, response); err != nil {
			return nil, fmt.Errorf("pluginhost: %s: %w", method, err)
		}
		return state.reads, nil
	case *pluginv1.Reply_Error:
		failure, err := readError(p.Description.GetName(), r.Error)
		if err != nil {
			return nil, err
		}
		return nil, failure
	default:
		return nil, fmt.Errorf("pluginhost: %s: the module answered nothing", method)
	}
}

func writeInto(ctx context.Context, m api.Module, alloc api.Function, b []byte) (uint32, error) {
	if len(b) == 0 {
		return 0, nil
	}
	got, err := alloc.Call(ctx, uint64(len(b)))
	if err != nil {
		return 0, err
	}
	ptr := uint32(got[0])
	if ptr == 0 || !m.Memory().Write(ptr, b) {
		return 0, errors.New("the module gave no memory to write to")
	}
	return ptr, nil
}
