package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

var created struct {
	name, image  string
	repositories []string
	entities     []string
	whole, push  bool
	// remote asks for a remote workspace, and task names the task it
	// works on.
	remote bool
	task   string
}

// createCmd makes a workspace of repositories or entities, or opens the
// one there is of what they name.
var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Make a workspace of repositories or entities",
	Long: `Make a workspace of the repositories and entities named, and select it
on this machine, as workspace open does: the one there is of what they
name, as it was left, or a new one. A repository is named by its address over
http or https, URL or URL#REF; an entity as kind:name, resolved through
the Backstage catalog the control plane's operator gives it, and refused
where it has none.

A workspace is local unless --remote is given, as workspace open says:
remote, each repository named is held from the control plane's snapshot
store, by its name, and the entities' repositories with it, on the task
--task names.

A workspace is given a name by what it was first asked for, or the one
--name gives. At a terminal it goes on into a shell there, as workspace
open does.`,
	Example: `  frontseat workspace create --repository https://example.com/acme/services.git#main
  frontseat workspace create --entity component:payments --entity system:kiosk
  frontseat workspace create --name payments --entity payments --whole --push
  frontseat workspace create --entity payments --remote --task fix-refunds`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(created.repositories)+len(created.entities) == 0 {
			return errors.New("name what the workspace holds with --repository or --entity: the workspace of this checkout is opened with `frontseat workspace open`")
		}
		if err := remotely(cmd, created.remote); err != nil {
			return err
		}
		more, err := sourced(created.repositories, created.entities, created.whole)
		if err != nil {
			return err
		}
		if created.remote {
			if more, err = stored(more, created.task); err != nil {
				return err
			}
		}
		fs, err := server()
		if err != nil {
			return err
		}
		key, err := machine.AuthorizedKey()
		if err != nil {
			return err
		}
		more.Name, more.Image, more.Push, more.AuthorizedKey = created.name, created.image, created.push, key
		return opening(cmd.Context(), fs, more)
	},
}

// sourced is the source repositories and entities named ask for, each
// read before any server is asked: of repositories alone, there is no
// less than the whole to clone.
func sourced(repositories, entities []string, whole bool) (frontseat.WorkspaceSpec, error) {
	if whole && len(entities) == 0 {
		return frontseat.WorkspaceSpec{}, errors.New("--whole is said of the entities named, and none is: a repository is cloned whole as it is")
	}
	patterns, err := typed(entities)
	if err != nil {
		return frontseat.WorkspaceSpec{}, err
	}
	spec := frontseat.WorkspaceSpec{Whole: whole, Entities: patterns}
	for _, r := range repositories {
		if !strings.Contains(r, "://") {
			return frontseat.WorkspaceSpec{}, fmt.Errorf("a workspace clones a repository over http or https, and is given its address so: %q is not one", r)
		}
		named, err := machine.Repository(r)
		if err != nil {
			return frontseat.WorkspaceSpec{}, err
		}
		spec.Repositories = append(spec.Repositories, named)
	}
	return spec, nil
}

// remotely holds the flags that are said of a remote workspace, or of a
// local one, to the one asked for, before anything is asked: a task is
// a remote workspace's, and a push a local one's, whose work leaves it
// so; a remote one's lands on its task.
func remotely(cmd *cobra.Command, remote bool) error {
	switch {
	case !remote && cmd.Flags().Changed("task"):
		return errors.New("--task names the task a remote workspace works on: give --remote with it, or leave it out for a local workspace")
	case remote && cmd.Flags().Changed("push"):
		return errors.New("--push is said of a local workspace, whose work leaves it by a push: a remote workspace's lands on its task")
	}
	return nil
}

// stored is a source asked for as a remote workspace holds it: each
// repository from the control plane's snapshot store, by its name, and
// none cloned; the entities as they were named, whose repositories the
// control plane holds from the store too; on a task, and landing.
func stored(local frontseat.WorkspaceSpec, task string) (frontseat.WorkspaceSpec, error) {
	remote := local
	remote.Repositories, remote.Task, remote.Land = nil, task, true
	for _, repository := range local.Repositories {
		held, err := machine.Stored(repository, checkout().NameOf)
		if err != nil {
			return frontseat.WorkspaceSpec{}, err
		}
		remote.Stored = append(remote.Stored, held)
	}
	return remote, nil
}

// remoteFlags gives a command that makes a workspace the flags that ask
// for a remote one.
func remoteFlags(cmd *cobra.Command, remote *bool, task *string) {
	cmd.Flags().BoolVar(remote, "remote", false, "make the workspace a remote one: its repositories are held from the control plane's snapshot store and mounted in it, not cloned, and what passes its tests lands on its task; served by Frontseat Enterprise. Without it the workspace is local: its repositories are cloned in it, and committed and pushed from there")
	cmd.Flags().StringVar(task, "task", "", "the task a remote workspace works on, made the first time it is named; without it, one named for the workspace")
}

func init() {
	workspaceCmd.AddCommand(createCmd)
	remoteFlags(createCmd, &created.remote, &created.task)
	createCmd.Flags().StringVar(&created.name, "name", "", "the workspace's name; without it, the one the control plane gives what is asked for")
	createCmd.Flags().StringArrayVar(&created.repositories, "repository", nil, "a repository for the workspace to clone, URL or URL#REF; give it once for each")
	createCmd.Flags().StringArrayVar(&created.entities, "entity", nil, "an entity whose source to clone, resolved through the control plane's Backstage catalog; give it once for each")
	createCmd.Flags().BoolVar(&created.whole, "whole", false, "clone the entities' repositories whole, not only the directories the entities govern")
	createCmd.Flags().BoolVar(&created.push, "push", false, "make the workspace one that may push to its repositories, with whatever credential the control plane keeps for their hosts")
	createCmd.Flags().StringVar(&created.image, "image", "", "the box image the workspace's sandbox runs; without it, the control plane's own")
	shellFlags(createCmd)
}
