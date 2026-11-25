package sandbox_test

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/core/ports"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

// A remote workspace's repositories are mounted in its sandbox and not
// cloned. Nothing here mounts one: a plain directory stands in for the
// mount, and another for its layer, both laid out where the gateway
// makes its sandboxes, and what holds stored repositories in memory
// says they are the mount's. What this tries is what is between that
// word and a build: that the gateway binds both into the sandbox, the
// layer to be read alone; that the box's supervisor then holds the
// repository and records it as mounted; that the command line there
// tells the build daemon it starts where the layers are; and that a
// build reads the repository and builds an entity of it, on Linux.
//
// What it does not prove. A plain directory's change times are the
// kernel's, so no file of it is shown at the stamp: every file is read
// and hashed, and none is taken at its attribute. That the stamp is
// read from the layer is proven only as far as the daemon is told
// where the layers are and the layer is there, on a file system
// mounted read-only, holding the stamp for the daemon's user to read;
// that a file is taken at its attribute on Linux, asked of a mount
// afresh, needs a mount that keeps to the layout (ports.Mounted), and
// is proven by nothing here. One release is asked for and refused; that
// one from a workspace of clones is served is the build daemon's own
// test's (TestAReleaseFromAWorkspaceThatHoldsAMountedRepositoryIsRefusedByName).
//
// The control: the same request, asked of a control plane given nothing
// that serves stored repositories, is refused by name and nothing is
// made (TestARemoteWorkspaceIsRefusedByNameByAControlPlaneGivenNoStoreAndNothingIsMade).
//
// Workspaces of a control plane / A directory bound as a remote workspace's repository is held by its supervisor and built in its sandbox, its layer bound to be read alone.
func TestADirectoryBoundAsARemoteWorkspacesRepositoryIsHeldAndBuiltInItsSandboxItsLayerReadAlone(t *testing.T) {
	spec := newScenario(t)
	ctx := context.Background()

	remote := spec.aRemoteWorkspace()
	at := remoteDirectory
	who := remote.prints("id; ls -ldn " + at + " " + remoteLayer + " " + remoteLayer + "/stamp 2>&1")

	// The gateway bound both: the repository where the workspace has
	// it, and its layer where a build looks for it.
	mounts := remote.holds("/proc/self/mountinfo")
	spec.Expect(optionsOf(mounts, at)).To(ContainElement("rw"), "the repository's directory is no mount of its own that is written, in the sandbox: the gateway bound no directory there, or bound it to be read alone. The sandbox's mounts:\n%s", mounts)
	spec.Expect(optionsOf(mounts, remoteLayer)).To(ContainElement("ro"), "the layer's directory is no mount of its own that is read alone, in the sandbox: the gateway bound no directory there, or bound it written, and a build takes no stamp from it. The sandbox's mounts:\n%s", mounts)

	// The repository is the directory laid out, and the box's user
	// writes it.
	spec.Expect(remote.holds(at+"/app/go.mod")).To(Equal(catalogModule), "the repository bound is not the directory laid out for it")
	printed, exit := remote.run("{ touch " + at + "/app/written && rm " + at + "/app/written; } 2>&1")
	spec.Expect(exit).To(Equal(0), "the box's user does not write the repository bound in its workspace: %s. Who it is, and whose the directories are there: %s", printed, who)

	// The layer says the stamp to the box's user, who writes nothing of
	// it. What it is laid out as lets every user write it, so what
	// refuses is the mount, and says so.
	spec.Expect(remote.holds(remoteLayer+"/stamp")).To(Equal(remote.Stamp), "the layer bound does not say the stamp laid out for it")
	for what, write := range map[string]string{
		"its stamp written to": "echo >> " + remoteLayer + "/stamp",
		"a file made in it":    "touch " + remoteLayer + "/made",
	} {
		printed, exit := remote.run("{ " + write + "; } 2>&1")
		spec.Expect(exit).NotTo(Equal(0), "%s: the layer is written from inside the sandbox, and a stamp there is anyone's to say. Who wrote, and whose it is there: %s", what, who)
		spec.Expect(printed).To(ContainSubstring("Read-only file system"), "%s: it is refused, and not by the mount being read alone, which is what a build goes by. Who wrote, and whose it is there: %s", what, who)
	}

	// The supervisor holds the repository as mounted, and recorded it
	// so for a build: at the changeset it was told, from no remote.
	described, err := workspacesAt(remote.Server).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: remote.Workspace.Workspace})
	spec.Expect(err).NotTo(HaveOccurred())
	holds := described.GetWorkspace().GetStatus().GetRepositories()
	spec.Expect(holds).To(HaveLen(1), described.String())
	spec.Expect(holds[0].GetStored()).To(Equal(remoteRepository), described.String())
	spec.Expect(holds[0].GetRemote()).To(BeEmpty(), described.String())
	manifest := remote.holds("/work/.frontseat/workspace.yaml")
	spec.Expect(manifest).To(And(
		ContainSubstring("name: "+remoteRepository+"\n"),
		ContainSubstring("commit: "+remote.Changeset+"\n"),
		ContainSubstring("mounted: true\n"),
		Not(ContainSubstring("remote:")),
	), "the workspace's manifest does not record its one repository as mounted, at the changeset told, from no remote")

	// The entity of the mounted repository is at the changeset, and is
	// built: its program says this workspace's name, so what compiled it
	// was run by this build, of what was read of the mount, and kept
	// from none before.
	listed, err := graphAt(remote.Workspace).ListEntities(ctx, &buildv1.ListEntitiesRequest{PageSize: 1000})
	spec.Expect(err).NotTo(HaveOccurred())
	commit := "no Component app is listed"
	for _, each := range listed.GetEntities() {
		if each.GetRef().GetKind() == "component" && each.GetRef().GetName() == "app" {
			commit = each.GetCommit()
		}
	}
	spec.Expect(commit).To(Equal(remote.Changeset), "the commit the Component of a mounted repository is at is the changeset the repository stands at: %s", listed.String())
	compiled := remote.build(func(r *buildv1.CreateBuildRequest) { r.Targets = []string{"component:default/app"} }).resultOf("//" + remoteRepository + "/app:go.compile")
	spec.Expect(compiled.GetOutcome()).To(Equal(buildv1.Outcome_OUTCOME_PASSED))
	spec.Expect(compiled.GetEntity().GetName()).To(Equal("app"))
	spec.Expect(compiled.GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED), "the program of this workspace alone was compiled by an action kept from a build before")

	// The command line builds it there, as it builds a clone.
	printed, exit = remote.run("frontseat build 2>&1")
	spec.Expect(exit).To(Equal(0), "frontseat build, in the sandbox, of a workspace whose repository is mounted: %s", printed)

	// The one build daemon there, which the command line started, was
	// told where the layers are.
	daemons := strings.Fields(remote.prints(buildDaemons))
	spec.Expect(daemons).To(HaveLen(1))
	spec.Expect(remote.prints(`tr '\0' '\n' < `+daemons[0]+"/cmdline")).To(ContainSubstring("--layers "+ports.Mounted), "the build daemon the command line started in the sandbox was not told where the layers of its mounted repositories are, and reads none: no file would be taken at a mount's stamp there")

	// It has no history, and nothing is released from it.
	printed, exit = remote.run("frontseat ship plan app 2>&1")
	spec.Expect(exit).NotTo(Equal(0), printed)
	spec.Expect(printed).To(ContainSubstring("RELEASE_NOT_SERVED"))
}

// optionsOf are the options of the mount at a directory, as a process
// reads them of itself (/proc/self/mountinfo, whose fifth field is where
// a mount is and whose sixth is its options): of the last such mount,
// which is the one in front; none where nothing is mounted there.
func optionsOf(mountinfo, directory string) []string {
	var options []string
	for _, line := range strings.Split(mountinfo, "\n") {
		if fields := strings.Fields(line); len(fields) > 5 && fields[4] == directory {
			options = strings.Split(fields[5], ",")
		}
	}
	return options
}
