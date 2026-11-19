package pluginhost_test

import (
	"errors"
	"math"
	"strconv"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

// This is an SDK transport fixture, not a substitute for any ecosystem build.
func TestSDKOptionsThroughWasm(t *testing.T) {
	plugin := conformance.SDKExample(t, "options")
	values := func(integer int64, boolean bool, text string, list []string) *pluginv1.Context {
		return &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT, Options: map[string]*typev1.OptionValue{
			"int":    {Value: &typev1.OptionValue_IntValue{IntValue: integer}},
			"bool":   {Value: &typev1.OptionValue_BoolValue{BoolValue: boolean}},
			"string": {Value: &typev1.OptionValue_StringValue{StringValue: text}},
			"list":   {Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{Values: list}}},
		}}
	}
	for _, integer := range []int64{math.MinInt64, -9007199254740993, 0, 9007199254740993, math.MaxInt64} {
		for _, boolean := range []bool{false, true} {
			for _, list := range [][]string{nil, {"", "a, b", "a", "b"}} {
				context := values(integer, boolean, "", list)
				response := new(pluginv1.ArrangeResponse)
				reads, err := plugin.Call(t.Context(), files{"contract.txt": "fixture"}, "Arrange", &pluginv1.ArrangeRequest{Context: context}, response)
				if err != nil {
					t.Fatal(err)
				}
				if len(reads) != 1 {
					t.Fatalf("valid call read %v, want contract.txt", reads)
				}
				want := &pluginv1.ArrangeResponse{Files: []*pluginv1.File{
					{Path: "integer", Content: []byte(strconv.FormatInt(integer, 10))},
					{Path: "boolean", Content: []byte(strconv.FormatBool(boolean))},
					{Path: "string"},
				}}
				for i, entry := range list {
					want.Files = append(want.Files, &pluginv1.File{Path: "item-" + strconv.Itoa(i), Content: []byte(entry)})
				}
				if !proto.Equal(response, want) {
					t.Fatalf("options changed in Wasm: got %v; want %v", response, want)
				}
			}
		}
	}
	for _, method := range []string{"Analyze", "Arrange"} {
		for _, change := range []func(map[string]*typev1.OptionValue){
			func(options map[string]*typev1.OptionValue) { delete(options, "int") },
			func(options map[string]*typev1.OptionValue) { options["bool"] = &typev1.OptionValue{} },
			func(options map[string]*typev1.OptionValue) {
				options["int"] = &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: "1"}}
			},
			func(options map[string]*typev1.OptionValue) { options["unknown"] = options["bool"] },
		} {
			context := values(0, false, "", nil)
			change(context.Options)
			source := &pluginhost.Recorder{Source: files{"contract.txt": "fixture"}}
			var request, response proto.Message
			if method == "Analyze" {
				request, response = &pluginv1.AnalyzeRequest{Context: context}, new(pluginv1.AnalyzeResponse)
			} else {
				request, response = &pluginv1.ArrangeRequest{Context: context}, new(pluginv1.ArrangeResponse)
			}
			_, err := plugin.Call(t.Context(), source, method, request, response)
			var detail *pluginhost.Error
			if !errors.As(err, &detail) || detail.Reason != "INVALID_OPTION" {
				t.Fatalf("%s accepted invalid options: %v", method, err)
			}
			if detail.Status.GetCode() != int32(codes.InvalidArgument) || detail.Detail.GetSourceLocation() != nil || detail.Detail.GetHelp() == "" {
				t.Fatalf("%s lost the source-free diagnostic's status or remediation: %v", method, detail.Status)
			}
			if reads := source.Reads(); len(reads) != 0 {
				t.Fatalf("%s read source before option validation: %v", method, reads)
			}
		}
	}
}
