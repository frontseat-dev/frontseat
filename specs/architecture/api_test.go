package architecture

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The API's conventions (AGENTS.md, "APIs"), held over the contracts as
// Buf reads them from api/: the protos themselves, not what was
// generated from them. Each rule is a function of the descriptors, and
// is tried on a fixture it allows and one it refuses.

// served are the packages of the API served over a network: their calls
// are held to every rule, and their types to being reached.
var served = []protoreflect.FullName{
	"frontseat.build.v1alpha1",
	"frontseat.workspace.v1alpha1",
	"frontseat.credential.v1alpha1",
	"frontseat.sandbox.v1alpha1",
	// The daemon's internal API, which the control plane alone asks.
	"frontseat.source.v1alpha1",
}

// shared is the package of the types more than one contract uses: each
// is reached from a call of one of them.
const shared protoreflect.FullName = "frontseat.type.v1alpha1"

// plugin is the plugin contract: its calls are no network API's, and
// follow the plugin ABI's names, but what they reach of the shared types
// is reached.
const plugin protoreflect.FullName = "frontseat.plugin.v1alpha1"

// verbs are the calls a service may make (AGENTS.md: Describe, List,
// Create, Update, Delete, Wait, Stream…, Cancel, and a few named verbs).
var verbs = []string{"Describe", "List", "Create", "Update", "Delete", "Wait", "Stream", "Cancel", "Plan", "Exec", "Pause", "Resume", "Set"}

// apiRules are the rules of the API's shape, by name, each a function of
// a contract that says what breaks it.
var apiRules = map[string]func(contract) []string{
	"allowed verbs":                        allowedVerbs,
	"a request and a response of its own":  ownMessages,
	"only Stream calls stream":             streams,
	"a response wraps its resource":        wrapsItsResource,
	"identifying fields are required":      identified,
	"a List pages":                         pages,
	"no reserved":                          nothingReserved,
	"every message reached by a call":      reached,
	"one failure shape":                    failures,
	"times and lengths of time":            times,
	"a contract imports only shared types": importsShared,
	"a Create can be sent again":           retried,
}

// contract is what the rules read: the files, the packages whose calls
// and types are held, and the packages whose calls only reach types.
type contract struct {
	files   *protoregistry.Files
	served  []protoreflect.FullName
	shared  []protoreflect.FullName
	reaches []protoreflect.FullName
}

