package build_test

import (
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	. "github.com/onsi/gomega"
	"strings"
)

// Catalog descriptors are discovered through the Backstage plugin using the
// snapshot SDK. Catalog relationships describe ownership and organization;
// source inference remains authoritative for component build dependencies.
// The workspace catalog graph / A component discovered at the repository root owns nested projects.
func TestAComponentDiscoveredAtTheRepositoryRootOwnsNestedProjects(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("nested")
	workspace.write("frontseat.yaml", "plugins:\n  go: {}\n  backstage: {}\n")
	workspace.write("catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: repository}\nspec: {type: service, lifecycle: production, owner: team}\n")
	spec.Expect(workspace.ownerOf("/nested")).To(Equal("component:default/repository"))
	spec.Expect(workspace.catalogEntities()["component:default/repository"].GetRoots()).To(Equal([]string{""}))
}

// The workspace catalog graph / Discovered catalog identities select builds and own their recorded actions.
func TestDiscoveredCatalogIdentitiesSelectBuildsAndOwnTheirRecordedActions(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("nested")
	workspace.write("frontseat.yaml", "plugins: {go: {}, backstage: {}}\n")
	workspace.write("catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: repository}\nspec: {type: service, lifecycle: production, owner: team}\n")
	build := workspace.build(of("component:default/repository"))
	spec.Expect(build.says()).To(BeEmpty())
	spec.Expect(build.outcomeOf("//nested:go.compile")).To(Equal("passed"))
	for _, action := range build.results() {
		spec.Expect(entity.FromProto(action.GetEntity()).String()).To(Equal("component:default/repository"))
	}
}

// The workspace catalog graph / Catalog aliases cannot hide stale component dependencies from build checks or fixes.
func TestCatalogAliasesCannotHideStaleComponentDependenciesFromBuildChecksOrFixes(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	aliased := "x-deps: &deps [component:invented, resource:database]\n" + strings.Replace(catalogFixture, "dependsOn: [component:greet, resource:database]", "dependsOn: *deps", 1)
	workspace.write("platform/gateway/catalog-info.yaml", aliased)
	build := workspace.build()
	spec.Expect(build.run.GetFailure()).To(BeEmpty())
	spec.Expect(build.outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("rewrite"))
	workspace.build(fixing())
	spec.Expect(workspace.build().outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("passed"))
	graph := workspace.catalogEntities()
	spec.Expect(spec.catalogRelation(graph["component:default/gateway"], "dependsOn", "component:default/greet")).To(BeTrue())
	spec.Expect(spec.catalogRelation(graph["component:default/gateway"], "dependsOn", "resource:default/database")).To(BeTrue())
}

// The workspace catalog graph / Two components cannot silently claim the same source directory.
func TestTwoComponentsCannotSilentlyClaimTheSameSourceDirectory(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	other := "\n---\napiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: other}\nspec: {type: service, lifecycle: production, owner: team}\n"
	workspace.write("platform/gateway/catalog-info.yaml", catalogFixture+other)
	spec.Expect(workspace.projectFailure().GetMessage()).To(ContainSubstring("both govern platform/gateway"))
	workspace.write("platform/gateway/catalog-info.yaml", catalogFixture)
	workspace.write("platform/gateway/other/catalog-info.yaml", strings.TrimPrefix(other, "\n---\n"))
	spec.Expect(workspace.catalogEntities()["component:default/other"].GetRoots()).To(Equal([]string{"gateway/other"}))
}

// The workspace catalog graph / Catalog documents expose every entity without giving organizational entities source ownership.
func TestCatalogDocumentsExposeEveryEntityWithoutGivingOrganizationalEntitiesSourceOwnership(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	graph := workspace.catalogEntities()
	spec.Expect(graph).To(HaveKey("component:default/gateway"))
	spec.Expect(graph).To(HaveKey("component:default/greet"))
	for _, ref := range []string{"group:default/team", "group:default/org", "user:default/alex", "system:default/shop", "domain:default/commerce", "api:default/orders", "resource:default/database"} {
		spec.Expect(graph).To(HaveKey(ref))
		spec.Expect(graph[ref].GetRoots()).To(BeEmpty(), ref)
	}
	spec.Expect(graph["component:default/gateway"].GetRoots()).To(Equal([]string{"gateway"}))
	spec.Expect(workspace.ownerOf("platform/gateway")).To(Equal("component:default/gateway"))
}

