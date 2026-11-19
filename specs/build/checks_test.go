package build_test

import (
	"crypto/sha256"
	"encoding/hex"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	. "github.com/onsi/gomega"
)

// A check is an action that checks the source and writes nothing. On the
// grid, and so in CI, it fails on what it finds: by its exit code, or by
// the findings its plugin reads from what it printed, each a rule, a
// file and a line, so that no client parses it. A check that can be put
// right has a fix, gofmt -w where the check is gofmt -l, and a fix never
// runs on the grid: `frontseat build --fix` runs it where the checkout
// is, in the daemon's own sandbox, over a copy of the check's inputs and
// with the same pinned tool the check runs with, and writes each change
// over the content the run read and nothing else. The check then runs on
// the grid over what was written. A file that changed since the run read
// it, two fixes that would change one file, and a change outside what
// the check was given are refused by name.
// Checks, their findings and what they would rewrite / A check whose tool would change a file fails, names the file, and leaves the source as it was.
func TestACheckWhoseToolWouldChangeAFileFailsNamesTheFileAndLeavesTheSourceAsItWas(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	const unformatted = "package main\n\nimport \"fmt\"\n\nfunc main() {\nfmt.Println(  \"hello\" )\n}\n"
	formatted := func(source string) string {
		out, err := format.Source([]byte(source))
		spec.Expect(err).NotTo(HaveOccurred())
		return string(out)
	}

	workspace := spec.embeddedModule("app")
	workspace.write("app/main.go", unformatted)
	build := workspace.build(of("./app"), only("format"))
	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
	spec.Expect(build.outcomeOf("//app:go.format")).To(Equal("failed"))
	found := build.diagnosticsOf("//app:go.format")
	spec.Expect(found).To(HaveLen(1), build.printed("//app:go.format"))
	spec.Expect(found[0].GetRuleId()).To(Equal("go/format"))
	spec.Expect(found[0].GetSourceLocation().GetPath()).To(Equal("app/main.go"))
	spec.Expect(build.saysOf("//app:go.format")).To(ContainSubstring("--fix"))
	spec.Expect(build.rewritesOf("//app:go.format")).To(BeEmpty(), "a check on the grid proposed a change")
	spec.Expect(workspace.file("app/main.go")).To(Equal(strings.TrimSpace(unformatted)), "a check wrote the source")
	// A file as the tool would have it passes, with no finding. (write
	// ends a file with a newline, as gofmt does: it is given none.)
	workspace.write("app/main.go", strings.TrimSuffix(formatted(unformatted), "\n"))
	passed := workspace.build(of("./app"), only("format"))
	spec.Expect(passed.outcome()).To(Equal("SUCCESS"), passed.diagnostics())
	spec.Expect(passed.diagnosticsOf("//app:go.format")).To(BeEmpty())
}

// Checks, their findings and what they would rewrite / What a check finds is reported as diagnostics, each a rule, a file and a line, and a check that finds nothing reports none.
func TestWhatACheckFindsIsReportedAsDiagnosticsEachARuleAFileAndALineAndACheckThatFindsNothingReportsNone(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("app")
	workspace.write("app/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Printf(\"%d\\n\", \"not a number\")\n}\n")
	build := workspace.build(of("./app"), only("lint"))
	spec.Expect(build.outcomeOf("//app:go.lint")).To(Equal("failed"))
	found := build.diagnosticsOf("//app:go.lint")
	spec.Expect(found).To(HaveLen(1), build.printed("//app:go.lint"))
	spec.Expect(found[0].GetRuleId()).To(Equal("go/vet"))
	spec.Expect(found[0].GetSourceLocation().GetPath()).To(Equal("app/main.go"))
	spec.Expect(found[0].GetSourceLocation().GetLine()).To(Equal(int32(6)))
	spec.Expect(found[0].GetMessage()).To(ContainSubstring("Printf"))
	workspace.write("app/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Printf(\"%d\\n\", 1)\n}\n")
	clean := workspace.build(of("./app"), only("lint"))
	spec.Expect(clean.outcomeOf("//app:go.lint")).To(Equal("passed"))
	spec.Expect(clean.diagnosticsOf("//app:go.lint")).To(BeEmpty())
}