// built is what Buf builds of the protos under a directory: every file,
// with its imports.
func built(t *testing.T, dir string) *protoregistry.Files {
	t.Helper()
	buf, err := exec.LookPath("buf")
	if err != nil {
		t.Fatalf("the API's rules read the contracts as Buf builds them, and buf is not on the PATH (run through mise): %v", err)
	}
	// What Buf reads is read here too, so that go test, which keeps a
	// test's result until what the test read changes, knows the protos
	// were read: a changed contract is held to the rules again.
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".proto") || strings.HasSuffix(path, ".yaml")) {
			_, err = os.ReadFile(path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	build := exec.Command(buf, "build", "--output", "-#format=binpb")
	build.Dir, build.Stdout, build.Stderr = dir, &out, &errs
	if err := build.Run(); err != nil {
		t.Fatalf("buf build in %s: %v: %s", dir, err, errs.String())
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(out.Bytes(), set); err != nil {
		t.Fatal(err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestTheAPIKeepsItsConventions(t *testing.T) {
	c := contract{files: built(t, filepath.Join(repositoryRoot(t), "api")), served: served, shared: []protoreflect.FullName{shared}, reaches: []protoreflect.FullName{plugin}}
	services := 0
	c.each(func(protoreflect.ServiceDescriptor) { services++ })
	if services < 5 {
		t.Fatalf("found %d services of the API; the contracts were not read", services)
	}
	for name, rule := range apiRules {
		for _, broken := range rule(c) {
			t.Errorf("%s: %s", name, broken)
		}
	}
}

// each calls do with every service of the served packages.
func (c contract) each(do func(protoreflect.ServiceDescriptor)) {
	c.files.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		if slices.Contains(c.served, f.Package()) {
			for i := range f.Services().Len() {
				do(f.Services().Get(i))
			}
		}
		return true
	})
}

// methods are the calls of the served packages.
func (c contract) methods() []protoreflect.MethodDescriptor {
	var out []protoreflect.MethodDescriptor
	c.each(func(s protoreflect.ServiceDescriptor) {
		for i := range s.Methods().Len() {
			out = append(out, s.Methods().Get(i))
		}
	})
	return out
}

// verbOf is the verb a call's name begins with, and what follows it;
// empty where it begins with none of them.
func verbOf(m protoreflect.MethodDescriptor) (string, string) {
	name := string(m.Name())
	for _, verb := range verbs {
		if rest, ok := strings.CutPrefix(name, verb); ok && rest != "" && unicode.IsUpper(rune(rest[0])) {
			return verb, rest
		}
	}
	return "", name
}

func allowedVerbs(c contract) []string {
	var out []string
	for _, m := range c.methods() {
		if verb, _ := verbOf(m); verb == "" {
			out = append(out, fmt.Sprintf("%s begins with no verb of %v", m.FullName(), verbs))
		}
	}
	return out
}

func ownMessages(c contract) []string {
	var out []string
	for _, m := range c.methods() {
		if want := string(m.Name()) + "Request"; string(m.Input().Name()) != want || m.Input().Parent() != m.Parent().Parent() {
			out = append(out, fmt.Sprintf("%s takes %s, not a %s of its own", m.FullName(), m.Input().FullName(), want))
		}
		if want := string(m.Name()) + "Response"; string(m.Output().Name()) != want || m.Output().Parent() != m.Parent().Parent() {
			out = append(out, fmt.Sprintf("%s answers %s, not a %s of its own", m.FullName(), m.Output().FullName(), want))
		}
	}
	return out
}

// streams holds that a call streams where it is Stream<X>Events or
// Stream<X>Logs, from the server, and nowhere else: a Wait is unary.
func streams(c contract) []string {
	var out []string
	for _, m := range c.methods() {
		verb, rest := verbOf(m)
		named := verb == "Stream" && (strings.HasSuffix(rest, "Events") || strings.HasSuffix(rest, "Logs"))
		switch {
		case m.IsStreamingClient():
			out = append(out, fmt.Sprintf("%s streams from its client", m.FullName()))
		case named && !m.IsStreamingServer():
			out = append(out, fmt.Sprintf("%s is a stream by its name and is unary", m.FullName()))
		case !named && m.IsStreamingServer():
			out = append(out, fmt.Sprintf("%s streams, and only Stream<X>Events and Stream<X>Logs do: a Wait returns once, when it has ended", m.FullName()))
		}
	}
	return out
}

// resourceOf is the message of the package a call is of: what follows
// its verb, as a message's name, or the singular of it for a List; nil
// where there is none.
func resourceOf(m protoreflect.MethodDescriptor) protoreflect.MessageDescriptor {
	verb, rest := verbOf(m)
	file := m.ParentFile()
	find := func(name string) protoreflect.MessageDescriptor {
		return file.Messages().ByName(protoreflect.Name(name))
	}
	switch verb {
	case "List":
		for _, singular := range singulars(rest) {
			if found := find(singular); found != nil {
				return found
			}
		}
		return nil
	case "Stream":
		rest = strings.TrimSuffix(strings.TrimSuffix(rest, "Events"), "Logs")
	}
	return find(rest)
}

// singulars are what a plural may be the plural of.
func singulars(plural string) []string {
	var out []string
	if stem, ok := strings.CutSuffix(plural, "ies"); ok {
		out = append(out, stem+"y")
	}
	if stem, ok := strings.CutSuffix(plural, "es"); ok {
		out = append(out, stem)
	}
	if stem, ok := strings.CutSuffix(plural, "s"); ok {
		out = append(out, stem)
	}
	return out
}

// within is, for a List of what is within a resource (ListBuildActions),
// the resource it is within; nil for a List of resources.
func within(m protoreflect.MethodDescriptor) protoreflect.MessageDescriptor {
	verb, rest := verbOf(m)
	if verb != "List" || resourceOf(m) != nil {
		return nil
	}
	file := m.ParentFile()
	for i := range file.Messages().Len() {
		candidate := file.Messages().Get(i)
		if name := string(candidate.Name()); strings.HasPrefix(rest, name) && len(rest) > len(name) && unicode.IsUpper(rune(rest[len(name)])) {
			return candidate
		}
	}
	return nil
}

// snake is a message's name as a field names it: ReleasePlan, release_plan.
func snake(name protoreflect.Name) string {
	var out []rune
	for i, r := range string(name) {
		if unicode.IsUpper(r) {
			if i > 0 {
				out = append(out, '_')
			}
			r = unicode.ToLower(r)
		}
		out = append(out, r)
	}
	return string(out)
}

func wrapsItsResource(c contract) []string {
	var out []string
	for _, m := range c.methods() {
		verb, _ := verbOf(m)
		switch verb {
		case "Create", "Describe", "Update", "Cancel", "Wait", "Set", "Pause", "Resume":
			resource := resourceOf(m)
			if resource == nil {
				out = append(out, fmt.Sprintf("%s is of no resource of its package", m.FullName()))
				continue
			}
			field := m.Output().Fields().ByName(protoreflect.Name(snake(resource.Name())))
			if field == nil || field.Message() != resource || field.Cardinality() == protoreflect.Repeated {
				out = append(out, fmt.Sprintf("%s does not wrap its %s in a field named %s", m.Output().FullName(), resource.Name(), snake(resource.Name())))
			}
		case "Delete":
			if m.Output().Fields().Len() != 0 {
				out = append(out, fmt.Sprintf("%s says more than that it is gone", m.Output().FullName()))
			}
		}
	}
	return out
}

// required is whether a field is marked REQUIRED.
func required(f protoreflect.FieldDescriptor) bool {
	options, ok := f.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil {
		return false
	}
	behaviors, _ := proto.GetExtension(options, annotations.E_FieldBehavior).([]annotations.FieldBehavior)
	return slices.Contains(behaviors, annotations.FieldBehavior_REQUIRED)
}

// identified holds that a call of one resource names it by a field that
// is required: <resource>_id where the server mints the resource's id,
// and otherwise the field of the resource that names it.
func identified(c contract) []string {
	var out []string
	for _, m := range c.methods() {
		verb, _ := verbOf(m)
		resource := within(m)
		switch verb {
		case "Describe", "Update", "Delete", "Wait", "Cancel", "Pause", "Resume", "Exec", "Stream":
			resource = resourceOf(m)
		}
		if resource == nil {
			continue
		}
		var key protoreflect.FieldDescriptor
		if resource.Fields().ByName("id") != nil {
			key = m.Input().Fields().ByName(protoreflect.Name(snake(resource.Name()) + "_id"))
			if key == nil {
				out = append(out, fmt.Sprintf("%s does not name its %s by %s_id", m.Input().FullName(), resource.Name(), snake(resource.Name())))
				continue
			}
		} else {
			if m.Input().Fields().Len() == 0 || resource.Fields().ByName(m.Input().Fields().Get(0).Name()) == nil {
				out = append(out, fmt.Sprintf("%s does not name its %s first by a field the %s has", m.Input().FullName(), resource.Name(), resource.Name()))
				continue
			}
			key = m.Input().Fields().Get(0)
		}
		if !required(key) {
			out = append(out, fmt.Sprintf("%s is not REQUIRED", key.FullName()))
		}
	}
	return out
}

// pages holds that a List takes page_size and page_token and answers
// next_page_token beside the one repeated field it lists.
func pages(c contract) []string {
	var out []string
	for _, m := range c.methods() {
		if verb, _ := verbOf(m); verb != "List" {
			continue
		}
		in, answer := m.Input().Fields(), m.Output().Fields()
		if size := in.ByName("page_size"); size == nil || size.Kind() != protoreflect.Int32Kind {
			out = append(out, fmt.Sprintf("%s takes no int32 page_size", m.Input().FullName()))
		}
		if token := in.ByName("page_token"); token == nil || token.Kind() != protoreflect.StringKind {
			out = append(out, fmt.Sprintf("%s takes no string page_token", m.Input().FullName()))
		}
		if next := answer.ByName("next_page_token"); next == nil || next.Kind() != protoreflect.StringKind {
			out = append(out, fmt.Sprintf("%s answers no string next_page_token", m.Output().FullName()))
		}
		listed := 0
		for i := range answer.Len() {
			if answer.Get(i).Cardinality() == protoreflect.Repeated && answer.Get(i).Message() != nil {
				listed++
			}
		}
		if listed != 1 || answer.Len() != 2 {
			out = append(out, fmt.Sprintf("%s answers other than one list and its next_page_token", m.Output().FullName()))
		}
	}
	return out
}

// held calls do with every message and enum of the held packages,
// nested ones too.
func (c contract) held(do func(protoreflect.Descriptor)) {
	var messages func(protoreflect.MessageDescriptors)
	enums := func(es protoreflect.EnumDescriptors) {
		for i := range es.Len() {
			do(es.Get(i))
		}
	}
	messages = func(ms protoreflect.MessageDescriptors) {
		for i := range ms.Len() {
			do(ms.Get(i))
			messages(ms.Get(i).Messages())
			enums(ms.Get(i).Enums())
		}
	}
	c.files.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		if slices.Contains(c.served, f.Package()) || slices.Contains(c.shared, f.Package()) {
			messages(f.Messages())
			enums(f.Enums())
		}
		return true
	})
}

func nothingReserved(c contract) []string {
	var out []string
	c.held(func(d protoreflect.Descriptor) {
		switch d := d.(type) {
		case protoreflect.MessageDescriptor:
			if d.ReservedNames().Len() > 0 || d.ReservedRanges().Len() > 0 {
				out = append(out, fmt.Sprintf("%s reserves what it no longer has", d.FullName()))
			}
		case protoreflect.EnumDescriptor:
			if d.ReservedNames().Len() > 0 || d.ReservedRanges().Len() > 0 {
				out = append(out, fmt.Sprintf("%s reserves what it no longer has", d.FullName()))
			}
		}
	})
	return out
}

// reached holds that each message and enum of the held packages is
// reached from a call: what it takes or answers, through their fields.
func reached(c contract) []string {
	seen := map[protoreflect.FullName]bool{}
	var walk func(protoreflect.MessageDescriptor)
	walk = func(m protoreflect.MessageDescriptor) {
		if seen[m.FullName()] {
			return
		}
		seen[m.FullName()] = true
		for i := range m.Fields().Len() {
			f := m.Fields().Get(i)
			if f.Message() != nil {
				walk(f.Message())
			}
			if f.Enum() != nil {
				seen[f.Enum().FullName()] = true
			}
		}
	}
	c.files.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		if slices.Contains(c.served, f.Package()) || slices.Contains(c.reaches, f.Package()) {
			for i := range f.Services().Len() {
				for j := range f.Services().Get(i).Methods().Len() {
					m := f.Services().Get(i).Methods().Get(j)
					walk(m.Input())
					walk(m.Output())
				}
			}
		}
		return true
	})
	var out []string
	c.held(func(d protoreflect.Descriptor) {
		if !seen[d.FullName()] {
			out = append(out, fmt.Sprintf("%s is reached by no call", d.FullName()))
		}
	})
	return out
}

