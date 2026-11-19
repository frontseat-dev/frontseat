package conformance

import (
	"context"
	"errors"
	"testing"
)

type sourceControl struct{}

func (sourceControl) Read(context.Context, string) ([]byte, bool, error) {
	return []byte("source"), true, nil
}

func (sourceControl) Glob(context.Context, string, []string) ([]string, error) {
	return []string{"pkg/main.go"}, nil
}

func TestFaultInjectionSelectsTheDeclaredOperation(t *testing.T) {
	failure := errors.New("source unavailable")
	source := failingSource{Source: sourceControl{}, operation: Observation{Import: "glob", Directory: "pkg", Argument: "**/*.go\x00!**/*_test.go"}, err: failure}
	for _, call := range []struct {
		directory string
		patterns  []string
		fail      bool
	}{
		{"pkg", []string{"**/*.toml"}, false},
		{"pkg", []string{"**/*.go"}, false},
		{"other", []string{"**/*.go", "!**/*_test.go"}, false},
		{"pkg", []string{"**/*.go", "!**/*_test.go"}, true},
		{"pkg", []string{"**/*.toml"}, false},
	} {
		paths, err := source.Glob(t.Context(), call.directory, call.patterns)
		if errors.Is(err, failure) != call.fail || (!call.fail && len(paths) != 1) {
			t.Fatalf("directory=%q patterns=%v: got paths=%v err=%v, want failure=%v", call.directory, call.patterns, paths, err, call.fail)
		}
	}
}
