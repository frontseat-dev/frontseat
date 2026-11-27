package stage

import (
	"context"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/download"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/common/confine"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
	"github.com/frontseat-dev/frontseat/core/telemetry"
)

// Of describes an action as the grid runs it: the files of the workspace
// its patterns name, what it was handed of other projects, what its
// plugin wrote for its tool, what the actions it needs left, its command
// where its project is, the tools it names, and the platform of the
// workers that run it. What was left is given by the path each thing
// sits at in the workspace. The files are those of the run's source, and
// are sent as the source says they were. A shared action is given none of
// them: only its own files, in its repository's directory or its home, and
// its tools; one that downloads is given what the asset service fetched
// for it, by digest, and a command that unpacks it, with no network.
func Of(ctx context.Context, snapshot *source.Snapshot, action plan.Action, provisioner *tools.Provisioner, downloads download.Fetcher, left map[string]exec.Held) (staged exec.Action, err error) {
	return of(ctx, snapshot, action, provisioner, downloads, left, false, nil)
}

// Program prepares a service's complete input root and pinned tool environment,
// without executing the program. Bindings are set only inside confinement.
func Program(ctx context.Context, snapshot *source.Snapshot, action plan.Action, provisioner *tools.Provisioner, downloads download.Fetcher, left map[string]exec.Held, bindings map[string]string) (exec.Action, error) {
	staged, err := of(ctx, snapshot, action, provisioner, downloads, left, true, bindings)
	if err != nil {
		return exec.Action{}, err
	}
	staged.Arguments, err = confine.ForProgram(staged.Arguments)
	return staged, err
}

