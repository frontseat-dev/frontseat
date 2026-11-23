package pluginhost_test

import (
	"context"
	"errors"
	"fmt"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	"google.golang.org/grpc/codes"
)

// These readers execute as Wasm, including their parser dependencies. This
// fixture proves SDK behavior; ecosystem builds remain separate API specs.
func TestSDKDocumentsThroughWasm(t *testing.T) {
	plugin := documentPlugin(t)
	for _, test := range []struct {
		name, path, source, reason string
		contains                   []string
	}{
		{"toml integers", "pkg[1]/Cargo.toml", "min = -9223372036854775808\nmax = 9223372036854775807\nexact = 9007199254740993\n", "", []string{"-9223372036854775808", "9223372036854775807", "9007199254740993"}},
		{"toml duplicate", "Cargo.toml", "name = 'one'\nname = 'two'\n", "INVALID_TOML", nil},
		{"toml overflow", "Cargo.toml", "number = 9223372036854775808\n", "INVALID_TOML", nil},
		{"toml syntax", "Cargo.toml", "[package\n", "INVALID_TOML", nil},
		{"toml invalid text", "Cargo.toml", "name = 'bad\xff'\n", "INVALID_TEXT", nil},
		{"yaml documents", "pkg[1]/catalog-info.yaml", "name: one\n---\nname: two\n", "", []string{"one", "two"}},
		{"yaml alias", "catalog-info.yaml", "defaults: &defaults {owner: team}\nspec:\n  <<: *defaults\n  name: component\n", "", []string{"team", "component"}},
		{"yaml duplicate", "catalog-info.yaml", "name: one\nname: two\n", "INVALID_YAML", nil},
		{"yaml nested duplicate", "catalog-info.yaml", "metadata: {name: one, name: two}\n", "INVALID_YAML", nil},
		{"yaml undefined alias", "catalog-info.yaml", "spec: *missing\n", "INVALID_YAML", nil},
		{"yaml cross-document alias", "catalog-info.yaml", "name: &name one\n---\nname: *name\n", "INVALID_YAML", nil},
		{"yaml recursive alias", "catalog-info.yaml", "spec: &loop [*loop]\n", "INVALID_YAML", nil},
		{"yaml syntax", "catalog-info.yaml", "name: [unterminated\n", "INVALID_YAML", nil},
		{"yaml invalid text", "catalog-info.yaml", "name: bad\xff\n", "INVALID_TEXT", nil},
		{"yaml invalid escape", "catalog-info.yaml", "name: \"bad\\q\"\n", "INVALID_YAML", nil},
		{"yaml invalid unicode escape", "catalog-info.yaml", "name: \"bad\\uZZZZ\"\n", "INVALID_YAML", nil},
		{"yaml raw control", "catalog-info.yaml", "name: bad\x01\n", "INVALID_YAML", nil},
		{"yaml raw nul", "catalog-info.yaml", "name: bad\x00\n", "INVALID_YAML", nil},
		{"yaml tab indentation", "catalog-info.yaml", "spec:\n\tname: bad\n", "INVALID_YAML", nil},
		{"yaml invalid integer tag", "catalog-info.yaml", "name: !!int banana\n", "INVALID_YAML", nil},
		{"yaml invalid null document", "catalog-info.yaml", "!!null banana\n", "INVALID_YAML", nil},
		{"yaml invalid null value", "catalog-info.yaml", "name: !!null banana\n", "INVALID_YAML", nil},
		{"yaml invalid null key", "catalog-info.yaml", "!!null banana: value\n", "INVALID_YAML", nil},
		{"yaml invalid null URI", "catalog-info.yaml", "name: !<tag:yaml.org,2002:null> banana\n", "INVALID_YAML", nil},
		{"yaml invalid null tag directive", "catalog-info.yaml", "%TAG !custom! tag:yaml.org,2002:\n---\nname: !custom!null banana\n", "INVALID_YAML", nil},
		{"yaml invalid null block", "catalog-info.yaml", "name: !!null |-\n  banana\n", "INVALID_YAML", nil},
		{"yaml valid null", "catalog-info.yaml", "name: !!null null\n", "", []string{"Null"}},
		{"yaml valid null quoted", "catalog-info.yaml", "name: !!null 'Null'\n", "", []string{"Null"}},
		{"yaml valid null escaped", "catalog-info.yaml", "name: !!null \"n\\u0075ll\"\n", "", []string{"Null"}},
		{"yaml valid null block", "catalog-info.yaml", "name: !!null |-\n  null\n", "", []string{"Null"}},
		{"yaml null tag in text", "catalog-info.yaml", "name: '!!null banana'\n", "", []string{"!!null banana"}},
		{"yaml unknown tag", "catalog-info.yaml", "name: !include /etc/passwd\n", "INVALID_YAML", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := files{test.path: test.source}
			out := new(pluginv1.ArrangeResponse)
			reads, err := plugin.Call(t.Context(), source, "Arrange", &pluginv1.ArrangeRequest{Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}, Root: test.path}, out)
			if test.reason != "" {
				var failure *pluginhost.Error
				if !errors.As(err, &failure) || failure.Reason != test.reason {
					t.Fatalf("want %s; got %v", test.reason, err)
				}
				if failure.Status.GetCode() != int32(codes.FailedPrecondition) || failure.Detail.GetSourceLocation().GetPath() != test.path || failure.Detail.GetHelp() == "" {
					t.Fatalf("lost diagnostic attribution: %v", failure.Status)
				}
				if test.reason == "INVALID_TOML" && failure.Detail.GetSourceLocation().GetLine() == 0 {
					t.Fatalf("lost TOML line: %v", failure.Detail)
				}
				// Repair the same path and reuse the plugin after the failed call.
				source[test.path] = "name: repaired\n"
				if strings.HasSuffix(test.path, ".toml") {
					source[test.path] = "name = 'repaired'\n"
				}
				reads, err = plugin.Call(t.Context(), source, "Arrange", &pluginv1.ArrangeRequest{Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}, Root: test.path}, out)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(reads) != 1 || reads[0].Argument != test.path || !reads[0].Found || reads[0].Digest == "" {
				t.Fatalf("lost tracked document read: %v", reads)
			}
			if len(out.Files) != 1 {
				t.Fatalf("want one parsed result: %v", out)
			}
			for _, wanted := range test.contains {
				if !strings.Contains(string(out.Files[0].Content), wanted) {
					t.Fatalf("parsed result lost %q: %s", wanted, out.Files[0].Content)
				}
			}
		})
	}
	for _, path := range []string{"Cargo.toml", "catalog-info.yaml"} {
		for _, present := range []bool{false, true} {
			source := files{}
			if present {
				source[path] = ""
			}
			out := new(pluginv1.ArrangeResponse)
			reads, err := plugin.Call(t.Context(), source, "Arrange", &pluginv1.ArrangeRequest{Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}, Root: path}, out)
			if err != nil {
				t.Fatal(err)
			}
			if len(reads) != 1 || reads[0].Found != present || (len(out.Files) == 1) != present {
				t.Fatalf("%s conflated absence with empty content: %v, %v", path, reads, out)
			}
		}
	}
}

