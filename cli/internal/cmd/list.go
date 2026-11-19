package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the workspaces there are",
	Long: `List the workspaces the control plane keeps, oldest first: each by its
name, where it is, and what it was asked for with. A name is what
` + "`frontseat workspace open`" + `, ` + "`--workspace`" + ` and ` + "`frontseat workspace delete`" + ` take.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fs, err := server()
		if err != nil {
			return err
		}
		got, err := fs.Workspaces(cmd.Context())
		if err != nil {
			return err
		}
		for _, each := range got {
			said("%s  %s  %s", each.GetName(), strings.ToLower(strings.TrimPrefix(each.GetStatus().GetState().String(), "STATE_")), askedFor(each.GetSpec()))
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
		parts = append(parts, entity.FromProto(ref).String())
	}
	if spec.GetPush() {
		parts = append(parts, "(may push)")
	}
	return strings.Join(parts, " ")
}

func init() {
	workspaceCmd.AddCommand(listCmd)
}
