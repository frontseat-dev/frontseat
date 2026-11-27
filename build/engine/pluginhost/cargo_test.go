package pluginhost_test

import (
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/pluginhosttest"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"testing"
)

// The Cargo fixture is a workspace of two packages: hello depends on
// greet by path, greet on one locked crate through the workspace's
// dependencies. Everything here is spelled out independently of the
// Rust implementation.
func cargoFixture(t *testing.T) conformance.Fixture {
	t.Helper()
	tools := map[string]string{"rust": "1.99.1"}
	// Its one option, wasm, at its default: nothing is built for wasm.
	options := map[string]*typev1.OptionValue{"wasm": {Value: &typev1.OptionValue_BoolValue{BoolValue: false}}}
	with := &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT, Tools: tools, Options: options}
	without := &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT, Options: options}
	narrowed := "[workspace]\nmembers = [\"greet\", \"hello\"]\nresolver = \"2\"\n\n[workspace.dependencies]\nitoa = \"1\"\n\n[workspace.package]\nedition = \"2021\"\nversion = \"0.1.0\"\n"
	return conformance.Fixture{
		ABI: 26, Description: &pluginv1.DescribeResponse{Name: "cargo", Interests: []string{"**/Cargo.toml", "**/Cargo.lock", "**/*.rs", "**/.cargo/config.toml", "**/rustfmt.toml", "**/.rustfmt.toml", "**/clippy.toml", "**/.clippy.toml", "!**/target/**", "!**/testdata/**"}, Options: []*pluginv1.OptionDeclaration{{Name: "wasm", Kind: pluginv1.OptionDeclaration_KIND_BOOL, Default: &typev1.OptionValue{Value: &typev1.OptionValue_BoolValue{BoolValue: false}}, EntitySettable: true}}},
		Name: "cargo", Source: fixtureSource(t, "cargo", "valid"),
		Absent: files{}, Malformed: fixtureSource(t, "cargo", "malformed"), ErrorCode: "INVALID_TOML",
		Projects: []*pluginv1.Project{{
			Root: "hello", Name: "hello", Kind: "cargo-package", Manifest: "hello/Cargo.toml",
			Actions:        expectedCargoActions(),
			InputSets:      []*pluginv1.InputSet{{Name: "package", AcceptsGenerated: true, Inputs: []*pluginv1.Input{pluginhost.SourceInput("**"), pluginhost.SourceInput("!target/**")}}},
			Provides:       []*pluginv1.Provision{{Coordinate: &pluginv1.Coordinate{Ecosystem: "cargo", Name: "hello"}, Sources: "package"}},
			Requires:       []*pluginv1.Requirement{{Coordinate: &pluginv1.Coordinate{Ecosystem: "cargo", Name: "greet"}, Origin: &pluginv1.Origin{Path: "hello/Cargo.toml", Line: 7}, Version: "0.1.0"}},
			AlwaysArranged: true,
		}},
		Reads: []conformance.Observation{
			{Import: "read", Argument: "hello/Cargo.toml", Found: true},
			{Import: "read", Argument: "Cargo.toml", Found: true},
			{Import: "glob", Argument: "greet/Cargo.toml\x00hello/Cargo.toml", Directory: "", Found: true},
			{Import: "read", Argument: "Cargo.lock", Found: true},
			{Import: "read", Argument: ".cargo/config.toml", Found: false},
			{Import: "glob", Argument: "**/Cargo.toml\x00!target/**\x00!**/testdata/**", Directory: "hello", Found: true},
		},
		Analyze:          &pluginv1.AnalyzeRequest{Roots: []string{"hello"}, Context: with},
		Arrange:          &pluginv1.ArrangeRequest{Root: "hello", Context: with, Actions: []string{"compile"}, Handed: []*pluginv1.Handoff{{Coordinate: &pluginv1.Coordinate{Ecosystem: "cargo", Name: "greet"}, Into: "../greet"}}},
		Files:            []*pluginv1.File{{Path: "Cargo.toml", Content: []byte(narrowed)}, {Path: "Cargo.lock", Content: []byte(fixtureSource(t, "cargo", "valid")["Cargo.lock"])}},
		InvalidArrange:   &pluginv1.ArrangeRequest{Root: "hello", Context: without},
		ArrangeErrorCode: "UNPINNED_TOOL", ArrangeErrorPath: "hello/Cargo.toml",
		Report: &pluginv1.ReportRequest{
			Context: with, Root: "hello", Action: "lint", ExitCode: 101,
			Stderr: []byte("    Checking hello v0.1.0\nhello/src/main.rs:2:9: warning: unused variable: `x`\nwarning: `hello` (bin \"hello\") generated 1 warning\nerror: could not compile `hello` (bin \"hello\") due to 1 previous error; 1 warning emitted\n"),
		},
		Findings: []*typev1.Diagnostic{{
			RuleId:         "cargo/clippy",
			Message:        "unused variable: `x`",
			SourceLocation: &typev1.SourceLocation{Path: "hello/src/main.rs", Line: 2},
			Help:           "Fix what clippy reports; its lints are explained at https://rust-lang.github.io/rust-clippy/.",
		}},
	}
}

