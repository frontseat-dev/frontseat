package build_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	. "github.com/onsi/gomega"
)

// A generator contributes worker-produced source to an existing module. The
// module keeps its handwritten metadata and remains the one provider of its
// coordinate. Both the module and its dependents receive the generated source
// through inferred action dependencies, without changing the checkout.
// Generated source dependencies / Go generator annotations stay worker-owned while consumers receive the selected Go source.
func TestGoGeneratorAnnotationsStayWorkerOwnedWhileConsumersReceiveTheSelectedGoSource(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	workspace.write("api/buf.gen.yaml", "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen/go\n    opt: paths=source_relative,annotate_code=true\ninputs:\n  - directory: .")
	built := workspace.build(of("//app:go.compile"))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
}

// Generated source dependencies / Buf lint runs on the worker and a malformed schema fails before generation.
func TestBufLintRunsOnTheWorkerAndAMalformedSchemaFailsBeforeGeneration(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	valid := workspace.build(of("//api:buf.lint"))
	spec.Expect(valid.outcome()).To(Equal("SUCCESS"), valid.diagnostics())
	workspace.write("api/hello/v1/hello.proto", "syntax = \"proto3\";\nmessage {")
	invalid := workspace.build(of("//api:buf.lint"))
	spec.Expect(invalid.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(invalid.outcomeOf("//api:buf.lint")).To(Equal("failed"))
}

// Generated source dependencies / The worker refuses a generator whose downloaded bytes disagree with the repository lock.
func TestTheWorkerRefusesAGeneratorWhoseDownloadedBytesDisagreeWithTheRepositoryLock(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	valid := workspace.build(of("//api:buf.generate"))
	spec.Expect(valid.outcome()).To(Equal("SUCCESS"), valid.diagnostics())
	workspace.breakGeneratorChecksum()
	invalid := workspace.build(of("//api:buf.generate"))
	spec.Expect(invalid.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(invalid.diagnostics()).To(ContainSubstring("Checksum mismatch"))
}

// Generated source dependencies / Buf generates this repository's protobuf and Connect API for the real client build.
func TestBufGeneratesThisRepositorySProtobufAndConnectAPIForTheRealClientBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	// The contracts' Buf workspace generates the Go SDK's API into
	// sdk/go/api, which the Go SDK's catalog file says it does, and the
	// client is built from what the worker generated.
	workspace := spec.ownCheckout()
	built := workspace.build(of("//cli:go.compile"))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	spec.Expect(built.actions()).To(ContainElement("//api:buf.generate-sdk"))

	// The control: where the Go SDK does not say so, the generator is
	// refused its directory, by name, and nothing runs.
	consenting := workspace.file("sdk/go/catalog-info.yaml")
	silent, _, said := strings.Cut(consenting, "  frontseat:\n")
	spec.Expect(said).To(BeTrue(), "the Go SDK's catalog file says nothing of what generates it")
	workspace.write("sdk/go/catalog-info.yaml", silent)
	refused := workspace.build(of("//cli:go.compile"))
	spec.Expect(refused.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(refused.says()).To(SatisfyAll(ContainSubstring("//api:buf.generate-sdk generates sdk/go/api"), ContainSubstring("does not say"), ContainSubstring("spec.frontseat.generated")))
	spec.Expect(refused.actions()).To(BeEmpty())
}

// Generated source dependencies / Buf-generated Go reaches its module and a downstream consumer without existing generated files.
func TestBufGeneratedGoReachesItsModuleAndADownstreamConsumerWithoutExistingGeneratedFiles(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	spec.Expect(workspace.has("api/gen/go/hello/v1/hello.pb.go")).To(BeFalse())
	before := workspace.file("api/gen/go/keep.go")
	module := workspace.build(of("//api/gen/go:go.compile"))
	spec.Expect(module.outcome()).To(Equal("SUCCESS"), module.diagnostics())
	spec.Expect(module.actions()).To(ContainElement("//api:buf.generate"))
	consumer := workspace.build(of("//app:go.compile"))
	spec.Expect(consumer.outcome()).To(Equal("SUCCESS"), consumer.diagnostics())
	spec.Expect(consumer.actions()).To(ContainElement("//api:buf.generate"))
	spec.Expect(workspace.has("api/gen/go/hello/v1/hello.pb.go")).To(BeFalse())
	spec.Expect(workspace.file("api/gen/go/keep.go")).To(Equal(before))
	spec.Expect(spec.fetches(consumer)).NotTo(BeEmpty())
	for _, fetch := range spec.fetches(consumer) {
		spec.Expect(consumer.needsOf(fetch)).To(BeEmpty(), fetch)
	}
}

// Generated source dependencies / Stale generated files cannot inject imports or embed directives into analysis or a downstream build.
func TestStaleGeneratedFilesCannotInjectImportsOrEmbedDirectivesIntoAnalysisOrADownstreamBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	const stale = "package hello\nimport _ \"example.com/does-not-exist\"\nimport _ \"embed\"\n//go:embed missing-secret.txt\nvar secret string"
	workspace.write("api/gen/go/hello/v1/hello.pb.go", stale)
	built := workspace.build(of("//app:go.compile"))
	// The copy is out of step, which the generator's action says; the
	// consumer is built from what the worker generated all the same.
	spec.Expect(built.outcomeOf("//api:buf.generate")).To(Equal("rewrite"))
	spec.Expect(built.outcomeOf("//app:go.compile")).To(Equal("passed"), built.diagnostics())
	spec.Expect(workspace.file("api/gen/go/hello/v1/hello.pb.go")).To(Equal(stale))
	// A new snapshot must not revive a cached analysis of a generated file.
	workspace.write("api/gen/go/hello/v1/hello.pb.go", "this is not Go")
	again := workspace.build(of("//app:go.compile"))
	spec.Expect(again.outcomeOf("//app:go.compile")).To(Equal("passed"), again.diagnostics())
}

// Generated source dependencies / A changed protobuf invalidates generation and a deleted message cannot return from stale source.
func TestAChangedProtobufInvalidatesGenerationAndADeletedMessageCannotReturnFromStaleSource(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	first := workspace.build(of("//app:go.compile"))
	spec.Expect(first.outcome()).To(Equal("SUCCESS"), first.diagnostics())
	workspace.write("api/hello/v1/hello.proto", "syntax = \"proto3\";\npackage hello.v1;\noption go_package = \"example.com/generated/hello/v1\";\nmessage Goodbye { string message = 1; }")
	workspace.write("api/gen/go/hello/v1/hello.pb.go", "package hello\ntype Hello struct { Message string }")
	failed := workspace.build(of("//app:go.compile"))
	spec.Expect(failed.outcome()).NotTo(Equal("SUCCESS"))
	workspace.write("app/main.go", "package main\n\nimport hello \"example.com/generated/hello/v1\"\n\nfunc main() { _ = hello.Goodbye{Message: \"generated\"} }")
	repaired := workspace.build(of("//app:go.compile"))
	spec.Expect(repaired.outcomeOf("//app:go.compile")).To(Equal("passed"), repaired.diagnostics())
}

// Generated source dependencies / A copy of generated source the checkout holds is kept in step with what the worker generated: out of step, the build fails as a rewrite, and a run asked to fix writes the worker's output.
func TestACopyOfGeneratedSourceTheCheckoutHoldsIsKeptInStepWithWhatTheWorkerGeneratedOutOfStepTheBuildFailsAsARewriteAndARunAskedToFixWritesTheWorkerSOutput(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	const stale = "package hello\n\ntype Hello struct{ Message string }"
	workspace.write("api/gen/go/hello/v1/hello.pb.go", stale)
	built := workspace.build(of("//api:buf.generate"))
	spec.Expect(built.outcome()).To(Equal("BUILD_FAILURE"))
	spec.Expect(built.outcomeOf("//api:buf.generate")).To(Equal("rewrite"))
	spec.Expect(built.saysOf("//api:buf.generate")).To(SatisfyAll(ContainSubstring("hello.pb.go"), ContainSubstring("--fix")))
	spec.Expect(workspace.file("api/gen/go/hello/v1/hello.pb.go")).To(Equal(stale), "a run not asked to fix wrote the checkout")
	fixed := workspace.build(of("//api:buf.generate"), fixing())
	spec.Expect(fixed.outcome()).To(Equal("SUCCESS"), fixed.diagnostics())
	spec.Expect(fixed.patched()).To(ConsistOf("api/gen/go/hello/v1/hello.pb.go"))
	spec.Expect(workspace.file("api/gen/go/hello/v1/hello.pb.go")).To(ContainSubstring("Code generated by protoc-gen-go"))
	// In step, there is nothing to write, and the copy is left as it is.
	again := workspace.build(of("//api:buf.generate"))
	spec.Expect(again.outcome()).To(Equal("SUCCESS"), again.diagnostics())
	spec.Expect(again.rewritesOf("//api:buf.generate")).To(BeEmpty())
}

// Generated source dependencies / A generated file the checkout holds and the generator no longer produces is deleted by a run asked to fix; a handwritten file beside it is left.
func TestAGeneratedFileTheCheckoutHoldsAndTheGeneratorNoLongerProducesIsDeletedByARunAskedToFixAHandwrittenFileBesideItIsLeft(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	workspace.write("api/gen/go/hello/v1/old.pb.go", "package hello\n")
	workspace.write("api/gen/go/hello/v1/doc.go", "// Package hello is written by hand.\npackage hello")
	built := workspace.build(of("//api:buf.generate"))
	spec.Expect(built.outcomeOf("//api:buf.generate")).To(Equal("rewrite"))
	spec.Expect(built.saysOf("//api:buf.generate")).To(ContainSubstring("old.pb.go"))
	fixed := workspace.build(of("//api:buf.generate"), fixing())
	spec.Expect(fixed.outcome()).To(Equal("SUCCESS"), fixed.diagnostics())
	spec.Expect(workspace.has("api/gen/go/hello/v1/old.pb.go")).To(BeFalse())
	spec.Expect(workspace.has("api/gen/go/hello/v1/doc.go")).To(BeTrue())
	spec.Expect(workspace.has("api/gen/go/keep.go")).To(BeTrue())
}

// Generated source dependencies / A protobuf file buf would format fails the format check, names the file, and is left as it was.
func TestAProtobufFileBufWouldFormatFailsTheFormatCheckNamesTheFileAndIsLeftAsItWas(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedGo()
	const unformatted = "syntax = \"proto3\";\npackage hello.v1;\noption go_package = \"example.com/generated/hello/v1\";\nmessage Hello {string message=1;}"
	workspace.write("api/hello/v1/hello.proto", unformatted)
	built := workspace.build(of("//api:buf.format"))
	spec.Expect(built.outcomeOf("//api:buf.format")).To(Equal("failed"))
	found := built.diagnosticsOf("//api:buf.format")
	spec.Expect(found).To(HaveLen(1))
	spec.Expect(found[0].GetRuleId()).To(Equal("buf/format"))
	spec.Expect(found[0].GetSourceLocation().GetPath()).To(Equal("api/hello/v1/hello.proto"))
	spec.Expect(workspace.file("api/hello/v1/hello.proto")).To(Equal(unformatted))
}

// Generated source dependencies / A generator writes into another Component's module that says it does: the module and its consumer are built from what the worker generated, and the checkout is left as it was.
func TestAGeneratorWritesIntoAnotherComponentsModuleThatSaysItDoes(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedElsewhere()
	module := workspace.build(of("//lib:go.compile"))
	spec.Expect(module.outcome()).To(Equal("SUCCESS"), module.diagnostics())
	spec.Expect(module.actions()).To(ContainElement("//api:buf.generate-lib"))
	built := workspace.build(of("//app:go.compile"))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	spec.Expect(built.actions()).To(ContainElement("//api:buf.generate-lib"))
	spec.Expect(workspace.has("lib/gen/hello/v1/hello.pb.go")).To(BeFalse())
	spec.Expect(workspace.has("api/.gen-lib")).To(BeFalse())
}

// Generated source dependencies / A copy the checkout holds in another Component's module is kept in step: out of step, the build fails as a rewrite naming it, and a run asked to fix writes it there.
func TestACopyOfGeneratedSourceInAnotherComponentIsKeptInStepAndARunAskedToFixWritesIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedElsewhere()
	const stale = "package hello\n\ntype Hello struct{ Message string }"
	workspace.write("lib/gen/hello/v1/hello.pb.go", stale)
	built := workspace.build(of("//api:buf.generate-lib"))
	spec.Expect(built.outcome()).To(Equal("BUILD_FAILURE"))
	spec.Expect(built.outcomeOf("//api:buf.generate-lib")).To(Equal("rewrite"))
	spec.Expect(built.saysOf("//api:buf.generate-lib")).To(SatisfyAll(ContainSubstring("lib/gen/hello/v1/hello.pb.go"), ContainSubstring("--fix")))
	spec.Expect(workspace.file("lib/gen/hello/v1/hello.pb.go")).To(Equal(stale), "a run not asked to fix wrote the checkout")
	fixed := workspace.build(of("//api:buf.generate-lib"), fixing())
	spec.Expect(fixed.outcome()).To(Equal("SUCCESS"), fixed.diagnostics())
	spec.Expect(fixed.patched()).To(ConsistOf("lib/gen/hello/v1/hello.pb.go"))
	spec.Expect(workspace.file("lib/gen/hello/v1/hello.pb.go")).To(ContainSubstring("Code generated by protoc-gen-go"))
	spec.Expect(workspace.has("api/.gen-lib")).To(BeFalse())
}

