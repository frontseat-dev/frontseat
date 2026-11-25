package reapi_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/digest"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
)

// served starts a grid that keeps what it ran by instance (Keeping),
// and returns a function that dials it as a daemon told an instance,
// and the instances it reads, is.
func served(t *testing.T, grid *reapitest.Keeping) func(instance string, reads ...string) *reapi.Grid {
	t.Helper()
	address := grid.Serve(t)
	return func(instance string, reads ...string) *reapi.Grid {
		t.Helper()
		dialed, err := reapi.Dial(t.Context(), reapi.Config{Address: address, Instance: instance, Reads: reads})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { dialed.Close() })
		return dialed
	}
}

// anAction is one action, the same each time it is made.
func anAction(t *testing.T) exec.Action {
	return exec.Action{Label: "//test:compile", Root: t.TempDir(), Inputs: []string{"."}, Arguments: []string{"tool"}}
}

func executed(t *testing.T, grid *reapi.Grid, action exec.Action) exec.Result {
	t.Helper()
	result, err := grid.Execute(t.Context(), action)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// A daemon told other instances to read looks an action's result up in
// its own instance first and then in each of those, in the order given,
// and uses what one of them kept: the grid runs nothing for it. The
// control is the same daemon told no other instance, for which the grid
// runs the action again, under the daemon's own instance; and one told
// another instance that kept nothing, which runs it under its own and
// never under one it reads.
func TestAResultKeptUnderAnInstanceADaemonReadsIsUsedAndNothingIsRun(t *testing.T) {
	grid := &reapitest.Keeping{}
	dial := served(t, grid)
	executed(t, dial("frontseat/b"), anAction(t))

	daemon := dial("frontseat/p", "frontseat/q", "frontseat/b")
	dialled, _, _ := grid.Asked()
	got := executed(t, daemon, anAction(t))
	looked, ran, _ := grid.Asked()
	looked = looked[len(dialled):]
	if !got.Cached || !slices.Equal(ran, []string{"frontseat/b"}) {
		t.Errorf("a daemon of frontseat/p that reads frontseat/b, where frontseat/b kept the action: the grid ran it for %v (cached: %v), want it run once, for frontseat/b, and the result taken from what it kept", ran, got.Cached)
	}
	if want := []string{"frontseat/p", "frontseat/q", "frontseat/b"}; !slices.Equal(looked, want) {
		t.Errorf("the grid's cache was asked of %v, want %v: the daemon's own instance first, then each it reads in order", looked, want)
	}

	// The control: told no other instance, the action is run again.
	control := &reapitest.Keeping{}
	dialControl := served(t, control)
	executed(t, dialControl("frontseat/b"), anAction(t))
	if again := executed(t, dialControl("frontseat/p"), anAction(t)); again.Cached {
		t.Error("the control: a daemon of frontseat/p that reads nothing else was answered from what frontseat/b kept")
	}
	if _, ran, _ := control.Asked(); !slices.Equal(ran, []string{"frontseat/b", "frontseat/p"}) {
		t.Errorf("the control: the grid ran the action for %v, want for frontseat/b and then for frontseat/p", ran)
	}

	// Where nothing it reads kept it, it is run under its own instance.
	missing := &reapitest.Keeping{}
	daemon = served(t, missing)("frontseat/p", "frontseat/b")
	dialled, _, _ = missing.Asked()
	executed(t, daemon, anAction(t))
	if looked, ran, _ := missing.Asked(); !slices.Equal(ran, []string{"frontseat/p"}) || !slices.Equal(looked[len(dialled):], []string{"frontseat/p", "frontseat/b"}) {
		t.Errorf("a miss everywhere: the cache was asked of %v and the action run for %v, want asked of frontseat/p then frontseat/b, and run for frontseat/p alone", looked, ran)
	}
}

// An action that is to run fresh is run, whatever the grid kept: its
// cache is asked of no instance, the daemon's own nor one it reads, and
// the grid is told to answer from none. The control is the same action
// not fresh, which the grid answers from what it kept.
func TestAFreshActionIsRunWhateverTheGridKept(t *testing.T) {
	grid := &reapitest.Keeping{}
	dial := served(t, grid)
	executed(t, dial("frontseat/b"), anAction(t))
	daemon := dial("frontseat/p", "frontseat/b")

	// The control: not fresh, it is answered from what was kept.
	if got := executed(t, daemon, anAction(t)); !got.Cached {
		t.Fatal("the control: an action kept under an instance read was not answered from it")
	}
	lookedBefore, _, _ := grid.Asked()

	fresh := anAction(t)
	fresh.Fresh = true
	got := executed(t, daemon, fresh)
	looked, ran, skipped := grid.Asked()
	if got.Cached || !slices.Equal(ran, []string{"frontseat/b", "frontseat/p"}) || !skipped[len(skipped)-1] {
		t.Errorf("a fresh action: cached %v, run for %v, told to skip the cache %v; want it run for frontseat/p, told to answer from no cache", got.Cached, ran, skipped)
	}
	if asked := looked[len(lookedBefore):]; len(asked) != 0 {
		t.Errorf("for a fresh action the grid's cache was asked of %v, want of nothing", asked)
	}
}

// Fresh is no part of what an action is: run fresh, and then not, it is
// one action, of one digest and one key, and the second is answered
// from what the first left. The control is another action, of another
// digest.
func TestFreshIsNoPartOfAnActionsDigestOrKey(t *testing.T) {
	grid := &reapitest.Keeping{}
	daemon := served(t, grid)("frontseat/p")
	fresh := anAction(t)
	fresh.Fresh = true
	first := executed(t, daemon, fresh)
	again := executed(t, daemon, anAction(t))
	if first.Action.Hash == "" || first.Action != again.Action {
		t.Errorf("run fresh the action is %v, and not fresh %v: want one digest", first.Action, again.Action)
	}
	if !again.Cached {
		t.Error("the action not fresh, after it was run fresh, was run again: want it answered from what the fresh run left")
	}
	freshKey, err := daemon.Key(t.Context(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := daemon.Key(t.Context(), anAction(t)); err != nil || key != freshKey {
		t.Errorf("the action's key is %s fresh and %s (%v) not: want one key", freshKey, key, err)
	}
	other := anAction(t)
	other.Arguments = []string{"another"}
	if got := executed(t, daemon, other); got.Action == first.Action {
		t.Errorf("the control: another action is of the digest %v too", got.Action)
	}
}

// What an instance the daemon reads answers, other than that it kept
// nothing, is taken for nothing kept: the action is looked up in the
// next it reads, and run under its own where none kept it, since running
// it is always right and what another instance kept is only a shortcut.
// The control is the daemon's own instance failing so, which fails the
// action, as it did before any instance was read. An instance read is
// asked once for an action, never again for an answer that may pass.
func TestAnInstanceReadThatFailsIsAMissAndTheDaemonsOwnIsNot(t *testing.T) {
	grid := &reapitest.Keeping{}
	dial := served(t, grid)
	executed(t, dial("frontseat/b"), anAction(t))
	daemon := dial("frontseat/p", "frontseat/x", "frontseat/b")
	for _, code := range []codes.Code{codes.Unavailable, codes.InvalidArgument, codes.PermissionDenied, codes.Internal} {
		grid.Refuse("frontseat/x", code)
		if got, err := daemon.Execute(t.Context(), anAction(t)); err != nil || !got.Cached {
			t.Errorf("an instance read before frontseat/b answering %s: %v (cached %v); want what frontseat/b kept", code, err, got.Cached)
		}
	}
	looked, ran, _ := grid.Asked()
	if !slices.Equal(ran, []string{"frontseat/b"}) {
		t.Errorf("the grid ran the action for %v, want for frontseat/b alone", ran)
	}
	// Asked once when the daemon dialled, and once for each action: an
	// instance read is not asked again, as the daemon's own is, since
	// that would hold each action up for a shortcut.
	asked := 0
	for _, each := range looked {
		if each == "frontseat/x" {
			asked++
		}
	}
	if asked != 5 {
		t.Errorf("the instance that fails was asked %d times, want once as the daemon dialled and once for each of 4 actions", asked)
	}

	// Where the instance that fails is the only one read, it is run.
	alone := &reapitest.Keeping{}
	dialAlone := served(t, alone)
	daemon = dialAlone("frontseat/p", "frontseat/x")
	alone.Refuse("frontseat/x", codes.Unavailable)
	if got, err := daemon.Execute(t.Context(), anAction(t)); err != nil || got.Cached {
		t.Errorf("the one instance read failing: %v (cached %v); want the action run", err, got.Cached)
	}
	if _, ran, _ := alone.Asked(); !slices.Equal(ran, []string{"frontseat/p"}) {
		t.Errorf("the one instance read failing, the grid ran the action for %v, want for frontseat/p", ran)
	}

	// The control: the daemon's own instance failing fails the action.
	alone.Refuse("frontseat/p", codes.Unavailable)
	if _, err := daemon.Execute(t.Context(), anAction(t)); err == nil || status.Code(err) != codes.Unavailable {
		t.Errorf("the control: the daemon's own instance failing: %v; want the action failed with what it answered", err)
	}
}

// A daemon asks the grid once, when it dials, of each instance it reads,
// and one the grid answers it does not serve is refused by name then and
// not asked again: Buildbarn answers InvalidArgument only for an
// instance's name it rejects or one it serves no one (its action cache's
// "Invalid instance name", a demultiplexer's "Unknown instance name"),
// and would answer the same again. The refusal says which entry of
// grid.reads it is and never its name, nor the grid's words, which
// repeat it. The control is each other answer, which is dialled: that it
// kept nothing; a failure that may pass; and PermissionDenied, which
// Buildbarn answers for an authorizer it cannot reach or a token run out
// as well as for an instance it does not let the daemon read (bb-storage's
// remoteAuthorizer at ae61334), and which the actions the
// daemon builds meet as a miss, as any failure of an instance read.
func TestAnInstanceTheDaemonCannotReadIsRefusedWhenItDials(t *testing.T) {
	grid := &reapitest.Keeping{}
	address := grid.Serve(t)
	grid.Refuse("frontseat/x", codes.InvalidArgument)
	dialed, err := reapi.Dial(t.Context(), reapi.Config{Address: address, Instance: "frontseat/p", Reads: []string{"frontseat/b", "frontseat/x"}})
	if err == nil {
		dialed.Close()
	}
	said := fmt.Sprint(err)
	if !reapi.Unread(err) || !reapi.Settled(err) || reapi.NotAdmitted(err) || !strings.Contains(said, "grid.reads' entry 2") {
		t.Errorf("an instance read the grid answers InvalidArgument: %v; want it refused by name, settled, saying which entry of grid.reads, and not said to be the grid not admitting the daemon", err)
	}
	if strings.Contains(said, "frontseat/x") {
		t.Errorf("the refusal of an instance read repeats its name: %v", err)
	}
	if looked, _, _ := grid.Asked(); !slices.Equal(looked, []string{"frontseat/b", "frontseat/x"}) {
		t.Errorf("dialling, the grid's cache was asked of %v, want each instance read, once", looked)
	}
	// The control: kept nothing, failing for now, or not letting the
	// daemon read it for now, it is dialled, and an action is run.
	for _, code := range []codes.Code{codes.NotFound, codes.Unavailable, codes.PermissionDenied, codes.Unauthenticated} {
		grid := &reapitest.Keeping{}
		address := grid.Serve(t)
		grid.Refuse("frontseat/x", code)
		dialed, err := reapi.Dial(t.Context(), reapi.Config{Address: address, Instance: "frontseat/p", Reads: []string{"frontseat/x"}})
		if err != nil {
			t.Errorf("the control: an instance read the grid answers %s: %v; want it dialled", code, err)
			continue
		}
		if got, err := dialed.Execute(t.Context(), anAction(t)); err != nil || got.Cached {
			t.Errorf("the control: an instance read the grid answers %s: %v (cached %v); want the action run", code, err, got.Cached)
		}
		dialed.Close()
	}
}

// What an instance the daemon reads kept is used only where the grid
// holds, under the daemon's own instance, every file it names: what the
// action printed, each file it left, each directory it left and every
// file in it. Where one is not held the result is no answer, and the
// action is run under the daemon's own instance, never failed for it.
// The control is the same result with every file held, which is used.
func TestAResultReadWhoseFilesAreNotAllHeldIsRunAgain(t *testing.T) {
	absent := func(name string) *repb.Digest { return digest.NewFromBlob([]byte("absent " + name)).ToProto() }
	for name, made := range map[string]func(grid *reapitest.Keeping, gone bool) *repb.ActionResult{
		"what it printed": func(grid *reapitest.Keeping, gone bool) *repb.ActionResult {
			printed := grid.Holds([]byte("printed"))
			if gone {
				printed = absent("printed")
			}
			return &repb.ActionResult{StdoutDigest: printed}
		},
		"what it printed as an error": func(grid *reapitest.Keeping, gone bool) *repb.ActionResult {
			printed := grid.Holds([]byte("said"))
			if gone {
				printed = absent("said")
			}
			return &repb.ActionResult{StderrDigest: printed}
		},
		"a file it left": func(grid *reapitest.Keeping, gone bool) *repb.ActionResult {
			left := grid.Holds([]byte("left"))
			if gone {
				left = absent("left")
			}
			return &repb.ActionResult{OutputFiles: []*repb.OutputFile{{Path: "out/left", Digest: left}}}
		},
		"a directory it left": func(grid *reapitest.Keeping, gone bool) *repb.ActionResult {
			tree, err := proto.Marshal(&repb.Tree{Root: &repb.Directory{}})
			if err != nil {
				t.Fatal(err)
			}
			kept := digest.NewFromBlob(tree).ToProto()
			if !gone {
				kept = grid.Holds(tree)
			}
			return &repb.ActionResult{OutputDirectories: []*repb.OutputDirectory{{Path: "out/tree", TreeDigest: kept}}}
		},
		"a file in the root of a directory it left": func(grid *reapitest.Keeping, gone bool) *repb.ActionResult {
			top := grid.Holds([]byte("top"))
			if gone {
				top = absent("top")
			}
			return leftInATree(t, grid, top)
		},
		"a file in a directory it left": func(grid *reapitest.Keeping, gone bool) *repb.ActionResult {
			inner := grid.Holds([]byte("inner"))
			if gone {
				inner = absent("inner")
			}
			deeper := &repb.Directory{Files: []*repb.FileNode{{Name: "deep", Digest: inner}}}
			deeperDigest := digest.TestNewFromMessage(deeper).ToProto()
			tree, err := proto.Marshal(&repb.Tree{Root: &repb.Directory{Directories: []*repb.DirectoryNode{{Name: "d", Digest: deeperDigest}}}, Children: []*repb.Directory{deeper}})
			if err != nil {
				t.Fatal(err)
			}
			return &repb.ActionResult{OutputDirectories: []*repb.OutputDirectory{{Path: "out/tree", TreeDigest: grid.Holds(tree)}}}
		},
	} {
		for _, gone := range []bool{true, false} {
			got, ran, err := readKept(t, func(grid *reapitest.Keeping) *repb.ActionResult { return made(grid, gone) }, nil)
			switch {
			case err != nil:
				t.Errorf("%s, held %v: %v; want the action answered", name, !gone, err)
			case gone && (got.Cached || !slices.Equal(ran, []string{"frontseat/b", "frontseat/p"})):
				t.Errorf("%s not held: cached %v, run for %v; want it run again, for frontseat/p", name, got.Cached, ran)
			case !gone && (!got.Cached || !slices.Equal(ran, []string{"frontseat/b"})):
				t.Errorf("the control: %s held: cached %v, run for %v; want what frontseat/b kept", name, got.Cached, ran)
			}
		}
	}
}

// A fresh action the grid answers from its cache though it was told not
// to is refused by name: what a release ships is made for it. The
// control is the same grid answering an action that is not fresh so,
// which is taken.
func TestAFreshActionTheGridAnswersFromItsCacheIsRefused(t *testing.T) {
	grid := &reapitest.Keeping{Cached: true}
	daemon := served(t, grid)("frontseat/p")
	fresh := anAction(t)
	fresh.Fresh = true
	if _, err := daemon.Execute(t.Context(), fresh); err == nil || !strings.Contains(err.Error(), "answered it from its cache") {
		t.Errorf("a fresh action the grid answered from its cache: %v; want it refused, saying so", err)
	}
	if got, err := daemon.Execute(t.Context(), anAction(t)); err != nil || !got.Cached {
		t.Errorf("the control: an action not fresh the grid answered from its cache: %v (cached %v)", err, got.Cached)
	}
}

// readKept has a grid keep, under frontseat/b, a result for an action
// that made returns, and has a daemon of frontseat/p that reads
// frontseat/b execute that action: what it answered, and the instances
// the grid ran it for. found, where given, is called with the grid
// before the daemon executes it.
func readKept(t *testing.T, made func(grid *reapitest.Keeping) *repb.ActionResult, found func(grid *reapitest.Keeping)) (exec.Result, []string, error) {
	t.Helper()
	grid := &reapitest.Keeping{}
	dial := served(t, grid)
	kept := executed(t, dial("frontseat/b"), anAction(t))
	grid.Keep("frontseat/b", kept.Action.Hash, made(grid))
	if found != nil {
		found(grid)
	}
	got, err := dial("frontseat/p", "frontseat/b").Execute(t.Context(), anAction(t))
	_, ran, _ := grid.Asked()
	return got, ran, err
}

// A result read from another instance that names a file by no digest,
// or by a digest of no bytes whose hash is not the hash of no bytes, is
// none to use, and the action is run, never failed for it: a file of no
// bytes is held by every grid only where its digest is the empty blob's.
// The control is the same result naming its files by the empty blob's
// digest, which is used.
func TestAResultReadThatNamesAFileByNoDigestIsRunAgain(t *testing.T) {
	wrong := &repb.Digest{Hash: digest.NewFromBlob([]byte("not empty")).Hash, SizeBytes: 0}
	inTree := func(grid *reapitest.Keeping, file *repb.Digest) *repb.ActionResult { return leftInATree(t, grid, file) }
	leaves := func(_ *reapitest.Keeping, file *repb.Digest) *repb.ActionResult {
		return &repb.ActionResult{OutputFiles: []*repb.OutputFile{{Path: "out/f", Digest: file}}}
	}
	for name, made := range map[string]func(grid *reapitest.Keeping) *repb.ActionResult{
		"a file it left of no digest":                 func(grid *reapitest.Keeping) *repb.ActionResult { return leaves(grid, nil) },
		"a file it left of no bytes and another hash": func(grid *reapitest.Keeping) *repb.ActionResult { return leaves(grid, wrong) },
		"a directory it left of no digest": func(*reapitest.Keeping) *repb.ActionResult {
			return &repb.ActionResult{OutputDirectories: []*repb.OutputDirectory{{Path: "out/tree"}}}
		},
		"a file in a directory of no digest":                 func(grid *reapitest.Keeping) *repb.ActionResult { return inTree(grid, nil) },
		"a file in a directory of no bytes and another hash": func(grid *reapitest.Keeping) *repb.ActionResult { return inTree(grid, wrong) },
	} {
		// Each on a grid that says it holds no such file, and on one
		// that says it holds every file of no bytes, whatever its hash,
		// as a grid that misbehaves could: the daemon does not ask.
		for misbehaving, found := range misbehaves {
			got, ran, err := readKept(t, made, found)
			if err != nil || got.Cached || !slices.Equal(ran, []string{"frontseat/b", "frontseat/p"}) {
				t.Errorf("%s, on %s: %v (cached %v, run for %v); want it run again, for frontseat/p", name, misbehaving, err, got.Cached, ran)
			}
		}
	}
	// The control: files of no bytes named by the empty blob's digest.
	for name, made := range map[string]func(grid *reapitest.Keeping) *repb.ActionResult{
		"a file it left of no bytes":        func(grid *reapitest.Keeping) *repb.ActionResult { return leaves(grid, digest.Empty.ToProto()) },
		"a file in a directory of no bytes": func(grid *reapitest.Keeping) *repb.ActionResult { return inTree(grid, digest.Empty.ToProto()) },
	} {
		for misbehaving, found := range misbehaves {
			got, ran, err := readKept(t, made, found)
			if err != nil || !got.Cached || !slices.Equal(ran, []string{"frontseat/b"}) {
				t.Errorf("the control: %s, on %s: %v (cached %v, run for %v); want what frontseat/b kept", name, misbehaving, err, got.Cached, ran)
			}
		}
	}
}

// misbehaves are a grid that says what it holds as it holds it, and one
// that says it holds every file of no bytes, whatever its hash.
var misbehaves = map[string]func(grid *reapitest.Keeping){
	"a grid that says what it holds":                 nil,
	"a grid that says it holds any file of no bytes": (*reapitest.Keeping).HoldAnyOfNoBytes,
}

// An instance read that the grid keeps answering other than with what
// it kept, or that it kept nothing, is said in the daemon's log once,
// however many actions meet it: by its entry of grid.reads and the code
// the grid answered, never by its name, which the grid's words repeat.
// The control is an instance read that kept nothing, which is said
// nowhere: that is an instance's answer, every action that is new.
func TestAnInstanceReadTheGridKeepsRefusingIsToldOnce(t *testing.T) {
	for _, code := range []codes.Code{codes.PermissionDenied, codes.NotFound} {
		grid := &reapitest.Keeping{}
		address := grid.Serve(t)
		var said strings.Builder
		daemon, err := reapi.Dial(t.Context(), reapi.Config{Address: address, Instance: "frontseat/p", Reads: []string{"frontseat/b", "frontseat/x"}, Log: &said})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { daemon.Close() })
		grid.Refuse("frontseat/x", code)
		other := anAction(t)
		other.Arguments = []string{"another"}
		for _, action := range []exec.Action{anAction(t), other} {
			if got, err := daemon.Execute(t.Context(), action); err != nil || got.Cached {
				t.Errorf("%s: an instance read answering %s: %v (cached %v); want the action run", action.Arguments, code, err, got.Cached)
			}
		}
		told := said.String()
		if code == codes.NotFound {
			if told != "" {
				t.Errorf("the control: an instance read that kept nothing is said in the daemon's log: %q; want nothing said", told)
			}
			continue
		}
		if strings.Count(told, "\n") != 1 || !strings.Contains(told, "grid.reads' entry 2") || !strings.Contains(told, code.String()) {
			t.Errorf("an instance read answering %s to two actions: the daemon's log says %q; want one line, saying grid.reads' entry 2 and %s", code, told, code)
		}
		if strings.Contains(told, "frontseat/x") {
			t.Errorf("what is said of an instance read repeats its name: %q", told)
		}
	}
}

// A grid that runs nothing for the daemon's own instance is refused when
// it is dialled, saying so by its role and never by its name. The
// control is the same grid running actions, which is dialled.
func TestAGridThatRunsNothingForTheDaemonIsRefusedWithoutItsInstancesName(t *testing.T) {
	for _, nothing := range []bool{true, false} {
		grid := &reapitest.Answering{ExecutesNothing: nothing}
		dialed, err := reapi.Dial(t.Context(), reapi.Config{Address: grid.Serve(t), Instance: "frontseat/p"})
		if err == nil {
			dialed.Close()
		}
		switch {
		case !nothing && err != nil:
			t.Errorf("the control: a grid that runs actions: %v; want it dialled", err)
		case nothing && (err == nil || !strings.Contains(err.Error(), "grid.instance")):
			t.Errorf("a grid that runs nothing: %v; want it refused, saying grid.instance", err)
		case nothing && strings.Contains(err.Error(), "frontseat/p"):
			t.Errorf("the refusal of a grid that runs nothing repeats the instance's name: %v", err)
		}
	}
}

// A result read from another instance whose tree the grid fails to
// read fails the action, saying which entry of grid.reads it was read
// from and never that instance's name. The control is the same tree
// read, whose result is used.
func TestAResultReadThatCannotBeReadSaysItsEntryAndNotItsName(t *testing.T) {
	for _, failing := range []bool{true, false} {
		var tree *repb.Digest
		made := func(grid *reapitest.Keeping) *repb.ActionResult {
			result := leftInATree(t, grid, grid.Holds([]byte("f")))
			tree = result.GetOutputDirectories()[0].GetTreeDigest()
			return result
		}
		found := func(grid *reapitest.Keeping) {
			if failing {
				grid.FailReading(tree, codes.FailedPrecondition)
			}
		}
		got, _, err := readKept(t, made, found)
		switch {
		case !failing && (err != nil || !got.Cached):
			t.Errorf("the control: a tree read: %v (cached %v); want what frontseat/b kept", err, got.Cached)
		case failing && (err == nil || !strings.Contains(err.Error(), "grid.reads' entry 1")):
			t.Errorf("a tree the grid fails to read: %v; want the action failed, saying grid.reads' entry 1", err)
		case failing && strings.Contains(err.Error(), "frontseat/b"):
			t.Errorf("the failure of a result read repeats the instance's name: %v", err)
		}
	}
}

// A result read from another instance whose tree the grid let go of
// after it said it held it, before the daemon read it, is a miss, as one
// the grid said it did not hold: the action is run, never failed for it.
// The control is the same tree held throughout, which is used.
func TestAResultReadWhoseTreeIsLetGoOfWhileItIsReadIsRunAgain(t *testing.T) {
	for _, evicted := range []bool{true, false} {
		var tree *repb.Digest
		made := func(grid *reapitest.Keeping) *repb.ActionResult {
			blob, err := proto.Marshal(&repb.Tree{Root: &repb.Directory{Files: []*repb.FileNode{{Name: "f", Digest: grid.Holds([]byte("f"))}}}})
			if err != nil {
				t.Fatal(err)
			}
			tree = grid.Holds(blob)
			return &repb.ActionResult{OutputDirectories: []*repb.OutputDirectory{{Path: "out/tree", TreeDigest: tree}}}
		}
		found := func(grid *reapitest.Keeping) {
			if evicted {
				grid.LetGoOnceFound(tree)
			}
		}
		got, ran, err := readKept(t, made, found)
		switch {
		case evicted && (err != nil || got.Cached || !slices.Equal(ran, []string{"frontseat/b", "frontseat/p"})):
			t.Errorf("a tree let go of while it is read: %v (cached %v, run for %v); want the action run again, for frontseat/p", err, got.Cached, ran)
		case !evicted && (err != nil || !got.Cached || !slices.Equal(ran, []string{"frontseat/b"})):
			t.Errorf("the control: a tree held throughout: %v (cached %v, run for %v); want what frontseat/b kept", err, got.Cached, ran)
		}
	}
}

// leftInATree is the result of an action that left a directory of one
// file, in the root of its tree, which the grid holds.
func leftInATree(t *testing.T, grid *reapitest.Keeping, file *repb.Digest) *repb.ActionResult {
	t.Helper()
	tree, err := proto.Marshal(&repb.Tree{Root: &repb.Directory{Files: []*repb.FileNode{{Name: "f", Digest: file}}}})
	if err != nil {
		t.Fatal(err)
	}
	return &repb.ActionResult{OutputDirectories: []*repb.OutputDirectory{{Path: "out/tree", TreeDigest: grid.Holds(tree)}}}
}
