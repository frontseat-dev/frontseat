package runner

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// runRequest is buildbarn.runner.RunRequest (bb-remote-execution b79ca71,
// pkg/proto/runner/runner.proto), built here so the real protobuf parser
// can be asked what a request says.
var runRequest = func() protoreflect.MessageDescriptor {
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	rep := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	msg := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	field := func(name string, n int32, label *descriptorpb.FieldDescriptorProto_Label) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(n), Label: label, Type: str, JsonName: proto.String(name)}
	}
	env := &descriptorpb.FieldDescriptorProto{Name: proto.String("environment_variables"), Number: proto.Int32(2), Label: rep, Type: msg, TypeName: proto.String(".buildbarn.runner.RunRequest.EnvironmentVariablesEntry"), JsonName: proto.String("environmentVariables")}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String("runner.proto"), Package: proto.String("buildbarn.runner"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("RunRequest"),
			Field: []*descriptorpb.FieldDescriptorProto{
				field("arguments", 1, rep), env, field("working_directory", 3, opt),
				field("stdout_path", 4, opt), field("stderr_path", 5, opt), field("input_root_directory", 6, opt),
				field("temporary_directory", 7, opt), field("server_logs_directory", 8, opt),
			},
			NestedType: []*descriptorpb.DescriptorProto{{
				Name:    proto.String("EnvironmentVariablesEntry"),
				Field:   []*descriptorpb.FieldDescriptorProto{field("key", 1, opt), field("value", 2, opt)},
				Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
			}},
		}},
	}, nil)
	if err != nil {
		panic(err)
	}
	return fd.Messages().ByName("RunRequest")
}()

// request is a RunRequest as the worker writes one
// (local_build_executor.go:288), with what is given in place of the paths.
func request(paths map[protowire.Number]string) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, "./svcprobe")
	b = protowire.AppendTag(b, 3, protowire.BytesType)
	b = protowire.AppendString(b, "")
	for n := protowire.Number(4); n <= 8; n++ {
		if p, ok := paths[n]; ok {
			b = protowire.AppendTag(b, n, protowire.BytesType)
			b = protowire.AppendString(b, p)
		}
	}
	return b
}

func good() map[protowire.Number]string {
	return map[protowire.Number]string{4: "165/stdout", 5: "165/stderr", 6: "165/root", 7: "165/tmp", 8: "165/server_logs"}
}

func with(n protowire.Number, p string) map[protowire.Number]string {
	m := good()
	m[n] = p
	return m
}

func without(n protowire.Number) map[protowire.Number]string {
	m := good()
	delete(m, n)
	return m
}

func TestARequestIsHeldToOneDirectoryOfTheBuildDirectory(t *testing.T) {
	// The control: what the worker sends is allowed, and names its directory.
	if got, err := pathsOf(request(good())); err != nil || got.directory != "165" || got.root != "165/root" {
		t.Fatalf("the worker's own request: %+v, %v; want 165 and its root allowed", got, err)
	}
	refused := map[string][]byte{
		"empty":                         {},
		"a tag cut short":               {0x80},
		"a length past the end":         append(protowire.AppendTag(nil, 4, protowire.BytesType), 0x7f, 'a'),
		"an unknown wire type":          {0x27},
		"stdout naming no directory":    request(with(4, "stdout")),
		"stdout absolute":               request(with(4, "/165/stdout")),
		"stdout in .":                   request(with(4, "./stdout")),
		"stdout in ..":                  request(with(4, "../stdout")),
		"stdout empty":                  request(with(4, "")),
		"stderr in another directory":   request(with(5, "166/stderr")),
		"root climbing out":             request(with(6, "165/../166/root")),
		"tmp absolute":                  request(with(7, "/tmp")),
		"logs a prefix, not a child":    request(with(8, "1650/server_logs")),
		"the input root the build root": request(with(6, "")),
		"a backslash in the directory":  request(map[protowire.Number]string{4: `a\b/stdout`, 5: `a\b/stderr`, 6: `a\b/root`, 7: `a\b/tmp`, 8: `a\b/server_logs`}),
		"no stdout":                     request(without(4)),
		"no stderr":                     request(without(5)),
		"no input root":                 request(without(6)),
		"no temporary directory":        request(without(7)),
		"no server logs directory":      request(without(8)),
		// The parser keeps the last of a field given twice, as this does:
		// a first that is good does not let a second through.
		"stdout twice, the last outside": append(request(good()), append(protowire.AppendTag(nil, 4, protowire.BytesType), protowire.AppendString(nil, "166/stdout")...)...),
		"stdout as a number":             append(protowire.AppendTag(nil, 4, protowire.VarintType), 1),
		"a root of no plain form":        request(with(6, "165/./root")),
	}
	for name, req := range refused {
		if got, err := pathsOf(req); err == nil {
			t.Errorf("%s: allowed, as %+v; want refused", name, got)
		}
	}
}

