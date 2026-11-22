package cmd

import (
	"context"
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

// credentialUsername is who a credential set is of.
var credentialUsername string

// longest is how long a secret may be.
const longest = 4096

// secretFrom is where a secret is read from, where a test says: nil is
// standard input.
var secretFrom io.Reader

// credentialCmd is the group of commands of your credentials for hosts.
var credentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "Give the control plane your credentials for hosts, which no sandbox holds",
	Long: `Your credentials for the hosts your workspaces fetch from and push to,
which the control plane keeps outside every sandbox: credential set
gives one, credential list lists them by host, and credential delete
lets go of one.`,
	Args: cobra.ArbitraryArgs,
	RunE: named("a credential command"),
}

// credentialSetCmd gives the control plane a host's credential.
var credentialSetCmd = &cobra.Command{
	Use:   "set HOST",
	Short: "Give the control plane your credential for a host",
	Long: `Give the control plane your credential for a host, read from
standard input: a password or a token, on one line. At a terminal it is
asked for, and what is typed is not shown. It is yours: the
control plane keeps it outside every sandbox, and a workspace of yours
that fetches a repository of that host is let through with it and never
holds it; no one else's workspace is. It is never shown or read back,
and it is no argument, so that it is in no history and no list of what
runs. It replaces the one there was for the host.

It is kept for the host over https, unless the host is written with
http:// before it. A workspace of yours may fetch any repository of the
host with it, and push to any it may write. What runs in such a
workspace can have it sent to the host unencrypted, so give one that
reads no more than it must.`,
	Example: `  frontseat credential set forge.example --username someone < token.txt
  frontseat credential set http://forge.test:8080 < password.txt`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fs, err := server()
		if err != nil {
			return err
		}
		// A secret is sent over https, or to this machine: the command
		// line asks no control plane otherwise (machine.Server).
		secret, err := secretRead(cmd.Context(), cmd.ErrOrStderr(), args[0])
		if err != nil {
			return err
		}
		return fs.Credentials().Set(cmd.Context(), frontseat.Credential{Host: args[0], Username: credentialUsername, Secret: secret})
	},
}

// credentialListCmd lists your credentials by host.
var credentialListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your credentials, by host, without their secrets",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fs, err := server()
		if err != nil {
			return err
		}
		listed, err := fs.Credentials().List(cmd.Context())
		if err != nil {
			return err
		}
		for _, each := range listed {
			if each.Username == "" {
				said("%s", plain.Shown(each.Host))
				continue
			}
			said("%s  %s", plain.Shown(each.Host), plain.Shown(each.Username))
		}
		return nil
	},
}

// credentialDeleteCmd has the control plane let go of a credential.
var credentialDeleteCmd = &cobra.Command{
	Use:   "delete HOST",
	Short: "Have the control plane let go of your credential for a host",
	Long: `Have the control plane let go of your credential for a host, once no
workspace of yours is let through with it.`,
	Example: `  frontseat credential delete forge.example`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fs, err := server()
		if err != nil {
			return err
		}
		return fs.Credentials().Delete(cmd.Context(), args[0])
	},
}

// secretRead reads the secret of a host's credential: a line, and no
// long one. A person at a terminal is asked for it on asking, and what
// they type is not shown; anything else is read from standard input to
// its end. An interrupt while a person is asked ends the asking: the
// terminal is given back as it was, and the read is left to end with
// the command.
func secretRead(ctx context.Context, asking io.Writer, host string) (string, error) {
	var read []byte
	var err error
	if stdin := int(os.Stdin.Fd()); secretFrom == nil && term.IsTerminal(stdin) {
		was, held := term.GetState(stdin)
		if held != nil {
			return "", held
		}
		type typing struct {
			read []byte
			err  error
		}
		typed := make(chan typing, 1)
		fmt.Fprintf(asking, "Secret for %s (it is not shown): ", plain.Shown(host))
		go func() {
			read, err := term.ReadPassword(stdin)
			typed <- typing{read, err}
		}()
		select {
		case got := <-typed:
			read, err = got.read, got.err
			fmt.Fprintln(asking)
		case <-ctx.Done():
			restored := term.Restore(stdin, was)
			fmt.Fprintln(asking)
			return "", errors.Join(ctx.Err(), restored)
		}
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
	rootCmd.AddCommand(credentialCmd)
	credentialCmd.AddCommand(credentialSetCmd, credentialListCmd, credentialDeleteCmd)
	credentialSetCmd.Flags().StringVar(&credentialUsername, "username", "", "who the credential is of, as the host asks")
}
