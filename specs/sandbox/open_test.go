package sandbox_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/server/testsupport/testidentity"
)

// A person standing in a checkout opens the workspace of its repository
// and branch with one command, and has the same one again each time:
// the workspace is found by what it is of, which its sandbox carries,
// and not by anything this machine remembers. Its daemon clones the
// branch from the remote. Nothing of the checkout is given to it.
//
// What a checkout says of itself (its remote, its branch, who commits)
// is read by the command line's version control package and proven
// there; here a checkout is one that says what the spec has it say, so
// that the repository is the spec's own forge, and the workspace is
// asked for as the command line asks for it once it has read one.
// Opening the workspace of a checkout / Opening a checkout gives the workspace of its repository and branch, and opening it again gives the same one.
func TestOpeningACheckoutGivesTheWorkspaceOfItsRepositoryAndBranchAndOpeningItAgainGivesTheSameOne(t *testing.T) {
	spec := newScenario(t)

	checkout := aCheckoutOf(spec.aRepositoryWithABranch("main"), "main")

	first := spec.opening(checkout)
	spec.Expect(first.How).To(Equal(frontseat.Made))
	reopened := &Workspace{spec, first.reached}
	spec.Expect(reopened.prints("cat " + code + "/branch")).To(Equal("main"))

	// What was done in it is still there the second time: it is the
	// same workspace, as it was left, and not one made anew.
	reopened.copyIn("/work/left-here", "by the first")
	again := spec.opening(checkout)
	spec.Expect(again.How).To(Equal(frontseat.Left))
	spec.Expect(again.Sandbox).To(Equal(first.Sandbox))
	spec.Expect((&Workspace{spec, again.reached}).prints("cat /work/left-here")).To(Equal("by the first"))
	// It says which directory the repository is in, beneath where a
	// shell there begins, so that a person is taken to it.
	spec.Expect(first.Repository).NotTo(BeEmpty())
	spec.Expect(again.Repository).To(Equal(first.Repository))
	spec.Expect((&Workspace{spec, again.reached}).prints("cd " + again.Repository + " && git rev-parse --is-inside-work-tree")).To(Equal("true"))
}

// Opening the workspace of a checkout / Another branch of the same repository is another workspace.
func TestAnotherBranchOfTheSameRepositoryIsAnotherWorkspace(t *testing.T) {
	spec := newScenario(t)

	repository := spec.aRepositoryWithABranch("main", "other")

	main := spec.opening(aCheckoutOf(repository, "main"))
	other := spec.opening(aCheckoutOf(repository, "other"))

	spec.Expect(other.How).To(Equal(frontseat.Made))
	spec.Expect(other.Sandbox).NotTo(Equal(main.Sandbox))
	spec.Expect((&Workspace{spec, main.reached}).prints("cat " + code + "/branch")).To(Equal("main"))
	spec.Expect((&Workspace{spec, other.reached}).prints("cat " + code + "/branch")).To(Equal("other"))
}

// Opening the workspace of a checkout / A workspace that was paused is opened running.
func TestAWorkspaceThatWasPausedIsOpenedRunning(t *testing.T) {
	spec := newScenario(t)

	checkout := aCheckoutOf(spec.aRepositoryWithABranch("main"), "main")
	first := spec.opening(checkout)
	(&Workspace{spec, first.reached}).pause()

	again := spec.opening(checkout)

	spec.Expect(again.How).To(Equal(frontseat.Left))
	spec.Expect(again.Sandbox).To(Equal(first.Sandbox))
	spec.Expect((&Workspace{spec, again.reached}).state()).To(Equal("running"))
	spec.Expect((&Workspace{spec, again.reached}).prints("cat " + code + "/branch")).To(Equal("main"))
}

