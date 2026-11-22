package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

var sshStdio, sshConfig bool

// sshCmd opens the workspace's sandbox over SSH. The connection goes
// through the server that made the sandbox, by this same command with
// --stdio as the SSH client's proxy; an editor is given the same.
var sshCmd = &cobra.Command{
	Use:   "ssh [COMMAND...]",
	Short: "A shell in the sandbox, or a command",
	Long: `Open a shell in the workspace's sandbox, or run a command there.

With --config, print what ~/.ssh/config needs for an editor to open the
workspace (VS Code Remote-SSH, JetBrains Gateway): the host
frontseat-workspace, which follows the workspace last opened, or, with
--workspace NAME, the host frontseat-NAME, which is that workspace's.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		// What prints the host for an editor runs nothing, and what
		// carries a connection is given no command: either with a command
		// is refused, and not run without it.
		switch {
		case sshConfig && len(args) > 0:
			return errors.New("--config prints what an SSH client is told and runs nothing: it is given with no command")
		case sshStdio && len(args) > 0:
			return errors.New("--stdio carries an SSH client's connection and runs nothing: it is given with no command")
		}
		if sshStdio {
			w, err := asked()
			if err != nil {
				return err
			}
			conn, err := w.Shell(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()
			go func() { _, _ = io.Copy(conn, os.Stdin) }()
			_, _ = io.Copy(os.Stdout, conn)
			return nil
		}
		if sshConfig {
			options, err := sshClientOptions(workspaceFlag)
			if err != nil {
				return err
			}
			said("Host %s", hostOf(workspaceFlag))
			for _, option := range options {
				said("  %s %s", option[0], option[1])
			}
			return nil
		}
		return shell(ctx, workspaceFlag, false, args)
	},
}

// hostOf is the host the SSH client is told a workspace's sandbox is:
// frontseat-NAME for a workspace named, and frontseat-workspace for the
// one last opened.
func hostOf(workspace string) string {
	if workspace == "" {
		return "frontseat-workspace"
	}
	return "frontseat-" + workspace
}

// sshClientOptions is what the SSH client is told on this machine, for
// a workspace named, or the one last opened where none is.
func sshClientOptions(workspace string) ([][2]string, error) {
	_, identity, err := machine.Identity()
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return sshOptions(self, identity, workspace)
}

// shell runs the SSH client into the sandbox of a workspace named, or
// of the one last opened, with what follows its host: nothing for a
// shell, a command, or the client's own flags and one. How the client
// ended is how this command ends. A terminal is asked for where one is
// to be given whatever runs.
func shell(ctx context.Context, workspace string, terminal bool, arguments []string) error {
	options, err := sshClientOptions(workspace)
	if err != nil {
		return err
	}
	var told []string
	for _, option := range options {
		told = append(told, "-o", option[0]+"="+option[1])
	}
	if terminal {
		told = append(told, "-t")
	}
	told = append(told, hostOf(workspace))
	client, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("no SSH client is found on this machine, and a workspace's shell is reached with one (ssh): %w", err)
	}
	ssh := exec.CommandContext(ctx, client, append(told, arguments...)...)
	ssh.Stdin, ssh.Stdout, ssh.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = ssh.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		exitCode = exit.ExitCode()
		return nil
	}
	return err
}

// sshOptions is what the SSH client is told to open a workspace's
// sandbox, on its command line or in ~/.ssh/config, given this command's
// own path and the key's, for a workspace named, or for the one last
// opened where none is. Each is written between quotes, so that a path
// that holds a space is one path. A path is read three times over on its
// way, by the SSH client's reader of options, by its expansion of % and
// ${}, and, for the command it goes by, by a person's shell, and no one
// way of writing it is read alike by all three: so a path that holds
// anything any of them reads otherwise is refused, and is not escaped.
func sshOptions(self, identity, workspace string) ([][2]string, error) {
	for what, path := range map[string]string{"frontseat is installed at": self, "this machine's key for its sandboxes is kept at": identity} {
		if !filepath.IsAbs(path) || !writtenPlainly(path) {
			return nil, fmt.Errorf("%s %q, which an SSH client or a shell would read as something else: a path it is told is whole, and of letters, digits, spaces and . _ - + , : = @ /", what, path)
		}
	}
	// A workspace's name is put to the SSH client as it is, and so is
	// one a workspace may have and nothing else.
	proxy := `"` + self + `" workspace ssh --stdio`
	if workspace != "" {
		if !frontseat.IsWorkspaceName(workspace) {
			return nil, fmt.Errorf("%q is no workspace's name: one is lower-case letters, digits and dashes, at most 48", workspace)
		}
		proxy += " --workspace=" + workspace
	}
	// The sandbox's host key is made when the sandbox is, so it is not
	// kept: the server the connection goes through vouches for it.
	return [][2]string{
		{"User", "frontseat"},
		{"ProxyCommand", proxy},
		{"IdentityFile", `"` + identity + `"`},
		{"IdentitiesOnly", "yes"},
		{"StrictHostKeyChecking", "no"},
		{"UserKnownHostsFile", "/dev/null"},
		{"LogLevel", "ERROR"},
	}, nil
}

// writtenPlainly is whether a path holds only what an SSH client and a
// shell read as it is written, between double quotes.
func writtenPlainly(path string) bool {
	for _, r := range path {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" ._-+,:=@/", r) {
			return false
		}
	}
	return path != ""
}

func init() {
	workspaceCmd.AddCommand(sshCmd)
	asksAWorkspace(sshCmd)
	sshCmd.Flags().BoolVar(&sshStdio, "stdio", false, "carry an SSH client's connection to the sandbox on standard input and output")
	sshCmd.Flags().BoolVar(&sshConfig, "config", false, "print the host an editor opens the workspace as, for ~/.ssh/config")
	_ = sshCmd.Flags().MarkHidden("stdio")
	sshCmd.MarkFlagsMutuallyExclusive("stdio", "config")
	// What follows the command to run is the command's own: its flags are
	// not this command's.
	sshCmd.Flags().SetInterspersed(false)
}
