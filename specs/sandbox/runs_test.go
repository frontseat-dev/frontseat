package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"

	. "github.com/onsi/gomega"
)

// A run runs a program the workspace builds. What can be run is
// said by a plugin from its ecosystem's own files: the Go plugin offers
// each directory of a module that says package main, as run/ and the
// name the go command gives its program. A run builds what the
// program is made of on the grid, as any build does, brings the program
// to where the daemon is by its digest, and starts it there, in the
// daemon's sandbox, with the arguments it was given; it is there until
// the program ends or the run is cancelled. No build runs a program,
// and a run runs nothing that is not one.
//
// The sandbox is the workers' platform, so what a worker built runs
// where the daemon is.
// Runs / A program of a module is built on the grid and run where the daemon is, with what it is given.
func TestAProgramOfAModuleIsBuiltOnTheGridAndRunWhereTheDaemonIsWithWhatItIsGiven(t *testing.T) {
	spec := newScenario(t)

	var said string

	said = fmt.Sprintf("hello-%d", time.Now().UnixNano())

	workspace := spec.aModule(saying(said), "hello/main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestNothing(t *testing.T) {}\n")
	run := workspace.runProgram("//code/hello:go.run/hello", "fail", "--name", "two words")
	spec.Expect(run.ended.GetState()).To(Equal(buildv1.Run_STATE_SUCCEEDED), run.ended.String())
	spec.Expect(run.ended.GetExitCode()).To(BeEquivalentTo(3))
	spec.Expect(run.ended.GetLabel()).To(Equal("//code/hello:go.run/hello"))
	spec.Expect(run.printed).To(ContainElement(said + " given fail,--name,two words"))
	spec.Expect(run.printed).To(ContainElement("to standard error"))
	// It ran in the daemon's sandbox, in its project's directory of
	// what was brought, and on no worker.
	spec.Expect(run.printed).To(ContainElement(And(
		HaveSuffix(" on "+strings.TrimSpace(workspace.prints("hostname"))),
		ContainSubstring("/code/hello on "),
	)))
	// What it is made of was built by a run, on the grid; the program
	// itself is the daemon's, and no action of the grid.
	actions, err := buildsAt(workspace.Build).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: run.ended.GetBuildId()})
	spec.Expect(err).NotTo(HaveOccurred())
	provenance := map[string]buildv1.Provenance{}
	for _, action := range actions.GetActions() {
		provenance[action.GetLabel()] = action.GetProvenance()
	}
	spec.Expect(provenance).To(HaveKeyWithValue("//code/hello:go.run/hello", buildv1.Provenance_PROVENANCE_DAEMON))
	spec.Expect(provenance).To(HaveKeyWithValue("//code/hello:go.compile", Or(Equal(buildv1.Provenance_PROVENANCE_EXECUTED), Equal(buildv1.Provenance_PROVENANCE_GRID_CACHE))))
	for label := range provenance {
		spec.Expect(label).NotTo(ContainSubstring(":go.test"), "a run builds what its program reads, and nothing else")
	}
	// The module has a test, which a build of it runs: its absence
	// above is the run's doing.
	built := workspace.build()
	listed, err := buildsAt(workspace.Build).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: built.run.GetId()})
	spec.Expect(err).NotTo(HaveOccurred())
	var labels []string
	for _, action := range listed.GetActions() {
		labels = append(labels, action.GetLabel())
	}
	spec.Expect(labels).To(ContainElement("//code/hello:go.test/."))
	// Given nothing to fail by, the same program ends well.
	spec.Expect(workspace.runProgram("//code/hello:go.run/hello").ended.GetExitCode()).To(BeEquivalentTo(0))
	// Nothing brought for a program outlives it.
	spec.Expect(strings.TrimSpace(workspace.prints("pgrep -x hello || echo none"))).To(Equal("none"))
}

