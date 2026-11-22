package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

var (
	addRepositories []string
	addEntities     []string
	addWhole        bool
)

// addCmd has a workspace clone more.
var addCmd = &cobra.Command{
	Use:   "add [NAME]",
	Short: "Clone more into a workspace",
	Long: `Clone more into the workspace named, or the one last opened, and say
what it holds once it can be worked in again.`,
	Example: `  frontseat workspace add --entity gateway
  frontseat workspace add services-main --repository https://example.com/acme/platform.git`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(addRepositories)+len(addEntities) == 0 {
			return errors.New("name what to add with --entity or --repository")
		}
		more, err := sourced(addRepositories, addEntities, addWhole)
		if err != nil {
			return err
		}
		w, err := subject(args)
		if err != nil {
			return err
		}
		if err := w.Add(cmd.Context(), more); err != nil {
			return err
		}
		return described(cmd, w)
	},
}

func init() {
	workspaceCmd.AddCommand(addCmd)
	addCmd.Flags().StringArrayVar(&addRepositories, "repository", nil, "a repository for the daemon to clone, URL or URL#REF")
	addCmd.Flags().StringArrayVar(&addEntities, "entity", nil, "an entity whose source to clone, resolved through the control plane's Backstage catalog")
	addCmd.Flags().BoolVar(&addWhole, "whole", false, "clone the entities' repositories whole, not only the directories the entities govern")
}