// Opening the workspace of a checkout / Two who open the same checkout at once are given one workspace.
func TestTwoWhoOpenTheSameCheckoutAtOnceAreGivenOneWorkspace(t *testing.T) {
	spec := newScenario(t)

	checkout := aCheckoutOf(spec.aRepositoryWithABranch("main"), "main")

	type answer struct {
		opened opened
		err    error
	}
	answers := make(chan answer, 2)
	for range 2 {
		go func() {

			opened, err := spec.attempting(checkout)
			answers <- answer{opened, err}
		}()
	}
	first, second := <-answers, <-answers

	spec.Expect(first.err).NotTo(HaveOccurred())
	spec.Expect(second.err).NotTo(HaveOccurred())
	spec.Expect(second.opened.Sandbox).To(Equal(first.opened.Sandbox))
	spec.Expect(spec.sandboxesOf(first.opened.Workspace)).To(HaveLen(1))
	spec.Expect((&Workspace{spec, first.opened.reached}).prints("cat " + code + "/branch")).To(Equal("main"))
}

// Opening the workspace of a checkout / A workspace whose making was stopped before it held its repository is filled when it is opened again.
func TestAWorkspaceWhoseMakingWasStoppedBeforeItHeldItsRepositoryIsFilledWhenItIsOpenedAgain(t *testing.T) {
	spec := newScenario(t)

	// A branch the remote does not hold yet: the sandbox is made, and
	// its daemon's clone fails, as it would have been stopped.
	repository := spec.aRepositoryWithABranch("main")
	checkout := aCheckoutOf(repository, "later")
	stopped, err := spec.attempting(checkout)
	spec.Expect(err).To(HaveOccurred())
	spec.Expect(stopped.Sandbox).NotTo(BeEmpty())
	spec.Expect(spec.sandboxesOf(stopped.Workspace)).To(ConsistOf(stopped.Sandbox))

	// The remote comes to hold it, and the same sandbox is found, and
	// is not taken for a workspace as it was left: it is filled.
	spec.aBranchMore(repository, "later")
	again := spec.opening(checkout)

	// Opening ended nothing when it failed: the sandbox is the one
	// that was there.
	spec.Expect(again.Sandbox).To(Equal(stopped.Sandbox))
	spec.Expect(again.How).To(Equal(frontseat.Filled))
	spec.Expect((&Workspace{spec, again.reached}).prints("cat " + code + "/branch")).To(Equal("later"))
}

// Opening the workspace of a checkout / Two repositories of one name are not one workspace: the second is refused, and takes a name of its own.
func TestTwoRepositoriesOfOneNameAreNotOneWorkspaceTheSecondIsRefusedAndTakesANameOfItsOwn(t *testing.T) {
	spec := newScenario(t)

	one := aCheckoutOf(spec.aRepositoryWithABranch("main"), "main")
	other := aCheckoutOf(spec.aRepositoryWithABranch("main"), "main")
	first := spec.opening(one)

	_, err := spec.attempting(other)
	spec.Expect(err).To(BeRefusedSaying("holds other source"))
	// The first is as it was, and the second is opened under a name.
	spec.Expect((&Workspace{spec, first.reached}).state()).To(Equal("running"))
	second := spec.openingAs(aName(), other)
	spec.Expect(second.How).To(Equal(frontseat.Made))
	spec.Expect(second.Sandbox).NotTo(Equal(first.Sandbox))
}

// Opening the workspace of a checkout / What is committed in the workspace is by who commits in the checkout.
func TestWhatIsCommittedInTheWorkspaceIsByWhoCommitsInTheCheckout(t *testing.T) {
	spec := newScenario(t)

	checkout := aCheckoutOf(spec.aRepositoryWithABranch("main"), "main")
	checkout.author = frontseat.Author{Name: "Someone Here", Email: "someone@example.com"}

	workspace := &Workspace{spec, spec.opening(checkout).reached}

	spec.Expect(workspace.prints("cd " + code + " && git commit --quiet --allow-empty -m made-here && git log -1 --format='%an <%ae>'")).
		To(Equal("Someone Here <someone@example.com>"))

	// The control: a checkout that names no one tells the workspace of
	// no one, and what is committed there is by no one it was told.
	// It is another repository of the same name and branch, so its
	// workspace is given a name of its own.
	unnamed := &Workspace{spec, spec.openingAs(aName(), aCheckoutOf(spec.aRepositoryWithABranch("main"), "main")).reached}
	spec.Expect(unnamed.prints("env | grep -c -E '^GIT_(AUTHOR|COMMITTER)_' || true")).To(Equal("0"))
}

// said is a checkout that says what a spec has it say. Two of its
// remotes are of one repository where they are equal, as the specs'
// forge on this machine spells each one way.
type said struct {
	remote, branch string
	author         frontseat.Author
}