// The workspace catalog graph / Catalog relations include inverses and retain targets outside the workspace.
func TestCatalogRelationsIncludeInversesAndRetainTargetsOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	workspace.write("platform/gateway/catalog-info.yaml", strings.Replace(catalogFixture, "providesApis: [orders]", "providesApis: [orders]\n  consumesApis: [elsewhere/payments]", 1))
	graph := workspace.catalogEntities()
	for _, edge := range [][3]string{
		{"component:default/gateway", "ownedBy", "group:default/team"},
		{"group:default/team", "ownerOf", "component:default/gateway"},
		{"component:default/gateway", "partOf", "system:default/shop"},
		{"system:default/shop", "hasPart", "component:default/gateway"},
		{"system:default/shop", "partOf", "domain:default/commerce"},
		{"domain:default/commerce", "hasPart", "system:default/shop"},
		{"component:default/gateway", "providesApi", "api:default/orders"},
		{"api:default/orders", "apiProvidedBy", "component:default/gateway"},
		{"component:default/gateway", "dependsOn", "resource:default/database"},
		{"resource:default/database", "dependencyOf", "component:default/gateway"},
		{"group:default/team", "childOf", "group:default/org"},
		{"group:default/org", "parentOf", "group:default/team"},
		{"user:default/alex", "memberOf", "group:default/team"},
		{"group:default/team", "hasMember", "user:default/alex"},
	} {
		spec.Expect(spec.catalogRelation(graph[edge[0]], edge[1], edge[2])).To(BeTrue(), "%v", edge)
	}
	spec.Expect(spec.catalogRelation(graph["component:default/gateway"], "consumesApi", "api:elsewhere/payments")).To(BeFalse())
	spec.Expect(graph).NotTo(HaveKey("api:elsewhere/payments"))
	spec.Expect(graph["component:default/gateway"].GetManifest()).To(Equal("gateway/catalog-info.yaml"))
}

// The workspace catalog graph / A catalog file's comments and other fields survive the rewrite that puts it in step.
func TestACatalogFileSCommentsAndOtherFieldsSurviveTheRewriteThatPutsItInStep(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	commented := "# The gateway, which fronts the shop.\n" + strings.Replace(catalogFixture, "  dependsOn: [component:greet, resource:database]", "  # what it leans on\n  dependsOn: [component:invented, resource:database] # kept in step", 1)
	workspace.write("platform/gateway/catalog-info.yaml", commented)
	spec.Expect(workspace.build().outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("rewrite"))
	fixed := workspace.build(of("//platform/gateway:catalog.depends-on"), fixing())
	spec.Expect(fixed.outcome()).To(Equal("SUCCESS"), fixed.diagnostics())
	after := workspace.file("platform/gateway/catalog-info.yaml")
	for _, kept := range []string{"# The gateway, which fronts the shop.", "# what it leans on", "# kept in step", "resource:database", "providesApis: [orders]", "kind: Group"} {
		spec.Expect(after).To(ContainSubstring(kept))
	}
	spec.Expect(after).NotTo(ContainSubstring("component:invented"))
	spec.Expect(after).To(ContainSubstring("component:greet"))
}

// The workspace catalog graph / An inverse the file states is held to the source: an invented dependent is taken out, a true one stands, an absent field is not introduced.
func TestAnInverseTheFileStatesIsHeldToTheSourceAnInventedDependentIsTakenOutATrueOneStandsAnAbsentFieldIsNotIntroduced(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	workspace.write("platform/gateway/catalog-info.yaml", strings.Replace(catalogFixture, "  dependsOn: [component:greet, resource:database]", "  dependsOn: [component:greet, resource:database]\n  dependencyOf: [component:invented]", 1))
	spec.Expect(workspace.build().outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("rewrite"))
	fixed := workspace.build(of("//platform/gateway:catalog.depends-on"), fixing())
	spec.Expect(fixed.outcome()).To(Equal("SUCCESS"), fixed.diagnostics())
	spec.Expect(workspace.file("platform/gateway/catalog-info.yaml")).NotTo(ContainSubstring("component:invented"))
	// The default fixture states no inverse, and none is introduced.
	spec.Expect(workspace.file("platform/gateway/catalog-info.yaml")).NotTo(ContainSubstring("dependencyOf"))
}

// The workspace catalog graph / Authored component dependency relations cannot add build dependencies in either direction.
func TestAuthoredComponentDependencyRelationsCannotAddBuildDependenciesInEitherDirection(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	workspace.write("platform/gateway/catalog-info.yaml", strings.Replace(catalogFixture, "dependsOn: [component:greet, resource:database]", "dependsOn: [component:invented, resource:database]\n  dependencyOf: [component:greet]", 1))
	graph := workspace.catalogEntities()
	spec.Expect(spec.catalogRelation(graph["component:default/gateway"], "dependsOn", "component:default/greet")).To(BeTrue())
	spec.Expect(spec.catalogRelation(graph["component:default/greet"], "dependencyOf", "component:default/gateway")).To(BeTrue())
	for _, ref := range []string{"component:default/gateway", "component:default/greet"} {
		for _, relation := range graph[ref].GetRelations() {
			spec.Expect(entity.FromProto(relation.GetTarget()).String()).NotTo(Equal("component:default/invented"))
			if ref == "component:default/gateway" {
				spec.Expect(relation.GetType()).NotTo(Equal("dependencyOf"))
			}
		}
	}
}

