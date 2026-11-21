package pluginhost

import (
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"maps"
	"net/url"
	"slices"
	"strconv"

	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/server/internal/testservice"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

var name = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// checkDescription holds what a module says of itself to the contract.
func checkDescription(d *pluginv1.DescribeResponse) error {
	if !name.MatchString(d.GetName()) {
		return fmt.Errorf("the plugin's name %q is not lower-case [a-z0-9-]", d.GetName())
	}
	if d.GetAbi() < MinABI || d.GetAbi() > MaxABI {
		return fmt.Errorf("plugin %s says it speaks ABI %d, and this host loads %d to %d", d.GetName(), d.GetAbi(), MinABI, MaxABI)
	}
	// What it reads is said in the contract's pattern language, of files
	// beneath the repository's root, as an action's source inputs are.
	for _, interest := range d.GetInterests() {
		if !pattern.Valid(interest) {
			return fmt.Errorf("plugin %s has an interest that is not a pattern of files beneath the repository", d.GetName())
		}
	}
	// A plugin says which branching and versioning strategies it serves,
	// each by a name of its own.
	if roles := d.GetRelease(); roles != nil {
		if len(roles.GetBranchings()) == 0 && len(roles.GetVersionings()) == 0 {
			return fmt.Errorf("plugin %s says what it serves of releasing, and says none", d.GetName())
		}
		for _, of := range []struct {
			kind       string
			strategies []string
		}{{"branching", roles.GetBranchings()}, {"versioning", roles.GetVersionings()}} {
			kind, seen := of.kind, map[string]bool{}
			for _, strategy := range of.strategies {
				switch {
				case !name.MatchString(strategy):
					return fmt.Errorf("plugin %s serves the %s strategy %q, whose name is not lower-case [a-z0-9-]", d.GetName(), kind, strategy)
				case seen[strategy]:
					return fmt.Errorf("plugin %s serves the %s strategy %s twice", d.GetName(), kind, strategy)
				}
				seen[strategy] = true
			}
		}
	}
	// What it starts is named as an entity names it, each name once.
	services := map[string]bool{}
	for _, as := range d.GetServices() {
		if !name.MatchString(as) || services[as] {
			return fmt.Errorf("plugin %s says it starts %q: a name is lower-case [a-z0-9-], and is said once", d.GetName(), as)
		}
		services[as] = true
	}
	// What a repository sets is checked against the options a plugin
	// declares, so the declarations are checked first: each has a name
	// of its own, a kind, and a default of that kind.
	declared := map[string]bool{}
	for _, option := range d.GetOptions() {
		switch {
		case !name.MatchString(option.GetName()):
			return fmt.Errorf("plugin %s declares the option %q, whose name is not lower-case [a-z0-9-]", d.GetName(), option.GetName())
		case declared[option.GetName()]:
			return fmt.Errorf("plugin %s declares the option %s twice", d.GetName(), option.GetName())
		case !ofKind(option.GetKind(), option.GetDefault()):
			return fmt.Errorf("plugin %s declares the option %s with a kind and a default that do not agree: a kind is text, a switch, a whole number or a list of text, and a default is one of it", d.GetName(), option.GetName())
		}
		declared[option.GetName()] = true
	}
	return nil
}

// ofKind reports whether a value is one of a kind an option takes.
func ofKind(kind pluginv1.Option_Kind, value *typev1.OptionValue) bool {
	switch kind {
	case pluginv1.Option_KIND_STRING:
		_, is := value.GetValue().(*typev1.OptionValue_StringValue)
		return is
	case pluginv1.Option_KIND_BOOL:
		_, is := value.GetValue().(*typev1.OptionValue_BoolValue)
		return is
	case pluginv1.Option_KIND_INT:
		_, is := value.GetValue().(*typev1.OptionValue_IntValue)
		return is
	case pluginv1.Option_KIND_STRING_LIST:
		list, is := value.GetValue().(*typev1.OptionValue_StringListValue)
		return is && list.StringListValue != nil
	}
	return false
}

// CheckAnalysis holds an Analyze answer to the contract, for the roots
// that were asked about. The host checks; a plugin need not.
func CheckAnalysis(plugin string, roots []string, a *pluginv1.AnalyzeResponse) error {
	if len(roots) == 0 {
		roots = []string{""}
	}
	if len(a.GetAnalyses()) != len(roots) {
		return fmt.Errorf("plugin %s answered for %d roots and was asked about %d", plugin, len(a.GetAnalyses()), len(roots))
	}
	found := map[string]bool{}
	for i, analysis := range a.GetAnalyses() {
		if analysis.GetRoot() != roots[i] {
			return fmt.Errorf("plugin %s answered for the root %q where %q was asked", plugin, analysis.GetRoot(), roots[i])
		}
		for _, project := range analysis.GetProjects() {
			if err := checkProject(plugin, analysis.GetRoot(), project, found); err != nil {
				return err
			}
		}
		for _, entity := range analysis.GetEntities() {
			if err := checkEntity(analysis.GetRoot(), entity); err != nil {
				return fmt.Errorf("plugin %s: %w", plugin, err)
			}
		}
	}
	return nil
}

var relationName = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

func checkEntity(root string, described *pluginv1.Entity) error {
	identity := func(ref *typev1.EntityRef) error {
		return (entity.Ref{Kind: ref.GetKind(), Namespace: ref.GetNamespace(), Name: ref.GetName()}).Validate()
	}
	if err := identity(described.GetRef()); err != nil {
		return err
	}
	manifest := described.GetManifest()
	if !pattern.Local(manifest) || path.Clean(manifest) != manifest || !beneath(root, manifest) || manifest == root || strings.ContainsAny(manifest, "\\\x00") {
		return fmt.Errorf("catalog manifest %q is not a file beneath %q", manifest, root)
	}
	seen := map[string]bool{}
	for _, at := range described.GetRoots() {
		if at == "." || !beneath(root, at) || at != "" && path.Clean(at) != at || strings.ContainsAny(at, "\\\x00") || seen[at] {
			return fmt.Errorf("catalog root %q is repeated or not beneath %q", at, root)
		}
		seen[at] = true
	}
	for _, relation := range described.GetRelations() {
		if !relationName.MatchString(relation.GetType()) || !relationName.MatchString(relation.GetInverseType()) {
			return fmt.Errorf("catalog relation must name its type and inverse type")
		}
		if err := identity(relation.GetTarget()); err != nil {
			return err
		}
	}
	return nil
}

// beneath reports whether a path is a directory or beneath it; the
// empty directory is the repository, which everything local is beneath.
func beneath(directory, at string) bool {
	if directory == "" {
		return at == "" || pattern.Local(at)
	}
	return at == directory || pattern.Local(at) && strings.HasPrefix(path.Clean(at)+"/", directory+"/")
}

// checkPatterns holds an input set to source patterns and nested sets.
func checkPatterns(what string, inputs []*pluginv1.Input, sets map[string]bool) error {
	for _, input := range inputs {
		if _, err := checkInput(what, input, sets); err != nil {
			return err
		}
		switch input.GetKind().(type) {
		case *pluginv1.Input_Source, *pluginv1.Input_InputSet:
		default:
			return fmt.Errorf("%s holds a produced or dependency input: a set is of source, and what an action left is named in an action's own inputs", what)
		}
	}
	return nil
}

// checkInput validates the explicit variant without interpreting its contents
// as another variant. Reference resolution belongs to the project and planner.
func checkInput(what string, input *pluginv1.Input, sets map[string]bool) (reference bool, err error) {
	switch kind := input.GetKind().(type) {
	case *pluginv1.Input_Output:
		return true, nil
	case *pluginv1.Input_InputSet:
		if !sets[kind.InputSet] {
			return false, fmt.Errorf("%s names the input set %q, which the project does not declare", what, kind.InputSet)
		}
	case *pluginv1.Input_Source:
		if err := checkSourcePattern(what, kind.Source); err != nil {
			return false, err
		}
	case *pluginv1.Input_Dependency:
		if err := checkDependency(what, kind.Dependency); err != nil {
			return false, err
		}
	default:
		return false, fmt.Errorf("%s has an input without a supported kind", what)
	}
	return false, nil
}

func checkSourcePattern(what, selection string) error {
	if !pattern.Valid(selection) {
		return fmt.Errorf("%s holds %q, which is not a pattern of files beneath the project", what, selection)
	}
	return nil
}

func checkDependency(what string, dependency *pluginv1.DependencyInput) error {
	switch selection := dependency.GetSelection().(type) {
	case *pluginv1.DependencyInput_All:
		if !selection.All {
			return fmt.Errorf("%s has a dependency input whose all selection is false", what)
		}
	case *pluginv1.DependencyInput_Coordinate:
		if selection.Coordinate.GetEcosystem() == "" || selection.Coordinate.GetName() == "" {
			return fmt.Errorf("%s has a dependency input with an incomplete coordinate", what)
		}
	default:
		return fmt.Errorf("%s has a dependency input without a selection", what)
	}
	into := dependency.GetInto()
	if into == "" || into != "{root}" && (strings.Contains(into, "{root}") || !pattern.Local(strings.ReplaceAll(into, "{name}", "name"))) {
		return fmt.Errorf("%s has what it is handed placed in %q, which is neither beneath the project nor {root}", what, into)
	}
	return nil
}

// CheckOutputs holds what an action says it writes to where an action
// may write: beneath its project, or, written "~/…", in its home, and
// never the whole of either. What is written becomes a path in the
// action's root and in the root of whatever reads it, and is a name
// others read it by: so it is written plainly, one spelling for one
// place, a directory's ending in a slash.
func CheckOutputs(what string, outputs []string) error {
	for _, output := range outputs {
		if !plainOutput(output) {
			return fmt.Errorf("%s writes %q, which is not a plain path beneath the project or the action's home", what, output)
		}
	}
	return nil
}

// plainOutput reports whether an output is written as CheckOutputs
// holds it to be.
func plainOutput(output string) bool {
	rest, home := strings.CutPrefix(output, "~/")
	place := strings.TrimSuffix(rest, "/")
	// .frontseat in a project is where an action's root holds its home
	// and what the daemon places for it: no output of a project's, or a
	// place there and "~/…" would be two spellings of one.
	if !home && (place == ".frontseat" || strings.HasPrefix(place, ".frontseat/")) {
		return false
	}
	return pattern.Local(place) && place != "." && place != "~" && path.Clean(place) == place && !strings.ContainsAny(output, "\\\x00")
}

// SplitInputs extracts source patterns and planner references after input-set
// expansion. Dependency handoffs are resolved separately through the graph.
func SplitInputs(inputs []*pluginv1.Input) (patterns, references []string) {
	// Each is made once, at the size it ends at.
	var sources, outputs int
	for _, input := range inputs {
		switch input.GetKind().(type) {
		case *pluginv1.Input_Source:
			sources++
		case *pluginv1.Input_Output:
			outputs++
		}
	}
	if sources > 0 {
		patterns = make([]string, 0, sources)
	}
	if outputs > 0 {
		references = make([]string, 0, outputs)
	}
	for _, input := range inputs {
		switch kind := input.GetKind().(type) {
		case *pluginv1.Input_Source:
			patterns = append(patterns, kind.Source)
		case *pluginv1.Input_Output:
			// Of its own project, always: a plugin names no other's.
			references = append(references, ":"+kind.Output)
		}
	}
	return patterns, references
}

// checkReferences holds what a project's actions name of each other to
// the project: a plugin names only its own actions, so each reference
// is ":what", and what follows the colon is one action of the project,
// which leaves something, or one output of one.
func checkReferences(project *pluginv1.Project) error {
	// A project whose actions name nothing of each other, as most are
	// answered on every build, is not looked at further.
	referenced := false
	for _, action := range project.GetActions() {
		for _, input := range action.GetInputs() {
			_, isOutput := input.GetKind().(*pluginv1.Input_Output)
			referenced = referenced || isOutput
		}
	}
	if !referenced {
		return nil
	}
	leaves := map[string]bool{}
	// How many actions of the project declare each output: one that
	// several declare names no one of them.
	outputs := map[string]int{}
	for _, action := range project.GetActions() {
		leaves[action.GetName()] = len(action.GetOutputs()) > 0
		for i, output := range action.GetOutputs() {
			// By where it sits: a file and a directory of one name
			// are one place, and an action that declares a place twice
			// leaves there once.
			place := strings.TrimSuffix(output.GetPath(), "/")
			again := false
			for _, before := range action.GetOutputs()[:i] {
				again = again || strings.TrimSuffix(before.GetPath(), "/") == place
			}
			if !again {
				outputs[place]++
			}
		}
	}
	for _, action := range project.GetActions() {
		for _, input := range action.GetInputs() {
			if _, isOutput := input.GetKind().(*pluginv1.Input_Output); !isOutput {
				continue
			}
			rest := input.GetOutput()
			if strings.HasPrefix(rest, "//") {
				return fmt.Errorf("the action %q reads %q, which names another project: a plugin names only its own actions", action.GetName(), input.GetOutput())
			}
			// A slash at the end changes nothing of what is named.
			place := strings.TrimSuffix(rest, "/")
			_, named := leaves[place]
			left := outputs[place]
			switch {
			case named && left > 0:
				return fmt.Errorf("the action %q reads %q, which names both an action and what an action leaves", action.GetName(), ":"+input.GetOutput())
			case !named && left == 0:
				return fmt.Errorf("the action %q reads %q, which names no action of the project and nothing one leaves", action.GetName(), ":"+input.GetOutput())
			case left > 1:
				return fmt.Errorf("the action %q reads %q, which more than one action of the project leaves: name the action whose it is", action.GetName(), ":"+input.GetOutput())
			case named && !leaves[place]:
				return fmt.Errorf("the action %q reads %q, which leaves nothing", action.GetName(), ":"+input.GetOutput())
			}
		}
	}
	return nil
}

// checkProject holds one project of an answer to the contract. What it
// names becomes paths in a workspace and in the root of an action, so
// each is held to the project it is of.
func checkProject(plugin, root string, project *pluginv1.Project, found map[string]bool) error {
	wrong := func(format string, args ...any) error {
		return fmt.Errorf("plugin %s: project %s: %s", plugin, project.GetRoot(), fmt.Sprintf(format, args...))
	}
	switch {
	case !beneath(root, project.GetRoot()):
		return wrong("it is not beneath %q, the root that was asked about", root)
	case path.Clean(project.GetRoot()) == ".frontseat" || strings.HasPrefix(path.Clean(project.GetRoot()), ".frontseat/"):
		// Where an action's root holds its home and what the daemon
		// places for it: a project there would leave its outputs where
		// "~/…" does, two spellings of one place.
		return wrong("it is in .frontseat, which an action's root keeps for the action's home: no project is there")
	case found[path.Clean(project.GetRoot())]:
		return wrong("it was found twice: a directory is one project of a plugin's")
	case !pattern.Local(project.GetManifest()):
		return wrong("its manifest %q is not a file of the repository", project.GetManifest())
	}
	found[path.Clean(project.GetRoot())] = true
	sets := map[string]bool{}
	for _, set := range project.GetInputSets() {
		if set.GetName() == "" || sets[set.GetName()] {
			return wrong("the input set %q has no name of its own", set.GetName())
		}
		sets[set.GetName()] = true
	}
	for _, set := range project.GetInputSets() {
		if err := checkPatterns(fmt.Sprintf("the input set %s", set.GetName()), set.GetInputs(), sets); err != nil {
			return wrong("%v", err)
		}
	}
	contents := map[string][]*pluginv1.Input{}
	for _, set := range project.GetInputSets() {
		contents[set.GetName()] = set.GetInputs()
	}
	for _, set := range project.GetInputSets() {
		if _, err := ResolveInputs([]*pluginv1.Input{SetInput(set.GetName())}, contents); err != nil {
			return wrong("%v", err)
		}
	}
	coordinate := func(c *pluginv1.Coordinate) error {
		if c.GetEcosystem() == "" || c.GetName() == "" {
			return wrong("the coordinate %q of %q names no package of an ecosystem", c.GetName(), c.GetEcosystem())
		}
		return nil
	}
	for _, provide := range project.GetProvides() {
		if err := coordinate(provide.GetCoordinate()); err != nil {
			return err
		}
		if provide.GetSources() != "" && !sets[provide.GetSources()] {
			return wrong("it hands over the input set %q, which it does not declare", provide.GetSources())
		}
	}
	for _, requirement := range project.GetRequires() {
		origin := requirement.GetOrigin()
		if !pattern.Local(origin.GetPath()) || origin.GetLine() < 1 {
			return wrong("it requires %s and does not say where that is said: origin must name a repository file and a positive line", requirement.GetCoordinate().GetName())
		}
		if err := coordinate(requirement.GetCoordinate()); err != nil {
			return err
		}
		// A version is what a lock spells, printed as it is: one word of
		// printable text, or none.
		if version := requirement.GetVersion(); version != "" && (len(version) > VersionAtMost || !plain(version) || strings.ContainsAny(version, " \t")) {
			return wrong("it requires %s at a version that is not one word of printable text of at most %d bytes", requirement.GetCoordinate().GetName(), VersionAtMost)
		}
	}
	seen := map[string]bool{}
	// tested is an action that tests the project, by whether it is a
	// piece of its tests (true) or the whole of them (false).
	// Coordinate membership is indexed once when an action first selects one.
	var required map[[2]string]bool
	tested := map[bool]string{}
	for _, action := range project.GetActions() {
		if action.GetProgram() {
			if err := checkProgram(action); err != nil {
				return wrong("%v", err)
			}
		} else if action.GetShared() {
			if err := checkShared(action); err != nil {
				return wrong("%v", err)
			}
		} else if err := checkNamed(action); err != nil {
			return wrong("%v", err)
		} else if len(action.GetFiles()) > 0 {
			return wrong("the action %q is given files, which only a shared action is", action.GetName())
		} else if len(action.GetDownloads()) > 0 {
			return wrong("the action %q downloads, which only a shared action does", action.GetName())
		}
		if seen[action.GetName()] {
			return wrong("it declares the action %q twice", action.GetName())
		}
		seen[action.GetName()] = true
		// A project's tests are one action or pieces, never both: asked
		// for its tests, a run would test what a piece tests twice.
		if kind, _, pieced := strings.Cut(action.GetName(), "/"); kind == "test" && !action.GetShared() {
			if other, had := tested[!pieced]; had {
				return wrong("it declares the tests %q and %q: a project's tests are one action named test, or pieces of it, never both", other, action.GetName())
			}
			tested[pieced] = action.GetName()
		}
		if err := checkDirectory(action); err != nil {
			return wrong("%v", err)
		}
		if err := checkIdle(project, action); err != nil {
			return wrong("%v", err)
		}
		// A shared action that downloads runs nothing of its own: the
		// host unpacks what it downloaded. One whose lock holds the
		// whole of what it fetches, as an apk lock holds a signing key,
		// downloads nothing either: its files are what it leaves.
		whole := action.GetShared() && len(action.GetFiles()) > 0 && len(action.GetOutputs()) > 0
		if len(action.GetCommand().GetArguments()) == 0 && len(action.GetDownloads()) == 0 && !whole {
			return wrong("the action %q runs nothing", action.GetName())
		}
		what := "the action " + action.GetName()
		for _, input := range action.GetInputs() {
			if _, err := checkInput(what, input, sets); err != nil {
				return wrong("%v", err)
			}
		}
		if err := CheckRewrites(fmt.Sprintf("the action %s", action.GetName()), action.GetRewrites()); err != nil {
			return wrong("%v", err)
		}
		if err := CheckFix(fmt.Sprintf("the action %s", action.GetName()), action); err != nil {
			return wrong("%v", err)
		}
		if err := CheckVersion(fmt.Sprintf("the action %s", action.GetName()), action); err != nil {
			return wrong("%v", err)
		}
		for _, output := range action.GetOutputs() {
			if !plainOutput(output.GetPath()) {
				return wrong("the action %q writes %q, which is not a plain path beneath the project or the action's home", action.GetName(), output.GetPath())
			}
		}
		for _, dependency := range Dependencies(action.GetInputs()) {
			if coordinate := dependency.GetCoordinate(); coordinate != nil {
				if required == nil {
					required = make(map[[2]string]bool, len(project.GetRequires()))
					for _, requirement := range project.GetRequires() {
						coordinate := requirement.GetCoordinate()
						required[[2]string{coordinate.GetEcosystem(), coordinate.GetName()}] = true
					}
				}
				if !required[[2]string{coordinate.GetEcosystem(), coordinate.GetName()}] {
					return wrong("the action %q selects a dependency coordinate %s:%s that its project does not require", action.GetName(), coordinate.GetEcosystem(), coordinate.GetName())
				}
			}
		}
		for _, tool := range action.GetTools() {
			if tool == "" {
				return wrong("the action %q names a tool with no name", action.GetName())
			}
		}
	}
	if err := checkShards(project); err != nil {
		return wrong("%v", err)
	}
	// What the actions name of each other, once each is sound in itself.
	if err := checkReferences(project); err != nil {
		return wrong("%v", err)
	}
	if err := checkGenerated(project); err != nil {
		return wrong("%v", err)
	}
	// What the project's tool reads above it, for the whole workspace:
	// each a literal file of the repository that is not the project's.
	workspace := map[string]bool{}
	for _, file := range project.GetWorkspaceFiles() {
		at := path.Clean(file)
		switch {
		case root == "":
			return wrong("it names %q as a file of its workspace, and a project at the repository's root has nothing above it", file)
		case !pattern.Local(file) || at == ".":
			return wrong("it names %q as a file of its workspace, which is not a file of the repository", file)
		case strings.ContainsAny(file, "*?[]{}!"):
			return wrong("it names %q as a file of its workspace, which is a pattern: a workspace file is named literally", file)
		case at == root || beneath(root, at):
			return wrong("it names %q as a file of its workspace, which is a file of the project", file)
		case workspace[at]:
			return wrong("it names %q as a file of its workspace twice", at)
		}
		workspace[at] = true
	}
	if err := checkProvidedArtifacts(project); err != nil {
		return wrong("%v", err)
	}
	return nil
}

// Each provided artifact is one declared output of the provider itself.
// Consumers know the coordinate, never these private action names.
func checkProvidedArtifacts(project *pluginv1.Project) error {
	if !slices.ContainsFunc(project.GetProvides(), func(p *pluginv1.Provide) bool { return len(p.GetArtifacts()) > 0 }) {
		return nil
	}
	actions := make(map[string]*pluginv1.Action, len(project.GetActions()))
	for _, action := range project.GetActions() {
		actions[action.GetName()] = action
	}
	for _, provide := range project.GetProvides() {
		var paths []string
		for _, artifact := range provide.GetArtifacts() {
			action := actions[artifact.GetAction()]
			if action == nil || action.GetShared() || action.GetProgram() {
				return fmt.Errorf("provided artifact %q names no finite action of its project", artifact.GetAction())
			}
			output := artifact.GetOutput()
			if strings.HasPrefix(output, "~/") || !slicesAny(action.GetOutputs(), func(o *pluginv1.Output) bool { return o.GetPath() == output }) {
				return fmt.Errorf("provided artifact %q is not an exact project output of %q", output, action.GetName())
			}
			at := strings.TrimSuffix(output, "/")
			for _, before := range paths {
				if at == before || strings.HasPrefix(at, before+"/") || strings.HasPrefix(before, at+"/") {
					return fmt.Errorf("provided artifacts %q and %q overlap", before, at)
				}
			}
			paths = append(paths, at)
		}
	}
	return nil
}

// SharedFilesAtMost is the most bytes a shared action's files may hold
// together. They are what it is told of one entry of a lock.
const SharedFilesAtMost = 1 << 20

// pieceName is what a piece of a project's tests is named: "test", a
// slash and what piece it is, as its tool spells it.
var pieceName = regexp.MustCompile(`^test/[A-Za-z0-9._~+@/-]+$`)

// checkNamed holds a project's action to its name: lower-case
// [a-z0-9-], or, for a test alone, "test", a slash and the piece of the
// project's tests it is, a plain path that is written one way.
func checkNamed(action *pluginv1.Action) error {
	n := action.GetName()
	_, piece, pieced := strings.Cut(n, "/")
	switch {
	case !pieced && !name.MatchString(n):
		return fmt.Errorf("the action name %q is not lower-case [a-z0-9-]", n)
	case !pieced:
		return nil
	case len(n) > SharedNameAtMost:
		return fmt.Errorf("the action name %.40q... is %d bytes, more than the %d allowed", n, len(n), SharedNameAtMost)
	case !pieceName.MatchString(n) || !pattern.Local(piece) || path.Clean(piece) != piece:
		return fmt.Errorf("the action name %q is not lower-case [a-z0-9-], nor test, a slash and the piece of the tests it is", n)
	case !action.GetTest():
		return fmt.Errorf("the action %q is named as a piece of the project's tests, and is no test", n)
	case strings.Contains(piece, "@") && !shardOf(piece):
		return fmt.Errorf("the action name %q holds an @ and is no shard's: a shard is named its piece, @, which it is and of how many, as test/internal/ledger@2of4", n)
	}
	return nil
}

// shardName is how a piece's name ends where it is a shard's: "@", which
// shard it is and of how many, neither written with a zero before it.
var shardName = regexp.MustCompile(`^[^@]+@([1-9][0-9]{0,8})of([1-9][0-9]{0,8})$`)

// shardOf reports whether a piece's name is a shard's: one of two or
// more, and no later than the last.
func shardOf(piece string) bool {
	parts := shardName.FindStringSubmatch(piece)
	if parts == nil {
		return false
	}
	which, _ := strconv.Atoi(parts[1])
	of, _ := strconv.Atoi(parts[2])
	return of >= 2 && which <= of
}

// checkShards holds the shards of each piece of a project's tests to
// being one cut of it: every shard of the count they all say, each once,
// and no action for the piece whole beside them. A run asked for the
// project's tests runs every action named for them, so a shard missing
// would leave tests unrun without a word, and a piece beside its shards
// would run them twice.
func checkShards(project *pluginv1.Project) error {
	type cut struct{ of, said int }
	cuts := map[string]*cut{}
	whole := map[string]bool{}
	for _, action := range project.GetActions() {
		kind, piece, pieced := strings.Cut(action.GetName(), "/")
		if kind != "test" || !pieced || action.GetShared() {
			continue
		}
		parts := shardName.FindStringSubmatch(piece)
		if parts == nil {
			whole["test/"+piece] = true
			continue
		}
		name := "test/" + piece[:strings.LastIndex(piece, "@")]
		of, _ := strconv.Atoi(parts[2])
		held, seen := cuts[name]
		switch {
		case !seen:
			cuts[name] = &cut{of: of, said: 1}
		case held.of != of:
			return fmt.Errorf("the shards of %q say they are %d and %d: a piece is cut one way", name, held.of, of)
		default:
			held.said++
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cuts)) {
		switch held := cuts[name]; {
		case whole[name]:
			return fmt.Errorf("it declares %q and shards of it: a piece is one action, or its shards, never both", name)
		case held.said != held.of:
			return fmt.Errorf("it declares %d of the %d shards of %q: every shard of a piece is said, or its tests are not all run", held.said, held.of, name)
		}
	}
	return nil
}

// checkIdle holds an action that says it has nothing to do without what
// it reads to reading, by itself, an output of its project that its
// action may pass without leaving, since nothing else can be lacking;
// and to being a step of a build, which a program to run is not.
func checkIdle(project *pluginv1.Project, action *pluginv1.Action) error {
	if !action.GetIdleWithoutInputs() {
		return nil
	}
	if action.GetProgram() {
		return fmt.Errorf("the action %q is a program to run, and says it has nothing to do without what it reads, which is said of a step of a build", action.GetName())
	}
	// An action that may not run leaves nothing another can count on:
	// what read its output would find nothing there, and run.
	if len(action.GetOutputs()) > 0 {
		return fmt.Errorf("the action %q says it has nothing to do without what it reads, and says it leaves something: an action that may not run leaves nothing", action.GetName())
	}
	// What may not be left is an output its action says it may pass
	// without: one such, read by itself, is what the action can lack.
	for _, input := range action.GetInputs() {
		if _, isOutput := input.GetKind().(*pluginv1.Input_Output); !isOutput {
			continue
		}
		for _, other := range project.GetActions() {
			for _, output := range other.GetOutputs() {
				if output.GetOptional() && input.GetOutput() == output.GetPath() {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("the action %q says it has nothing to do without what it reads, and reads no output another action may pass without leaving", action.GetName())
}

// checkDirectory holds where an action's command runs to its project: a
// plain path beneath it, written one way. Only a project's own command
// says one: what a shared action, a fix or a program to run says of
// one is refused by name, since nothing serves it.
func checkDirectory(action *pluginv1.Action) error {
	n := action.GetName()
	if action.GetFix().GetDirectory() != "" {
		return fmt.Errorf("the fix of the action %q says where it runs, which is not served yet", n)
	}
	directory := action.GetCommand().GetDirectory()
	switch {
	case directory == "":
		return nil
	case action.GetProgram():
		return fmt.Errorf("the action %q is a program to run, and says where it runs, which is not served yet", n)
	case action.GetShared():
		return fmt.Errorf("the shared action %q says where it runs: it runs in a directory of its own", n)
	case !plainDirectory(directory):
		return fmt.Errorf("the action %q runs in %q, which is not a plain path beneath the project", n, directory)
	}
	return nil
}

// plainDirectory reports whether a directory a command runs in, or a
// generated source goes to, is written as an output beneath the project
// is, and is no place of the action's home: a directory by one spelling,
// with no slash at its end and nothing in it a path is not written with.
func plainDirectory(directory string) bool {
	if !plainOutput(directory) || strings.HasPrefix(directory, "~") || strings.HasSuffix(directory, "/") {
		return false
	}
	for _, r := range directory {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return strings.TrimSpace(directory) == directory
}

// SharedNameAtMost is the most bytes a shared action's name may hold:
// a kind and what one entry of a lock is of.
const SharedNameAtMost = 1024

// VersionAtMost is the longest version a requirement may say it
// resolves to.
const VersionAtMost = 256

// sharedName is a shared action's name: a kind, as a project's action is
// named, a slash, and what it is of.
var sharedName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*/[A-Za-z0-9._~+@/-]+$`)

// checkShared holds a shared action to what no project gives it: it is a
// fetch, reads no source, is handed nothing, needs nothing, leaves only
// what its home holds, and is given files of its own directory alone.
func checkShared(action *pluginv1.Action) error {
	n := action.GetName()
	kind, of, _ := strings.Cut(n, "/")
	switch {
	case len(n) > SharedNameAtMost:
		return fmt.Errorf("the shared action name %.40q... is %d bytes, more than the %d allowed", n, len(n), SharedNameAtMost)
	case !sharedName.MatchString(n) || !pattern.Local(of) || path.Clean(of) != of:
		return fmt.Errorf("the shared action name %q is not a kind, a slash and what it is of", n)
	case kind == "test":
		return fmt.Errorf("the shared action %q is named as a piece of a project's tests, which only a test is", n)
	case !action.GetFetch():
		return fmt.Errorf("the shared action %s is no fetch: only a fetch is shared", kind)
	case len(action.GetInputs()) > 0:
		return fmt.Errorf("the shared action %q reads source, which no project gives it", n)
	case action.GetCheck() || action.GetTest() || action.GetFix() != nil || len(action.GetRewrites()) > 0:
		return fmt.Errorf("the shared action %q checks, tests or fixes; it only fetches", n)
	case len(action.GetDownloads()) > 0 && len(action.GetCommand().GetArguments()) > 0:
		return fmt.Errorf("the shared action %q downloads and runs a command; one that downloads runs nothing of its own", n)
	case len(action.GetDownloads()) > 0 && len(action.GetTools()) > 0:
		return fmt.Errorf("the shared action %q downloads and names tools; one that downloads runs nothing of its own", n)
	}
	for _, output := range action.GetOutputs() {
		rest, home := strings.CutPrefix(output.GetPath(), "~/")
		if !home || !pattern.Local(rest) || path.Clean(rest) == "." {
			return fmt.Errorf("the shared action %q writes %q, which is not beneath its home", n, output.GetPath())
		}
	}
	if err := checkDownloads(n, action.GetDownloads(), action.GetOutputs()); err != nil {
		return err
	}
	// A shared fetch with neither a command nor downloads is of what its
	// lock holds whole: its files, of its home, are all it leaves, so
	// each output is one of them or holds one, and it names no tool,
	// having nothing to run one with.
	if len(action.GetCommand().GetArguments()) == 0 && len(action.GetDownloads()) == 0 {
		if len(action.GetTools()) > 0 {
			return fmt.Errorf("the shared action %q names tools and runs nothing", n)
		}
		for _, file := range action.GetFiles() {
			if !strings.HasPrefix(file.GetPath(), "~/") {
				return fmt.Errorf("the shared action %q runs nothing, and is given %s in its own directory, which it cannot leave", n, file.GetPath())
			}
		}
		for _, output := range action.GetOutputs() {
			left := false
			for _, file := range action.GetFiles() {
				left = left || leaves(output.GetPath(), file.GetPath())
			}
			if !left {
				return fmt.Errorf("the shared action %q runs nothing, and none of its files is left as %s", n, output.GetPath())
			}
		}
	}
	seen := map[string]bool{}
	size := 0
	for _, file := range action.GetFiles() {
		at := path.Clean(file.GetPath())
		// A file of its own directory, or of its home.
		if rest, home := strings.CutPrefix(file.GetPath(), "~/"); home {
			if !pattern.Local(rest) || path.Clean(rest) == "." {
				return fmt.Errorf("the shared action %q is given %q, which is not a file of its home", n, file.GetPath())
			}
			at = "~/" + path.Clean(rest)
		} else if !pattern.Local(file.GetPath()) || at == "." || at == ".frontseat" || strings.HasPrefix(at, ".frontseat/") {
			return fmt.Errorf("the shared action %q is given %q, which is not a file of its own directory", n, file.GetPath())
		}
		if seen[at] {
			return fmt.Errorf("the shared action %q is given %s twice", n, at)
		}
		seen[at] = true
		size += len(file.GetContent())
	}
	// A part of a download is kept where nothing the action is given is.
	for _, download := range action.GetDownloads() {
		for _, part := range download.GetParts() {
			if rest, _ := strings.CutPrefix(part.GetPath(), "~/"); !seen["~/"+path.Clean(rest)] {
				seen["~/"+path.Clean(rest)] = true
				continue
			}
			return fmt.Errorf("the shared action %q keeps a part of %s as %s, which it is also given as a file", n, download.GetUrl(), part.GetPath())
		}
	}
	// A file cannot also be a directory of another.
	for at := range seen {
		for dir := path.Dir(at); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				return fmt.Errorf("the shared action %q is given %s, and %s beneath it as though it were a directory", n, dir, at)
			}
		}
	}
	if size > SharedFilesAtMost {
		return fmt.Errorf("the shared action %q is given %d bytes of files, more than the %d allowed", n, size, SharedFilesAtMost)
	}
	return nil
}

// checkDownloads holds a shared action's downloads to the contract: each
// a plain public https address, and what the lock holds of what it
// serves: the hex sha256 of the whole and a directory of the action's
// home to unpack into, or parts of it, each a run of bytes, its checksum
// and the file of the home it is kept as; no address twice, and an
// action's downloads all of one form, since one command places them. A
// plain address has a host and a clean path, and no query, fragment,
// escape or user: what is sent on to the proxy is what the deployment's
// rewrites were written for, and nothing passes them by.
func checkDownloads(n string, downloads []*pluginv1.Download, outputs []*pluginv1.Output) error {
	seen := map[string]bool{}
	kept := map[string]bool{}
	cut := 0
	for _, download := range downloads {
		address, err := url.Parse(download.GetUrl())
		switch {
		case err != nil || address.Scheme != "https" || address.Host == "" || address.User != nil || address.Fragment != "" || address.RawQuery != "" || address.ForceQuery:
			return fmt.Errorf("the shared action %q downloads %q, which is not a plain public https address", n, download.GetUrl())
		case strings.ContainsAny(download.GetUrl(), "%?#\\") || address.Path == "" || address.Path != path.Clean(address.Path) || strings.Contains(address.Path+"/", "/../") || strings.Contains(address.Path+"/", "/./"):
			return fmt.Errorf("the shared action %q downloads %q, whose path is not plain: no escape, no \".\" or \"..\" step, no slash twice", n, download.GetUrl())
		case seen[download.GetUrl()]:
			return fmt.Errorf("the shared action %q downloads %s twice", n, download.GetUrl())
		}
		seen[download.GetUrl()] = true
		if len(download.GetParts()) > 0 {
			cut++
			if err := checkParts(n, download, outputs, kept); err != nil {
				return err
			}
			continue
		}
		if !sha256Hex.MatchString(download.GetSha256()) {
			return fmt.Errorf("the shared action %q downloads %s with the checksum %q, which is no hex sha256", n, download.GetUrl(), download.GetSha256())
		}
		rest, home := strings.CutPrefix(download.GetInto(), "~/")
		if !home || rest != "" && !pattern.Local(rest) {
			return fmt.Errorf("the shared action %q unpacks %s into %q, which is not a directory of its home", n, download.GetUrl(), download.GetInto())
		}
	}
	if cut != 0 && cut != len(downloads) {
		return fmt.Errorf("the shared action %q unpacks some of what it downloads and cuts parts of the rest; an action's downloads are all of one form", n)
	}
	return nil
}

// PartsAtMost is how many parts one download may be cut into.
const PartsAtMost = 16

// partEndsAtMost is the furthest a part may reach into a download: far
// past any file, and near enough that a worker's shell adds an offset
// and a length without wrapping.
const partEndsAtMost = 1 << 40

// checkParts holds a download that is cut into parts to the contract: no
// checksum or directory of the whole, and each part a run of at least
// one byte, the hex sha1 or sha256 of it, one and not both, and a file
// of the action's home that no other part of the action is kept as,
// written plainly as an output is, and left by the action: an output of
// its, or beneath one, or it would be cut, checked and thrown away.
func checkParts(n string, download *pluginv1.Download, outputs []*pluginv1.Output, kept map[string]bool) error {
	switch {
	case download.GetSha256() != "" || download.GetInto() != "":
		return fmt.Errorf("the shared action %q downloads %s whole and in parts; it is unpacked or cut, not both", n, download.GetUrl())
	case len(download.GetParts()) > PartsAtMost:
		return fmt.Errorf("the shared action %q cuts %s into %d parts, more than the %d allowed", n, download.GetUrl(), len(download.GetParts()), PartsAtMost)
	}
	for _, part := range download.GetParts() {
		rest, home := strings.CutPrefix(part.GetPath(), "~/")
		at := "~/" + path.Clean(rest)
		switch {
		case part.GetLength() == 0 || part.GetOffset() > partEndsAtMost || part.GetLength() > partEndsAtMost-part.GetOffset():
			return fmt.Errorf("the shared action %q cuts %d bytes at %d from %s, which is no run of bytes of a download", n, part.GetLength(), part.GetOffset(), download.GetUrl())
		case (part.GetSha1() == "") == (part.GetSha256() == ""):
			return fmt.Errorf("the shared action %q cuts a part from %s with a sha1 and a sha256 both, or neither; a part has one checksum", n, download.GetUrl())
		case part.GetSha1() != "" && !sha1Hex.MatchString(part.GetSha1()):
			return fmt.Errorf("the shared action %q cuts a part from %s with the checksum %q, which is no hex sha1", n, download.GetUrl(), part.GetSha1())
		case part.GetSha256() != "" && !sha256Hex.MatchString(part.GetSha256()):
			return fmt.Errorf("the shared action %q cuts a part from %s with the checksum %q, which is no hex sha256", n, download.GetUrl(), part.GetSha256())
		case !home || strings.HasSuffix(rest, "/") || !plainOutput(part.GetPath()):
			return fmt.Errorf("the shared action %q keeps a part of %s as %q, which is not a file of its home, written plainly", n, download.GetUrl(), part.GetPath())
		case !slicesAny(outputs, func(output *pluginv1.Output) bool { return leaves(output.GetPath(), part.GetPath()) }):
			return fmt.Errorf("the shared action %q keeps a part of %s as %s, which is no output of its and beneath none", n, download.GetUrl(), part.GetPath())
		case kept[at]:
			return fmt.Errorf("the shared action %q keeps two parts as %s", n, at)
		}
		kept[at] = true
	}
	return nil
}

// leaves reports whether an output is a file, or holds it: the file
// itself, or a directory it is beneath.
func leaves(output, file string) bool {
	directory := strings.TrimSuffix(output, "/")
	return output == file || strings.HasPrefix(file, directory+"/")
}

// slicesAny reports whether any output satisfies a condition.
func slicesAny(outputs []*pluginv1.Output, is func(*pluginv1.Output) bool) bool {
	for _, output := range outputs {
		if is(output) {
			return true
		}
	}
	return false
}

// sha256Hex is a sha256 as a lock writes one: 64 lower-case hex digits.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sha1Hex is a sha1 in hex: 40 lower-case digits.
var sha1Hex = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Placed returns where something an action is handed is placed, relative
// to the action's project: the dependency input's into with the name of
// what is handed put in, or, for "{root}", the way from the project to
// the handed project's own root. A name comes from the plugin of the
// project that hands it over, and one that would lead out of the project
// that is handed it is refused. A root is a place in one repository: a
// project of another is not placed by it, and a project is never placed
// over itself.
func Placed(into, coordinate, handedRepository, actionRepository, project, handedRoot string) (string, error) {
	if into == "{root}" {
		switch {
		case handedRepository != actionRepository:
			return "", fmt.Errorf("%q is of another repository, and is not placed at its root in this one", coordinate)
		case path.Clean(handedRoot) == path.Clean(project):
			return "", fmt.Errorf("%q is the project itself, and is not placed over it", coordinate)
		}
		rel, err := filepath.Rel(filepath.FromSlash("/"+project), filepath.FromSlash("/"+handedRoot))
		if err != nil {
			return "", err
		}
		return filepath.ToSlash(rel), nil
	}
	placed := strings.ReplaceAll(into, "{name}", coordinate)
	if !pattern.Local(placed) {
		return "", fmt.Errorf("%q, placed as %q says, would not be beneath the project it is handed to", coordinate, into)
	}
	return placed, nil
}

// CheckArrangement holds an Arrange answer to the contract: each file a
// plugin writes for its tool is a file of the project it was asked
// about, or one directly in a directory above the project, for the
// workspace the project is in: never the project itself, and never a
// file of a directory beside it, which may be another project's.
// Staging refuses one above the project in place of a workspace file
// the project reads.
func CheckArrangement(plugin, root string, a *pluginv1.ArrangeResponse) error {
	for _, file := range a.GetFiles() {
		at := path.Clean(file.GetPath())
		if !pattern.Local(file.GetPath()) || at == "." || at == root || !(beneath(root, at) || ancestor(root, path.Dir(at))) {
			return fmt.Errorf("plugin %s: project %s: it writes %q, which is neither a file of the project nor one directly above it", plugin, root, file.GetPath())
		}
	}
	return nil
}

// ancestor reports whether a directory is the project's own ancestor:
// the repository's root, or a directory the project is beneath.
func ancestor(project, directory string) bool {
	directory = path.Clean(directory)
	return directory == "." || directory == "" || (directory != project && beneath(directory, project))
}

// CheckRewrites holds an action's rewrite patterns to its project: each
// is a pattern of files beneath the project, and names no input set.
func CheckRewrites(what string, patterns []string) error {
	for _, each := range patterns {
		if strings.HasPrefix(each, "=") || !pattern.Valid(each) {
			return fmt.Errorf("%s rewrites %q, which is not a pattern of files beneath the project", what, each)
		}
	}
	return nil
}

// FindingsAtMost is the most findings a check may report.
const FindingsAtMost = 10000

// CheckReport holds what a plugin says a check found: each finding names
// a rule the plugin qualifies, says something, and names a file of the
// repository or none. The host binds the repository; a plugin that names
// one is wrong.
func CheckReport(plugin string, response *pluginv1.ReportResponse) error {
	if len(response.GetDiagnostics()) > FindingsAtMost {
		return fmt.Errorf("the report holds %d findings, more than the %d a check may have", len(response.GetDiagnostics()), FindingsAtMost)
	}
	for i, found := range response.GetDiagnostics() {
		what := fmt.Sprintf("the finding %d", i+1)
		if rule, qualified := strings.CutPrefix(found.GetRuleId(), plugin+"/"); !qualified || rule == "" {
			return fmt.Errorf("%s names the rule %q, which is not %s/<rule>", what, found.GetRuleId(), plugin)
		}
		if strings.TrimSpace(found.GetMessage()) == "" {
			return fmt.Errorf("%s says nothing", what)
		}
		if location := found.GetSourceLocation(); location != nil {
			if location.GetRepository() != "" {
				return fmt.Errorf("%s names the repository %q, which is the host's to name", what, location.GetRepository())
			}
			if !pattern.Local(location.GetPath()) {
				return fmt.Errorf("%s names %q, which is not a file of the repository", what, location.GetPath())
			}
			if location.GetLine() < 0 {
				return fmt.Errorf("%s names line %d", what, location.GetLine())
			}
		}
	}
	return nil
}

// programName is what an action that is a program is named: "run", a
// slash and what it runs.
var programName = regexp.MustCompile(`^run/[A-Za-z0-9._-]+$`)

// checkProgram holds an action that is a program to run to what one is:
// named for what it runs, a command whose program is where another
// action left it, reading only what other actions left, and nothing a
// build's action is. What a program may say that is not served yet is
// refused by name, so that nothing said is taken and ignored.
func checkProgram(action *pluginv1.Action) error {
	n := action.GetName()
	arguments := action.GetCommand().GetArguments()
	switch {
	case len(n) > SharedNameAtMost:
		return fmt.Errorf("the program name %.40q... is %d bytes, more than the %d allowed", n, len(n), SharedNameAtMost)
	case !programName.MatchString(n) || strings.HasSuffix(n, "/.") || strings.HasSuffix(n, "/.."):
		return fmt.Errorf("the action %q is a program to run, and is not named run, a slash and what it runs", n)
	case action.GetShared() || action.GetFetch() || action.GetTest() || action.GetCheck():
		return fmt.Errorf("the action %q is a program to run, and says it is a step of a build too", n)
	case action.GetFix() != nil || len(action.GetRewrites()) > 0:
		return fmt.Errorf("the action %q is a program to run, and has a fix", n)
	case len(action.GetOutputs()) > 0:
		return fmt.Errorf("the action %q is a program to run, and says it leaves something: nothing reads what a program leaves", n)
	case len(action.GetFiles()) > 0 || len(action.GetDownloads()) > 0:
		return fmt.Errorf("the action %q is a program to run, and is given files or downloads, which only a shared action is", n)
	case len(Dependencies(action.GetInputs())) > 0:
		return fmt.Errorf("the action %q is a program to run, and is handed what its project requires, which is not served yet", n)
	case len(arguments) == 0:
		return fmt.Errorf("the action %q is a program to run, and names no program", n)
	case len(action.GetInputs()) == 0 && len(action.GetTools()) == 0:
		return fmt.Errorf("the action %q is a program to run, and reads nothing: its program is what another action left", n)
	}
	// The program is where an action left it: beneath the project or in
	// the action's home, by a plain path. A bare name would be found on
	// a path, and what is there is the daemon's, not the build's.
	program := arguments[0]
	rest := strings.TrimPrefix(program, "~/")
	tool := len(action.GetTools()) > 0 && !strings.Contains(program, "/") && pattern.Local(program) && program != "."
	if !tool && (!strings.Contains(program, "/") || !pattern.Local(rest) || path.Clean(rest) != rest) {
		return fmt.Errorf("the action %q runs %q, which is not a plain path beneath the project or the action's home: a program is where another action left it", n, program)
	}
	// A name is one a shell can export, so that it is one name and
	// nothing else: "HOME=/x" as a name would set HOME after the host.
	for name := range action.GetCommand().GetEnvironment() {
		switch {
		case !testservice.Exportable(name):
			return fmt.Errorf("the action %q is a program to run that sets %q, which is no name of a variable", n, name)
		case name == "PATH" || name == "HOME" || name == "TMPDIR":
			return fmt.Errorf("the action %q is a program to run that sets %s, which the host sets", n, name)
		}
	}
	return nil
}

// CheckProgram applies the program contract to explicitly authored custom
// programs too, after their ordinary input and output validation.
func CheckProgram(action *pluginv1.Action) error { return checkProgram(action) }

// CheckFix holds an action's fix to its check: a fix runs something, is
// of a check, and says what it may change; what may be changed is of a
// fix.
func CheckFix(what string, action *pluginv1.Action) error {
	switch {
	case action.GetFix() != nil && len(action.GetFix().GetArguments()) == 0:
		return fmt.Errorf("%s has a fix that runs nothing", what)
	case action.GetFix() != nil && !action.GetCheck():
		return fmt.Errorf("%s has a fix, and is not a check", what)
	case action.GetFix() != nil && len(action.GetRewrites()) == 0:
		return fmt.Errorf("%s has a fix, and says nothing it may change", what)
	case action.GetFix() == nil && len(action.GetRewrites()) > 0:
		return fmt.Errorf("%s says what it may change, and has no fix", what)
	}
	// The host sets where a fix's programs are found and what mise reads:
	// a fix that set them would find a program the host did not give it.
	for name := range action.GetFix().GetEnvironment() {
		if name == "PATH" || name == "HOME" || name == "TMPDIR" || strings.HasPrefix(name, "MISE_") {
			return fmt.Errorf("%s has a fix that sets %s, which the host sets", what, name)
		}
	}
	return nil
}

// EditsAtMost is the most edits a reconciliation may answer with, and
// ValuesAtMost the most strings one edit may set.
const (
	EditsAtMost  = 1000
	ValuesAtMost = 10000
)

// CheckReconciliation holds a Reconcile answer to the contract: each
// edit is of a file the request named, of a document counted from zero,
// at a path of plain keys, with plain strings; and no two edits name one
// place. What the keys mean is the plugin's; the host writes them.
func CheckReconciliation(plugin string, request *pluginv1.ReconcileRequest, response *pluginv1.ReconcileResponse) error {
	wrong := func(format string, args ...any) error {
		return fmt.Errorf("plugin %s: reconcile: %s", plugin, fmt.Sprintf(format, args...))
	}
	manifests := map[string]bool{}
	for _, entity := range request.GetEntities() {
		manifests[entity.GetManifest()] = true
	}
	if len(response.GetEdits()) > EditsAtMost {
		return wrong("it answers %d edits, more than the %d allowed", len(response.GetEdits()), EditsAtMost)
	}
	seen := map[string]bool{}
	for _, edit := range response.GetEdits() {
		if !manifests[edit.GetPath()] {
			return wrong("it edits %q, which is no file of the entities asked about", edit.GetPath())
		}
		if edit.GetDocument() < 0 {
			return wrong("it edits document %d of %s", edit.GetDocument(), edit.GetPath())
		}
		if len(edit.GetKeys()) == 0 || len(edit.GetKeys()) > 16 {
			return wrong("it edits %s at a path of %d keys; one to sixteen are allowed", edit.GetPath(), len(edit.GetKeys()))
		}
		for _, key := range edit.GetKeys() {
			if !plain(key) || len(key) > 256 {
				return wrong("it edits %s at a key %q, which is not a plain key", edit.GetPath(), key)
			}
		}
		if len(edit.GetValues()) > ValuesAtMost {
			return wrong("it sets %d values at %s, more than the %d allowed", len(edit.GetValues()), edit.GetPath(), ValuesAtMost)
		}
		for _, value := range edit.GetValues() {
			if !plain(value) || len(value) > 4096 {
				return wrong("it sets %q at %s, which is not a plain string", value, edit.GetPath())
			}
		}
		place := fmt.Sprintf("%s\x00%d\x00%s", edit.GetPath(), edit.GetDocument(), strings.Join(edit.GetKeys(), "\x00"))
		if seen[place] {
			return wrong("it edits %s at %v twice", edit.GetPath(), edit.GetKeys())
		}
		seen[place] = true
	}
	return nil
}

// plain reports whether a string is one line of printable text.
func plain(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// CheckService holds what a plugin answered of a service to what it was
// asked: a service, with each thing the entity wrote as it was written,
// whole. A person's word is not a plugin's to change, and a plugin that
// dropped or altered one would start something its entity did not say.
// What a service must be beyond that is held by whoever starts it.
func CheckService(plugin string, request *pluginv1.ServiceRequest, response *pluginv1.ServiceResponse) error {
	answered, written := response.GetService(), request.GetWritten()
	if answered == nil {
		return fmt.Errorf("plugin %s was asked how %s is started, and answered nothing", plugin, request.GetName())
	}
	changed := func(what string) error {
		return fmt.Errorf("plugin %s changed the %s that was written for a service of %s: what an entity writes is kept as it is", plugin, what, request.GetName())
	}
	switch {
	case written.GetImage() != "" && answered.GetImage() != written.GetImage():
		return changed("image")
	case len(written.GetArguments()) > 0 && !slices.Equal(answered.GetArguments(), written.GetArguments()):
		return changed("args")
	case len(written.GetPorts()) > 0 && !maps.Equal(answered.GetPorts(), written.GetPorts()):
		return changed("ports")
	case len(written.GetEnvironment()) > 0 && !maps.Equal(answered.GetEnvironment(), written.GetEnvironment()):
		return changed("env")
	case written.GetTimeout() != nil && !proto.Equal(answered.GetTimeout(), written.GetTimeout()):
		return changed("timeout")
	case len(written.GetExports()) > 0 && !maps.Equal(answered.GetExports(), written.GetExports()):
		return changed("exports")
	case len(written.GetEndpoints()) > 0 && !maps.Equal(answered.GetEndpoints(), written.GetEndpoints()):
		return changed("endpoints")
	case len(written.GetApis()) > 0 && !maps.EqualFunc(answered.GetApis(), written.GetApis(), func(a, b *typev1.ServiceApi) bool { return proto.Equal(a, b) }):
		return changed("apis")
	case len(written.GetReady()) > 0 && !slices.EqualFunc(answered.GetReady(), written.GetReady(), func(a, b *typev1.ServiceReady) bool { return proto.Equal(a, b) }):
		return changed("ready")
	case written.GetUser() != "" && answered.GetUser() != written.GetUser():
		return changed("user")
	case len(written.GetWritable()) > 0 && !slices.Equal(answered.GetWritable(), written.GetWritable()):
		return changed("writable")
	}
	return nil
}
