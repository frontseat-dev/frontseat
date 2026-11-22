package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/plain"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"io"
	"slices"
	"strings"
	"sync"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"google.golang.org/protobuf/proto"
)

// The plugin ABIs this host loads.
const (
	MinABI = 25
	MaxABI = 25
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
	// ABI is the plugin ABI the module speaks, as its export
	// frontseat_abi says: the one place it is said.
	ABI uint32

	rt       wazero.Runtime
	compiled wazero.CompiledModule
}

// Named is the plugin of a name among those loaded, the last loaded of
// that name: one loaded after another of its name is the one asked.
// Nil where there is none.
func Named(plugins []*Plugin, name string) *Plugin {
	for _, loaded := range slices.Backward(plugins) {
		if loaded.Description.GetName() == name {
			return loaded
		}
	}
	return nil
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
	instance, err := p.instantiate(ctx, io.Discard)
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
		return nil, fmt.Errorf("pluginhost: %s speaks plugin ABI %d, and this host loads %d to %d: build it against an SDK of an ABI this host loads", digest[:12], v, MinABI, MaxABI)
	}
	p.ABI = uint32(got[0])
	description := &pluginv1.DescribeResponse{}
	if _, err := p.Call(ctx, nil, "Describe", &pluginv1.DescribeRequest{}, description); err != nil {
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
// nothing in it of any call before. What the instance prints to
// standard error is written to stderr.
func (p *Plugin) instantiate(ctx context.Context, stderr io.Writer) (api.Module, error) {
	config := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithStdout(io.Discard).
		WithStderr(stderr)
	return p.rt.InstantiateModule(ctx, p.compiled, config)
}

// printedAtMost is how much is kept of what a module printed to standard
// error: its end, where a module that trapped says why.
const printedAtMost = 8 << 10

// printed keeps the end of what a module printed to standard error.
type printed struct {
	end []byte
	cut bool
}

// Write implements io.Writer. It keeps the last printedAtMost bytes.
func (p *printed) Write(b []byte) (int, error) {
	written := len(b)
	if len(b) > printedAtMost {
		b, p.cut = b[len(b)-printedAtMost:], true
	}
	p.end = append(p.end, b...)
	if over := len(p.end) - printedAtMost; over > 0 {
		p.end, p.cut = append(p.end[:0], p.end[over:]...), true
	}
	return written, nil
}

// said is what was kept, as an error says it: text of one piece and of
// as many lines as the module printed, with nothing in it that a
// terminal acts on.
func (p *printed) said() string {
	text := plain.Shown(string(p.end))
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return "it printed nothing to standard error"
	case p.cut:
		return "the end of what it printed to standard error: " + text
	}
	return "it printed to standard error: " + text
}

// named is what the plugin is named by in what is said of a call: its
// name, or the start of its digest while it has not said its name.
func (p *Plugin) named() string {
	if name := p.Description.GetName(); name != "" {
		return name
	}
	return p.Digest[:12]
}

// Close releases the module.
func (p *Plugin) Close(ctx context.Context) error { return p.compiled.Close(ctx) }

// ErrUnsupported is what a plugin's answer is where the plugin does not
// serve the method: UNIMPLEMENTED, for the method's own reason (Report's
// is REPORT_NOT_SERVED, Branch's BRANCHING_STRATEGY_NOT_SERVED) or, of a
// method its SDK does not know, METHOD_NOT_SERVED. UNIMPLEMENTED
// for another reason is of what the method was asked about, which the
// plugin does not build yet, and is a failure of the call.
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
//
// A call that does not end in an answer says which of three things
// happened: its caller gave it up, the host could not serve what the
// plugin read, or the module trapped, which is a bug in the module and
// is said with what the module printed to standard error.
func (p *Plugin) Call(ctx context.Context, src Source, method string, request, response proto.Message) (reads []Read, err error) {
	ctx, span := telemetry.Start(ctx, "plugin", attribute.String("plugin", p.Description.GetName()), attribute.String("method", method))
	defer func() {
		span.SetAttributes(attribute.Int("reads", len(reads)))
		telemetry.End(span, err)
	}()
	unanswered := func(format string, args ...any) error {
		return fmt.Errorf("pluginhost: plugin %s: %s: %w", p.named(), method, fmt.Errorf(format, args...))
	}
	in, err := proto.Marshal(request)
	if err != nil {
		return nil, unanswered("%w", err)
	}
	envelope, err := proto.Marshal(&pluginv1.Call{Method: method, Request: in})
	if err != nil {
		return nil, unanswered("%w", err)
	}
	state := &callState{source: src}
	ctx = context.WithValue(ctx, callKey{}, state)

	stderr := &printed{}
	// A module is ended where its caller has gone, and is told nothing
	// of a read the host could not serve: neither is the module's bug,
	// whatever the module did after. This holds of every step a module
	// runs in: where it starts, where it gives memory and where it
	// answers.
	stopped := func(err error) error {
		switch {
		case ctx.Err() != nil:
			return unanswered("its caller gave the call up: %w", context.Cause(ctx))
		case state.err != nil:
			return unanswered("the host could not serve what the plugin read: %w", state.err)
		}
		return unanswered("the module trapped, which is a bug in the plugin: %w; %s", err, stderr.said())
	}
	instance, err := p.instantiate(ctx, stderr)
	if err != nil {
		return nil, stopped(err)
	}
	defer instance.Close(context.WithoutCancel(ctx))
	alloc, free, call := instance.ExportedFunction("frontseat_alloc"), instance.ExportedFunction("frontseat_free"), instance.ExportedFunction("frontseat_call")

	ptr, err := writeInto(ctx, instance, alloc, envelope)
	switch {
	case errors.Is(err, errNoMemory):
		return nil, unanswered("%w", err)
	case err != nil:
		return nil, stopped(err)
	}
	packed, err := call.Call(ctx, uint64(ptr), uint64(len(envelope)))
	if err != nil || ctx.Err() != nil || state.err != nil {
		return nil, stopped(err)
	}
	_, _ = free.Call(ctx, uint64(ptr), uint64(len(envelope)))
	rptr, rsize := uint32(packed[0]>>32), uint32(packed[0])
	raw, ok := instance.Memory().Read(rptr, rsize)
	if !ok {
		return nil, unanswered("the module answered outside its memory")
	}
	reply := &pluginv1.Reply{}
	if err := proto.Unmarshal(raw, reply); err != nil {
		return nil, unanswered("%w", err)
	}
	switch r := reply.GetResult().(type) {
	case *pluginv1.Reply_Response:
		if err := proto.Unmarshal(r.Response, response); err != nil {
			return nil, unanswered("%w", err)
		}
		return state.reads, nil
	case *pluginv1.Reply_Error:
		failure, err := readError(p.Description.GetName(), r.Error)
		if err != nil {
			return nil, unanswered("%w", err)
		}
		failure.method = method
		return nil, failure
	default:
		return nil, unanswered("the module answered nothing")
	}
}

// errNoMemory is a module that ran and gave nothing to write a call
// into.
var errNoMemory = errors.New("the module gave no memory to write to")

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
		return 0, errNoMemory
	}
	return ptr, nil
}