// Every action of a package: a shared fetch of each crate of crates.io
// the package depends on, which downloads the crate's archive by the
// lock's checksum into cargo's home, then cargo over the package in its
// own directory, from its lock, offline, reading the crates from there.
func expectedCargoActions() []*pluginv1.Action {
	fetch := func(name, version, checksum string) *pluginv1.Action {
		return &pluginv1.Action{
			Name: "fetch/" + name + "@" + version,
			Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{
				{Path: "~/.cargo/vendor/" + name + "-" + version + "/.cargo-checksum.json", Content: []byte(`{"files":{},"package":"` + checksum + `"}`)},
				{Path: "~/.cargo/config.toml", Content: []byte("[source.crates-io]\nreplace-with = \"vendored\"\n\n[source.vendored]\ndirectory = \".cargo/vendor\"\n")},
			}, Downloads: []*pluginv1.Download{{Url: "https://static.crates.io/crates/" + name + "/" + version + "/download", Sha256: checksum, Into: "~/.cargo/vendor/"}}}},
			Outputs: []*pluginv1.Output{{Path: "~/.cargo/vendor/" + name + "-" + version + "/"}, {Path: "~/.cargo/config.toml"}},
		}
	}
	cfgIf := fetch("cfg-if", "1.0.0", "baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd")
	itoa := fetch("itoa", "1.0.18", "8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682")
	action := func(name string, args ...string) *pluginv1.Action {
		return &pluginv1.Action{
			Name:    name,
			Command: &pluginv1.Command{Arguments: append([]string{"cargo"}, args...), Environment: map[string]string{"CARGO_TARGET_DIR": "target", "CARGO_INCREMENTAL": "0"}},
			Inputs:  append([]*pluginv1.Input{pluginhost.SetInput("package"), pluginhosttest.OutputInput(cfgIf.Name), pluginhosttest.OutputInput(itoa.Name)}, pluginhosttest.AllDependencies("{root}")), Tools: []string{"rust"}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}},
		}
	}
	compile := action("compile", "build", "--locked", "--offline")
	test := action("test", "test", "--locked", "--offline")
	test.Kind = &pluginv1.Action_Test{Test: &pluginv1.Test{}}
	lint := action("lint", "clippy", "--locked", "--offline", "--all-targets", "--message-format", "short", "--", "-D", "warnings")
	lint.Kind = &pluginv1.Action_Check{Check: &pluginv1.Check{}}
	format := action("format", "fmt", "--", "--check")
	format.Inputs = []*pluginv1.Input{pluginhost.SetInput("package"), pluginhosttest.AllDependencies("{root}")}
	format.Kind = &pluginv1.Action_Check{Check: &pluginv1.Check{
		Fix:      &pluginv1.Command{Arguments: []string{"cargo", "fmt"}, Environment: map[string]string{"CARGO_TARGET_DIR": "target", "CARGO_INCREMENTAL": "0"}},
		Rewrites: []string{"**/*.rs"},
	}}
	return []*pluginv1.Action{cfgIf, itoa, compile, test, lint, format}
}