// failures holds the one failure shape: a resource's state enum is
// nested in what holds the state, and what can fail says why in one
// string field, failure.
func failures(c contract) []string {
	var out []string
	c.held(func(d protoreflect.Descriptor) {
		switch d := d.(type) {
		case protoreflect.EnumDescriptor:
			if _, top := d.Parent().(protoreflect.FileDescriptor); top && strings.HasSuffix(string(d.Name()), "State") {
				out = append(out, fmt.Sprintf("%s is a state enum of no message: nest it in what it is the state of", d.FullName()))
			}
		case protoreflect.MessageDescriptor:
			state, failure := d.Fields().ByName("state"), d.Fields().ByName("failure")
			if failure != nil && (failure.Kind() != protoreflect.StringKind || failure.Cardinality() == protoreflect.Repeated) {
				out = append(out, fmt.Sprintf("%s is not one string", failure.FullName()))
			}
			if state == nil || state.Enum() == nil {
				return
			}
			if state.Enum().Parent() != d {
				out = append(out, fmt.Sprintf("%s is a %s, not an enum nested in %s", state.FullName(), state.Enum().FullName(), d.Name()))
			}
			if state.Enum().Values().ByName(protoreflect.Name("STATE_FAILED")) != nil && failure == nil {
				out = append(out, fmt.Sprintf("%s can fail and says why in no string failure", d.FullName()))
			}
		}
	})
	return out
}