// The workspace catalog graph / Catalog identities and relations are reevaluated after an edit.
func TestCatalogIdentitiesAndRelationsAreReevaluatedAfterAnEdit(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	spec.Expect(workspace.catalogEntities()).To(HaveKey("group:default/team"))
	workspace.write("platform/gateway/catalog-info.yaml", strings.ReplaceAll(catalogFixture, "team", "new-team"))
	graph := workspace.catalogEntities()
	spec.Expect(graph).NotTo(HaveKey("group:default/team"))
	spec.Expect(spec.catalogRelation(graph["component:default/gateway"], "ownedBy", "group:default/new-team")).To(BeTrue())
}

// The workspace catalog graph / Catalog duplicate identities fail with a source diagnostic and recover after repair.
func TestCatalogDuplicateIdentitiesFailWithASourceDiagnosticAndRecoverAfterRepair(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	workspace.write("platform/gateway/duplicate/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: TEAM}\nspec: {type: team, children: []}\n")
	failure := workspace.projectFailure()
	spec.Expect(failure.GetMessage()).To(ContainSubstring("duplicate entity group:default/team"))
	finding := new(typev1.Diagnostic)
	spec.Expect(failure.GetDetails()).To(HaveLen(2))
	spec.Expect(failure.GetDetails()[1].UnmarshalTo(finding)).To(Succeed())
	spec.Expect(finding.GetRuleId()).To(Equal("catalog/duplicate-entity"))
	workspace.write("platform/gateway/duplicate/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: team, namespace: other}\nspec: {type: team, children: []}\n")
	spec.Expect(workspace.catalogEntities()).To(HaveKey("group:other/team"))
}

// The workspace catalog graph / Malformed catalog fields fail through the shared diagnostic API and recover.
func TestMalformedCatalogFieldsFailThroughTheSharedDiagnosticAPIAndRecover(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	workspace.write("platform/gateway/catalog-info.yaml", strings.Replace(catalogFixture, "owner: team", "owner: 123", 1))
	failure := workspace.projectFailure()
	finding := new(typev1.Diagnostic)
	spec.Expect(failure.GetDetails()).To(HaveLen(2))
	spec.Expect(failure.GetDetails()[1].UnmarshalTo(finding)).To(Succeed())
	spec.Expect(finding.GetRuleId()).To(Equal("backstage/invalid-catalog"))
	spec.Expect(finding.GetSourceLocation().GetPath()).To(Equal("gateway/catalog-info.yaml"))
	workspace.write("platform/gateway/catalog-info.yaml", catalogFixture)
	spec.Expect(workspace.catalogEntities()).To(HaveKey("group:default/team"))
}

// Asserts that the edge exists exactly once, then returns target presence.
func (spec *scenario) catalogRelation(source *buildv1.Entity, kind, target string) bool {
	spec.Helper()
	var found []*buildv1.EntityRelation
	for _, relation := range source.GetRelations() {
		if relation.GetType() == kind && entity.FromProto(relation.GetTarget()).String() == target {
			found = append(found, relation)
		}
	}
	spec.Expect(found).To(HaveLen(1), "%s %s %s", entity.FromProto(source.GetRef()), kind, target)
	return found[0].GetTargetPresent()
}

// locationFixture is a Location, as Backstage imports a repository by:
// it lists catalog files, one of which is no catalog-info.yaml.
const locationFixture = `---
apiVersion: backstage.io/v1alpha1
kind: Location
metadata: {name: platform}
spec:
  type: url
  targets: [./catalog-info.yaml, ./more/listed.yaml]
  presence: optional
`

