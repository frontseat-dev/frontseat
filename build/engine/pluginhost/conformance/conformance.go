// Package conformance tests every plugin against the same served API using its
// real Wasm module. Fixtures supply ecosystem data; assertions live here.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// Observation identifies a required host read independently of the module's answer.
type Observation struct {
	Import, Argument, Directory string
	Found                       bool
}

// Fixture supplies independent API expectations and ecosystem source.
type Fixture struct {
	Name                      string
	Module                    []byte
	Source, Absent, Malformed pluginhost.Source
	Analyze                   *pluginv1.AnalyzeRequest
	Arrange                   *pluginv1.ArrangeRequest
	ErrorCode                 string
	InvalidArrange            *pluginv1.ArrangeRequest
	ArrangeErrorCode          string
	ArrangeErrorPath          string
	Projects                  []*pluginv1.Project
	Entities                  []*pluginv1.Entity
	Description               *pluginv1.DescribeResponse
	// ABI is the plugin ABI the fixture is of: what the module's export
	// frontseat_abi must say.
	ABI   uint32
	Files []*pluginv1.File
	Reads []Observation
	// Report is what a check of the plugin printed, and Findings what the
	// plugin says it found there; nil for a plugin that reads no findings,
	// which must say so.
	Report *pluginv1.ReportRequest
	// Reconcile is what the source says of the fixture's entities, and
	// Edits what the plugin answers their files should say; nil where the
	// plugin finds no entities, which must then refuse the call.
	Reconcile *pluginv1.ReconcileRequest
	Edits     []*pluginv1.Edit
	Findings  []*typev1.Diagnostic
	// Branches and Versions are what a strategy plugin is asked, each with
	// what it must answer. A fixture that has them is a strategy plugin's:
	// such a plugin reads no file and finds no project, so it has no source
	// to analyze and is held to its answers instead.
	Branches []BranchCase
	Versions []VersionCase
	// Services are what a plugin that starts is asked of the names it
	// claims, each with the whole service it must answer or the reason it
	// refuses. A plugin that only services reads no file and finds no
	// project either, and is held to its answers as a strategy plugin is.
	Services []ServiceCase
}

// BranchCase is one thing a branching strategy is asked, and its answer:
// what the branch releases, or the reason it is refused with.
type BranchCase struct {
	Request *pluginv1.BranchRequest
	Want    *pluginv1.BranchResponse
	Refused string
}

// VersionCase is one thing a versioning strategy is asked, and its answer:
// the next version, or the reason it is refused with.
type VersionCase struct {
	Request *pluginv1.VersionRequest
	Want    *pluginv1.VersionResponse
	Refused string
}

// strategy says the fixture is a strategy plugin's.
func (f Fixture) strategy() bool { return len(f.Branches)+len(f.Versions)+len(f.Services) > 0 }

// ServiceCase is one question of how something is started, and either
// its answer or the reason it is refused.
type ServiceCase struct {
	Request *pluginv1.ServiceRequest
	Want    *pluginv1.ServiceResponse
	Refused string
}