// Checks, their findings and what they would rewrite / An entity's own check may have a fix that deletes a file and makes one executable, and a run asked to fix does as it says.
func TestAnEntitySOwnCheckMayHaveAFixThatDeletesAFileAndMakesOneExecutableAndARunAskedToFixDoesAsItSays(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.checkingEntity("tidy", []string{"sh", "-c", "test ! -e gone.txt && test -x run.sh"}, []string{"sh", "-c", "rm gone.txt && chmod +x run.sh"})
	workspace.write("gone.txt", "to go\n")
	workspace.write("run.sh", "#!/bin/sh\necho hi\n")
	seen := workspace.build(of("//:custom.tidy"))
	spec.Expect(seen.outcomeOf("//:custom.tidy")).To(Equal("failed"))
	spec.Expect(workspace.has("gone.txt")).To(BeTrue(), "a run not asked to fix deleted the file")
	fixed := workspace.build(of("//:custom.tidy"), fixing())
	spec.Expect(fixed.outcome()).To(Equal("SUCCESS"), fixed.diagnostics())
	kinds := map[string]buildv1.Rewrite_Kind{}
	for _, rewrite := range fixed.rewritesOf("//:custom.tidy") {
		spec.Expect(rewrite.GetApplied()).To(BeTrue())
		kinds[rewrite.GetLocation().GetPath()] = rewrite.GetKind()
	}
	spec.Expect(kinds).To(Equal(map[string]buildv1.Rewrite_Kind{"gone.txt": buildv1.Rewrite_KIND_DELETE, "run.sh": buildv1.Rewrite_KIND_REPLACE}))
	spec.Expect(workspace.has("gone.txt")).To(BeFalse())
	spec.Expect(workspace.mode("run.sh")&0o100).NotTo(BeZero(), "run.sh is not executable")
}

// Checks, their findings and what they would rewrite / A file changed since the run read it is not written over, and is named.
func TestAFileChangedSinceTheRunReadItIsNotWrittenOverAndIsNamed(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	// The fix says when it has begun, by which time the run has read
	// its copy, and waits to be let on: the file is changed in between.
	marks := spec.TB.TempDir()
	begun, let := filepath.Join(marks, "begun"), filepath.Join(marks, "let")
	workspace := spec.checkingEntity("slow", []string{"sh", "-c", "grep -q changed f.txt"}, []string{"sh", "-c", "touch " + begun + " && while [ ! -e " + let + " ]; do sleep 0.1; done && echo changed > f.txt"})
	workspace.write("f.txt", "as read\n")
	build := workspace.start(of("//:custom.slow"), fixing())
	spec.Eventually(func() bool { _, err := os.Stat(begun); return err == nil }, time.Minute, 50*time.Millisecond).Should(BeTrue())
	workspace.write("f.txt", "mine\n")
	spec.Expect(os.WriteFile(let, nil, 0o644)).To(Succeed())
	spec.Expect(build.ended().outcomeOf("//:custom.slow")).To(Equal("error"))
	spec.Expect(build.saysOf("//:custom.slow")).To(ContainSubstring("f.txt"))
	spec.Expect(workspace.file("f.txt")).To(Equal("mine"))
	// Left alone, the same fix's change is written, and the check passes over it.
	workspace.write("f.txt", "as read\n")
	spec.Expect(os.Remove(begun)).To(Succeed())
	again := workspace.build(of("//:custom.slow"), fixing())
	spec.Expect(again.outcome()).To(Equal("SUCCESS"), again.diagnostics())
	spec.Expect(workspace.file("f.txt")).To(Equal("changed"))
}

