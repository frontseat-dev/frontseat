package cmd

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

var runProfile string

// runCmd builds a program and runs it.
var runCmd = &cobra.Command{
	Use:   "run PROGRAM [-- ARGUMENT...]",
	Short: "Build a program of the workspace and run it",
	Long: `Build a program of the workspace and run it in the workspace's
sandbox. A program is an action a plugin says is one, named as it is
printed:

  //services/greeter:go.run/greeter

Everything after the program is given to the program, with or without
--; a flag of this command is written before the program. A program
named list, describe, logs or cancel is written in full, as it is
printed. The command
prints the build, then what the program prints, and exits with the
program's exit code. Interrupted, or where it can follow the program no
longer, it stops the program.`,
	Example: `  frontseat run //services/greeter:go.run/greeter
  frontseat run //services/greeter:go.run/greeter --name world
  frontseat run --profile production //services/greeter:go.run/greeter`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// One program, and what it is given: whatever follows the
		// program is the program's, read by nobody here (init).
		w, err := asked()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		// The dashes, where they are written, part the program from
		// what it is given, and are no argument of it.
		given := args[1:]
		if len(given) > 0 && given[0] == "--" {
			given = given[1:]
		}
		run, err := w.Run(ctx, frontseat.RunRequest{Target: args[0], Arguments: given, Profile: runProfile})
		if err != nil {
			return err
		}
		// Whoever interrupts the command stops what it ran: the
		// program is the daemon's, and would go on without it.
		followed, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			select {
			case <-followed:
				return
			case <-ctx.Done():
			}
			stop(ctx, run)
		}()
		ended, err := followRun(cmd, w, run)
		if ctx.Err() != nil {
			<-stopped
			return ctx.Err()
		}
		close(followed)
		if err != nil {
			// The program is the daemon's, and would go on unseen where
			// the command can follow it no longer: it is stopped.
			stop(ctx, run)
			return err
		}
		switch ended.GetState() {
		case buildv1.Run_STATE_FAILED:
			complained("not run: %s", plain.Shown(ended.GetFailure()))
			exitCode = 1
		case buildv1.Run_STATE_CANCELLED:
			// Cancelled by someone else: it did not run to its end, and a
			// program that was never started exited with nothing.
			complained("cancelled%s", after(plain.Shown(ended.GetFailure())))
			if exitCode = int(ended.GetExitCode()); exitCode == 0 {
				exitCode = 1
			}
		default:
			exitCode = int(ended.GetExitCode())
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	asksAWorkspace(runCmd)
	// What follows the program is the program's own, flags and all:
	// nothing after it is read as a flag of this command.
	runCmd.Flags().SetInterspersed(false)
	profileFlag(runCmd, &runProfile)
}

// after is what follows a word where there is more to say.
func after(more string) string {
	if more == "" {
		return ""
	}
	return ": " + more
}

// followRun prints a run of a program to its end: the build of what
// the program is made of, then what the program prints. It returns the
// run as it ended.
func followRun(cmd *cobra.Command, w *frontseat.Workspace, run *frontseat.Run) (*buildv1.Run, error) {
	built, err := run.BuildID(cmd.Context())
	if err != nil {
		return nil, err
	}
	if _, err := follow(cmd, w.BuildByID(built), 0); err != nil {
		return nil, err
	}
	printer := plain.Printer{Out: out}
	for line, err := range run.Logs(cmd.Context()) {
		if err != nil {
			return nil, err
		}
		printer.Line(line)
	}
	return run.Wait(cmd.Context())
}

// stop cancels a run in a moment of its own, half a minute, whatever
// became of the context the command ran in.
func stop(ctx context.Context, run *frontseat.Run) {
	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_, _ = run.Cancel(stopping)
}