// FuzzARequestIsHeldToOneDirectoryOfTheBuildDirectory holds the check to the real parser: whatever it
// allows, protobuf reads as a RunRequest, and every path that request
// gives the runner is within the one directory the check named.
func FuzzARequestIsHeldToOneDirectoryOfTheBuildDirectory(f *testing.F) {
	f.Add(request(good()))
	f.Add(request(with(6, "165/../166/root")))
	f.Add(request(without(7)))
	f.Add(append(request(good()), request(with(4, "166/stdout"))...))
	f.Fuzz(func(t *testing.T, req []byte) {
		got, err := pathsOf(req)
		if err != nil {
			return
		}
		dir := got.directory
		m := dynamicpb.NewMessage(runRequest)
		if err := proto.Unmarshal(req, m); err != nil {
			// Allowed is not run: the test sandbox's runner
			// (Confined), which is handed the request as it came,
			// refuses what protobuf cannot read.
			return
		}
		fields := runRequest.Fields()
		for n := protoreflect.FieldNumber(4); n <= 8; n++ {
			p := m.Get(fields.ByNumber(n)).String()
			if !strings.HasPrefix(p, dir+"/") || strings.Contains(p, "..") {
				t.Fatalf("allowed %q for %q, but field %d is %q", req, dir, n, p)
			}
		}
	})
}

// answering is the socket of a service that answers every request with
// body, in a directory of its own whose path is short enough for a
// socket's.
func answering(t *testing.T, body string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ready")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Close() })
	return socket
}

// A service is checked ready through the socket of its route, in a
// directory the service writes, and never through a link it left there
// to a socket of anyone else's: both checks refuse a link to a socket
// that answers, and the control is that socket, which each finds ready.
func TestAServiceIsCheckedReadyThroughItsOwnSocketAndNoLink(t *testing.T) {
	socket := answering(t, "ok")
	link := filepath.Join(filepath.Dir(socket), "link")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	if err := reached(t.Context(), link); err == nil {
		t.Error("a port was reached through a link")
	}
	if err := answered(t.Context(), link, "/", regexp.MustCompile("ok")); err == nil {
		t.Error("a request was answered through a link")
	}
	if err := reached(t.Context(), socket); err != nil {
		t.Errorf("the control, the socket itself, was not reached: %v", err)
	}
	if err := answered(t.Context(), socket, "/", regexp.MustCompile("ok")); err != nil {
		t.Errorf("the control, the socket itself, did not answer: %v", err)
	}
}

// A check that fails says the beginning of what the service answered,
// and no more: a service answers what it likes, at any length.
func TestAFailedCheckRepeatsOnlyTheBeginningOfWhatAServiceAnswered(t *testing.T) {
	socket := answering(t, "starting"+strings.Repeat("x", 64<<10))

	err := answered(t.Context(), socket, "/", regexp.MustCompile("^ready$"))

	if err == nil || !strings.Contains(err.Error(), "starting") {
		t.Fatalf("a check the answer does not match: %v", err)
	}
	if len(err.Error()) > 1024 {
		t.Errorf("the failure repeats %d bytes of what the service answered", len(err.Error()))
	}
}