// refusedGeneration builds the consumer of what api generates, fixing,
// over a stale copy of the generated file in lib, and expects the build
// refused before any action ran, saying each of what is given, and the
// stale copy left as it was.
func refusedGeneration(spec *scenario, workspace *Workspace, says ...string) {
	spec.Helper()
	const stale = "package hello\n\ntype Hello struct{ Message string }"
	workspace.write("lib/gen/hello/v1/hello.pb.go", stale)
	refused := workspace.build(of("//app:go.compile"), fixing())
	spec.Expect(refused.outcome()).NotTo(Equal("SUCCESS"))
	for _, each := range says {
		spec.Expect(refused.says()).To(ContainSubstring(each))
	}
	spec.Expect(refused.actions()).To(BeEmpty())
	spec.Expect(workspace.file("lib/gen/hello/v1/hello.pb.go")).To(Equal(stale))
}

// Generated source dependencies / A generator is refused, by name, a directory outside its project whose Component does not say it generates it.
func TestAGeneratorIsRefusedADirectoryWhoseComponentDoesNotSayItGeneratesIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedElsewhere()
	workspace.write("lib/catalog-info.yaml", testforge.ConsentingComponent("lib", ""))
	refusedGeneration(spec, workspace, "//api:buf.generate-lib generates lib/gen", "component:default/lib does not say", "spec.frontseat.generated")
}

