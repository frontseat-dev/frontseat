package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// shipPlanned is the etag of a plan that was seen, where a release is to
// be of that plan and no other.
var shipPlanned string

// shipCmd represents the ship command
var shipCmd = &cobra.Command{
	Use:   "ship ENTITY...",
	Short: "Release entities: build what moves for production, and tag it",
	Long: `Release entities of the workspace. Each is released with its unit: a
component alone, with its system where the system's members version
together, or with its domain where the domain says its systems version
together. A domain that does not is released as each of its systems.

The release is planned, what moves is built for production, and each
unit that moves is tagged in the workspace once that build has passed.
The tags are not pushed. The command follows the build and exits 0 where
the release ended tagged, and 1 where it failed.

With --plan, the release is of the plan that etag names, as ship plan
printed it, and is refused where the workspace has moved since.

An entity named plan is written in full, component:plan.`,
	Example: `  frontseat ship greet
  frontseat ship system:kiosk
  frontseat ship domain:retail
  frontseat ship greet --plan 94d84b5ff042...`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := releasing(args)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		printer := plain.Printer{Out: out}
		etag := shipPlanned
		if etag == "" {
			if etag, err = plan(cmd, w, args); err != nil {
				return err
			}
		}
		release, err := w.Release(ctx, etag, args...)
		if err != nil {
			return err
		}
		// It is named at once: whoever is interrupted from here on, or
		// loses the daemon, finds it again by this, and it goes on.
		printer.Began(release.ID)
		// Its build is followed as a build is; a plan that builds nothing
		// has none.
		if release.Build != nil {
			// A build that cannot be followed is still a release that
			// goes on: it is waited for all the same, and says how it
			// ended.
			if _, err := follow(cmd, release.Build, 0); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fmt.Fprintf(os.Stderr, "frontseat: the build %s could not be followed: %v\n", release.Build.ID, err)
			}
		}
		ended, err := release.Wait(ctx)
		if err != nil {
			return err
		}
		printer.Release(ended)
		if ended.GetState() == buildv1.Release_STATE_FAILED {
			exitCode = 1
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(shipCmd)
	asksAWorkspace(shipCmd)
	shipCmd.Flags().StringVar(&shipPlanned, "plan", "", "release the plan this etag names, as ship plan printed it")
}

// releasing is the workspace a command that releases entities asks,
// once what names them is read: a name that names none is refused before
// any server is asked.
func releasing(names []string) (*frontseat.Workspace, error) {
	if err := typed(names); err != nil {
		return nil, err
	}
	return asked()
}

// plan prints what releasing entities would do, and returns the plan's
// etag.
func plan(cmd *cobra.Command, w *frontseat.Workspace, names []string) (string, error) {
	planned, err := w.PlanRelease(cmd.Context(), names...)
	if err != nil {
		return "", err
	}
	plain.Printer{Out: out}.Plan(planned)
	return planned.GetEtag(), nil
}

// typed refuses, before any server is asked, a name that is no entity
// as a person types one: greet, system:kiosk, component:default/greet.
func typed(names []string) error {
	for _, name := range names {
		if _, err := entity.Typed(name); err != nil {
			return err
		}
	}
	return nil
}