// Run exercises Describe, Analyze and Arrange, including repetition,
// concurrency, cancellation and recovery, through the host's real Wasm ABI.
func Run(t *testing.T, f Fixture) {
	t.Helper()
	if err := ValidateFixtures([]string{f.Name}, map[string]Fixture{f.Name: f}); err != nil {
		t.Fatal(err)
	}
	plugin := Load(t, f.Module)
	if plugin.Description.GetName() != f.Name {
		t.Fatalf("module describes %q, fixture is %q", plugin.Description.GetName(), f.Name)
	}
	if f.strategy() {
		runStrategy(t, plugin, f)
		return
	}
	describes(t, plugin, f, f.Source)
	// The fixture's request, told what matched of listed as a host tells it.
	asked := func(ctx context.Context, listed pluginhost.Source) *pluginv1.AnalyzeRequest {
		t.Helper()
		r := proto.Clone(f.Analyze).(*pluginv1.AnalyzeRequest)
		if err := pluginhost.Matched(ctx, listed, plugin.Description.GetInterests(), r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	// analyzeOver asks the plugin over src, told what matched of listed:
	// the same source, but where src fails what listed holds.
	analyzeOver := func(ctx context.Context, listed, src pluginhost.Source) (*pluginv1.AnalyzeResponse, error) {
		answer := new(pluginv1.AnalyzeResponse)
		_, err := plugin.Call(ctx, src, "Analyze", asked(context.WithoutCancel(ctx), listed), answer)
		if err == nil {
			err = pluginhost.CheckAnalysis(f.Name, f.Analyze.GetRoots(), answer)
		}
		return answer, err
	}
	analyze := func(ctx context.Context, src pluginhost.Source) (*pluginv1.AnalyzeResponse, error) {
		return analyzeOver(ctx, src, src)
	}
	observed := new(pluginv1.AnalyzeResponse)
	reads, err := plugin.Call(t.Context(), f.Source, "Analyze", asked(t.Context(), f.Source), observed)
	if err != nil {
		t.Fatal(err)
	}
	// A fixture with no malformed source is of a plugin that reads no
	// file's content, and so has none to find malformed: it is held to
	// that by what the plugin read here.
	if f.Malformed == nil {
		for _, got := range reads {
			if got.Import == "read" {
				t.Fatalf("the fixture has no malformed source, and the plugin read %s: what a plugin reads it is shown malformed", got.Argument)
			}
		}
	}
	for _, want := range f.Reads {
		found := false
		for _, got := range reads {
			if got.Import == want.Import && got.Argument == want.Argument && got.Directory == want.Directory && got.Found == want.Found && (!got.Found || got.Digest != "") {
				found = true
			}
		}
		if !found {
			t.Errorf("missing declared read dependency %+v; got %+v", want, reads)
		}
	}
	baseline, err := analyze(t.Context(), f.Source)
	if err != nil {
		t.Fatal(err)
	}
	var declarations []*pluginv1.Project
	var entities []*pluginv1.Entity
	for _, analysis := range baseline.GetAnalyses() {
		declarations = append(declarations, analysis.GetProjects()...)
		entities = append(entities, analysis.GetEntities()...)
	}
	if len(declarations) != len(f.Projects) {
		t.Fatalf("discovered %d projects, want %d", len(declarations), len(f.Projects))
	}
	for i, want := range f.Projects {
		if !proto.Equal(declarations[i], want) {
			t.Fatalf("project differs from fixture: got %v; want %v", declarations[i], want)
		}
	}
	if !proto.Equal(&pluginv1.Analysis{Entities: entities}, &pluginv1.Analysis{Entities: f.Entities}) {
		t.Fatalf("catalog entities differ: got %v; want %v", entities, f.Entities)
	}
	t.Run("Analyze", func(t *testing.T) {
		empty, err := analyze(t.Context(), f.Absent)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range empty.GetAnalyses() {
			if len(a.GetProjects()) != 0 || len(a.GetEntities()) != 0 {
				t.Fatal("an absent manifest created a project or catalog entity")
			}
		}
		if f.Malformed == nil {
			return
		}
		_, err = analyze(t.Context(), f.Malformed)
		var detail *pluginhost.Error
		if !errors.As(err, &detail) || detail.Reason != f.ErrorCode || detail.Detail.GetSourceLocation().GetPath() == "" || detail.Detail.GetHelp() == "" {
			t.Fatalf("malformed manifest needs a coded diagnostic with path and help: %v", err)
		}
		again, err := analyze(t.Context(), f.Source)
		if err != nil || !proto.Equal(again, baseline) {
			t.Fatalf("valid call after failure changed: %v", err)
		}
	})
	t.Run("Report", func(t *testing.T) {
		if f.Report == nil {
			// A plugin that reads no findings says so, and its checks pass
			// or fail by their exit code alone.
			request := &pluginv1.ReportRequest{Context: f.Analyze.GetContext(), Root: f.Analyze.GetRoots()[0], Action: "lint", ExitCode: 1, Stderr: []byte("something\n")}
			_, err := plugin.Call(t.Context(), f.Source, "Report", request, new(pluginv1.ReportResponse))
			if !errors.Is(err, pluginhost.ErrUnsupported) {
				t.Fatalf("a plugin with no report fixture must answer Report as unsupported, got %v", err)
			}
			return
		}
		var baseline *pluginv1.ReportResponse
		for range 2 {
			got := new(pluginv1.ReportResponse)
			if _, err := plugin.Call(t.Context(), f.Source, "Report", f.Report, got); err != nil {
				t.Fatal(err)
			}
			if err := pluginhost.CheckReport(f.Name, got); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(got, &pluginv1.ReportResponse{Diagnostics: f.Findings}) {
				t.Fatalf("findings differ from fixture: got %v, want %v", got, f.Findings)
			}
			if baseline != nil && !proto.Equal(baseline, got) {
				t.Fatal("Report is not deterministic")
			}
			baseline = got
		}
		// A check that printed nothing found nothing.
		quiet := proto.Clone(f.Report).(*pluginv1.ReportRequest)
		quiet.ExitCode, quiet.Stdout, quiet.Stderr = 0, nil, nil
		got := new(pluginv1.ReportResponse)
		if _, err := plugin.Call(t.Context(), f.Source, "Report", quiet, got); err != nil || len(got.GetDiagnostics()) != 0 {
			t.Fatalf("a quiet check found something: %v %v", got, err)
		}
	})
	t.Run("Reconcile", func(t *testing.T) {
		if f.Reconcile == nil {
			request := &pluginv1.ReconcileRequest{Context: f.Analyze.GetContext(), Root: f.Analyze.GetRoots()[0]}
			_, err := plugin.Call(t.Context(), f.Source, "Reconcile", request, new(pluginv1.ReconcileResponse))
			if !errors.Is(err, pluginhost.ErrUnsupported) {
				t.Fatalf("a plugin with no reconcile fixture must answer Reconcile as unsupported, got %v", err)
			}
			return
		}
		var baseline *pluginv1.ReconcileResponse
		for range 2 {
			got := new(pluginv1.ReconcileResponse)
			if _, err := plugin.Call(t.Context(), f.Source, "Reconcile", f.Reconcile, got); err != nil {
				t.Fatal(err)
			}
			if err := pluginhost.CheckReconciliation(f.Name, f.Reconcile, got); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(got, &pluginv1.ReconcileResponse{Edits: f.Edits}) {
				t.Fatalf("edits differ from fixture: got %v, want %v", got, f.Edits)
			}
			if baseline != nil && !proto.Equal(baseline, got) {
				t.Fatal("Reconcile is not deterministic")
			}
			baseline = got
		}
		// An entity the file does not describe is refused by name.
		stranger := proto.Clone(f.Reconcile).(*pluginv1.ReconcileRequest)
		stranger.Entities = append(stranger.Entities, &pluginv1.EntityDependencies{Ref: &typev1.EntityRef{Kind: "component", Namespace: "default", Name: "nobody"}, Manifest: f.Reconcile.GetEntities()[0].GetManifest()})
		if _, err := plugin.Call(t.Context(), f.Source, "Reconcile", stranger, new(pluginv1.ReconcileResponse)); err == nil {
			t.Fatal("an entity the file does not describe was reconciled")
		}
	})
	t.Run("options are checked before ecosystem code", func(t *testing.T) {
		methods := []string{"Analyze", "Arrange"}
		if f.Report != nil {
			methods = append(methods, "Report")
		}
		if f.Reconcile != nil {
			methods = append(methods, "Reconcile")
		}
		for _, method := range methods {
			t.Run(method, func(t *testing.T) {
				invoke := func(change func(map[string]*typev1.OptionValue)) error {
					var request, response proto.Message
					var context *pluginv1.Context
					switch method {
					case "Analyze":
						r := asked(t.Context(), f.Source)
						context, request, response = r.Context, r, new(pluginv1.AnalyzeResponse)
					case "Report":
						r := proto.Clone(f.Report).(*pluginv1.ReportRequest)
						context, request, response = r.Context, r, new(pluginv1.ReportResponse)
					case "Reconcile":
						r := proto.Clone(f.Reconcile).(*pluginv1.ReconcileRequest)
						context, request, response = r.Context, r, new(pluginv1.ReconcileResponse)
					default:
						r := proto.Clone(f.Arrange).(*pluginv1.ArrangeRequest)
						context, request, response = r.Context, r, new(pluginv1.ArrangeResponse)
					}
					if context == nil {
						t.Fatal("fixture must supply an explicit context")
					}
					if context.Options == nil {
						context.Options = map[string]*typev1.OptionValue{}
					}
					change(context.Options)
					source := &pluginhost.Recorder{Source: f.Source}
					_, err := plugin.Call(t.Context(), source, method, request, response)
					if reads := source.Reads(); err != nil && len(reads) != 0 {
						t.Fatalf("invalid options reached source: %v", reads)
					}
					return err
				}
				refused := func(change func(map[string]*typev1.OptionValue)) {
					t.Helper()
					err := invoke(change)
					var detail *pluginhost.Error
					if !errors.As(err, &detail) || detail.Reason != "INVALID_OPTION" {
						t.Fatalf("expected invalid-option, got %v", err)
					}
				}
				if err := invoke(func(map[string]*typev1.OptionValue) {}); err != nil {
					t.Fatal(err)
				}
				refused(func(options map[string]*typev1.OptionValue) {
					options["undeclared-option"] = &typev1.OptionValue{Value: &typev1.OptionValue_BoolValue{BoolValue: false}}
				})
				for _, option := range f.Description.GetOptions() {
					refused(func(options map[string]*typev1.OptionValue) { delete(options, option.GetName()) })
					refused(func(options map[string]*typev1.OptionValue) { options[option.GetName()] = &typev1.OptionValue{} })
					refused(func(options map[string]*typev1.OptionValue) {
						value := &typev1.OptionValue{Value: &typev1.OptionValue_BoolValue{}}
						if option.GetKind() == pluginv1.OptionDeclaration_KIND_BOOL {
							value = &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{}}
						}
						options[option.GetName()] = value
					})
				}
				if err := invoke(func(map[string]*typev1.OptionValue) {}); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
	t.Run("Arrange", func(t *testing.T) {
		_, err := plugin.Call(t.Context(), f.Source, "Arrange", f.InvalidArrange, new(pluginv1.ArrangeResponse))
		var detail *pluginhost.Error
		if !errors.As(err, &detail) || detail.Reason != f.ArrangeErrorCode || detail.Detail.GetSourceLocation().GetPath() != f.ArrangeErrorPath || detail.Detail.GetHelp() == "" {
			t.Fatalf("invalid arrangement needs its declared diagnostic location and help: %v", err)
		}
		// Successful calls after the rejected request also prove recovery.

		var baseline *pluginv1.ArrangeResponse
		for range 2 {
			got := new(pluginv1.ArrangeResponse)
			_, err := plugin.Call(t.Context(), f.Source, "Arrange", f.Arrange, got)
			if err != nil {
				t.Fatal(err)
			}
			if err := pluginhost.CheckArrangement(f.Name, f.Arrange.GetRoot(), got); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(got, &pluginv1.ArrangeResponse{Files: f.Files}) {
				t.Fatalf("arranged files differ from fixture: got %v, want %v", got, f.Files)
			}
			if baseline != nil && !proto.Equal(baseline, got) {
				t.Fatal("Arrange is not deterministic")
			}
			baseline = got
		}
	})
	t.Run("profiles are checked before ecosystem code", func(t *testing.T) {
		for _, method := range []string{"Analyze", "Arrange"} {
			for _, profile := range []typev1.Profile{typev1.Profile_PROFILE_DEVELOPMENT, typev1.Profile(0), typev1.Profile(-1), typev1.Profile(999), typev1.Profile_PROFILE_PRODUCTION} {
				var request, response proto.Message
				if method == "Analyze" {
					r := asked(t.Context(), f.Source)
					r.Context.Profile = profile
					request, response = r, new(pluginv1.AnalyzeResponse)
				} else {
					r := proto.Clone(f.Arrange).(*pluginv1.ArrangeRequest)
					r.Context.Profile = profile
					request, response = r, new(pluginv1.ArrangeResponse)
				}
				source := &pluginhost.Recorder{Source: f.Source}
				_, err := plugin.Call(t.Context(), source, method, request, response)
				if profile == typev1.Profile_PROFILE_DEVELOPMENT || profile == typev1.Profile_PROFILE_PRODUCTION {
					if err != nil {
						t.Fatal(err)
					}
					continue
				}
				var detail *pluginhost.Error
				if !errors.As(err, &detail) || detail.Reason != "INVALID_PROFILE" || len(source.Reads()) != 0 {
					t.Fatalf("%s accepted profile %d or reached source: %v, %v", method, profile, err, source.Reads())
				}
			}
		}
	})
	t.Run("cancelled calls leave the plugin usable", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := analyze(ctx, f.Source); err == nil {
			t.Fatal("cancelled call was accepted")
		}
		got, err := analyze(t.Context(), f.Source)
		if err != nil || !proto.Equal(got, baseline) {
			t.Fatalf("call after cancellation changed: %v", err)
		}
	})
	t.Run("failed source operations cannot mean absence", func(t *testing.T) {
		for _, operation := range f.Reads {
			failed := errors.New("fixture source unavailable")
			_, err := analyzeOver(t.Context(), f.Source, failingSource{Source: f.Source, operation: operation, err: failed})
			if !errors.Is(err, failed) {
				t.Fatalf("%+v failure was lost: %v", operation, err)
			}
		}
		got, err := analyze(t.Context(), f.Source)
		if err != nil || !proto.Equal(got, baseline) {
			t.Fatalf("valid call after source failure changed: %v", err)
		}
	})
	t.Run("concurrent calls agree", func(t *testing.T) {
		var joined sync.WaitGroup
		for range 8 {
			joined.Go(func() {
				got, err := analyze(t.Context(), f.Source)
				if err != nil || !proto.Equal(got, baseline) {
					t.Errorf("concurrent answer changed: %v", err)
				}
			})
		}
		joined.Wait()
	})
	t.Run("unknown methods are explicit", func(t *testing.T) {
		_, err := plugin.Call(t.Context(), f.Source, "UnknownMethod", &pluginv1.DescribeRequest{}, new(pluginv1.DescribeResponse))
		if !errors.Is(err, pluginhost.ErrUnsupported) {
			t.Fatalf("unknown method: %v", err)
		}
	})
	t.Run("what it claims none of is unserved", func(t *testing.T) {
		// A plugin that claims no strategy or service of a kind answers
		// the method of that kind as unserved, for the method's own
		// reason.
		left := map[string][2]proto.Message{}
		if len(plugin.Description.GetRelease().GetBranchings()) == 0 {
			left["Branch"] = [2]proto.Message{&pluginv1.BranchRequest{Strategy: "unclaimed"}, new(pluginv1.BranchResponse)}
		}
		if len(plugin.Description.GetRelease().GetVersionings()) == 0 {
			left["Version"] = [2]proto.Message{&pluginv1.VersionRequest{Strategy: "unclaimed"}, new(pluginv1.VersionResponse)}
		}
		if len(plugin.Description.GetServices()) == 0 {
			left["Service"] = [2]proto.Message{&pluginv1.ServiceRequest{Name: "unclaimed"}, new(pluginv1.ServiceResponse)}
		}
		for method, call := range left {
			if _, err := plugin.Call(t.Context(), f.Source, method, call[0], call[1]); !errors.Is(err, pluginhost.ErrUnsupported) {
				t.Errorf("%s of a plugin that claims none must be unsupported, got %v", method, err)
			}
		}
	})
}

// describes holds Describe to the fixture: the same answer as when the
// module was loaded, with nothing read.
func describes(t *testing.T, plugin *pluginhost.Plugin, f Fixture, source pluginhost.Source) {
	t.Run("Describe", func(t *testing.T) {
		answer := new(pluginv1.DescribeResponse)
		reads, err := plugin.Call(t.Context(), source, "Describe", &pluginv1.DescribeRequest{}, answer)
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(answer, plugin.Description) || !proto.Equal(answer, f.Description) || len(reads) != 0 {
			t.Fatal("Describe changed or read source")
		}
		if plugin.ABI != f.ABI {
			t.Fatalf("the module speaks ABI %d, and the fixture is of ABI %d", plugin.ABI, f.ABI)
		}
	})
}

// unread is a source a strategy plugin is given: reading it fails, so a
// strategy that read a file would not answer.
type unread struct{}

var errRead = errors.New("a strategy plugin reads no file")

func (unread) Read(context.Context, string) ([]byte, bool, error) { return nil, false, errRead }
func (unread) Glob(context.Context, string, []string) ([]string, error) {
	return nil, errRead
}

// runStrategy holds a strategy plugin to its fixture through the host's
// real Wasm ABI: what it says it serves, each answer and each refusal,
// twice and at once, that it finds nothing and reads nothing, and that a
// cancelled or refused call leaves it as it was.
func runStrategy(t *testing.T, plugin *pluginhost.Plugin, f Fixture) {
	describes(t, plugin, f, unread{})
	ask := func(ctx context.Context, method string, request, answer proto.Message) error {
		reads, err := plugin.Call(ctx, unread{}, method, request, answer)
		if len(reads) != 0 {
			t.Errorf("%s read source: %+v", method, reads)
		}
		return err
	}
	// Every case, as a call that either gives its answer or is refused
	// with its reason.
	type asked struct {
		method  string
		request proto.Message
		want    proto.Message
		refused string
	}
	var cases []asked
	for _, c := range f.Branches {
		cases = append(cases, asked{"Branch", c.Request, c.Want, c.Refused})
	}
	for _, c := range f.Versions {
		cases = append(cases, asked{"Version", c.Request, c.Want, c.Refused})
	}
	for _, c := range f.Services {
		want := c.Want
		if want == nil {
			want = &pluginv1.ServiceResponse{}
		}
		cases = append(cases, asked{"Service", c.Request, want, c.Refused})
	}
	holds := func(t *testing.T, ctx context.Context, c asked) {
		t.Helper()
		answer := c.want.ProtoReflect().New().Interface()
		err := ask(ctx, c.method, c.request, answer)
		if c.refused != "" {
			var detail *pluginhost.Error
			if !errors.As(err, &detail) || detail.Reason != c.refused || detail.Detail.GetHelp() == "" {
				t.Errorf("%s %v: want a refusal %s with help, got %v", c.method, c.request, c.refused, err)
			}
			return
		}
		if err != nil || !proto.Equal(answer, c.want) {
			t.Errorf("%s %v:\n got %v, %v\nwant %v", c.method, c.request, answer, err, c.want)
		}
		// What it says of a service keeps what was written, as the host
		// holds every such answer.
		if started, is := answer.(*pluginv1.ServiceResponse); is && err == nil {
			if err := pluginhost.CheckService(f.Name, c.request.(*pluginv1.ServiceRequest), started); err != nil {
				t.Errorf("Service %v: %v", c.request, err)
			}
		}
	}
	t.Run("answers are the fixture's, and the same each time", func(t *testing.T) {
		for range 2 {
			for _, c := range cases {
				holds(t, t.Context(), c)
			}
		}
	})
	t.Run("it finds nothing and reads nothing", func(t *testing.T) {
		for _, profile := range []typev1.Profile{typev1.Profile_PROFILE_DEVELOPMENT, typev1.Profile_PROFILE_PRODUCTION} {
			answer := new(pluginv1.AnalyzeResponse)
			request := &pluginv1.AnalyzeRequest{Context: &pluginv1.Context{Profile: profile}, Roots: []string{"", "app"}}
			if err := ask(t.Context(), "Analyze", request, answer); err != nil {
				t.Fatal(err)
			}
			if err := pluginhost.CheckAnalysis(f.Name, request.GetRoots(), answer); err != nil {
				t.Fatal(err)
			}
			for _, a := range answer.GetAnalyses() {
				if len(a.GetProjects())+len(a.GetEntities()) != 0 {
					t.Fatalf("a strategy plugin found something: %v", a)
				}
			}
		}
		for method, request := range map[string]proto.Message{
			"Report":    &pluginv1.ReportRequest{Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}, Action: "lint", ExitCode: 1},
			"Reconcile": &pluginv1.ReconcileRequest{Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}},
		} {
			answer := map[string]proto.Message{"Report": new(pluginv1.ReportResponse), "Reconcile": new(pluginv1.ReconcileResponse)}[method]
			if err := ask(t.Context(), method, request, answer); !errors.Is(err, pluginhost.ErrUnsupported) {
				t.Errorf("a strategy plugin must answer %s as unsupported, got %v", method, err)
			}
		}
	})
	t.Run("cancelled calls leave the plugin usable", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		for _, c := range cases {
			if err := ask(ctx, c.method, c.request, c.want.ProtoReflect().New().Interface()); err == nil {
				t.Fatalf("a cancelled %s was answered", c.method)
			}
		}
		for _, c := range cases {
			holds(t, t.Context(), c)
		}
	})
	t.Run("concurrent calls agree", func(t *testing.T) {
		var joined sync.WaitGroup
		for range 8 {
			joined.Go(func() {
				for _, c := range cases {
					holds(t, t.Context(), c)
				}
			})
		}
		joined.Wait()
	})
	t.Run("unknown methods are explicit", func(t *testing.T) {
		if err := ask(t.Context(), "UnknownMethod", &pluginv1.DescribeRequest{}, new(pluginv1.DescribeResponse)); !errors.Is(err, pluginhost.ErrUnsupported) {
			t.Fatalf("unknown method: %v", err)
		}
	})
}

// validateStrategy rejects a strategy fixture that could pass with a
// plugin that does nothing: a branching strategy the plugin says it
// serves is asked of a branch that releases and of one that does not; a
// versioning one of a unit that moves, of one that holds, and something
// it refuses; a strategy the plugin does not say is asked, and refused;
// and no answer is expected of one.
func validateStrategy(name string, f Fixture) error {
	roles := f.Description.GetRelease()
	services := f.Description.GetServices()
	if (roles == nil && len(services) == 0) || len(f.Description.GetInterests()) != 0 {
		return fmt.Errorf("%s: a fixture of answers alone describes release roles or what it starts, and no interests", name)
	}
	if f.Analyze != nil || f.Source != nil || f.Arrange != nil || len(f.Projects)+len(f.Entities) != 0 {
		return fmt.Errorf("%s: a strategy fixture has no source to analyze or arrange", name)
	}
	type seen struct{ yes, no, refused bool }
	branchings, versionings := map[string]*seen{}, map[string]*seen{}
	for _, s := range roles.GetBranchings() {
		branchings[s] = &seen{}
	}
	for _, s := range roles.GetVersionings() {
		versionings[s] = &seen{}
	}
	// What each kind of answer does not serve is asked of it, and refused:
	// a refusal of one kind proves nothing of another.
	unserved, unclaimed := 0, 0
	for i, c := range f.Branches {
		if c.Request == nil || (c.Want == nil) == (c.Refused == "") {
			return fmt.Errorf("%s: branch case %d needs a request and either an answer or a refusal", name, i)
		}
		got, known := branchings[c.Request.GetStrategy()]
		switch {
		case !known && c.Refused != "":
			unserved++
		case !known:
			return fmt.Errorf("%s: branch case %d asks a strategy %q the plugin does not say, and expects an answer", name, i, c.Request.GetStrategy())
		case c.Refused != "":
			got.refused = true
		case c.Want.GetReleases():
			got.yes = true
		default:
			got.no = true
		}
	}
	for i, c := range f.Versions {
		if c.Request == nil || (c.Want == nil) == (c.Refused == "") {
			return fmt.Errorf("%s: version case %d needs a request and either an answer or a refusal", name, i)
		}
		got, known := versionings[c.Request.GetStrategy()]
		switch {
		case !known && c.Refused != "":
			unserved++
		case !known:
			return fmt.Errorf("%s: version case %d asks a strategy %q the plugin does not say, and expects an answer", name, i, c.Request.GetStrategy())
		case c.Refused != "":
			got.refused = true
		case c.Want.GetVersion() != "":
			got.yes = true
		default:
			got.no = true
		}
	}
	// What it claims to service is asked both ways: with nothing written,
	// where all of it is the plugin's, and with something written, which
	// it must keep. A name it does not claim is asked, and refused.
	started := map[string]*seen{}
	for _, l := range services {
		started[l] = &seen{}
	}
	for i, c := range f.Services {
		if c.Request == nil || (c.Want == nil) == (c.Refused == "") {
			return fmt.Errorf("%s: service case %d needs a request and either an answer or a refusal", name, i)
		}
		got, known := started[c.Request.GetName()]
		switch {
		case !known && c.Refused != "":
			unclaimed++
		case !known:
			return fmt.Errorf("%s: service case %d asks a name %q the plugin does not claim, and expects an answer", name, i, c.Request.GetName())
		case c.Refused != "":
			got.refused = true
		case proto.Size(c.Request.GetWritten()) == 0:
			got.no = true
		default:
			got.yes = true
		}
	}
	for l, got := range started {
		if !got.yes || !got.no {
			return fmt.Errorf("%s: what is started as %s needs a service with nothing written and one with something written", name, l)
		}
	}
	for s, got := range branchings {
		if !got.yes || !got.no {
			return fmt.Errorf("%s: the branching strategy %s needs a branch that releases and one that does not", name, s)
		}
	}
	for s, got := range versionings {
		if !got.yes || !got.no || !got.refused {
			return fmt.Errorf("%s: the versioning strategy %s needs a unit that moves, one that holds and a request it refuses", name, s)
		}
	}
	if roles != nil && unserved == 0 {
		return fmt.Errorf("%s: a strategy the plugin does not serve must be asked, and refused", name)
	}
	if len(services) > 0 && unclaimed == 0 {
		return fmt.Errorf("%s: a name the plugin does not claim to service must be asked, and refused", name)
	}
	return nil
}

// The same faults are injected at each plugin's independently declared reads,
// including optional absent files. A plugin cannot turn a failed read into a
// successful empty discovery, even if it never uses the returned content.
type failingSource struct {
	pluginhost.Source
	operation Observation
	err       error
}

func (s failingSource) Read(ctx context.Context, path string) ([]byte, bool, error) {
	if s.operation.Import == "read" && s.operation.Argument == path {
		return nil, false, s.err
	}
	return s.Source.Read(ctx, path)
}

func (s failingSource) Glob(ctx context.Context, directory string, patterns []string) ([]string, error) {
	if s.operation.Import == "glob" && s.operation.Directory == directory && s.operation.Argument == strings.Join(patterns, "\x00") {
		return nil, s.err
	}
	return s.Source.Glob(ctx, directory, patterns)
}

// ValidateFixtures rejects gaps before loading modules. An empty successful
// arrangement is explicit (a non-nil empty Files slice), never missing coverage.
func ValidateFixtures(plugins []string, fixtures map[string]Fixture) error {
	if len(plugins) != len(fixtures) {
		return fmt.Errorf("%d plugins, %d fixtures", len(plugins), len(fixtures))
	}
	seen := map[string]bool{}
	for _, name := range plugins {
		f, ok := fixtures[name]
		if !ok || seen[name] || f.Name != name {
			return fmt.Errorf("plugin %q needs exactly one matching fixture", name)
		}
		seen[name] = true
		if f.Description == nil {
			return fmt.Errorf("%s: missing Describe expectations", name)
		}
		if f.strategy() {
			if err := validateStrategy(name, f); err != nil {
				return err
			}
			continue
		}
		// A plugin that reads a file's content is shown it malformed; one
		// that reads none, found by a file's being there, has no
		// malformed source and names no error, and Run holds it to
		// having read none.
		reads := false
		for _, read := range f.Reads {
			reads = reads || read.Import == "read"
		}
		if f.Analyze == nil || f.Source == nil || f.Absent == nil || len(f.Projects)+len(f.Entities) == 0 || len(f.Reads) == 0 || reads && f.Malformed == nil || (f.Malformed == nil) != (f.ErrorCode == "") {
			return fmt.Errorf("%s: missing Analyze success/error/read coverage", name)
		}
		if f.Arrange == nil || f.Files == nil || f.InvalidArrange == nil || f.ArrangeErrorCode == "" {
			return fmt.Errorf("%s: missing Arrange success/error coverage", name)
		}
	}
	return nil
}