// The workspace catalog graph / A Location is kept as it is written, and frontseat does not follow its targets.
func TestALocationIsKeptAsItIsWrittenAndItsTargetsAreNotFollowed(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	// The Location is the file's first document, and the component after
	// it depends on what its source does not: the fix rewrites this file,
	// and what it rewrites is the component alone.
	stale := strings.Replace(catalogFixture, "dependsOn: [component:greet, resource:database]", "dependsOn: [component:invented, resource:database]", 1)
	spec.Expect(stale).NotTo(Equal(catalogFixture))
	location := strings.TrimPrefix(locationFixture, "---\n")
	written := location + "---\n" + stale
	workspace.write("platform/gateway/catalog-info.yaml", written)
	workspace.write("platform/gateway/more/listed.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: listed}\nspec: {type: service, lifecycle: production, owner: team}\n")
	graph := workspace.catalogEntities()
	spec.Expect(graph).To(HaveKey("location:default/platform"))
	spec.Expect(graph["location:default/platform"].GetRoots()).To(BeEmpty())
	spec.Expect(graph["location:default/platform"].GetRelations()).To(BeEmpty())
	spec.Expect(graph["location:default/platform"].GetManifest()).To(Equal("gateway/catalog-info.yaml"))
	// What a Location lists is Backstage's to read: frontseat finds
	// catalog files by their name alone.
	spec.Expect(graph).NotTo(HaveKey("component:default/listed"))
	spec.Expect(workspace.build().outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("rewrite"))
	fixed := workspace.build(of("//platform/gateway:catalog.depends-on"), fixing())
	spec.Expect(fixed.outcome()).To(Equal("SUCCESS"), fixed.diagnostics())
	after := workspace.file("platform/gateway/catalog-info.yaml")
	spec.Expect(after).To(HavePrefix(location+"---\n"), "the Location is not as it was written")
	spec.Expect(after).To(Equal(strings.TrimSpace(location + "---\n" + strings.Replace(stale, "dependsOn: [component:invented, resource:database]", "dependsOn: ['resource:database', 'component:greet']", 1))))
	spec.Expect(workspace.build().outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("passed"))
}

// The workspace catalog graph / A malformed Location is refused by the field it gets wrong, and the repaired one is taken.
func TestAMalformedLocationIsRefusedByTheFieldItGetsWrong(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	for field, malformed := range map[string]string{
		"spec.targets":  strings.Replace(locationFixture, "targets: [./catalog-info.yaml, ./more/listed.yaml]", "targets: ./catalog-info.yaml", 1),
		"spec.target":   strings.Replace(locationFixture, "targets: [./catalog-info.yaml, ./more/listed.yaml]", "target: \"\"", 1),
		"spec.type":     strings.Replace(locationFixture, "type: url", "type: [url]", 1),
		"spec.presence": strings.Replace(locationFixture, "presence: optional", "presence: sometimes", 1),
		"apiVersion":    strings.Replace(locationFixture, "backstage.io/v1alpha1", "example.com/v1", 1),
		"spec is":       "---\napiVersion: backstage.io/v1alpha1\nkind: Location\nmetadata: {name: platform}\n",
	} {
		workspace.write("platform/gateway/catalog-info.yaml", catalogFixture+malformed)
		failure := workspace.projectFailure()
		spec.Expect(failure.GetMessage()).To(ContainSubstring(field), malformed)
		finding := new(typev1.Diagnostic)
		spec.Expect(failure.GetDetails()).To(HaveLen(2))
		spec.Expect(failure.GetDetails()[1].UnmarshalTo(finding)).To(Succeed())
		spec.Expect(finding.GetRuleId()).To(Equal("backstage/invalid-catalog"), malformed)
		spec.Expect(finding.GetSourceLocation().GetPath()).To(Equal("gateway/catalog-info.yaml"))
	}
	// The control: the same Location, sound, is taken.
	workspace.write("platform/gateway/catalog-info.yaml", catalogFixture+locationFixture)
	spec.Expect(workspace.catalogEntities()).To(HaveKey("location:default/platform"))
}

// The workspace catalog graph / A name finds its entity in whatever namespace its catalog put it in, and a name two namespaces hold is refused naming both.
func TestANameFindsItsEntityInWhateverNamespaceItsCatalogPutItIn(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("nested")
	workspace.write("frontseat.yaml", "plugins: {go: {}, backstage: {}}\n")
	workspace.write("catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: repository, namespace: frontseat}\nspec: {type: service, lifecycle: production, owner: team}\n")
	byName := workspace.build(of("repository"), only("compile"))
	spec.Expect(byName.says()).To(BeEmpty())
	spec.Expect(byName.outcomeOf("//nested:go.compile")).To(Equal("passed"))

	// The control: a second entity of the name in another namespace
	// makes the name ambiguous, and it is refused naming both; written
	// in full, each is found.
	workspace.write("pkg1/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: repository, namespace: other}\nspec: {type: service, lifecycle: production, owner: team}\n")
	ambiguous := workspace.build(of("repository"), only("compile"))
	spec.Expect(ambiguous.says()).To(ContainSubstring("component:frontseat/repository"))
	spec.Expect(ambiguous.says()).To(ContainSubstring("component:other/repository"))
	inFull := workspace.build(of("component:frontseat/repository"), only("compile"))
	spec.Expect(inFull.says()).To(BeEmpty())
	spec.Expect(inFull.outcomeOf("//nested:go.compile")).To(Equal("passed"))
}