// opened is the workspace of a checkout that was opened, or as much of
// one as there is, as the command line opens one: how it came to be, and
// the directory the checkout's repository is in.
type opened struct {
	reached
	How        frontseat.How
	Repository string
}

// aRepositoryWithABranch writes a repository whose branches each hold a
// file, branch, naming them, and returns where the forge serves it.
func (spec *scenario) aRepositoryWithABranch(branches ...string) string {
	spec.Helper()
	directory := spec.aRepository()
	spec.Expect(os.WriteFile(filepath.Join(directory, "branch"), []byte(branches[0]), 0o644)).To(Succeed())
	testforge.Committed(spec.TB, directory)
	git := func(args ...string) { spec.commitsIn(directory, args...) }
	// The first branch is the one the repository was committed on,
	// whatever it is called; it is called what the spec calls it.
	git("branch", "--move", "--force", branches[0])
	for _, branch := range branches[1:] {
		git("checkout", "--quiet", "-b", branch)
		spec.Expect(os.WriteFile(filepath.Join(directory, "branch"), []byte(branch), 0o644)).To(Succeed())
		git("commit", "--quiet", "--all", "-m", branch)
		git("checkout", "--quiet", branches[0])
	}
	at, err := filepath.Rel(sources, directory)
	spec.Expect(err).NotTo(HaveOccurred())
	return forge + "/" + filepath.ToSlash(at)
}

// aBranchMore adds a branch, holding a file that names it, to a
// repository the forge serves.
func (spec *scenario) aBranchMore(remote, branch string) {
	spec.Helper()
	directory := filepath.Join(sources, filepath.FromSlash(strings.TrimPrefix(remote, forge+"/")))
	git := func(args ...string) { spec.commitsIn(directory, args...) }
	git("checkout", "--quiet", "-b", branch)
	spec.Expect(os.WriteFile(filepath.Join(directory, "branch"), []byte(branch), 0o644)).To(Succeed())
	git("commit", "--quiet", "--all", "-m", branch)
}

// aCheckoutOf is a checkout of a repository at a branch.
func aCheckoutOf(remote, branch string) said { return said{remote: remote, branch: branch} }

// opening opens the workspace of a checkout, and ends its sandbox with
// the spec.
func (spec *scenario) opening(checkout said) opened {
	spec.Helper()
	opened, err := spec.attempting(checkout)
	spec.Expect(err).NotTo(HaveOccurred())
	return opened
}

// attempting is opening, for a spec that expects it may fail. No image
// is named: the sandbox runs the control plane's own.
func (spec *scenario) attempting(checkout said) (opened, error) {
	spec.Helper()
	return spec.attemptingAs("", checkout)
}

// openingAs is opening, of a workspace that is given a name.
func (spec *scenario) openingAs(name string, checkout said) opened {
	spec.Helper()
	opened, err := spec.attemptingAs(name, checkout)
	spec.Expect(err).NotTo(HaveOccurred())
	return opened
}

func (spec *scenario) attemptingAs(name string, checkout said) (opened, error) {
	spec.Helper()
	at, got, err := made(context.Background(), making{Server: server, Spec: frontseat.Spec{
		Name: name, Repositories: []frontseat.Repository{{Remote: checkout.remote, Ref: checkout.branch}}, Author: checkout.author, AuthorizedKey: testidentity.Authorized(),
	}})
	if got.Workspace != nil {
		named := got.Workspace.Name()
		spec.Cleanup(func() {
			ended(named)
		})
	}
	out := opened{reached: at, How: got.How}
	if err != nil {
		return out, err
	}
	// What it holds is read, and not taken on its name's word, as the
	// command line reads it: one that holds other source is not this one.
	for _, held := range got.Held.GetStatus().GetRepositories() {
		if held.GetRemote() == checkout.remote {
			out.Repository = held.GetName()
			return out, nil
		}
	}
	return out, errors.New("the workspace of this name holds other source than the repository asked for")
}

// sandboxesOf are the sandboxes that carry a workspace's name.
func (spec *scenario) sandboxesOf(workspace string) []string {
	spec.Helper()
	return spec.sandboxesLabelled("frontseat.dev/workspace=" + workspace)
}
