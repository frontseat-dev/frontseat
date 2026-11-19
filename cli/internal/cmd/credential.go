package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

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
standard input: a password or a token, on one line. It is yours: the
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
		// A secret is sent over https, or to this machine.
		if at, err := url.Parse(fs.Server()); err != nil || (at.Scheme != "https" && !local(fs.Server())) {
			return fmt.Errorf("a credential is given to a control plane over https, or on this machine, and %s is neither", fs.Server())
		}
		credentials := fs.Credentials()
		if credentialForget {
			return credentials.Delete(cmd.Context(), args[0])
		}
		// A secret is a line, and no long one.
		from := secretFrom
		if from == nil {
			from = os.Stdin
		}
		read, err := io.ReadAll(io.LimitReader(from, longest+2))
		if err != nil {
			return err
		}
		// One longer is refused, never cut and handed over as another.
		if len(strings.TrimRight(string(read), "\r\n")) > longest {
			return errors.New("the secret on standard input is longer than a credential is")
		}
		secret := strings.TrimRight(string(read), "\r\n")
		if secret == "" || strings.ContainsAny(secret, "\r\n") {
			return errors.New("give the secret on standard input, on one line")
		}
		return credentials.Set(cmd.Context(), frontseat.Credential{Host: args[0], Username: credentialUsername, Secret: secret})
	},
}

func init() {
	workspaceCmd.AddCommand(credentialCmd)
	credentialCmd.Flags().StringVar(&credentialUsername, "username", "", "who the credential is of, as the host asks")
	credentialCmd.Flags().BoolVar(&credentialForget, "forget", false, "let go of the host's credential")
}
