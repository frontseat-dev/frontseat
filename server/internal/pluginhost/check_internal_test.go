package pluginhost

import (
	"strings"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// A plugin that says it speaks an ABI outside what this host loads is
// refused, by number: the contract it was built for is not this one. The
// control is each end of the range.
func TestAPluginOfAnotherABIIsRefused(t *testing.T) {
	for _, abi := range []uint32{MinABI, MaxABI} {
		if err := checkDescription(&pluginv1.DescribeResponse{Name: "tool", Abi: abi}); err != nil {
			t.Errorf("ABI %d was refused: %v", abi, err)
		}
	}
	for _, abi := range []uint32{0, MinABI - 1, MaxABI + 1} {
		err := checkDescription(&pluginv1.DescribeResponse{Name: "tool", Abi: abi})
		if err == nil || !strings.Contains(err.Error(), "says it speaks ABI") {
			t.Errorf("ABI %d: err = %v, want it refused by number", abi, err)
		}
	}
}

// What a repository sets is held to the options a plugin declares, so a
// plugin whose declarations do not hold together is not loaded.
func TestAPluginWhoseOptionsDoNotHoldTogetherIsRefused(t *testing.T) {
	described := func(options ...*pluginv1.Option) *pluginv1.DescribeResponse {
		return &pluginv1.DescribeResponse{Name: "tool", Abi: MinABI, Options: options}
	}
	list := &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{Values: []string{"a", "b"}}}}
	sound := []*pluginv1.Option{
		{Name: "flavour", Kind: pluginv1.Option_KIND_STRING, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: "plain"}}},
		{Name: "strict", Kind: pluginv1.Option_KIND_BOOL, Default: &typev1.OptionValue{Value: &typev1.OptionValue_BoolValue{BoolValue: false}}},
		{Name: "jobs", Kind: pluginv1.Option_KIND_INT, Default: &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: 4}}},
		{Name: "tags", Kind: pluginv1.Option_KIND_STRING_LIST, Default: list},
	}
	if err := checkDescription(described(sound...)); err != nil {
		t.Fatalf("a plugin with one sound option of each kind was refused: %v", err)
	}

	for name, options := range map[string][]*pluginv1.Option{
		"a name that is not lower-case":    {{Name: "Flavour", Kind: pluginv1.Option_KIND_STRING, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: ""}}}},
		"no name":                          {{Kind: pluginv1.Option_KIND_STRING, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: ""}}}},
		"one name twice":                   {sound[0], sound[0]},
		"no kind":                          {{Name: "flavour", Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: ""}}}},
		"no default":                       {{Name: "flavour", Kind: pluginv1.Option_KIND_STRING}},
		"a switch whose default is text":   {{Name: "strict", Kind: pluginv1.Option_KIND_BOOL, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: "true"}}}},
		"an integer with no value":         {{Name: "jobs", Kind: pluginv1.Option_KIND_INT, Default: &typev1.OptionValue{}}},
		"an integer with a string default": {{Name: "jobs", Kind: pluginv1.Option_KIND_INT, Default: sound[0].Default}},
		"a list with a nil message":        {{Name: "tags", Kind: pluginv1.Option_KIND_STRING_LIST, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{}}}},
	} {
		if err := checkDescription(described(options...)); err == nil {
			t.Errorf("a plugin that declares %s was loaded", name)
		}
	}
}

// What a plugin says a check found is held to the contract: a rule it
// qualifies, a message, a file of the repository or none, and never the
// repository's name, which is the host's.
func TestWhatACheckFoundIsHeldToTheContract(t *testing.T) {
	sound := &pluginv1.ReportResponse{Diagnostics: []*typev1.Diagnostic{
		{RuleId: "go/vet", Message: "Printf format %d has arg of wrong type", SourceLocation: &typev1.SourceLocation{Path: "app/main.go", Line: 6}},
		{RuleId: "go/vet", Message: "something about no file"},
	}}
	if err := CheckReport("go", sound); err != nil {
		t.Fatalf("a sound report was refused: %v", err)
	}
	many := &pluginv1.ReportResponse{}
	for range FindingsAtMost + 1 {
		many.Diagnostics = append(many.Diagnostics, &typev1.Diagnostic{RuleId: "go/vet", Message: "x"})
	}
	for name, response := range map[string]*pluginv1.ReportResponse{
		"more findings than a check may have": many,
		"a rule of another plugin":            {Diagnostics: []*typev1.Diagnostic{{RuleId: "buf/lint", Message: "x"}}},
		"a rule with no name":                 {Diagnostics: []*typev1.Diagnostic{{RuleId: "go/", Message: "x"}}},
		"no rule":                             {Diagnostics: []*typev1.Diagnostic{{Message: "x"}}},
		"nothing said":                        {Diagnostics: []*typev1.Diagnostic{{RuleId: "go/vet", Message: "  "}}},
		"a path out of the repository":        {Diagnostics: []*typev1.Diagnostic{{RuleId: "go/vet", Message: "x", SourceLocation: &typev1.SourceLocation{Path: "../main.go"}}}},
		"an absolute path":                    {Diagnostics: []*typev1.Diagnostic{{RuleId: "go/vet", Message: "x", SourceLocation: &typev1.SourceLocation{Path: "/etc/passwd"}}}},
		"a repository named":                  {Diagnostics: []*typev1.Diagnostic{{RuleId: "go/vet", Message: "x", SourceLocation: &typev1.SourceLocation{Repository: "lab", Path: "main.go"}}}},
		"a line below zero":                   {Diagnostics: []*typev1.Diagnostic{{RuleId: "go/vet", Message: "x", SourceLocation: &typev1.SourceLocation{Path: "main.go", Line: -1}}}},
	} {
		if err := CheckReport("go", response); err == nil {
			t.Errorf("a report with %s was accepted", name)
		}
	}
}