// times holds that a time is a Timestamp named *_time (or time), and a
// length of time a Duration, never a number of seconds. It holds the
// served and shared packages; the plugin contract's own messages cross
// the Wasm boundary under the plugin ABI, and are not held by it.
func times(c contract) []string {
	var out []string
	c.held(func(d protoreflect.Descriptor) {
		m, ok := d.(protoreflect.MessageDescriptor)
		if !ok {
			return
		}
		for i := range m.Fields().Len() {
			f := m.Fields().Get(i)
			name := string(f.Name())
			if f.Message() != nil && f.Message().FullName() == "google.protobuf.Timestamp" && name != "time" && !strings.HasSuffix(name, "_time") {
				out = append(out, fmt.Sprintf("%s is a time and not named *_time", f.FullName()))
			}
			if f.Message() == nil && f.Kind() != protoreflect.StringKind && f.Kind() != protoreflect.BoolKind && f.Enum() == nil {
				for _, unit := range []string{"_seconds", "_secs", "_millis", "_ms", "_us", "_nanos", "_ns", "_minutes", "_hours", "_days", "_at", "_unix", "timeout"} {
					if strings.HasSuffix(name, unit) {
						out = append(out, fmt.Sprintf("%s is a length of time as a number: make it a google.protobuf.Duration", f.FullName()))
					}
				}
			}
		}
	})
	return out
}

