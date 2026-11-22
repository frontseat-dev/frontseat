package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

var (
	addRepositories []string
	addEntities     []string
	addWhole        bool
)

// addCmd has a workspace clone more.
var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Clone more into the workspace",
	Example: `  frontseat workspace add --entity gateway
  frontseat workspace add --repository https://example.com/acme/platform.git`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		more := frontseat.WorkspaceSpec{Whole: addWhole}
		if len(addRepositories)+len(addEntities) == 0 {
			return errors.New("name what to add with --entity or --repository")
		}
		// Repositories are cloned whole: there are no entities whose
		// directories alone could be.
		if addWhole && len(addEntities) == 0 {
			return errors.New("--whole is said of the entities added, and none is named: a repository is cloned whole as it is")
		}
		for _, r := range addRepositories {
			named, err := machine.Repository(r)
			if err != nil {
				return err
			}
			more.Repositories = append(more.Repositories, named)
		}
		entities, err := typed(addEntities)
		if err != nil {
			return err
		}
		more.Entities = entities
		w, err := asked()
		if err != nil {
			return err
		}
		if err := w.Add(cmd.Context(), more); err != nil {
			return err
		}
		return describe(cmd)
	},
}

func init() {
	workspaceCmd.AddCommand(addCmd)
	asksAWorkspace(addCmd)
	addCmd.Flags().StringArrayVar(&addRepositories, "repository", nil, "a repository for the daemon to clone, URL or URL#REF")
	addCmd.Flags().StringArrayVar(&addEntities, "entity", nil, "an entity whose source to clone, resolved through the control plane's Backstage catalog")
	addCmd.Flags().BoolVar(&addWhole, "whole", false, "clone the entities' repositories whole, not only the directories the entities govern")
}