// Generated source dependencies / A generator is refused a directory whose Component says another Component generates it.
func TestAGeneratorIsRefusedADirectoryWhoseComponentSaysAnotherGeneratesIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedElsewhere()
	workspace.write("other/catalog-info.yaml", testforge.ConsentingComponent("other", ""))
	workspace.write("lib/catalog-info.yaml", testforge.ConsentingComponent("lib", "    generated:\n      - directory: gen\n        by: component:other\n"))
	refusedGeneration(spec, workspace, "//api:buf.generate-lib generates lib/gen", "component:default/lib does not say that component:default/api generates it")
}

// Generated source dependencies / A Component that says a System generates its directory is refused by name: only a Component generates.
func TestAComponentThatSaysASystemGeneratesItsDirectoryIsRefused(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedElsewhere()
	workspace.write("lib/catalog-info.yaml", testforge.ConsentingComponent("lib", "    generated:\n      - directory: gen\n        by: system:api\n"))
	refusedGeneration(spec, workspace, "lib/catalog-info.yaml", "by names system:default/api", "a Component")
}

// Generated source dependencies / A generator that writes outside the repository is refused by the Buf plugin.
func TestAGeneratorThatWritesOutsideTheRepositoryIsRefused(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedElsewhere()
	workspace.write("api/buf.gen.lib.yaml", "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: ../../outside\n    opt: paths=source_relative\ninputs:\n  - directory: .\n")
	refusedGeneration(spec, workspace, "api/buf.gen.lib.yaml", "outside the repository")
}

// Generated source dependencies / A Component cannot say a generator writes into a directory of another Component beneath it.
func TestAComponentCannotSayAGeneratorWritesIntoAnotherComponentsDirectory(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.generatedElsewhere()
	workspace.write("api/buf.gen.lib.yaml", "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: ../lib/inner/gen\n    opt: paths=source_relative\ninputs:\n  - directory: .\n")
	workspace.write("lib/inner/catalog-info.yaml", testforge.ConsentingComponent("inner", ""))
	workspace.write("lib/catalog-info.yaml", testforge.ConsentingComponent("lib", "    generated:\n      - directory: inner/gen\n        by: component:api\n"))
	refusedGeneration(spec, workspace, "lib/catalog-info.yaml", "lib/inner/gen", "component:default/inner")
}