func of(ctx context.Context, snapshot *source.Snapshot, action plan.Action, provisioner *tools.Provisioner, downloads download.Fetcher, left map[string]exec.Held, program bool, exported map[string]string) (staged exec.Action, err error) {
	ctx, span := telemetry.Start(ctx, "stage")
	defer func() {
		span.SetAttributes(attribute.Int("inputs", len(staged.Inputs)))
		telemetry.End(span, err)
	}()
	if err := plan.CheckHanded(action); err != nil {
		return exec.Action{}, err
	}
	// Its own repository is at the top of its root (own), wherever the
	// workspace holds it (folder) and whatever it names it: an action of a
	// repository is one action, by one key, whether the repository is
	// built as a checkout of its own, alone in a workspace or beside
	// others. What an action of another repository left sits beneath
	// tools.External, and its home where it is (placed); files of
	// another repository's project it is handed sit in its own project.
	folder := snapshot.Paths[action.Repository]
	if left, err = placed(snapshot.Tree, action.Repository, left); err != nil {
		return exec.Action{}, fmt.Errorf("%s: %w", action.Label, err)
	}
	// A shared action reads no source: it is what it was declared, with
	// its files and its tools, whichever project declared it.
	var files []string
	if !action.IsShared() {
		var err error
		if files, err = snapshot.Select(ctx, action.Repository, action.Sources); err != nil {
			return exec.Action{}, err
		}
	}
	// What a group reads as committed is given as committed, generated
	// source included: nothing generates it for the action.
	var committed map[string]bool
	for i, group := range action.Sources {
		if !group.Committed || action.IsShared() {
			continue
		}
		read, err := snapshot.Select(ctx, action.Repository, action.Sources[i:i+1])
		if err != nil {
			return exec.Action{}, err
		}
		if committed == nil {
			committed = make(map[string]bool, len(read))
		}
		for _, file := range read {
			committed[file] = true
		}
	}
	inputs := make([]string, 0, len(files)+len(action.WorkspaceFiles))
	for _, file := range files {
		if !committed[file] && !action.AcceptsSnapshot(action.Repository, file) {
			continue
		}
		inputs = append(inputs, path.Join(own, file))
	}
	// What its tool reads above it, for the whole workspace, where it is.
	for _, file := range action.WorkspaceFiles {
		if _, _, _, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(folder), filepath.FromSlash(file))); err != nil {
			return exec.Action{}, fmt.Errorf("%s names %s as a file of its workspace, which the repository does not hold", action.Label, path.Join(folder, file))
		}
		inputs = append(inputs, path.Join(own, file))
	}
	// What it is handed of other projects, in the directory its tool
	// looks in.
	copies := map[string]exec.Copy{}
	// What the action is given of its own repository, once it is handed
	// some where it is: each file once.
	var given map[string]bool
	for _, handed := range action.Handed {
		if len(handed.Patterns) == 0 {
			continue
		}
		files, err := snapshot.Within(ctx, handed.Repository, handed.Root, handed.Patterns)
		if err != nil {
			return exec.Action{}, err
		}
		from := snapshot.Paths[handed.Repository]
		// Handed of its own repository at its provider's own directory
		// ({root}), they are that repository's files where they are: the
		// action is given them as its own, beside its sources, and nothing
		// is copied over them.
		where := path.Join(action.ProjectRoot, handed.Into)
		if handed.Repository == action.Repository && where == path.Clean(path.Join(".", handed.Root)) {
			if given == nil {
				given = make(map[string]bool, len(inputs))
				for _, input := range inputs {
					given[input] = true
				}
			}
			for _, file := range files {
				if at := path.Join(own, file); action.AcceptsSnapshot(handed.Repository, file) && !given[at] {
					given[at] = true
					inputs = append(inputs, at)
				}
			}
			continue
		}
		within := make([]string, 0, len(files))
		for _, file := range files {
			if !action.AcceptsSnapshot(handed.Repository, file) {
				continue
			}
			rel, err := filepath.Rel(filepath.FromSlash("/"+handed.Root), filepath.FromSlash("/"+file))
			if err != nil {
				return exec.Action{}, err
			}
			within = append(within, filepath.ToSlash(rel))
		}
		// Dependencies of one directory may share a place (CheckHanded):
		// what each selects of it is placed there together, each file once.
		at := path.Join(own, action.ProjectRoot, handed.Into)
		if placed, is := copies[at]; is {
			within = append(placed.Files, within...)
			slices.Sort(within)
			within = slices.Compact(within)
		}
		copies[at] = exec.Copy{
			From:  filepath.Join(snapshot.Root, filepath.FromSlash(from), filepath.FromSlash(handed.Root)),
			Files: within,
		}
	}
	written := make(map[string]exec.Written, len(action.Arranged)+len(action.Declared.GetSharedFetch().GetFiles()))
	for _, file := range action.Arranged {
		// Above the project, a file is written for the workspace, and
		// never in place of a workspace file the action reads: nothing
		// the run read is shadowed.
		if above(action.ProjectRoot, file.Path) && slices.Contains(action.WorkspaceFiles, path.Clean(file.Path)) {
			return exec.Action{}, fmt.Errorf("%s writes %s for its workspace, which is a workspace file it reads", action.Label, path.Join(folder, file.Path))
		}
		written[path.Join(own, file.Path)] = exec.Written{Content: file.Content, Executable: file.Executable}
	}
	// A shared action's files are its own, in its directory or its home.
	for _, file := range action.Declared.GetSharedFetch().GetFiles() {
		at, _, err := Sits(own, action.ProjectRoot, "", file.GetPath())
		if err != nil {
			return exec.Action{}, err
		}
		written[at] = exec.Written{Content: file.GetContent(), Executable: file.GetExecutable()}
	}
	// Nothing written is put where a file of the source is: one would
	// replace the other without a word.
	for _, input := range inputs {
		if _, over := written[input]; over {
			return exec.Action{}, fmt.Errorf("%s writes %s, which is a file it is given", action.Label, input)
		}
	}
	command := action.Declared.GetCommand()
	arguments, environment := command.GetArguments(), command.GetEnvironment()
	held := make(map[string]exec.Held, len(left)+len(action.Declared.GetSharedFetch().GetDownloads()))
	for at, each := range left {
		held[at] = each
	}
	// What a shared fetch downloads is brought by the asset service and
	// placed in the root by digest, beside the home; the command unpacks
	// each where the fetch says, and reaches nothing.
	fetch := action.Declared.GetFetch() != nil || action.Declared.GetSharedFetch() != nil
	if declared := action.Declared.GetSharedFetch().GetDownloads(); len(declared) > 0 {
		if downloads == nil {
			return exec.Action{}, fmt.Errorf("%s downloads, and the daemon was given no asset service to download with (grid.assets)", action.Label)
		}
		var unpacking, cutting []string
		for _, each := range declared {
			// A download that is cut into parts has no checksum of the
			// whole: it is fetched unchecked, and each part is checked
			// where it is cut.
			digest, err := downloads.Fetch(ctx, each.GetUrl(), each.GetSha256())
			if err != nil {
				return exec.Action{}, fmt.Errorf("%s: %w", action.Label, err)
			}
			at := path.Join(tools.Downloads, digest.Hash)
			held[at] = exec.Held{Digest: digest}
			archive, err := filepath.Rel(filepath.FromSlash("/"+path.Join(own, action.ProjectRoot)), filepath.FromSlash("/"+at))
			if err != nil {
				return exec.Action{}, err
			}
			for _, part := range each.GetParts() {
				_, as, err := Sits(own, action.ProjectRoot, "", part.GetPath())
				if err != nil {
					return exec.Action{}, err
				}
				kind, sum := "sha256", part.GetSha256()
				if part.GetSha1() != "" {
					kind, sum = "sha1", part.GetSha1()
				}
				cutting = append(cutting, filepath.ToSlash(archive), strconv.FormatUint(part.GetOffset(), 10), strconv.FormatUint(part.GetLength(), 10), kind, sum, as, each.GetUrl())
			}
			if len(each.GetParts()) > 0 {
				continue
			}
			_, into, err := Sits(own, action.ProjectRoot, "", each.GetInto())
			if err != nil {
				return exec.Action{}, err
			}
			unpacking = append(unpacking, filepath.ToSlash(archive), into)
		}
		// An action's downloads are all of one form (pluginhost), so one
		// command places them.
		arguments, environment, fetch = download.Unpacking(unpacking...), nil, false
		if len(cutting) > 0 {
			arguments = download.Cutting(cutting...)
		}
	}
	// A shared fetch of what its lock holds whole has no command and
	// downloads nothing: the host's command places nothing, the files it
	// was given are what it leaves, and it reaches nothing.
	if action.IsShared() && len(arguments) == 0 {
		arguments, environment, fetch = download.Unpacking(), nil, false
	}
	outputs := make([]string, 0, len(action.Outputs()))
	for _, output := range action.Outputs() {
		_, asked, err := Sits(own, action.ProjectRoot, action.Declared.GetCommand().GetDirectory(), output)
		if err != nil {
			return exec.Action{}, err
		}
		outputs = append(outputs, asked)
	}
	// What it is handed sits where the other action left it. A file of
	// the repository that the action reads at that place would be two
	// sources of one path, as Bazel refuses a generated file named as a
	// source file is; the grid refuses it too, and says less of why.
	// What is handed of other projects is placed apart (Handed.Into):
	// generated source goes among it by design (generated), and nothing
	// handed sits at the very place of one.
	// It is said by where it is in the workspace, which a person reads,
	// not by where it sits in the action's root.
	if at, is := shadowed(inputs, copies, left); is {
		return exec.Action{}, plan.Shadows(action, path.Join(folder, at))
	}
	platform := provisioner.Platform
	within := maps.Clone(action.Environment)
	bindings := [2]map[string]string{exported}
	// A test with services has a network: its runner sandbox's, which
	// holds a loopback, its services and nothing else; it runs only where
	// it is sent below, on the runners that make such a sandbox, which
	// refuse an action that names none. Any other action that reaches the
	// network, a fetch or a program, is sent to the runner sandboxes whose
	// policy lets them reach it (exec.NetworkProperty); every other action to
	// those that reach nothing.
	// A test with no services runs on the runner sandboxes that reach
	// nothing, as any step of a build does: its sandbox's loopback is its
	// own, and it listens there at any port, as Go's httptest does.
	network := fetch || program
	runner := ""
	if beside := action.Beside; beside != nil {
		network = true
		// A test with services: what to start beside it is in its root, so
		// in its key; it is told what they export; and it is sent to the
		// runner that makes a runner sandbox for each action. Nothing else
		// stands where the runner reads what to start: not a file of the
		// repository, nor one written for the action, nor one another
		// action left in its home.
		if taken(inputs, written, held, copies) {
			return exec.Action{}, fmt.Errorf("%s is given %s, which is where what its runner sandbox holds is written", action.Label, RunnerSandbox)
		}
		written[RunnerSandbox] = exec.Written{Content: beside.Sandbox}
		// What they export is set as written, inside what confines the
		// action and nowhere else (Use): it is what the repository's
		// catalog says, and reaches nothing that starts or confines the
		// action. One set again, by its plugin or anything else, is
		// refused there.
		bindings[1] = beside.Environment
		runner = RunnerSandboxes
	}
	if runner != "" {
		platform = make(map[string]string, len(provisioner.Platform)+1)
		for k, v := range provisioner.Platform {
			platform[k] = v
		}
		platform[RunnerProperty] = RunnerSandboxes
	} else if network {
		platform = exec.Reaching(provisioner.Platform)
	}
	for _, values := range bindings {
		// Most actions are given nothing this way, and are not made to
		// pay for the order a refusal is said in.
		if len(values) == 0 {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(values)) {
			if _, twice := within[name]; twice {
				return exec.Action{}, fmt.Errorf("%s is given %s by both test configuration and a service", action.Label, name)
			}
			if within == nil {
				within = make(map[string]string, len(values))
			}
			within[name] = values[name]
		}
	}
	// A test is told where its repository is, as it stands in its root:
	// what it reads beyond its package it finds there, and never by
	// looking up from where it runs. Nothing else names it: not its
	// Component's catalog file, a service, nor the plugin that made it.
	if action.IsTest() {
		_, given := within[SourceVariable]
		if _, said := environment[SourceVariable]; given || said {
			return exec.Action{}, fmt.Errorf("%s must be given %s as it is, and what runs it sets %s itself", action.Label, SourceVariable, SourceVariable)
		}
		if within == nil {
			within = map[string]string{}
		}
		from := path.Join(own, action.ProjectRoot, action.Declared.GetCommand().GetDirectory())
		up, err := filepath.Rel(filepath.FromSlash("/"+from), filepath.FromSlash("/"+own))
		if err != nil {
			return exec.Action{}, err
		}
		within[SourceVariable] = filepath.ToSlash(up)
	}
	return provisioner.Use(ctx, exec.Action{
		Label: action.Label.String(),
		// Its files are read from its repository's directory, and each
		// sits at its path there in the root.
		Root:    snapshot.Directory(action.Repository),
		Inputs:  inputs,
		Files:   snapshot,
		Held:    held,
		Copies:  copies,
		Written: written,
		Outputs: outputs,
		// In its project's directory, or beneath it where its command
		// says so, which the plugin host held to the project.
		WorkingDirectory: path.Join(own, action.ProjectRoot, action.Declared.GetCommand().GetDirectory()),
		Arguments:        arguments,
		Environment:      environment,
		Platform:         platform,
		Fresh:            action.Fresh,
	}, own, action.Declared.GetTools(), network, within)
}