// Checks, their findings and what they would rewrite / Two fixes that would change one file are refused together, and neither is written.
func TestTwoFixesThatWouldChangeOneFileAreRefusedTogetherAndNeitherIsWritten(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.checkingEntity("one", []string{"true"}, []string{"sh", "-c", "echo one > f.txt"})
	workspace.write("f.txt", "as read\n")
	workspace.write("catalog-info.yaml", entityWithActions(map[string]ownCheck{
		"one": {check: []string{"true"}, fix: []string{"sh", "-c", "echo one > f.txt"}},
		"two": {check: []string{"true"}, fix: []string{"sh", "-c", "echo two > f.txt"}},
	}))
	build := workspace.build(fixing())
	spec.Expect(build.outcomeOf("//:custom.one")).To(Equal("error"))
	spec.Expect(build.outcomeOf("//:custom.two")).To(Equal("error"))
	spec.Expect(build.saysOf("//:custom.one")).To(ContainSubstring("//:custom.two"))
	spec.Expect(workspace.file("f.txt")).To(Equal("as read"))
}

// Checks, their findings and what they would rewrite / A nested module is checked as its own project, and not as its parent's files, whatever its directory is named.
func TestANestedModuleIsCheckedAsItsOwnProjectAndNotAsItsParentSFilesWhateverItsDirectoryIsNamed(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	// The nested module's directory is named as a pattern would read
	// something else: a name is never a pattern.
	workspace := spec.embeddedModule("app")
	workspace.write("app/main.go", "package main\n\nfunc main() {}")
	workspace.write("app/nest[1]/go.mod", "module example.com/nested\n\ngo 1.27\n")
	workspace.write("app/nest[1]/x.go", "package nested\nfunc  X() {}")
	workspace.write("app/nest1/y.go", "package nest1\nfunc  Y() {}")
	build := workspace.build(of("./app"), only("format"))
	spec.Expect(build.outcomeOf("//app/nest[1]:go.format")).To(Equal("failed"))
	spec.Expect(build.diagnosticsOf("//app/nest[1]:go.format")).To(HaveLen(1))
	spec.Expect(build.diagnosticsOf("//app/nest[1]:go.format")[0].GetSourceLocation().GetPath()).To(Equal("app/nest[1]/x.go"))
	// The parent's own files are its: nest1 is not the nested module.
	spec.Expect(build.outcomeOf("//app:go.format")).To(Equal("failed"))
	spec.Expect(build.diagnosticsOf("//app:go.format")).To(HaveLen(1))
	spec.Expect(build.diagnosticsOf("//app:go.format")[0].GetSourceLocation().GetPath()).To(Equal("app/nest1/y.go"))
	// The fix of each needs the workers' Go, which runs where the
	// daemon's platform is the workers': specs/sandbox proves it.
}

// Checks, their findings and what they would rewrite / An entity's own fix is held to its check: a fix of what is not a check, or that says nothing it may change, is refused by name.
func TestAnEntitySOwnFixIsHeldToItsCheckAFixOfWhatIsNotACheckOrThatSaysNothingItMayChangeIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.checkingEntity("sound", []string{"true"}, []string{"true"})
	workspace.write("catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: lab}\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      nocheck:\n        command: [\"true\"]\n        fix: [\"true\"]\n        inputs: [f.txt]\n        rewrites: [f.txt]\n")
	spec.Expect(workspace.build().says()).To(SatisfyAll(ContainSubstring("nocheck"), ContainSubstring("not a check")))
	workspace.write("catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: lab}\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      norewrites:\n        check: true\n        command: [\"true\"]\n        fix: [\"true\"]\n        inputs: [f.txt]\n")
	spec.Expect(workspace.build().says()).To(SatisfyAll(ContainSubstring("norewrites"), ContainSubstring("nothing it may change")))
	// Sound, the same check builds.
	workspace.write("catalog-info.yaml", entityWithActions(map[string]ownCheck{"sound": {check: []string{"true"}, fix: []string{"true"}}}))
	spec.Expect(workspace.build().outcome()).To(Equal("SUCCESS"))
}