// retried holds that a Create a retry would make twice takes a
// request_id: one keyed by a name the caller chooses, or by a plan's etag,
// is made once already.
func retried(c contract) []string {
	var out []string
	for _, m := range c.methods() {
		if verb, _ := verbOf(m); verb != "Create" {
			continue
		}
		fields := m.Input().Fields()
		if fields.ByName("name") != nil || fields.ByName("etag") != nil {
			continue
		}
		if id := fields.ByName("request_id"); id == nil || id.Kind() != protoreflect.StringKind {
			out = append(out, fmt.Sprintf("%s takes no request_id, and a retry after a lost answer makes another", m.Input().FullName()))
		}
	}
	return out
}

// importsShared holds that a contract names no type of another
// contract: what two need is the shared package's (an entity's reference,
// a version's bump), so that the plugin contract and the workspace API do
// not import the build API.
func importsShared(c contract) []string {
	var out []string
	c.files.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		if !slices.Contains(c.served, f.Package()) && !slices.Contains(c.reaches, f.Package()) {
			return true
		}
		for i := range f.Imports().Len() {
			imported := f.Imports().Get(i).FileDescriptor
			if pkg := imported.Package(); pkg != f.Package() && !slices.Contains(c.shared, pkg) && !strings.HasPrefix(string(pkg), "google.") {
				out = append(out, fmt.Sprintf("%s imports %s, of %s", f.Path(), imported.Path(), pkg))
			}
		}
		return true
	})
	return out
}

