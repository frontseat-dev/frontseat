package cmd

import (
	"errors"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
)

// inBox runs a command in the sandbox of a workspace, over its shell
// (shell); a test stands in for it.
var inBox = shell

// given is the command line as it was written, after the program's name.
var given []string

// inTheBox is how a command that builds, tests, runs, queries or
// releases is run where a workspace is selected: in that workspace's
// sandbox, where the program of the box (machine.Program) is given what
// this command was given, over the workspace's shell, so that what it
// prints there and how it ends are this command's. The workspace is the
// one named, or the one last opened, asked for as such. Inside a
// sandbox, and with no workspace selected, the command is run here, and
// asks the daemon there or the one of its directory (building).
func inTheBox(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if inside() {
			return run(cmd, args)
		}
		switch _, _, err := selected(); {
		case errors.Is(err, errNoWorkspace):
			return run(cmd, args)
		case err != nil:
			return err
		}
		line, err := passed(cmd, given)
		if err != nil {
			return err
		}
		// One line, which the SSH client sends as it is and reads no
		// flag of its own in: the sandbox's shell reads it with sh -c.
		said := machine.Program
		for _, word := range line {
			said += " " + quoted(word)
		}
		return inBox(cmd.Context(), workspaceFlag, false, []string{said})
	}
}

// quoted is a word as a shell reads it whole, and as nothing but itself:
// between single quotes, where a shell reads everything as it is
// written, and each single quote of its own written outside them.
func quoted(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// errUnsaid is a command line the command line cannot say again as it
// read it. It repeats nothing of the line.
var errUnsaid = errors.New("this command line cannot be said again in the workspace's sandbox as it is written: write each flag with two dashes, or run it there (frontseat workspace ssh)")

// passed is the command line a command was given, as it is said again
// in the workspace's sandbox: every word of it but --workspace and the
// name it takes, which is said outside alone. A word is the flag where
// the command read it as one: not after --, not where another flag
// takes it for its value, and not among what the command read as its
// arguments. Whatever the line is, it is said again only where what is
// left of it holds the arguments the command itself read (errUnsaid).
func passed(cmd *cobra.Command, line []string) ([]string, error) {
	flags, read := cmd.Flags(), cmd.Flags().Args()
	// The words that name the command, which are no arguments of it.
	names := strings.Count(cmd.CommandPath(), " ")
	var kept, arguments []string
walk:
	for i := 0; i < len(line); i++ {
		word := line[i]
		switch {
		case word == "--":
			kept, arguments = append(kept, line[i:]...), append(arguments, line[i+1:]...)
			break walk
		case names > 0 && word != "" && !strings.HasPrefix(word, "-"):
			names--
		case word == "" || word == "-" || !strings.HasPrefix(word, "-"):
			// Where the rest of the line is the arguments the command
			// read, they are its arguments, flags among them or not: a
			// program's own, which run reads none of.
			if slices.Equal(slices.Concat(arguments, line[i:]), read) {
				kept, arguments = append(kept, line[i:]...), read
				break walk
			}
			arguments = append(arguments, word)
		default:
			// A flag takes the next word for its value where it is
			// written without one and is no switch.
			name, _, valued := strings.Cut(strings.TrimPrefix(word, "--"), "=")
			flag := flags.Lookup(name)
			takes := strings.HasPrefix(word, "--") && !valued && flag != nil && flag.NoOptDefVal == "" && i+1 < len(line)
			if takes {
				i++
			}
			if flag != nil && flag.Name == "workspace" && strings.HasPrefix(word, "--") {
				continue
			}
			if takes {
				kept = append(kept, word)
				word = line[i]
			}
		}
		kept = append(kept, word)
	}
	if names != 0 || !slices.Equal(arguments, read) {
		return nil, errUnsaid
	}
	return kept, nil
}