func TestSDKYAMLAliasExpansionIsBounded(t *testing.T) {
	plugin := documentPlugin(t)
	for _, depth := range []int{4, 18, 4} {
		var input strings.Builder
		input.WriteString("a0: &a0 x\n")
		for level := 1; level <= depth; level++ {
			fmt.Fprintf(&input, "a%d: &a%d [*a%d, *a%d]\n", level, level, level-1, level-1)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, err := plugin.Call(ctx, files{"catalog-info.yaml": input.String()}, "Arrange", &pluginv1.ArrangeRequest{Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}, Root: "catalog-info.yaml"}, new(pluginv1.ArrangeResponse))
		cancel()
		if depth == 4 {
			if err != nil {
				t.Fatalf("valid alias control failed: %v", err)
			}
			continue
		}
		var failure *pluginhost.Error
		if !errors.As(err, &failure) || failure.Reason != "INVALID_YAML" || !strings.Contains(failure.Status.GetMessage(), "limit") || failure.Detail.GetSourceLocation().GetPath() != "catalog-info.yaml" {
			t.Fatalf("expansion must return a source diagnostic, not succeed or trap: %v", err)
		}
	}
}

func documentPlugin(t testing.TB) *pluginhost.Plugin {
	t.Helper()
	return conformance.SDKExample(t, "documents")
}

// BenchmarkDocumentParsing includes an actual tracked read, parser, semantic
// conversion and response across Wasm. Setup and compilation are excluded.
func BenchmarkDocumentParsing(b *testing.B) {
	for _, test := range documentBenchmarks {
		b.Run(test.path, func(b *testing.B) {
			benchmarkDocumentParsing(b, test.path, test.source)
		})
	}
}

var documentBenchmarks = []struct{ path, source string }{
	{"Cargo.toml", "[package]\nname = 'example'\nversion = '1.0.0'\n[dependencies]\nprost = '0.14'\n"},
	{"catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: example\nspec:\n  type: service\n  owner: team\n  lifecycle: production\n"},
}

func benchmarkDocumentParsing(b *testing.B, path, text string) {
	plugin := documentPlugin(b)
	source := files{path: text}
	request := &pluginv1.ArrangeRequest{Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}, Root: path}
	for b.Loop() {
		if _, err := plugin.Call(b.Context(), source, "Arrange", request, new(pluginv1.ArrangeResponse)); err != nil {
			b.Fatal(err)
		}
	}
}

// Measured on Apple M4 Max with `mise exec -- go test -run '^$' -bench
// 'Benchmark(DocumentParsing|Load|Analyze)$' -benchmem -count=3
// ./build/engine/pluginhost`: TOML 112.3–115.7 us/1026 allocations,
// YAML 196.6–200.8 us/1027 allocations including null-schema validation.
// Each allowance has roughly 10% allocation headroom and 10x time headroom.
// Wazero allocations are measured here;
// guest allocations are bounded by parser budgets and Wasm memory, not counted.
func TestBudgetOfDocumentParsing(t *testing.T) {
	for _, test := range documentBenchmarks {
		t.Run(test.path, func(t *testing.T) {
			testbudget.Hold(t, testbudget.Budget{Time: 2 * time.Millisecond, Allocs: 1130}, func(b *testing.B) {
				benchmarkDocumentParsing(b, test.path, test.source)
			})
		})
	}
}
