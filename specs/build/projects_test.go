package build_test

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
)

// Projects are identified by repository, directory and plugin. A real Go
// project and an SDK contract fixture exercise dependency resolution through
// the API; the fixture's comparison runs on the grid over the handed source.
// Projects sharing a directory / A project receives a dependency from another plugin in its own directory.
func TestAProjectReceivesADependencyFromAnotherPluginInItsOwnDirectory(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.colocatedProjects("pkg[1]")
	spec.Expect(workspace.projects()).To(HaveLen(2))
	build := workspace.build(of("//pkg[1]:cohabiting.compare"))
	spec.Expect(build.says()).To(BeEmpty(), build.diagnostics())
	spec.Expect(build.outcomeOf("//pkg[1]:cohabiting.compare")).To(Equal("passed"), build.diagnostics())
}

// Projects sharing a directory / Two plugins in one directory cannot silently provide the same coordinate.
func TestTwoPluginsInOneDirectoryCannotSilentlyProvideTheSameCoordinate(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.colocatedProjects("")
	workspace.write("cohabiting.toml", "provides = \"example.com/application\"\nrequires = \"example.com/outside\"\n")
	failure := workspace.projectFailure()
	spec.Expect(failure.GetMessage()).To(ContainSubstring("go:example.com/application"))
	spec.Expect(failure.GetMessage()).To(ContainSubstring("cohabiting"))
	workspace.write("cohabiting.toml", "provides = \"example.com/secondary\"\nrequires = \"example.com/application\"\n")
	spec.Expect(workspace.projects()).To(HaveLen(2))
}

// Projects sharing a directory / A resolved dependency identifies its provider plugin even at the repository root.
func TestAResolvedDependencyIdentifiesItsProviderPluginEvenAtTheRepositoryRoot(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.colocatedProjects("")
	provider := func() any {
		for _, project := range workspace.projects() {
			if project.GetRef().GetPlugin() != "cohabiting" {
				continue
			}
			spec.Expect(project.GetRequires()).To(HaveLen(1))
			raw, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(project.GetRequires()[0])
			spec.Expect(err).NotTo(HaveOccurred())
			var fields map[string]any
			spec.Expect(json.Unmarshal(raw, &fields)).To(Succeed())
			return fields["provider_project"]
		}
		spec.Fatal("the fixture project was not discovered")
		return nil
	}
	spec.Expect(provider()).To(Equal(map[string]any{"repository": "", "root": "", "plugin": "go"}))
	workspace.write("cohabiting.toml", "provides = \"example.com/secondary\"\nrequires = \"example.com/outside\"\n")
	spec.Expect(provider()).To(BeNil())
}

// Projects sharing a directory / Two dependencies cannot silently overwrite one another at the same destination.
func TestTwoDependenciesCannotSilentlyOverwriteOneAnotherAtTheSameDestination(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.colocatedProjects("app")
	workspace.write("app/go.mod", "module example.com/application\n\ngo 1.27\nrequire example.com/library v0.0.0\n")
	workspace.write("lib/go.mod", "module example.com/library\n\ngo 1.27\n")
	workspace.write("lib/library.go", "package library\n")
	workspace.write("app/cohabiting.toml", "provides = \"example.com/secondary\"\nrequires = \"example.com/application\"\ndependency_directory = \".frontseat/shared\"\n")
	spec.Expect(workspace.projectFailure().GetMessage()).To(ContainSubstring("more than one dependency at .frontseat/shared"))
	workspace.write("app/cohabiting.toml", "provides = \"example.com/secondary\"\nrequires = \"example.com/application\"\n")
	spec.Expect(workspace.projects()).To(HaveLen(3))
	build := workspace.build(of("//app:cohabiting.compare"))
	spec.Expect(build.outcomeOf("//app:cohabiting.compare")).To(Equal("passed"), build.diagnostics())
}
