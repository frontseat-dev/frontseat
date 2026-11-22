package pluginhost

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// A plugin's interests are patterns of the contract's language
// (host.proto), each of files beneath the repository's root: one that is
// no pattern, or reaches out of the repository, is refused. The control
// is a plugin whose interests select and exclude as the language allows.
func TestAPluginWhoseInterestsAreNoPatternsIsRefused(t *testing.T) {
	described := func(interests ...string) *pluginv1.DescribeResponse {
		return &pluginv1.DescribeResponse{Name: "tool", Interests: interests}
	}
	if err := checkDescription(described("**/go.mod", "*.{go,s}", "[a-z]?.txt", "!**/testdata/**", `\!name`)); err != nil {
		t.Fatalf("a plugin with sound interests was refused: %v", err)
	}
	for _, interest := range []string{"[", "a/[]", "{a,b", `a\`, "/etc/passwd", "../x", "!../x", ""} {
		if err := checkDescription(described("**/go.mod", interest)); err == nil || !strings.Contains(err.Error(), "interest") {
			t.Errorf("the interest %q: err = %v, want it refused", interest, err)
		}
	}
}

// What a repository sets is held to the options a plugin declares, so a
// plugin whose declarations do not hold together is not loaded: each
// says what it does, what kind it takes, bounds and words where its kind
// has them, and a default it takes. The control is one sound option of
// each kind, bounded and worded where it may be.
func TestAPluginWhoseOptionsDoNotHoldTogetherIsRefused(t *testing.T) {
	described := func(options ...*pluginv1.Option) *pluginv1.DescribeResponse {
		return &pluginv1.DescribeResponse{Name: "tool", Options: options}
	}
	text := func(value string) *typev1.OptionValue {
		return &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: value}}
	}
	number := func(value int64) *typev1.OptionValue {
		return &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: value}}
	}
	least, most := int64(1), int64(8)
	list := &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{Values: []string{"a", "b"}}}}
	words := &typev1.OptionValue{Value: &typev1.OptionValue_StringMapValue{StringMapValue: &typev1.OptionValue_StringMap{Values: map[string]string{"main.version": "version"}}}}
	length := &typev1.OptionValue{Value: &typev1.OptionValue_DurationValue{DurationValue: durationpb.New(10 * time.Minute)}}
	option := func(name string, kind pluginv1.Option_Kind, value *typev1.OptionValue) *pluginv1.Option {
		return &pluginv1.Option{Name: name, Description: "Says what " + name + " does.", Kind: kind, Default: value}
	}
	sound := []*pluginv1.Option{
		option("flavour", pluginv1.Option_KIND_STRING, text("plain")),
		option("strict", pluginv1.Option_KIND_BOOL, &typev1.OptionValue{Value: &typev1.OptionValue_BoolValue{BoolValue: false}}),
		option("jobs", pluginv1.Option_KIND_INT, number(4)),
		option("tags", pluginv1.Option_KIND_STRING_LIST, list),
		option("timeout", pluginv1.Option_KIND_DURATION, length),
		option("variables", pluginv1.Option_KIND_STRING_MAP, words),
	}
	sound[2].Minimum, sound[2].Maximum = &least, &most
	sound[3].Allowed = []string{"a", "b", "c"}
	sound[5].Allowed = []string{"version", "commit"}
	if err := checkDescription(described(sound...)); err != nil {
		t.Fatalf("a plugin with one sound option of each kind was refused: %v", err)
	}
	with := func(o *pluginv1.Option, change func(*pluginv1.Option)) *pluginv1.Option {
		changed := proto.Clone(o).(*pluginv1.Option)
		change(changed)
		return changed
	}
	below, negative := int64(9), int64(-1)
	for name, c := range map[string]struct {
		options []*pluginv1.Option
		says    string
	}{
		"a name that is not lower-case":    {[]*pluginv1.Option{option("Flavour", pluginv1.Option_KIND_STRING, text(""))}, "not lower-case"},
		"no name":                          {[]*pluginv1.Option{option("", pluginv1.Option_KIND_STRING, text(""))}, "not lower-case"},
		"one name twice":                   {[]*pluginv1.Option{sound[0], sound[0]}, "twice"},
		"no description":                   {[]*pluginv1.Option{with(sound[0], func(o *pluginv1.Option) { o.Description = " " })}, "says nothing of what it does"},
		"a description of two lines":       {[]*pluginv1.Option{with(sound[0], func(o *pluginv1.Option) { o.Description = "One.\nTwo." })}, "not one line of printable text"},
		"a description past the longest":   {[]*pluginv1.Option{with(sound[0], func(o *pluginv1.Option) { o.Description = strings.Repeat("a", 201) })}, "of at most 200 bytes"},
		"no kind":                          {[]*pluginv1.Option{option("flavour", pluginv1.Option_KIND_UNSPECIFIED, text(""))}, "does not say what kind"},
		"no default":                       {[]*pluginv1.Option{option("flavour", pluginv1.Option_KIND_STRING, nil)}, "a default it does not take"},
		"a switch whose default is text":   {[]*pluginv1.Option{option("strict", pluginv1.Option_KIND_BOOL, text("true"))}, "a default it does not take"},
		"an integer with no value":         {[]*pluginv1.Option{option("jobs", pluginv1.Option_KIND_INT, &typev1.OptionValue{})}, "a default it does not take"},
		"an integer with a string default": {[]*pluginv1.Option{option("jobs", pluginv1.Option_KIND_INT, text("4"))}, "a default it does not take"},
		"a list with a nil message":        {[]*pluginv1.Option{option("tags", pluginv1.Option_KIND_STRING_LIST, &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{}})}, "a default it does not take"},
		"a map with a nil message":         {[]*pluginv1.Option{option("variables", pluginv1.Option_KIND_STRING_MAP, &typev1.OptionValue{Value: &typev1.OptionValue_StringMapValue{}})}, "a default it does not take"},
		"a length of time below zero":      {[]*pluginv1.Option{option("timeout", pluginv1.Option_KIND_DURATION, &typev1.OptionValue{Value: &typev1.OptionValue_DurationValue{DurationValue: durationpb.New(-time.Second)}})}, "a default it does not take"},
		"bounds of text":                   {[]*pluginv1.Option{with(sound[0], func(o *pluginv1.Option) { o.Minimum = &least })}, "only an integer has a least and a most"},
		"a least above the most":           {[]*pluginv1.Option{with(sound[2], func(o *pluginv1.Option) { o.Minimum = &below })}, "a least above its most"},
		"a default below the least":        {[]*pluginv1.Option{with(sound[2], func(o *pluginv1.Option) { o.Default = number(0) })}, "jobs is at least 1"},
		"a default above the most":         {[]*pluginv1.Option{with(sound[2], func(o *pluginv1.Option) { o.Maximum = &negative; o.Minimum = nil })}, "jobs is at most -1"},
		"words of a number":                {[]*pluginv1.Option{with(sound[2], func(o *pluginv1.Option) { o.Allowed = []string{"4"} })}, "takes no text"},
		"a word twice":                     {[]*pluginv1.Option{with(sound[3], func(o *pluginv1.Option) { o.Allowed = []string{"a", "b", "a"} })}, "allows a word twice"},
		"an empty word":                    {[]*pluginv1.Option{with(sound[3], func(o *pluginv1.Option) { o.Allowed = []string{"a", "b", ""} })}, "not one line of printable text"},
		"a default entry not allowed":      {[]*pluginv1.Option{with(sound[3], func(o *pluginv1.Option) { o.Allowed = []string{"a"} })}, "an entry of tags is one of a"},
		"a default map value not allowed":  {[]*pluginv1.Option{with(sound[5], func(o *pluginv1.Option) { o.Allowed = []string{"commit"} })}, "a value of variables is one of commit"},
	} {
		err := checkDescription(described(c.options...))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("a plugin that declares %s: %v, want it refused saying %q", name, err, c.says)
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
		return &pluginv1.Action{Name: "format", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"gofmt", "-w", "."}, Environment: env}, Rewrites: []string{"**/*.go"}}}}
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
	if err := checkDescription(&pluginv1.DescribeResponse{Name: "tool", Release: sound}); err != nil {
		t.Fatalf("a branching and a versioning strategy were refused, so this test proves nothing: %v", err)
	}
	if err := checkDescription(&pluginv1.DescribeResponse{Name: "tool"}); err != nil {
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
		err := checkDescription(&pluginv1.DescribeResponse{Name: "tool", Release: c.roles})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: what a plugin says it serves of releasing was taken, or refused for another reason: %v", name, err)
		}
	}
}

// What a plugin says it starts is names, each said once: a name that
// is none, or said twice, is refused, beside two that are different.
func TestTheNamesAPluginStartsAreNamesEachSaidOnce(t *testing.T) {
	described := func(services ...string) *pluginv1.DescribeResponse {
		return &pluginv1.DescribeResponse{Name: "p", Services: services}
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