// protoFixture builds the protos given, as files beside the googleapis protos
// the contracts import, and is the contract of their packages.
func protoFixture(t *testing.T, protos map[string]string) contract {
	t.Helper()
	dir := t.TempDir()
	third := filepath.Join(repositoryRoot(t), "api", "third-party-proto")
	for _, name := range []string{"google/api/field_behavior.proto"} {
		raw, err := os.ReadFile(filepath.Join(third, name))
		if err != nil {
			t.Fatal(err)
		}
		protos[name] = string(raw)
	}
	var packages []protoreflect.FullName
	for name, text := range protos {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(name, "fixture/") {
			packages = append(packages, protoreflect.FullName(strings.ReplaceAll(filepath.Dir(name), "/", ".")))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "buf.yaml"), []byte("version: v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return contract{files: built(t, dir), served: packages}
}

// fixtureOf is a proto of a fixture package: its header, and what it
// says.
func fixtureOf(name, says string) map[string]string {
	imports := ""
	if strings.Contains(says, "fixture.other.v1.") {
		imports = "import \"fixture/other/v1/other.proto\";\n"
	}
	return map[string]string{"fixture/" + name + "/v1/x.proto": `syntax = "proto3";
package fixture.` + name + `.v1;
import "google/api/field_behavior.proto";
import "google/protobuf/duration.proto";
import "google/protobuf/timestamp.proto";
` + imports + says}
}

// thing is a resource the fixtures serve, as the conventions have it.
const thing = `
message Thing {
  string id = 1 [(google.api.field_behavior) = OUTPUT_ONLY];
  State state = 2;
  string failure = 3;
  google.protobuf.Timestamp create_time = 4;
  google.protobuf.Duration timeout = 5;
  enum State {
    STATE_UNSPECIFIED = 0;
    STATE_FAILED = 1;
  }
}
message DescribeThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message DescribeThingResponse { Thing thing = 1; }
`

// sound is the service of a fixture that keeps every rule.
const sound = `
message WaitThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message WaitThingResponse { Thing thing = 1; }
message ListThingsRequest { int32 page_size = 1; string page_token = 2; }
message ListThingsResponse { repeated Thing things = 1; string next_page_token = 2; }
message DeleteThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message DeleteThingResponse {}
message StreamThingLogsRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message StreamThingLogsResponse { repeated string lines = 1; }
message CreateThingRequest { string request_id = 1; }
message CreateThingResponse { Thing thing = 1; }
service ThingService {
  rpc CreateThing(CreateThingRequest) returns (CreateThingResponse);
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc WaitThing(WaitThingRequest) returns (WaitThingResponse);
  rpc ListThings(ListThingsRequest) returns (ListThingsResponse);
  rpc DeleteThing(DeleteThingRequest) returns (DeleteThingResponse);
  rpc StreamThingLogs(StreamThingLogsRequest) returns (stream StreamThingLogsResponse);
}
`

// TestEachRuleOfTheAPIRefusesWhatBreaksItAndAllowsTheRest is the rules'
// control: the sound fixture breaks none of them, and each fixture beside
// it breaks the one rule it is named for, and is refused by it.
func TestEachRuleOfTheAPIRefusesWhatBreaksItAndAllowsTheRest(t *testing.T) {
	broken := map[string]string{
		// A call with no verb of the list.
		"allowed verbs": thing + `service ThingService {
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc FetchThing(FetchThingRequest) returns (FetchThingResponse);
}
message FetchThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message FetchThingResponse { Thing thing = 1; }
`,
		// Two calls that share a request.
		"a request and a response of its own": thing + `service ThingService {
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc WaitThing(DescribeThingRequest) returns (WaitThingResponse);
}
message WaitThingResponse { Thing thing = 1; }
`,
		// A Wait that streams, as WaitWorkspace did.
		"only Stream calls stream": thing + `service ThingService {
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc WaitThing(WaitThingRequest) returns (stream WaitThingResponse);
}
message WaitThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message WaitThingResponse { Thing thing = 1; }
`,
		// A response of a resource named otherwise, as Launched was.
		"a response wraps its resource": thing + `service ThingService {
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc CancelThing(CancelThingRequest) returns (CancelThingResponse);
}
message CancelThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message CancelThingResponse { Thinged thing = 1; }
message Thinged { string id = 1; }
`,
		// An id that is not required.
		"identifying fields are required": thing + `service ThingService {
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc CancelThing(CancelThingRequest) returns (CancelThingResponse);
}
message CancelThingRequest { string thing_id = 1; }
message CancelThingResponse { Thing thing = 1; }
`,
		// A List of everything at once.
		"a List pages": thing + `service ThingService {
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc ListThings(ListThingsRequest) returns (ListThingsResponse);
}
message ListThingsRequest {}
message ListThingsResponse { repeated Thing things = 1; }
`,
		// A field number kept for later.
		"no reserved": thing + sound + `message Kept { reserved 2; string name = 1; }
message Holding { Kept kept = 1; }
`,
		// A message no call reaches.
		"every message reached by a call": thing + sound + `message Orphan { string name = 1; }
`,
		// A state enum beside its message, as ReleaseState was, and a
		// failure that is a status.
		"one failure shape": `
message Thing {
  string id = 1 [(google.api.field_behavior) = OUTPUT_ONLY];
  ThingState state = 2;
  google.protobuf.Duration failure = 3;
}
enum ThingState {
  THING_STATE_UNSPECIFIED = 0;
  THING_STATE_FAILED = 1;
}
message DescribeThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message DescribeThingResponse { Thing thing = 1; }
service ThingService { rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse); }
`,
		// A contract that names another's type, as the workspace API
		// named the build API's EntityRef.
		"a contract imports only shared types": thing + sound + `message Holding { fixture.other.v1.Ref ref = 1; }
message SetHoldingRequest { Holding holding = 1; }
message SetHoldingResponse { Holding holding = 1; }
service HoldingService { rpc SetHolding(SetHoldingRequest) returns (SetHoldingResponse); }
`,
		// A Create that a retry repeats, as CreateBuild was.
		"a Create can be sent again": thing + `service ThingService {
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  rpc CreateThing(CreateThingRequest) returns (CreateThingResponse);
}
message CreateThingRequest { repeated string targets = 1; }
message CreateThingResponse { Thing thing = 1; }
`,
		// A timeout in seconds, as Launch.timeout_seconds was, and a time
		// named otherwise.
		"times and lengths of time": `
message Thing {
  string id = 1 [(google.api.field_behavior) = OUTPUT_ONLY];
  int32 timeout_seconds = 2;
  google.protobuf.Timestamp created = 3;
}
message DescribeThingRequest { string thing_id = 1 [(google.api.field_behavior) = REQUIRED]; }
message DescribeThingResponse { Thing thing = 1; }
service ThingService { rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse); }
`,
	}
	for name := range apiRules {
		if _, ok := broken[name]; !ok {
			t.Errorf("the rule %q has no fixture that breaks it", name)
		}
	}
	protos := fixtureOf("sound", thing+sound)
	protos["fixture/other/v1/other.proto"] = "syntax = \"proto3\";\npackage fixture.other.v1;\nmessage Ref { string name = 1; }\n"
	keys := map[string]string{}
	for name, says := range broken {
		key := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) {
				return unicode.ToLower(r)
			}
			return ' '
		}, name)), "")
		keys[name] = key
		for file, text := range fixtureOf(key, says) {
			protos[file] = text
		}
	}
	all := protoFixture(t, protos)
	of := func(key string) contract {
		c := all
		c.served = []protoreflect.FullName{protoreflect.FullName("fixture." + key + ".v1")}
		return c
	}
	for name, rule := range apiRules {
		if found := rule(of("sound")); len(found) != 0 {
			t.Errorf("%s refuses the sound fixture: %v", name, found)
		}
		if found := rule(of(keys[name])); len(found) == 0 {
			t.Errorf("%s allows the fixture that breaks it", name)
		}
	}
}

