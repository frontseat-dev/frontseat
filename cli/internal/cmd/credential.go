package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

var (
	credentialUsername string
	credentialForget   bool
)

// longest is how long a secret may be.
const longest = 4096

// secretFrom is where a secret is read from, where a test says: nil is
// standard input.
var secretFrom io.Reader

// credentialCmd represents the credential command
var credentialCmd = &cobra.Command{
	Use:   "credential HOST",
	Short: "Give the control plane your credential for a host, which no sandbox holds",
	Long: `Give the control plane your credential for a host, read from
standard input: a password or a token, on one line. At a terminal it is
asked for, and what is typed is not shown. It is yours: the
control plane keeps it outside every sandbox, and a workspace of yours
that fetches a repository of that host is let through with it and never
holds it; no one else's workspace is. It is never shown or read back,
and it is no argument, so that it is in no history and no list of what
runs. With --forget the control plane lets go of your credential for the
host, once no workspace of yours is let through with it.

It is kept for the host over https, unless the host is written with
http:// before it. A workspace of yours may fetch any repository of the
host with it, and push to any it may write. What runs in such a
workspace can have it sent to the host unencrypted, so give one that
reads no more than it must.`,
	Example: `  frontseat workspace credential forge.example --username someone < token.txt
  frontseat workspace credential forge.example --forget
  frontseat workspace credential http://forge.test:8080 < password.txt`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fs, err := server()
		if err != nil {
			return err
		}
		// A secret is sent over https, or to this machine: the command
		// line asks no control plane otherwise (machine.Server).
		credentials := fs.Credentials()
		if credentialForget {
			return credentials.Delete(cmd.Context(), args[0])
		}
		secret, err := secretRead(cmd.ErrOrStderr(), args[0])
		if err != nil {
			return err
		}
		return credentials.Set(cmd.Context(), frontseat.Credential{Host: args[0], Username: credentialUsername, Secret: secret})
	},
}

// secretRead reads the secret of a host's credential: a line, and no
// long one. A person at a terminal is asked for it on asking, and what
// they type is not shown; anything else is read from standard input to
// its end.
func secretRead(asking io.Writer, host string) (string, error) {
	var read []byte
	var err error
	if stdin := int(os.Stdin.Fd()); secretFrom == nil && term.IsTerminal(stdin) {
		fmt.Fprintf(asking, "Secret for %s (it is not shown): ", plain.Shown(host))
		read, err = term.ReadPassword(stdin)
		fmt.Fprintln(asking)
	} else {
		from := secretFrom
		if from == nil {
			from = os.Stdin
		}
		read, err = io.ReadAll(io.LimitReader(from, longest+2))
	}
	if err != nil {
		return "", err
	}
	secret := strings.TrimRight(string(read), "\r\n")
	// One longer is refused, never cut and handed over as another.
	if len(secret) > longest {
		return "", errors.New("the secret is longer than a credential is")
	}
	if secret == "" || strings.ContainsAny(secret, "\r\n") {
		return "", errors.New("give the secret on standard input, on one line")
	}
	return secret, nil
}

func init() {
	workspaceCmd.AddCommand(credentialCmd)
	credentialCmd.Flags().StringVar(&credentialUsername, "username", "", "who the credential is of, as the host asks")
	credentialCmd.Flags().BoolVar(&credentialForget, "forget", false, "let go of the host's credential")
}
