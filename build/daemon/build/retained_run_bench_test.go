package build

import (
	"context"
	"fmt"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// groupedModules is a workspace of a hundred Go modules, m000 to m099,
// in groups of ten whose first module the other nine require. It returns
// the workspace and the name of each module.
func groupedModules(b testing.TB) (string, []string) {
	b.Helper()
	workspace := b.TempDir()
	testpins.Go(b, workspace)
	var names []string
	for i := range 100 {
		name := fmt.Sprintf("m%03d", i)
		mod := fmt.Sprintf("module example.com/%s\n\ngo 1.27\n", name)
		if first := i - i%10; first != i {
			mod += fmt.Sprintf("\nrequire example.com/m%03d v0.0.0\n", first)
		}
		write(b, workspace, name+"/go.mod", mod)
		write(b, workspace, name+"/main.go", "package main\n\nfunc main() {}\n")
		names = append(names, name)
	}
	return workspace, names
}

// BenchmarkRetainedRunAPI measures complete warm run requests through
// Connect: creation, waiting for completion, and reading the projected
// action results. The workspace has 100 Go modules, with each group of
// ten requiring its first module as source.
func BenchmarkRetainedRunAPI(b *testing.B) {
	for _, scenario := range []struct {
		name, target string
		compiles     int
	}{
		{name: "AllCompile", compiles: 100},
		{name: "OneCompile", target: "m001/", compiles: 1},
	} {
		b.Run(scenario.name, func(b *testing.B) {
			workspace, _ := groupedModules(b)

			clients, _ := serveWithGraph(b, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}})
			ctx := context.Background()
			request := &buildv1.CreateBuildRequest{Only: []string{"compile"}}
			if scenario.target != "" {
				request.Targets = []string{scenario.target}
			}
			run := func() error {
				created, err := clients.runs.CreateBuild(ctx, request)
				if err != nil {
					return fmt.Errorf("CreateBuild: %w", err)
				}
				finished := wait(b, clients.runs, created.GetBuild().GetId())
				if finished.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
					return fmt.Errorf("run ended %s: %s", finished.GetExitCode(), finished.GetFailure())
				}
				listed, err := clients.runs.ListBuildActions(ctx, &buildv1.ListBuildActionsRequest{BuildId: finished.GetId()})
				if err != nil {
					return fmt.Errorf("ListBuildActions: %w", err)
				}
				compiles := 0
				for _, action := range listed.GetActions() {
					if action.GetName() == "compile" {
						compiles++
					}
				}
				if compiles != scenario.compiles {
					return fmt.Errorf("run selected %d compile actions; want %d", compiles, scenario.compiles)
				}
				return nil
			}

			// Prime the workspace and daemon; count only complete API runs.
			if err := run(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
