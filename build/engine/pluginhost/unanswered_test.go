package pluginhost_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// Where a module of the tests below keeps what it is written with, in
// its one page of memory.
const (
	vectorAt  = 0    // the one vector fd_write is given, and after it what it wrote
	printedAt = 64   // what the module prints to standard error
	askedAt   = 512  // the ReadRequest the module asks the host
	givenAt   = 1024 // what frontseat_alloc returns, always
	replyAt   = 2048 // the module's reply to Describe
)

// uleb and sleb are a number as wasm writes one, unsigned and signed.
func uleb(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		if v >>= 7; v == 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

func sleb(v int64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v == 0 && b&0x40 == 0 || v == -1 && b&0x40 != 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

// counted is bytes with their length before them, as wasm writes a
// name, a section and a function's body.
func counted(b []byte) []byte { return append(uleb(uint64(len(b))), b...) }

// The instructions the modules below are written in.
func constant(v int32) []byte { return append([]byte{0x41}, sleb(int64(v))...) }
func calling(function byte) []byte {
	return []byte{0x10, function}
}

var (
	drop        = []byte{0x1a}
	unreachable = []byte{0x00}
	forever     = []byte{0x03, 0x40, 0x0c, 0x00, 0x0b} // loop, branch to its start, end
)

// The functions a module below imports, by their index.
const (
	fdWrite  = 0
	hostRead = 1
)

// printing is the instructions that write what is at printedAt to
// standard error.
func printing() []byte {
	var out []byte
	for _, argument := range []int32{2, vectorAt, 1, vectorAt + 8} {
		out = append(out, constant(argument)...)
	}
	return append(append(out, calling(fdWrite)...), drop...)
}

// readsThroughTheHost is the instructions that ask the host for a file.
func readsThroughTheHost(t *testing.T) (instructions, request []byte) {
	t.Helper()
	request, err := proto.Marshal(&pluginv1.ReadRequest{Path: "go.mod"})
	if err != nil {
		t.Fatal(err)
	}
	instructions = append(append(constant(askedAt), constant(int32(len(request)))...), calling(hostRead)...)
	return append(instructions, drop...), request
}

// moduleThat is a plugin written by hand: it says it is the plugin
// "hand" when it is asked Describe, and asked anything else it does
// otherwise, the instructions of a function that returns the packed
// address and size of its answer. Its memory holds printed at printedAt
// and asked at askedAt.
func moduleThat(t *testing.T, otherwise []byte, printed string, asked []byte) []byte {
	t.Helper()
	return moduleGiving(t, constant(givenAt), otherwise, printed, asked)
}

// moduleGiving is moduleThat whose frontseat_alloc runs the instructions
// given, which leave the address it gives.
func moduleGiving(t *testing.T, alloc, otherwise []byte, printed string, asked []byte) []byte {
	t.Helper()
	return moduleStarting(t, nil, alloc, otherwise, printed, asked)
}

// moduleStarting is moduleGiving that exports _initialize, the
// instructions given, where they are not nil, as a WASI reactor does.
func moduleStarting(t *testing.T, initialize, alloc, otherwise []byte, printed string, asked []byte) []byte {
	t.Helper()
	description, err := proto.Marshal(&pluginv1.DescribeResponse{Name: "hand"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := proto.Marshal(&pluginv1.Reply{Result: &pluginv1.Reply_Response{Response: description}})
	if err != nil {
		t.Fatal(err)
	}
	section := func(id byte, entries ...[]byte) []byte {
		body := uleb(uint64(len(entries)))
		for _, entry := range entries {
			body = append(body, entry...)
		}
		return append([]byte{id}, counted(body)...)
	}
	const i32, i64 = 0x7f, 0x7e
	function := func(params []byte, results ...byte) []byte {
		return append(append(append([]byte{0x60}, counted(params)...), byte(len(results))), results...)
	}
	imported := func(module, name string, of byte) []byte {
		return append(append(counted([]byte(module)), counted([]byte(name))...), 0x00, of)
	}
	exported := func(name string, kind, index byte) []byte {
		return append(counted([]byte(name)), kind, index)
	}
	body := func(instructions ...[]byte) []byte {
		code := []byte{0} // no locals
		for _, each := range instructions {
			code = append(code, each...)
		}
		return counted(append(code, 0x0b))
	}
	data := func(at int32, held []byte) []byte {
		return append(append(append([]byte{0x00}, constant(at)...), 0x0b), counted(held)...)
	}
	vector := append(word(printedAt), word(uint32(len(printed)))...)
	// The third byte of a Call is the first letter of its method.
	describe := append(append([]byte{0x20, 0x00, 0x2d, 0x00, 0x02}, constant('D')...), 0x46)
	answer := append([]byte{0x42}, sleb(int64(replyAt)<<32|int64(len(reply)))...)
	call := body(describe, []byte{0x04, i64}, answer, []byte{0x05}, otherwise, []byte{0x0b})

	functions := [][]byte{{0}, {1}, {2}, {3}}
	exports := [][]byte{exported("memory", 0x02, 0), exported("frontseat_abi", 0x00, 2), exported("frontseat_alloc", 0x00, 3), exported("frontseat_free", 0x00, 4), exported("frontseat_call", 0x00, 5)}
	bodies := [][]byte{body(constant(pluginhost.MaxABI)), body(alloc), body(), call}
	if initialize != nil {
		functions = append(functions, []byte{5})
		exports = append(exports, exported("_initialize", 0x00, 6))
		bodies = append(bodies, body(initialize))
	}
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	for _, each := range [][]byte{
		section(1,
			function(nil, i32),                        // frontseat_abi
			function([]byte{i32}, i32),                // frontseat_alloc
			function([]byte{i32, i32}),                // frontseat_free
			function([]byte{i32, i32}, i64),           // frontseat_call, and the host's read
			function([]byte{i32, i32, i32, i32}, i32), // fd_write
			function(nil)),                            // _initialize
		section(2, imported("wasi_snapshot_preview1", "fd_write", 4), imported("frontseat", "read", 3)),
		section(3, functions...),
		section(5, []byte{0x00, 0x01}),
		section(7, exports...),
		section(10, bodies...),
		section(11, data(vectorAt, vector), data(printedAt, []byte(printed)), data(askedAt, asked), data(replyAt, reply)),
	} {
		module = append(module, each...)
	}
	return module
}

// word is a number as a module's memory holds one: four bytes, the
// least first.
func word(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }

// answering is the instructions of a module that answers every call but
// Describe with one failure, held in its memory where it reads a request
// from, and the failure as that memory holds it.
func answering(t *testing.T, code codes.Code, reason string) (instructions, held []byte) {
	t.Helper()
	detail := func(m proto.Message) *anypb.Any {
		packed, err := anypb.New(m)
		if err != nil {
			t.Fatal(err)
		}
		return packed
	}
	rule := "hand/" + strings.ToLower(strings.ReplaceAll(reason, "_", "-"))
	held, err := proto.Marshal(&pluginv1.Reply{Result: &pluginv1.Reply_Error{Error: &statuspb.Status{
		Code: int32(code), Message: "it is not built yet",
		Details: []*anypb.Any{
			detail(&errdetails.ErrorInfo{Reason: reason, Domain: "hand.plugins.frontseat.dev"}),
			detail(&typev1.Diagnostic{RuleId: rule, Message: "it is not built yet"}),
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte{0x42}, sleb(int64(askedAt)<<32|int64(len(held)))...), held
}

// report asks a module Report.
func report(t *testing.T, ctx context.Context, module []byte, source pluginhost.Source) error {
	t.Helper()
	return asking(t, ctx, "Report", module, source)
}

// asking asks a module a method, of an empty request.
func asking(t *testing.T, ctx context.Context, method string, module []byte, source pluginhost.Source) error {
	t.Helper()
	plugin, err := loadModule(t, module)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plugin.Call(ctx, source, method, &pluginv1.ReportRequest{}, &pluginv1.ReportResponse{})
	return err
}

// A plugin that panics says why on standard error and traps. The host
// says it trapped, which plugin and in which call, with what it printed.
func TestAModuleThatTrapsIsSaidToWithWhatItPrinted(t *testing.T) {
	const panicked = "thread '<unnamed>' panicked at src/lib.rs:7:9:\nthe lock is gone\n"
	err := report(t, t.Context(), moduleThat(t, append(printing(), unreachable...), panicked, nil), files{})
	if err == nil {
		t.Fatal("a call whose module trapped was answered")
	}
	for _, want := range []string{"plugin hand", "Report", "the module trapped", "src/lib.rs:7:9", "the lock is gone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the trap is said as %q, without %q", err, want)
		}
	}

	// One that prints more than is kept is said by the end of it, and one
	// that prints nothing is said to have printed nothing.
	long := strings.Repeat("x", 300) + "the end of it\n"
	module := moduleThat(t, append(forty(printing()), unreachable...), long, nil)
	err = report(t, t.Context(), module, files{})
	if err == nil || !strings.Contains(err.Error(), "the end of what it printed") || !strings.HasSuffix(err.Error(), "the end of it") || len(err.Error()) > 9<<10 {
		t.Errorf("a module that printed %d bytes is said in %d: %.200q", 40*len(long), len(err.Error()), err)
	}
	err = report(t, t.Context(), moduleThat(t, unreachable, "", nil), files{})
	if err == nil || !strings.Contains(err.Error(), "it printed nothing") {
		t.Errorf("a module that trapped in silence is said as %v", err)
	}

	// What it printed is said without what a terminal acts on: an
	// escape, the one-rune escape above ASCII, a turn of direction.
	err = report(t, t.Context(), moduleThat(t, append(printing(), unreachable...), "a\x1b[31mb\u009bc\u202ed\n", nil), files{})
	if err == nil || !strings.Contains(err.Error(), "a[31mbcd") {
		t.Errorf("what a module printed is said as %q", err)
	}
}

// forty is instructions forty times over.
func forty(instructions []byte) []byte {
	var out []byte
	for range 40 {
		out = append(out, instructions...)
	}
	return out
}

// A call its caller gave up is ended by the host, and is no bug of the
// module's: it is said to be given up, and is the caller's cancellation.
func TestACallItsCallerGaveUpIsNotSaidToBeATrap(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	err := report(t, ctx, moduleThat(t, append(forever, unreachable...), "", nil), files{})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "its caller gave the call up") || !strings.Contains(err.Error(), "plugin hand") {
		t.Errorf("a call given up is said as %v", err)
	}
	// One given up before it began is said so too, though the module had
	// not yet started.
	before, cancel := context.WithCancel(t.Context())
	cancel()
	err = report(t, before, moduleThat(t, unreachable, "", nil), files{})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "its caller gave the call up") {
		t.Errorf("a call given up before it began is said as %v", err)
	}
}

// A module that traps where it gives memory to be written into, before
// it is called, is said to have trapped, with what it printed, as one
// that traps once called is.
func TestAModuleThatTrapsAsItGivesMemoryIsSaidToWithWhatItPrinted(t *testing.T) {
	const failed = "memory allocation of 1024 bytes failed\n"
	_, err := loadModule(t, moduleGiving(t, append(printing(), unreachable...), unreachable, failed, nil))
	if err == nil || !strings.Contains(err.Error(), "the module trapped") || !strings.Contains(err.Error(), "memory allocation of 1024 bytes failed") {
		t.Errorf("a module that trapped as it gave memory is said as %v", err)
	}
}

// unavailable is a repository none of which can be read.
type unavailable struct{ files }

var errUnavailable = errors.New("the repository is not there")

func (unavailable) Read(context.Context, string) ([]byte, bool, error) {
	return nil, false, errUnavailable
}

// A module is not told of a read the host could not serve, and may trap
// on what it was given in its place. What went wrong is the read.
func TestAReadTheHostCouldNotServeIsNotSaidToBeATrap(t *testing.T) {
	reads, request := readsThroughTheHost(t)
	module := moduleThat(t, append(reads, unreachable...), "", request)
	err := report(t, t.Context(), module, unavailable{})
	if !errors.Is(err, errUnavailable) || strings.Contains(err.Error(), "trapped") || !strings.Contains(err.Error(), "plugin hand") {
		t.Errorf("a read that failed is said as %v", err)
	}
	// The same module traps where the read is served.
	if err := report(t, t.Context(), module, files{}); err == nil || !strings.Contains(err.Error(), "the module trapped") {
		t.Errorf("the control: %v", err)
	}
}

// UNIMPLEMENTED says a plugin does not serve the method only for the
// method's own reason. For any other reason it is of something the
// method was asked about, which the plugin does not build yet: a failure
// of the call, and no word that the plugin has nothing to answer.
func TestUnimplementedIsAMethodUnservedOnlyForTheMethodsOwnReason(t *testing.T) {
	for _, each := range []struct {
		method, reason string
		unserved       bool
	}{
		{"Report", "REPORT_NOT_SERVED", true},
		{"Report", "METHOD_NOT_SERVED", true},
		{"Report", "SARIF_NOT_SERVED", false},
		{"Report", "ANALYZE_NOT_SERVED", false},
		{"Reconcile", "RECONCILE_NOT_SERVED", true},
		{"Service", "SERVICE_NOT_SERVED", true},
		// A release strategy's methods are unserved by the kind of
		// strategy, as the SDK answers them.
		{"Branch", "BRANCHING_STRATEGY_NOT_SERVED", true},
		{"Branch", "BRANCH_NOT_SERVED", false},
		{"Version", "VERSIONING_STRATEGY_NOT_SERVED", true},
		{"Version", "BRANCHING_STRATEGY_NOT_SERVED", false},
		// Every plugin serves Analyze and Arrange.
		{"Analyze", "ANALYZE_NOT_SERVED", false},
		{"Arrange", "ARRANGE_NOT_SERVED", false},
	} {
		instructions, held := answering(t, codes.Unimplemented, each.reason)
		err := asking(t, t.Context(), each.method, moduleThat(t, instructions, "", held), files{})
		var failure *pluginhost.Error
		if !errors.As(err, &failure) || failure.Reason != each.reason {
			t.Fatalf("%s to %s: the plugin's failure did not arrive: %v", each.reason, each.method, err)
		}
		if got := errors.Is(err, pluginhost.ErrUnsupported); got != each.unserved {
			t.Errorf("%s answered to %s: the method is unserved = %v, want %v", each.reason, each.method, got, each.unserved)
		}
	}
	// Another code is never a method unserved, whatever its reason.
	instructions, held := answering(t, codes.FailedPrecondition, "REPORT_NOT_SERVED")
	if err := report(t, t.Context(), moduleThat(t, instructions, "", held), files{}); errors.Is(err, pluginhost.ErrUnsupported) {
		t.Errorf("a failed precondition is read as a method unserved: %v", err)
	}
}

// A module that exports _initialize, as a WASI reactor does, is started
// by it before it is asked anything: one whose _initialize traps is not
// loaded, and the same module whose _initialize returns is, as is one
// that exports none.
func TestAReactorIsStartedByItsInitialize(t *testing.T) {
	if _, err := loadModule(t, moduleStarting(t, unreachable, constant(givenAt), unreachable, "", nil)); err == nil || !strings.Contains(err.Error(), "instantiating") {
		t.Errorf("a module whose _initialize traps was loaded: %v", err)
	}
	for name, module := range map[string][]byte{
		"returns": moduleStarting(t, []byte{}, constant(givenAt), unreachable, "", nil),
		"is none": moduleThat(t, unreachable, "", nil),
	} {
		plugin, err := loadModule(t, module)
		if err != nil || plugin.Description.GetName() != "hand" {
			t.Errorf("a module whose _initialize %s was not loaded: %v", name, err)
		}
	}
}