// handled holds that each call of the served packages has a handler in
// the server's code that is not a test: a method named for it that takes
// its request, as Connect calls one. A service whose servers each embed
// its Unimplemented handler answers UNIMPLEMENTED for a call none of them
// declares, which this finds. It holds the server as a whole: each call
// is handled somewhere in it.
func handled(c contract, sources map[string][]byte) []string {
	declared := map[string]bool{}
	for name, source := range sources {
		f, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
		if err != nil {
			return []string{err.Error()}
		}
		imported := map[string]string{}
		for _, spec := range f.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			local := filepath.Base(path)
			if spec.Name != nil {
				local = spec.Name.Name
			}
			imported[local] = path
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			// Each parameter's type, once for each name it has.
			var types []ast.Expr
			for _, param := range fn.Type.Params.List {
				for range max(1, len(param.Names)) {
					types = append(types, param.Type)
				}
			}
			if len(types) < 2 {
				continue
			}
			second := types[1]
			star, ok := second.(*ast.StarExpr)
			if !ok {
				continue
			}
			sel, ok := star.X.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || sel.Sel.Name != fn.Name.Name+"Request" {
				continue
			}
			declared[imported[pkg.Name]+"."+fn.Name.Name] = true
		}
	}
	var out []string
	for _, m := range c.methods() {
		if !declared[generatedPackage(m.ParentFile().Package())+"."+string(m.Name())] {
			out = append(out, fmt.Sprintf("%s has no handler in the server: it answers UNIMPLEMENTED", m.FullName()))
		}
	}
	return out
}