// An action's rewrite patterns are patterns of files beneath its project.
func TestRewritePatternsAreHeldToTheProject(t *testing.T) {
	if err := CheckRewrites("the action format", []string{"**/*.go", "!**/testdata/**", "f.txt"}); err != nil {
		t.Fatalf("sound patterns were refused: %v", err)
	}
	for _, refused := range [][]string{{"../other.txt"}, {"/etc/passwd"}, {"=sources"}, {"["}, {""}} {
		if err := CheckRewrites("the action format", refused); err == nil {
			t.Errorf("%q was accepted as rewrite patterns", refused)
		}
	}
}

// A fix's environment is its own. What the host sets, where its programs
// are found and what mise reads, it cannot set: a fix that did would find
// a program the host did not give it.
func TestAFixCannotSetWhatTheHostSets(t *testing.T) {
	sound := func(env map[string]string) *pluginv1.Action {
		return &pluginv1.Action{Name: "format", Check: true, Rewrites: []string{"**/*.go"}, Fix: &pluginv1.Command{Arguments: []string{"gofmt", "-w", "."}, Environment: env}}
	}
	if err := CheckFix("the action format", sound(map[string]string{"GOFLAGS": "-mod=mod"})); err != nil {
		t.Fatalf("a fix with an environment of its own was refused: %v", err)
	}
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "MISE_DATA_DIR", "MISE_OFFLINE"} {
		err := CheckFix("the action format", sound(map[string]string{name: "x"}))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("a fix that sets %s was accepted or refused without naming it: %v", name, err)
		}
	}
}

// A plugin says which branching and versioning strategies it serves,
// each by a name of its own: a role that names no strategy, a name said
// twice and a name that is none are refused.
func TestWhatAPluginSaysItServesOfReleasingIsHeldToWhatIsServed(t *testing.T) {
	sound := &pluginv1.ReleaseRoles{Branchings: []string{"release-flow"}, Versionings: []string{"conventional-commits"}}
	if err := checkDescription(&pluginv1.DescribeResponse{Name: "tool", Abi: MaxABI, Release: sound}); err != nil {
		t.Fatalf("a branching and a versioning strategy were refused, so this test proves nothing: %v", err)
	}
	if err := checkDescription(&pluginv1.DescribeResponse{Name: "tool", Abi: MaxABI}); err != nil {
		t.Fatalf("a plugin with no release role was refused: %v", err)
	}
	for name, c := range map[string]struct {
		roles *pluginv1.ReleaseRoles
		says  string
	}{
		"no role":                   {&pluginv1.ReleaseRoles{}, "says none"},
		"a branching named twice":   {&pluginv1.ReleaseRoles{Branchings: []string{"release-flow", "release-flow"}}, "twice"},
		"a versioning named twice":  {&pluginv1.ReleaseRoles{Versionings: []string{"plans", "plans"}}, "twice"},
		"a branching with no name":  {&pluginv1.ReleaseRoles{Branchings: []string{""}}, "lower-case"},
		"a branching in upper case": {&pluginv1.ReleaseRoles{Branchings: []string{"Release-Flow"}}, "lower-case"},
		"a versioning with a colon": {&pluginv1.ReleaseRoles{Versionings: []string{"release:plans"}}, "lower-case"},
	} {
		err := checkDescription(&pluginv1.DescribeResponse{Name: "tool", Abi: MaxABI, Release: c.roles})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: what a plugin says it serves of releasing was taken, or refused for another reason: %v", name, err)
		}
	}
}

// What a plugin says it starts is names, each said once: a name that
// is none, or said twice, is refused, beside two that are different.
func TestTheNamesAPluginStartsAreNamesEachSaidOnce(t *testing.T) {
	described := func(services ...string) *pluginv1.DescribeResponse {
		return &pluginv1.DescribeResponse{Name: "p", Abi: MaxABI, Services: services}
	}
	if err := checkDescription(described("postgres", "timescale-db")); err != nil {
		t.Fatalf("two names, each once: %v", err)
	}
	for _, services := range [][]string{{"postgres", "postgres"}, {""}, {"Postgres"}, {"-x"}, {"a/b"}, {"a--b"}, {"a b"}} {
		if err := checkDescription(described(services...)); err == nil {
			t.Errorf("a plugin that starts %q was loaded", services)
		}
	}
}