// Checks, their findings and what they would rewrite / A fix that would write a file the repository ignores is refused by name, and the file is not written.
func TestAFixThatWouldWriteAFileTheRepositoryIgnoresIsRefusedByNameAndTheFileIsNotWritten(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.checkingEntity("hidden", []string{"true"}, []string{"sh", "-c", "echo made > hidden.txt"})
	workspace.write(".frontseatignore", "hidden.txt\n")
	refused := workspace.build(of("//:custom.hidden"), fixing())
	spec.Expect(refused.outcomeOf("//:custom.hidden")).To(Equal("error"))
	spec.Expect(refused.saysOf("//:custom.hidden")).To(SatisfyAll(ContainSubstring("hidden.txt"), ContainSubstring("ignores")))
	spec.Expect(workspace.has("hidden.txt")).To(BeFalse(), "the ignored file was written")
	// Not ignored, the same fix's file is written.
	workspace.write(".frontseatignore", "# nothing\n")
	written := workspace.build(of("//:custom.hidden"), fixing())
	spec.Expect(written.outcome()).To(Equal("SUCCESS"), written.diagnostics())
	spec.Expect(workspace.file("hidden.txt")).To(Equal("made"))
}

// Checks, their findings and what they would rewrite / A rewrite pattern that reaches outside what the check is given is refused by name.
func TestARewritePatternThatReachesOutsideWhatTheCheckIsGivenIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.checkingEntity("escape", []string{"true"}, []string{"true"})
	workspace.write("catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: lab}\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      escape:\n        check: true\n        command: [\"true\"]\n        fix: [\"true\"]\n        inputs: [f.txt]\n        rewrites: [../other.txt]\n")
	spec.Expect(workspace.build().says()).To(ContainSubstring("../other.txt"))
}

// digestOf is the SHA-256 of content, in hex, as a run names it.
func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ownCheck is an entity's own check: its command, and its fix.
type ownCheck struct {
	check, fix []string
}

// entityWithActions is a catalog file of a component at the repository's
// root with the checks given, each reading and allowed to change every
// text and shell file beside it.
func entityWithActions(checks map[string]ownCheck) string {
	out := "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: lab}\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n"
	quoted := func(words []string) string {
		var parts []string
		for _, word := range words {
			parts = append(parts, "\""+word+"\"")
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	for name, check := range checks {
		out += "      " + name + ":\n        check: true\n        command: " + quoted(check.check) + "\n        fix: " + quoted(check.fix) + "\n        inputs: [\"*.txt\", \"*.sh\"]\n        rewrites: [\"*.txt\", \"*.sh\"]\n"
	}
	return out
}

// mode is a file's permissions in the workspace.
func (w *Workspace) mode(name string) os.FileMode {
	w.scenario.Helper()
	info, err := os.Stat(filepath.Join(w.directory, filepath.FromSlash(name)))
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return info.Mode().Perm()
}

// rewritesOf are the changes an action proposed.
func (b *Build) rewritesOf(label string) []*buildv1.Rewrite {
	b.scenario.Helper()
	return b.result(label).GetRewrites()
}

// diagnosticsOf are what a check found.
func (b *Build) diagnosticsOf(label string) []*typev1.Diagnostic {
	b.scenario.Helper()
	return b.result(label).GetDiagnostics()
}

// patched are the files the run wrote to the checkout, as its events say.
func (b *Build) patched() []string {
	b.scenario.Helper()
	var paths []string
	for _, event := range b.events(0) {
		if patched := event.GetPatched(); patched != nil {
			for _, path := range patched.GetPaths() {
				paths = append(paths, filepath.Join(patched.GetRepository(), path))
			}
		}
	}
	return paths
}