// own is where an action's own repository is in its root: at the top.
const own = "."

// placed returns what an action of a repository is handed, each thing
// by where it sits in the action's root, of what is kept by where it
// sits in the workspace (Left): what is in the action's own repository's
// directory, at the top; what is in another repository's, beneath
// tools.External, in a directory of the workspace's name for that
// repository, never of where the workspace holds it; and anything else,
// its home, where it is. Where nothing moves, as for every action of a
// checkout built on its own, what was handed is returned as it is. Two
// places of the workspace that would sit at one place in the root are
// refused, naming both: neither is dropped for the other.
func placed(tree source.Tree, repository string, left map[string]exec.Held) (map[string]exec.Held, error) {
	if len(left) == 0 || (len(tree.Paths) == 1 && tree.Paths[repository] == own) {
		return left, nil
	}
	out := make(map[string]exec.Held, len(left))
	for at, held := range left {
		in := place(tree, repository, at)
		if _, there := out[in]; there {
			return nil, collided(tree, repository, left, at, in)
		}
		out[in] = held
	}
	return out, nil
}

// collided says which two places of the workspace an action of a
// repository would be handed at one place of its root (placed), the
// first of them first.
func collided(tree source.Tree, repository string, left map[string]exec.Held, at, in string) error {
	first, second := at, at
	for other := range left {
		if other != at && place(tree, repository, other) == in {
			first, second = min(other, at), max(other, at)
		}
	}
	return fmt.Errorf("it is handed %s and %s of the workspace, which would both be at %s in its root", first, second, in)
}