// Runs / A program runs until it is cancelled, and is listed while it does.
func TestAProgramRunsUntilItIsCancelledAndIsListedWhileItDoes(t *testing.T) {
	spec := newScenario(t)

	var said string

	said = fmt.Sprintf("hello-%d", time.Now().UnixNano())

	workspace := spec.aModule(`package main

import (
	"fmt"
	"os/exec"
	"time"
)

func main() {
	// Something it starts, which is to end with it.
	if err := exec.Command("sleep", "3611").Start(); err != nil {
		panic(err)
	}
	fmt.Println("` + said + ` up")
	time.Sleep(time.Hour)
}
`)
	runs := runsAt(workspace.Build)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	created, err := runs.CreateRun(ctx, &buildv1.CreateRunRequest{Target: "//code/hello:go.run/hello"})
	spec.Expect(err).NotTo(HaveOccurred())
	id := created.GetRun().GetId()
	// What it prints is sent as it prints it, while it runs.
	stream, err := runs.StreamRunLogs(ctx, &buildv1.StreamRunLogsRequest{RunId: id})
	spec.Expect(err).NotTo(HaveOccurred())
	defer stream.Close()
	part, err := stream.Receive()
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(part.GetLines()[0].GetText()).To(Equal(said + " up"))
	listed, err := runs.ListRuns(ctx, &buildv1.ListRunsRequest{PageSize: 1000})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(listed.GetRuns()[0].GetId()).To(Equal(id))
	spec.Expect(listed.GetRuns()[0].GetState()).To(Equal(buildv1.Run_STATE_RUNNING))
	spec.Expect(strings.TrimSpace(workspace.prints("pgrep -x hello | wc -l"))).To(Equal("1"))
	spec.Expect(strings.TrimSpace(workspace.prints("pgrep -x sleep | wc -l"))).To(Equal("1"))

	_, err = runs.CancelRun(ctx, &buildv1.CancelRunRequest{RunId: id})
	spec.Expect(err).NotTo(HaveOccurred())
	// A cancel returns at once; the run is followed to its end.
	cancelled, err := runs.WaitRun(ctx, &buildv1.WaitRunRequest{RunId: id})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(cancelled.GetRun().GetState()).To(Equal(buildv1.Run_STATE_CANCELLED))
	spec.Expect(cancelled.GetRun().GetFailure()).To(BeEmpty())
	spec.Expect(cancelled.GetRun().GetEndTime()).NotTo(BeNil())
	spec.Expect(strings.TrimSpace(workspace.prints("pgrep -x hello || echo none"))).To(Equal("none"))
	spec.Expect(strings.TrimSpace(workspace.prints("pgrep -x sleep || echo none"))).To(Equal("none"), "what the program started outlived it")
	// Its stream ends with it, and cancelling it again changes nothing.
	for {
		if _, err := stream.Receive(); err != nil {
			spec.Expect(errors.Is(err, io.EOF)).To(BeTrue(), "%v", err)
			break
		}
	}
	again, err := runs.CancelRun(ctx, &buildv1.CancelRunRequest{RunId: id})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(again.GetRun().GetEndTime().AsTime()).To(Equal(cancelled.GetRun().GetEndTime().AsTime()))
}

// Runs / No build runs a program.
func TestNoBuildRunsAProgram(t *testing.T) {
	spec := newScenario(t)

	var said string

	said = fmt.Sprintf("hello-%d", time.Now().UnixNano())

	workspace := spec.aModule(saying(said))
	// A build of everything builds the module, and not its program.
	built := workspace.build()
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.run.String())
	actions, err := buildsAt(workspace.Build).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: built.run.GetId()})
	spec.Expect(err).NotTo(HaveOccurred())
	var labels []string
	for _, action := range actions.GetActions() {
		labels = append(labels, action.GetLabel())
	}
	spec.Expect(labels).To(ContainElement("//code/hello:go.compile"))
	spec.Expect(labels).NotTo(ContainElement(ContainSubstring("run")))
	// Pointed at by itself, it is refused, and says how it is run.
	pointed := workspace.build(func(r *buildv1.CreateBuildRequest) { r.Targets = []string{"//code/hello:go.run/hello"} })
	spec.Expect(pointed.outcome()).To(Equal("BUILD_FAILURE"))
	spec.Expect(pointed.run.GetFailure()).To(ContainSubstring("frontseat run //code/hello:go.run/hello"))
}

// Runs / What is no program is not run, and says what is.
func TestWhatIsNoProgramIsNotRunAndSaysWhatIs(t *testing.T) {
	spec := newScenario(t)

	var said string

	said = fmt.Sprintf("hello-%d", time.Now().UnixNano())

	workspace := spec.aModule(saying(said))
	// A step of a build is built, not run.
	step := workspace.runProgram("//code/hello:go.compile")
	spec.Expect(step.ended.GetState()).To(Equal(buildv1.Run_STATE_FAILED))
	spec.Expect(step.ended.GetFailure()).To(ContainSubstring("is a step of a build, and no program"))
	spec.Expect(step.printed).To(BeEmpty())
	// A name nothing has says the nearest that is one.
	mistyped := workspace.runProgram("//code/hello:go.run/helo")
	spec.Expect(mistyped.ended.GetState()).To(Equal(buildv1.Run_STATE_FAILED))
	spec.Expect(mistyped.ended.GetFailure()).To(And(ContainSubstring("there is no program"), ContainSubstring("//code/hello:go.run/hello")))
	// Nothing was named.
	_, err := runsAt(workspace.Build).CreateRun(context.Background(), &buildv1.CreateRunRequest{})
	spec.Expect(err).To(BeRefusedSaying("name the program to run"))
	// The control: the program itself is run.
	spec.Expect(workspace.runProgram("//code/hello:go.run/hello").ended.GetState()).To(Equal(buildv1.Run_STATE_SUCCEEDED))
}

