package gatecommand_test

import (
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/specs/internal/gatecommand"
)

// The gate's options are read as Go's flag package reads them, with one
// dash or two, and what follows -- or the first word that is no option is
// go's; a verb that takes nothing refuses more, and -except with -only is
// refused.
func TestAGateCommandIsReadAsTheGateReadsIt(t *testing.T) {
	for _, each := range []struct {
		arguments []string
		want      gatecommand.Command
	}{
		{[]string{"test", "--no-stack", "--except", "specs/build/", "--", "-race"}, gatecommand.Command{Verb: "test", NoStack: true, Except: []string{"specs/build"}, Go: []string{"-race"}}},
		{[]string{"test", "-only=specs/build", "--", "-race", "-v"}, gatecommand.Command{Verb: "test", Only: []string{"specs/build"}, Go: []string{"-race", "-v"}}},
		{[]string{"test", "--only", "a", "--only=b"}, gatecommand.Command{Verb: "test", Only: []string{"a", "b"}}},
		{[]string{"go-test", "-run", "X", "./p"}, gatecommand.Command{Verb: "go-test", Go: []string{"-run", "X", "./p"}}},
		{[]string{"vet"}, gatecommand.Command{Verb: "vet"}},
		{[]string{"generated"}, gatecommand.Command{Verb: "generated"}},
	} {
		got, err := gatecommand.Parse(each.arguments)
		if err != nil || got.Verb != each.want.Verb || got.NoStack != each.want.NoStack || !slices.Equal(got.Except, each.want.Except) || !slices.Equal(got.Only, each.want.Only) || !slices.Equal(got.Go, each.want.Go) {
			t.Errorf("%q: read %+v (%v), want %+v", each.arguments, got, err, each.want)
		}
	}
	for _, refused := range [][]string{nil, {"tests"}, {"vet", "./..."}, {"generated", "api"}, {"test", "-count=1"}, {"test", "-except", "a", "-only", "b"}, {"test", "-no-stack=maybe"}} {
		if got, err := gatecommand.Parse(refused); err == nil {
			t.Errorf("%q was read as %+v", refused, got)
		}
	}
}