// place returns where a place of the workspace sits in the root of an
// action of a repository (placed): in the one repository's directory
// that holds it. No repository's directory is within another's: a
// snapshot of such a workspace is refused (source.Keeper.Take), so a
// place is never in two.
func place(tree source.Tree, repository, at string) string {
	// Its home is no repository's, even where one is at the top of the
	// workspace.
	if at == tools.Home || strings.HasPrefix(at, tools.Home+"/") {
		return at
	}
	for name, folder := range tree.Paths {
		var within string
		switch {
		case folder == own || folder == "":
			within = at
		case at == folder:
			within = own
		case strings.HasPrefix(at, folder) && at[len(folder)] == '/':
			within = at[len(folder)+1:]
		default:
			continue
		}
		if name == repository {
			return within
		}
		return path.Join(tools.External, name, within)
	}
	return at
}

// taken says whether something of the action's own already stands where
// what its runner sandbox holds is written: a file of the repository it
// reads, one written for it, or one another action left.
func taken(inputs []string, written map[string]exec.Written, held map[string]exec.Held, copies ...map[string]exec.Copy) bool {
	reserved := func(at string) bool {
		at = path.Clean(at)
		return at == "." || at == RuntimeDirectory || strings.HasPrefix(at, RuntimeDirectory+"/") || strings.HasPrefix(RuntimeDirectory, at+"/")
	}
	for _, at := range inputs {
		if reserved(at) {
			return true
		}
	}
	for at := range written {
		if reserved(at) {
			return true
		}
	}
	for at := range held {
		if reserved(at) {
			return true
		}
	}
	for _, group := range copies {
		for at, copy := range group {
			for _, file := range copy.Files {
				if reserved(path.Join(at, file)) {
					return true
				}
			}
		}
	}
	return false
}