// generatedPackage is the Go package the generated code of a served proto
// package is in: the server's own for the daemon's internal API
// (api/buf.gen.source.yaml), and the Go SDK's for every other.
func generatedPackage(pkg protoreflect.FullName) string {
	if pkg == "frontseat.source.v1alpha1" {
		return "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
	}
	return sdkPackage(pkg)
}

// sdkPackage is the Go package the Go SDK's generated code of a proto
// package is in (api/buf.gen.sdk.yaml).
func sdkPackage(pkg protoreflect.FullName) string {
	return "github.com/frontseat-dev/frontseat/sdk/go/api/" + strings.ReplaceAll(string(pkg), ".", "/")
}

func TestEveryCallOfTheAPIHasAHandler(t *testing.T) {
	root := repositoryRoot(t)
	c := contract{files: built(t, filepath.Join(root, "api")), served: served}
	sources := map[string][]byte{}
	err := filepath.WalkDir(filepath.Join(root, "server"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == "testsupport" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		sources[path] = raw
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, broken := range handled(c, sources) {
		t.Error(broken)
	}
}

// TestACallWithNoHandlerIsFound is the control: a server that declares a
// call's method is allowed, and one that only embeds the Unimplemented
// handler, or declares a method of that name taking another request, is
// refused.
func TestACallWithNoHandlerIsFound(t *testing.T) {
	c := protoFixture(t, fixtureOf("served", thing+sound))
	header := `package server
import (
	"context"
	thingv1 "` + sdkPackage("fixture.served.v1") + `"
	other "example.com/other"
)
`
	every := `
func (s *server) CreateThing(ctx context.Context, req *thingv1.CreateThingRequest) (*thingv1.CreateThingResponse, error) { return nil, nil }
func (s *server) DescribeThing(ctx context.Context, req *thingv1.DescribeThingRequest) (*thingv1.DescribeThingResponse, error) { return nil, nil }
func (s *server) WaitThing(_ context.Context, req *thingv1.WaitThingRequest) (*thingv1.WaitThingResponse, error) { return nil, nil }
func (s *server) ListThings(context.Context, *thingv1.ListThingsRequest) (*thingv1.ListThingsResponse, error) { return nil, nil }
func (s *server) StreamThingLogs(ctx context.Context, req *thingv1.StreamThingLogsRequest, stream any) error { return nil }
`
	for name, c2 := range map[string]struct {
		source string
		want   bool
	}{
		"each declared": {header + every + `func (s *server) DeleteThing(ctx context.Context, req *thingv1.DeleteThingRequest) (*thingv1.DeleteThingResponse, error) { return nil, nil }`, false},
		"one embedded":  {header + every + `type server struct { thingv1connect.UnimplementedThingServiceHandler }`, true},
		"another's":     {header + every + `func (s *server) DeleteThing(ctx context.Context, req *other.DeleteThingRequest) (*thingv1.DeleteThingResponse, error) { return nil, nil }`, true},
	} {
		got := handled(c, map[string][]byte{"server.go": []byte(c2.source)})
		if (len(got) > 0) != c2.want {
			t.Errorf("%s: found %v; want refusal %v", name, got, c2.want)
		}
	}
}
