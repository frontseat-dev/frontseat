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
)

var sshStdio, sshConfig bool

// sshCmd represents the ssh command. It opens the workspace's sandbox
// over SSH. The connection goes through the server that made the
// sandbox, by this same command with --stdio as the SSH client's proxy;
// an editor is given the same.
var sshCmd = &cobra.Command{
	Use:   "ssh [COMMAND...]",
	Short: "A shell in the sandbox, or a command",
	Long: `Open a shell in the workspace's sandbox, or run a command there.

With --config, print what ~/.ssh/config needs for an editor to open the
workspace (VS Code Remote-SSH, JetBrains Gateway): the host
frontseat-workspace.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
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
			options, err := sshClientOptions()
			if err != nil {
				return err
			}
			said("Host frontseat-workspace")
			for _, option := range options {
				said("  %s %s", option[0], option[1])
			}
			return nil
		}
		return shell(ctx, append([]string{"frontseat-workspace"}, args...))
	},
}

// sshClientOptions is what the SSH client is told on this machine.
func sshClientOptions() ([][2]string, error) {
	_, identity, err := machine.Identity()
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return sshOptions(self, identity)
}

// shell runs the SSH client into the sandbox of the workspace the other
// commands ask, with what follows its host: nothing for a shell, a
// command, or the client's own flags and one. How the client ended is
// how this command ends.
func shell(ctx context.Context, arguments []string) error {
	options, err := sshClientOptions()
	if err != nil {
		return err
	}
	var told []string
	for _, option := range options {
		told = append(told, "-o", option[0]+"="+option[1])
	}
	ssh := exec.CommandContext(ctx, "ssh", append(told, arguments...)...)
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
// own path and the key's. Each is written between quotes, so that a path
// that holds a space is one path. A path is read three times over on its
// way, by the SSH client's reader of options, by its expansion of % and
// ${}, and, for the command it goes by, by a person's shell, and no one
// way of writing it is read alike by all three: so a path that holds
// anything any of them reads otherwise is refused, and is not escaped.
func sshOptions(self, identity string) ([][2]string, error) {
	for what, path := range map[string]string{"frontseat is installed at": self, "this machine's key for its sandboxes is kept at": identity} {
		if !filepath.IsAbs(path) || !writtenPlainly(path) {
			return nil, fmt.Errorf("%s %q, which an SSH client or a shell would read as something else: a path it is told is whole, and of letters, digits, spaces and . _ - + , : = @ /", what, path)
		}
	}
	// The sandbox's host key is made when the sandbox is, so it is not
	// kept: the server the connection goes through vouches for it.
	return [][2]string{
		{"User", "frontseat"},
		{"ProxyCommand", `"` + self + `" workspace ssh --stdio`},
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
	sshCmd.Flags().BoolVar(&sshStdio, "stdio", false, "carry an SSH client's connection to the sandbox on standard input and output")
	sshCmd.Flags().BoolVar(&sshConfig, "config", false, "print the host an editor opens the workspace as, for ~/.ssh/config")
	_ = sshCmd.Flags().MarkHidden("stdio")
	// What follows the command to run is the command's own: its flags are
	// not this command's.
	sshCmd.Flags().SetInterspersed(false)
}