// Runs / A program that does not build is not started.
func TestAProgramThatDoesNotBuildIsNotStarted(t *testing.T) {
	spec := newScenario(t)

	var said string

	said = fmt.Sprintf("hello-%d", time.Now().UnixNano())

	workspace := spec.aModule("package main\n\nfunc main() { " + said + " }\n")
	run := workspace.runProgram("//code/hello:go.run/hello")
	spec.Expect(run.ended.GetState()).To(Equal(buildv1.Run_STATE_FAILED), run.ended.String())
	// It says why: which action of its build did not pass.
	spec.Expect(run.ended.GetFailure()).To(And(ContainSubstring("BUILD_FAILURE"), ContainSubstring("//code/hello:go.compile")))
	spec.Expect(run.ended.GetStartTime()).To(BeNil())
	spec.Expect(run.printed).To(BeEmpty())
}

// Runs / A file that is not built makes no program, and a module's programs are each its own.
func TestAFileThatIsNotBuiltMakesNoProgramAndAModuleSProgramsAreEachItsOwn(t *testing.T) {
	spec := newScenario(t)

	var said string

	said = fmt.Sprintf("hello-%d", time.Now().UnixNano())

	workspace := spec.aModule(saying(said),
		// A script kept in a directory of its own, as the go command
		// is told to leave it out: no program, and no "gen".
		"hello/tools/gen/gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n",
		"hello/cmd/other/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\""+said+" other\") }\n",
		"hello/lib/lib.go", "package lib\n",
	)
	spec.Expect(workspace.runProgram("//code/hello:go.run/other").printed).To(ConsistOf(said + " other"))
	spec.Expect(workspace.runProgram("//code/hello:go.run/hello", "x").printed).To(ContainElement(said + " given x"))
	for _, none := range []string{"lib", "gen"} {
		refused := workspace.runProgram("//code/hello:go.run/" + none)
		// What can be run is said, and is the two programs alone.
		spec.Expect(refused.ended.GetFailure()).To(And(
			ContainSubstring("there is no program"),
			HaveSuffix("what can be run: //code/hello:go.run/hello, //code/hello:go.run/other"),
		))
	}
}

// Run is a run that has ended, and what its program printed.
type Run struct {
	ended   *buildv1.Run
	printed []string
}

// runProgram runs a program with arguments and follows it to its end.
func (w *Workspace) runProgram(target string, arguments ...string) *Run {
	w.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runs := runsAt(w.Build)
	created, err := runs.CreateRun(ctx, &buildv1.CreateRunRequest{Target: target, Arguments: arguments})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	id := created.GetRun().GetId()
	w.scenario.Expect(id).To(HavePrefix("run_"))
	stream, err := runs.StreamRunLogs(ctx, &buildv1.StreamRunLogsRequest{RunId: id})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	defer stream.Close()
	out := &Run{}
	for {
		part, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}
		w.scenario.Expect(err).NotTo(HaveOccurred())
		for _, line := range part.GetLines() {
			out.printed = append(out.printed, line.GetText())
		}
	}
	waited, err := runs.WaitRun(ctx, &buildv1.WaitRunRequest{RunId: id})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	out.ended = waited.GetRun()
	return out
}

func (spec *scenario) aModule(main string, others ...string) *Workspace {
	checkout := spec.aRepository()
	files := map[string]string{
		"hello/go.mod":  "module example.com/hello\n\ngo 1.27\n",
		"hello/main.go": main,
	}
	for i := 0; i+1 < len(others); i += 2 {
		files[others[i]] = others[i+1]
	}
	testfiles.Tree(spec.TB, checkout, files)
	testpins.Go(spec.TB, checkout)
	return spec.inASandboxOfItsOwn(spec.cloning(checkout))
}

func saying(said string) string {
	return `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	directory, _ := os.Getwd()
	host, _ := os.Hostname()
	fmt.Println("` + said + ` given " + strings.Join(os.Args[1:], ","))
	fmt.Println("in " + directory + " on " + host)
	fmt.Fprintln(os.Stderr, "to standard error")
	if len(os.Args) > 1 && os.Args[1] == "fail" {
		os.Exit(3)
	}
}
`
}