const (
	// RunnerSandbox is where in an action's root what its runner sandbox
	// holds is written (frontseat.sandbox.v1alpha1.RunnerSandbox).
	RunnerSandbox = testservice.RunnerDescription
	// SourceVariable is what a test is told its repository is at: a
	// path from the directory it runs in, in its root. What it reads of
	// the repository beyond its own package, as its Component's catalog
	// file names it (spec.frontseat.test.inputs), is beneath it.
	SourceVariable = "FRONTSEAT_TEST_SRCDIR"
	// RuntimeDirectory is masked from the consuming test's mounted view.
	RuntimeDirectory = testservice.RuntimeDirectory
	// RunnerProperty is the platform property a test with services is
	// sent by, and RunnerSandboxes its value: the workers whose runner
	// makes a runner sandbox for each action advertise it.
	RunnerProperty  = "frontseat-runner"
	RunnerSandboxes = "sandbox-v2"
)

// shadowed returns a place something handed sits at where a file the
// action reads of the repository is, or is within, or where what it is
// handed of another project is placed. The inputs are files, so each
// handed place is looked for among them in order, by itself and as a
// directory. An action handed nothing is not looked at.
func shadowed(inputs []string, copies map[string]exec.Copy, left map[string]exec.Held) (string, bool) {
	if len(left) == 0 {
		return "", false
	}
	// A few places, as most actions are handed, are each looked for
	// among the inputs as they are; many are looked up among them in
	// order. Either way the first place in order is the one named.
	if len(left) <= lookedFor {
		var few [lookedFor]string
		n := 0
		for at := range left {
			few[n] = at
			n++
		}
		slices.Sort(few[:n])
		for _, at := range few[:n] {
			if _, placed := copies[at]; placed {
				return at, true
			}
			for _, input := range inputs {
				if strings.HasPrefix(input, at) && (len(input) == len(at) || input[len(at)] == '/') {
					return at, true
				}
			}
		}
		return "", false
	}
	sorted := slices.Clone(inputs)
	slices.Sort(sorted)
	places := make([]string, 0, len(left))
	for at := range left {
		places = append(places, at)
	}
	slices.Sort(places)
	for _, at := range places {
		if _, placed := copies[at]; placed {
			return at, true
		}
		// The place itself, or the first file within it: which is not
		// the file after the place, where a neighbour's name sorts
		// between the two ("made-x" between "made" and "made/").
		if _, found := slices.BinarySearch(sorted, at); found {
			return at, true
		}
		if i, _ := slices.BinarySearch(sorted, at+"/"); i < len(sorted) && strings.HasPrefix(sorted[i], at+"/") {
			return at, true
		}
	}
	return "", false
}

