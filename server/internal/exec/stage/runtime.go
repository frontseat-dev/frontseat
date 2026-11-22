package stage

import (
	"fmt"
	"maps"
	"path"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/image"
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/testservice"
)

// Runtimes places each separately prepared root below the reserved runtime
// directory. Each retains ordinary REAPI input composition and collision
// checks; the complete closure is included in the action's key.
func Runtimes(action exec.Action, description *runnerv1.RunnerSandbox, programs map[string]exec.Action) (exec.Action, error) {
	if action.Prepared == nil {
		action.Prepared = map[string]exec.Action{}
	}
	if action.Written == nil {
		action.Written = map[string]exec.Written{}
	}
	for _, service := range description.GetServices() {
		program, found := programs[service.GetName()]
		if !found {
			continue
		}
		if !testservice.Named(service.GetName()) {
			return exec.Action{}, fmt.Errorf("invalid prepared runtime name")
		}
		root := path.Join(RuntimeDirectory, service.GetName())
		layout, err := confine.ProgramLayout(program.Arguments)
		if err != nil {
			return exec.Action{}, err
		}
		program.Directories = append(program.Directories, layout.Home, layout.Temporary)
		action.Prepared[root] = program
		runtimeImage := strings.TrimPrefix(program.Platform["container-image"], "docker://")
		if _, err := image.Read(runtimeImage); err != nil {
			return exec.Action{}, fmt.Errorf("the image a program is prepared in %w", err)
		}
		service.Program = &runnerv1.RunnerProgram{Root: root, Directory: path.Clean(program.WorkingDirectory), Arguments: program.Arguments, Image: runtimeImage, Platform: maps.Clone(program.Platform)}
		// Program environment is encoded inside its confinement command,
		// never passed to the image entrypoint or confiner.
		service.Service.Environment = nil
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(description)
	if err != nil {
		return exec.Action{}, err
	}
	action.Written[RunnerSandbox] = exec.Written{Content: encoded}
	return action, nil
}
