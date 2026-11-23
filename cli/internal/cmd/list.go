package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/sdk/go/enum"
)

// listCmd lists the workspaces the control plane keeps.
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the workspaces there are",
	Long: `List the workspaces the control plane keeps, oldest first: each by its
name, where it is, and what it was asked for with. A name is what
` + "`frontseat workspace open`" + `, ` + "`frontseat workspace ssh`" + ` and ` + "`frontseat workspace delete`" + ` take.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fs, err := server()
		if err != nil {
			return err
		}
		for each, err := range fs.Workspaces(cmd.Context()) {
			if err != nil {
				return err
			}
			said("%s  %s  %s", each.GetName(), enum.Word(each.GetStatus().GetState()), askedFor(each.GetSpec()))
			if why := each.GetStatus().GetFailure(); why != "" {
				said("  %s", plain.Shown(why))
			}
		}
		return nil
	},
}

// askedFor says what a workspace was asked for with, on one line.
func askedFor(spec *workspacev1.WorkspaceSpec) string {
	var parts []string
	for _, repository := range spec.GetRepositories() {
		part := repository.GetRemote()
		if repository.GetRef() != "" {
			part += "#" + repository.GetRef()
		}
		parts = append(parts, part)
	}
	for _, ref := range spec.GetEntities() {
		// As it was asked for: a namespace left out stands for any.
		parts = append(parts, entity.PatternFromProto(ref).String())
	}
	if spec.GetPush() {
		parts = append(parts, "(may push)")
	}
	return strings.Join(parts, " ")
}

func init() {
	workspaceCmd.AddCommand(listCmd)
}
