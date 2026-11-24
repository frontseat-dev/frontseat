package sandbox_test

import (
	"bytes"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"golang.org/x/crypto/ssh"

	"github.com/frontseat-dev/frontseat/core/ports"
)

// When you work in the same sandbox as the supervisor, you open it from
// your editor: VS Code's remote development, or JetBrains Gateway. Both reach
// a machine over SSH and install what they need themselves, so a sandbox
// only has to let you in.
//
// It lets in the key of the machine the workspace was made from, and no
// other. There is no password, and nothing to set up in the sandbox. The
// connection goes through the server that made the sandbox, like
// everything else.
//
// What an editor does first is what the specs do: log in, look around,
// copy something in, and reach a port inside.
// Opening a workspace in an editor / A command run over SSH starts in the workspace's directory.
func TestACommandRunOverSSHStartsInTheWorkspacesDirectory(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.prints("pwd")).To(Equal("/work"))
}

// Opening a workspace in an editor / A shell at a terminal opens in the workspace.
func TestAShellAtATerminalOpensInTheWorkspace(t *testing.T) {
	spec := newScenario(t)

	shell := spec.theWorkspace().shell()
	defer shell.Close()
	session, err := shell.NewSession()
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(session.RequestPty("xterm", 24, 80, ssh.TerminalModes{})).To(Succeed())
	typed, err := session.StdinPipe()
	spec.Expect(err).NotTo(HaveOccurred())
	var printed bytes.Buffer
	session.Stdout = &printed
	// No command is given: the shell is one a person types into.
	spec.Expect(session.Shell()).To(Succeed())
	_, err = typed.Write([]byte("tty; echo in-$(pwd); exit\n"))
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(session.Wait()).To(Succeed(), printed.String())
	spec.Expect(printed.String()).To(And(ContainSubstring("/dev/pts/"), ContainSubstring("in-/work")))
}

// Opening a workspace in an editor / The shell is on the sandbox, not on your machine.
func TestTheShellIsOnTheSandboxNotOnYourMachine(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.prints("uname -s")).To(Equal("Linux"))
}

// Opening a workspace in an editor / A command's result comes back to you.
func TestACommandSResultComesBackToYou(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.exits("exit 4")).To(Equal(4))
}

// Opening a workspace in an editor / Nothing is copied into the sandbox: a file there is written by work done in it.
func TestNothingIsCopiedIntoTheSandboxAFileThereIsWrittenByWorkDoneInIt(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	// The shell serves no transfer of files, SFTP among them.
	spec.Expect(workspace.servesFileTransfer()).To(BeFalse())
	// What a file holds is what a command in the sandbox wrote.
	workspace.copyIn("/tmp/note", "written in the sandbox")
	spec.Expect(workspace.prints("cat /tmp/note")).To(Equal("written in the sandbox"))
}

// Opening a workspace in an editor / A port inside the sandbox is reached from your machine.
func TestAPortInsideTheSandboxIsReachedFromYourMachine(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	// The port of the box's own that answers HTTP, forwarded over the
	// shell from inside the sandbox: its supervisor's.
	spec.Expect(workspace.answersOnPort(ports.Source)).To(BeTrue())
}

// Opening a workspace in an editor / Every reverse-forwarded port is released when its editor disconnects.
func TestEveryReverseForwardedPortIsReleasedWhenItsEditorDisconnects(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()
	for _, count := range []int{1, 2} {
		editor := workspace.shell()
		spec.Cleanup(func() { _ = editor.Close() })
		var addresses []string
		for range count {
			forward, err := editor.Listen("tcp", "127.0.0.1:0")
			spec.Expect(err).NotTo(HaveOccurred())
			addresses = append(addresses, forward.Addr().String())
			spec.Cleanup(func() { _ = forward.Close() })
		}
		next := workspace.shell()
		spec.Cleanup(func() { spec.Expect(next.Close()).To(Succeed()) })
		for _, address := range addresses {
			occupied, err := next.Listen("tcp", address)
			if occupied != nil {
				_ = occupied.Close()
			}
			spec.Expect(err).To(HaveOccurred(), "the first editor still owns the port")
		}
		spec.Expect(editor.Close()).To(Succeed())
		for _, address := range addresses {
			spec.expectReclaimed(next, address)
		}
	}
}

// Opening a workspace in an editor / An editor can reuse a forwarded port and release it on disconnect.
func TestAnEditorCanReuseAForwardedPortAndReleaseItOnDisconnect(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()
	editor := workspace.shell()
	spec.Cleanup(func() { _ = editor.Close() })
	forward, err := editor.Listen("tcp", "127.0.0.1:0")
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Cleanup(func() { _ = forward.Close() })
	address := forward.Addr().String()
	for range 100 {
		spec.Expect(forward.Close()).To(Succeed())
		replacement, err := editor.Listen("tcp", address)
		spec.Expect(err).NotTo(HaveOccurred(), "the cancelled port can be reused")
		forward = replacement
	}
	next := workspace.shell()
	spec.Cleanup(func() { spec.Expect(next.Close()).To(Succeed()) })
	spec.Consistently(func() bool {
		occupied, err := next.Listen("tcp", address)
		if occupied != nil {
			_ = occupied.Close()
		}
		return err != nil
	}, 200*time.Millisecond, 20*time.Millisecond).Should(BeTrue(), "the editor still owns the reused port")
	spec.Expect(editor.Close()).To(Succeed())
	spec.expectReclaimed(next, address)
}

// Opening a workspace in an editor / A key the sandbox was not given does not get in.
func TestAKeyTheSandboxWasNotGivenDoesNotGetIn(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.letsInAKeyItWasNotGiven()).To(BeFalse())
}
