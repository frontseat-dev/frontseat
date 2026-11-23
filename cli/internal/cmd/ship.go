package cmd

import (
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// shipPlanned is the etag of a plan that was seen, where a release is to
// be of that plan and no other.
var shipPlanned string

// shipCmd releases entities.
var shipCmd = &cobra.Command{
	Use:   "ship ENTITY...",
	Short: "Release entities: build what moves for production, and tag it",
	Long: `Release entities of the workspace. Each is released with its unit: a
component alone, with its system where the system's members version
together, or with its domain where the domain says its systems version
together. A domain that does not is released as each of its systems.

The release is planned, what moves is built for production, and each
unit that moves is tagged in the workspace once that build has passed.
Each tag is then pushed to where its repository was brought from, the
tag alone. A workspace that may not push (one opened without --push)
keeps its tags, and the release says it is unpushed and why; made
again where it may, it pushes them. The command follows the build and
exits 0 where the release ended with its tags pushed, or with nothing
to tag, and 1 where it failed or ended unpushed.

With --plan, the release is of the plan that etag names, as ship plan
printed it, and is refused where the workspace has moved since.

An entity named plan is written in full, component:plan.`,
	Example: `  frontseat ship greet
  frontseat ship system:kiosk
  frontseat ship domain:retail
  frontseat ship greet --plan 94d84b5ff042...`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entities, w, err := releasing(args)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		printer := plain.Printer{Out: out}
		etag := shipPlanned
		if etag == "" {
			if etag, err = plan(cmd, w, entities); err != nil {
				return err
			}
		}
		release, err := w.Release(ctx, frontseat.ReleaseRequest{Entities: entities, Etag: etag})
		if err != nil {
			return err
		}
		// It is named at once: whoever is interrupted from here on, or
		// loses the daemon, finds it again by this, and it goes on.
		printer.Began(release.ID())
		// Its build is followed as a build is; a plan that builds nothing
		// has none.
		built, err := release.BuildID(ctx)
		if err != nil {
			return err
		}
		if built != "" {
			build := w.BuildByID(built)
			// A build that cannot be followed is still a release that
			// goes on: it is waited for all the same, and says how it
			// ended.
			if _, err := follow(cmd, build, 0); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				complained("frontseat: the build %s could not be followed: %v", build.ID(), err)
			}
		}
		ended, err := release.Wait(ctx)
		if err != nil {
			return err
		}
		printer.Release(ended)
		// A release that failed, and one whose tags did not all leave
		// the workspace, did not do what was asked: whatever runs the
		// command is told so.
		if state := ended.GetState(); state == buildv1.Release_STATE_FAILED || state == buildv1.Release_STATE_UNPUSHED {
			exitCode = 1
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(shipCmd)
	worksWhereItIsRun(shipCmd)
	shipCmd.Flags().StringVar(&shipPlanned, "plan", "", "release the plan this etag names, as ship plan printed it")
}

// releasing reads what names the entities a command releases, and the
// workspace it asks: a name that names none is refused before any server
// is asked.
func releasing(names []string) ([]entity.Pattern, *frontseat.Workspace, error) {
	entities, err := typed(names)
	if err != nil {
		return nil, nil, err
	}
	w, err := building()
	return entities, w, err
}

// plan prints what releasing entities would do, and returns the plan's
// etag.
func plan(cmd *cobra.Command, w *frontseat.Workspace, names []entity.Pattern) (string, error) {
	planned, err := w.PlanRelease(cmd.Context(), frontseat.ReleaseRequest{Entities: names})
	if err != nil {
		return "", err
	}
	plain.Printer{Out: out}.Plan(planned)
	return planned.GetEtag(), nil
}

// typed reads names of entities as a person types them (greet,
// system:kiosk, component:default/greet), and refuses, before any
// server is asked, one that is none.
func typed(names []string) ([]entity.Pattern, error) {
	var out []entity.Pattern
	for _, name := range names {
		p, err := entity.ParsePattern(name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