// lookedFor is how many handed places are each looked for among an
// action's inputs as they are, before the inputs are put in order.
const lookedFor = 4

// above reports whether a file of the repository is not beneath a
// project's directory.
func above(project, file string) bool {
	rel, err := filepath.Rel(filepath.FromSlash("/"+project), filepath.FromSlash("/"+file))
	return err != nil || rel == ".." || strings.HasPrefix(rel, "../")
}

// Left returns what an action that passed left, of what the grid says it
// left: each thing by where it sits in the workspace, which an action
// that needs it is handed it by (placed puts it where that action finds
// it, in its own root). It also returns the first output the
// action says it writes and did not leave, as the action wrote it; an
// action that passed leaves what it says it writes, or whatever needs it
// would run without it. An output it says it may not leave is not
// missed.
func Left(tree source.Tree, action plan.Action, outputs map[string]exec.Held) (left map[string]exec.Held, missing string, err error) {
	left = map[string]exec.Held{}
	for _, output := range action.Declared.GetOutputs() {
		// Asked of the grid from where it ran, its repository at the top
		// of its root; kept by where it sits in the workspace.
		_, asked, err := Sits(own, action.ProjectRoot, action.Declared.GetCommand().GetDirectory(), output.GetPath())
		if err != nil {
			return nil, "", err
		}
		at := Place(tree.Paths[action.Repository], action.ProjectRoot, output.GetPath())
		held, there := outputs[asked]
		switch {
		case there:
			left[at] = held
		case !output.GetOptional():
			return left, output.GetPath(), nil
		}
	}
	return left, "", nil
}

// Sits returns where an output of an action sits in the action's root,
// given where its repository is there (own, the top, for every action
// of the grid; its directory in the workspace for whoever keeps it by
// where it is there), its project's directory and the directory beneath that its command runs
// in, empty for the project's own; and how it is asked of the grid:
// relative to where the command runs. An output is in the action's
// project, or, written "~/…", in its home. It is where an action that
// reads the output finds it.
func Sits(repository, projectRoot, directory, output string) (at, asked string, err error) {
	at = Place(repository, projectRoot, output)
	rel, err := filepath.Rel(filepath.FromSlash("/"+path.Join(repository, projectRoot, directory)), filepath.FromSlash("/"+at))
	return at, filepath.ToSlash(rel), err
}

// Place returns where an output of an action sits in the action's root:
// the first of what Sits returns, for whoever asks nothing of the grid.
func Place(repository, projectRoot, output string) string {
	output = strings.TrimSuffix(output, "/")
	if rest, inHome := strings.CutPrefix(output, "~/"); inHome {
		return path.Join(tools.Home, rest)
	}
	return path.Join(repository, projectRoot, output)
}
