package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// bringing is a grid that leaves files, and brings one where the daemon
// is: its content says which it is.
type bringing struct{ leaving }

func (bringing) Materialize(_ context.Context, held exec.Held, into string) error {
	return os.WriteFile(into, []byte("the file "+held.Digest.Hash[60:]+"\n"), 0o644)
}

// publisher is a tool that publishes: it writes down, in a file named for
// the tag it is told, how it was run, what it was given of where the
// daemon runs, and the files it finds beside it; and fails where it is
// told to, printing its token.
const publisher = `out="$1/$(echo "$2" | tr / _)"; tag="$2"; notes="$4"; shift 2; { echo "$tag"; for each in "$@"; do echo "${each##*/}"; done; echo "token=${RELEASE_TOKEN-unset} other=${OTHER_TOKEN-unset} home=${HOME##*/}"; echo "notes=$(cat "$notes")"; ls; cat checksums.txt; } > "$out"; if [ -n "$FAILS" ]; then echo "refused $RELEASE_TOKEN"; exit 3; fi`

// publishingSource writes, into a remote of releasingRemote, greet
// with what it ships and an action that publishes it, into a directory.
func publishingSource(t *testing.T, into string, fails bool, atRoot bool) func(remote string) {
	failing := ""
	if fails {
		failing = "        inherit-environment: [RELEASE_TOKEN, OTHER_TOKEN, FAILS]\n"
	}
	publish := "      release:\n        command: [sh, -c, '" + publisher + "', publish, " + into + ", '{frontseat.tag}', '{frontseat.version}', '{frontseat.notes}', '{frontseat.remote}', 'prerelease={frontseat.prerelease}', '{frontseat.artifacts}']\n        publish: true\n"
	if !fails {
		publish += "        inherit-environment: [RELEASE_TOKEN, OTHER_TOKEN]\n"
	}
	publish += failing
	return func(remote string) {
		t.Helper()
		own := publish
		if atRoot {
			own = ""
			write(t, remote, "catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: both\nspec:\n  owner: team\n  frontseat:\n    release:\n      grouping: independent\n    actions:\n"+publish)
		}
		for _, name := range []string{"greet", "log"} {
			system := ""
			if atRoot {
				system = "  system: both\n"
			}
			actions := "    actions:\n      pack:\n        command: [tool, pack]\n        outputs: [out/" + name + "-linux-amd64, out/" + name + ".tgz]\n        package: true\n"
			if name == "greet" {
				actions += own
			}
			write(t, remote, name+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+name+"\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n"+system+"  frontseat:\n"+actions)
		}
	}
}

func readAt(t *testing.T, into, name string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(into, name))
	if err != nil {
		return ""
	}
	return string(got)
}

// A release publishes what it shipped: once its tags are pushed, the
// action of its unit that publishes is run where the daemon is, told
// the release, given the unit's artifacts with their sums beside them,
// and given what it names of where the daemon runs that the daemon was
// told it may be: one it names that the daemon was not told is not
// given, nor anything else of the daemon's. No build runs it, and
// pointed at, it is refused by name.
func TestAReleasePublishesWhatItShippedWhereTheDaemonIs(t *testing.T) {
	into := t.TempDir()
	t.Setenv("RELEASE_TOKEN", "a-token-of-this-tests")
	t.Setenv("OTHER_TOKEN", "another-of-this-tests")
	c, clone, remote := givenACheckoutWith(t, bringing{}, "release/1.0", publishingSource(t, into, false, false), 3)
	c.daemon.releases.graph.runs.given = []string{"RELEASE_TOKEN"}
	// A build is of everything, and publishes nothing.
	if ended := wait(t, c.runs, create(t, c.runs).GetId()); ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || readAt(t, into, "greet_v1.0.0") != "" {
		t.Fatalf("a build ended %s, and published %q", ended.GetExitCode(), readAt(t, into, "greet_v1.0.0"))
	}
	refusal := ""
	if pointed, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{Targets: []string{"//greet:custom.publish/release"}}); err != nil {
		refusal = err.Error()
	} else if ended := wait(t, c.runs, pointed.GetBuild().GetId()); ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		refusal = ended.GetFailure()
	}
	if !strings.Contains(refusal, "publishes, which no build runs") || readAt(t, into, "greet_v1.0.0") != "" {
		t.Errorf("a build pointed at what publishes says %q, and published %q", refusal, readAt(t, into, "greet_v1.0.0"))
	}
	s, release := releasedAlone(t, c, "greet")
	published := release.GetUnits()[0].GetPublishes()
	if len(published) != 1 || published[0].GetLabel() != "//greet:custom.publish/release" || published[0].GetFailure() != "" {
		t.Errorf("the unit says it was published as %v", published)
	}
	got := strings.Split(strings.TrimSpace(readAt(t, into, "greet_v1.0.0")), "\n")
	want := []string{
		// How it was run: the release, then each artifact, by its name.
		"greet/v1.0.0", "1.0.0", "notes.md", remoteOf(t, clone)[strings.LastIndex(remoteOf(t, clone), "/")+1:], "prerelease=false", "checksums.txt", "greet-linux-amd64", "greet.tgz",
		// What it was given of where the daemon runs.
		"token=a-token-of-this-tests other=unset home=home",
	}
	if len(got) < len(want)+1 || strings.Join(got[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Fatalf("it was run as\n%s\nwant it to begin\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	rest := strings.Join(got[len(want):], "\n")
	// The notes, the files beside it and nothing else, and their sums.
	if !strings.HasPrefix(rest, "notes=") || !strings.Contains(rest, "\nchecksums.txt\ngreet-linux-amd64\ngreet.tgz\n") || strings.Count(rest, "  greet") != 2 || strings.Contains(rest, "log") {
		t.Errorf("it found beside it, and in the sums:\n%s", rest)
	}
	for _, artifact := range release.GetUnits()[0].GetArtifacts() {
		if !strings.Contains(rest, artifact.GetDigest().GetHash()+"  "+artifact.GetName()) {
			t.Errorf("the sums do not hold %s as the release lists it:\n%s", artifact.GetName(), rest)
		}
	}
	if tagsOf(t, remote) != "greet/v1.0.0" {
		t.Errorf("the remote has the tags %q", tagsOf(t, remote))
	}
	// log has nothing that publishes: its release is as it ever was.
	if plain := releaseEnded(t, s, releaseBegun(t, s, "log").GetId()); plain.GetState() != buildv1.Release_STATE_SUCCEEDED || len(plain.GetUnits()[0].GetPublishes()) != 0 || readAt(t, into, "log_v1.0.0") != "" {
		t.Errorf("a release of a unit with nothing that publishes ended %v (%s), published %v", plain.GetState(), plain.GetFailure(), plain.GetUnits()[0].GetPublishes())
	}
}

// A publish that fails fails its release, which says which on the unit
// with what the tool printed, whatever it was given of where the daemon
// runs left out; the tags stay pushed.
func TestAPublishThatFailsFailsTheReleaseAndItsTagsStay(t *testing.T) {
	into := t.TempDir()
	t.Setenv("RELEASE_TOKEN", "a-token-of-this-tests")
	t.Setenv("FAILS", "yes")
	c, _, remote := givenACheckoutWith(t, bringing{}, "release/1.0", publishingSource(t, into, true, false), 3)
	c.daemon.releases.graph.runs.given = []string{"RELEASE_TOKEN", "FAILS"}
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_FAILED || !strings.Contains(release.GetFailure(), "//greet:custom.publish/release of greet could not be published; every tag is pushed") {
		t.Fatalf("the release ended %v (%s)", release.GetState(), release.GetFailure())
	}
	published := release.GetUnits()[0].GetPublishes()
	if len(published) != 1 || !strings.Contains(published[0].GetFailure(), "exit status 3") || !strings.Contains(published[0].GetFailure(), "refused …") {
		t.Errorf("the unit says it was published as %v", published)
	}
	if whole := release.String(); strings.Contains(whole, "a-token-of-this-tests") {
		t.Errorf("the release repeats the token: %s", whole)
	}
	if tagsOf(t, remote) != "greet/v1.0.0" {
		t.Errorf("the remote has the tags %q, want the release's tag still", tagsOf(t, remote))
	}
}

// What publishes and is owned by no entity, as at a repository's root,
// publishes each unit of its repository that moves, one at a time, each
// with its own artifacts and under its own tag.
func TestAPublishNoEntityOwnsPublishesEachUnitOfItsRepository(t *testing.T) {
	into := t.TempDir()
	c, _, _ := givenACheckoutWith(t, bringing{}, "release/1.0", publishingSource(t, into, false, true), 3)
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	release := releaseEnded(t, s, releaseBegun(t, s, "greet", "log").GetId())
	if release.GetState() != buildv1.Release_STATE_SUCCEEDED || len(release.GetUnits()) != 2 {
		t.Fatalf("the release ended %v (%s) with %v", release.GetState(), release.GetFailure(), release.GetUnits())
	}
	for _, name := range []string{"greet", "log"} {
		other := map[string]string{"greet": "log", "log": "greet"}[name]
		got := readAt(t, into, name+"_v1.0.0")
		if !strings.HasPrefix(got, name+"/v1.0.0\n") || !strings.Contains(got, name+".tgz") || strings.Contains(got, other+".tgz") {
			t.Errorf("%s was published as\n%s", name, got)
		}
	}
	for _, unit := range release.GetUnits() {
		if len(unit.GetPublishes()) != 1 || unit.GetPublishes()[0].GetLabel() != "//:custom.publish/release" || unit.GetPublishes()[0].GetFailure() != "" {
			t.Errorf("%s says it was published as %v", unit.GetEntity().GetName(), unit.GetPublishes())
		}
	}
}

// A package that leaves what a publish could not be given, as a file
// named for the file of sums, fails the release before anything is
// tagged, where something publishes. The control is the same package
// where nothing does: it ships.
func TestAnArtifactAPublishCannotBeGivenFailsTheReleaseBeforeItTags(t *testing.T) {
	source := func(publishes string) func(remote string) {
		return func(remote string) {
			write(t, remote, "greet/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: greet\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      pack:\n        command: [tool, pack]\n        outputs: [out/checksums.txt]\n        package: true\n"+publishes)
		}
	}
	c, clone, _ := givenACheckoutWith(t, bringing{}, "release/1.0", source("      release:\n        command: [sh, -c, 'exit 0']\n        publish: true\n"), 3)
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_FAILED || !strings.Contains(release.GetFailure(), "//greet:custom.pack left what cannot be published: an artifact is named checksums.txt") || !strings.Contains(release.GetFailure(), "nothing is tagged") || tagsOf(t, clone) != "" {
		t.Errorf("the release ended %v (%s) with the tags %q", release.GetState(), release.GetFailure(), tagsOf(t, clone))
	}
	control, _, _ := givenACheckoutWith(t, bringing{}, "release/1.0", source(""), 3)
	if _, shipped := releasedAlone(t, control, "greet"); len(shipped.GetUnits()[0].GetArtifacts()) != 1 {
		t.Errorf("the control shipped %v", shipped.GetUnits()[0].GetArtifacts())
	}
}

// The GitHub plugin's publish is run by a release for the unit it
// tagged, as any action that publishes is: a repository that lists the
// plugin and pins GitHub's command line is published with it. The grid
// of this test holds no tool, so the publish fails for want of gh, on
// the unit, and the tag stays pushed. No build runs it. A repository
// that lists the plugin and pins no gh is refused before anything is
// built or tagged; the control is the repository without the plugin.
func TestTheGitHubPluginsPublishIsRunByARelease(t *testing.T) {
	listing := func(plugins string, pinned bool) func(remote string) {
		return func(remote string) {
			if pinned {
				testpins.Own(t, remote)
			}
			write(t, remote, "frontseat.yaml", "plugins:\n  go: {}\n  backstage: {}\n  release: {}\n"+plugins)
		}
	}
	c, _, remote := givenACheckoutWith(t, bringing{}, "release/1.0", listing("  github: {}\n", true), 4)
	if ended := wait(t, c.runs, create(t, c.runs).GetId()); ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Fatalf("a build ended %s (%s)", ended.GetExitCode(), ended.GetFailure())
	}
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_FAILED || len(release.GetUnits()) != 1 {
		t.Fatalf("the release ended %v (%s) with %v", release.GetState(), release.GetFailure(), release.GetUnits())
	}
	published := release.GetUnits()[0].GetPublishes()
	if len(published) != 1 || published[0].GetLabel() != "//:github.publish" || !strings.Contains(published[0].GetFailure(), "gh") {
		t.Errorf("the unit says it was published as %v", published)
	}
	if tagsOf(t, remote) != "greet/v1.0.0" {
		t.Errorf("the remote has the tags %q", tagsOf(t, remote))
	}
	unpinned, bare, _ := givenACheckoutWith(t, bringing{}, "release/1.0", listing("  github: {}\n", false), 4)
	if ended := wait(t, unpinned.runs, create(t, unpinned.runs).GetId()); ended.GetExitCode() == buildv1.ExitCode_EXIT_CODE_SUCCESS || !strings.Contains(ended.GetFailure(), "pins no gh") || tagsOf(t, bare) != "" {
		t.Errorf("a build of a repository that pins no gh ended %s (%s), with the tags %q", ended.GetExitCode(), ended.GetFailure(), tagsOf(t, bare))
	}
	control, _, _ := givenACheckoutWith(t, bringing{}, "release/1.0", listing("", false), 3)
	if _, plain := releasedAlone(t, control, "greet"); len(plain.GetUnits()[0].GetPublishes()) != 0 {
		t.Errorf("without the plugin the unit says it was published as %v", plain.GetUnits()[0].GetPublishes())
	}
}

// A release from a workspace whose plan something publishes is refused
// by name, before anything is built or tagged: what publishes runs where
// the daemon is, a workspace's sandbox, which reaches no forge. A release
// there of a unit nothing publishes is still made, and so is the first
// release from a checkout given the daemon, which publishes: the
// controls.
func TestAReleaseFromAWorkspaceThatWouldPublishIsRefusedBeforeItBuilds(t *testing.T) {
	into := t.TempDir()
	c, clone := releasingClients(t, bringing{}, "release/1.0", publishingSource(t, into, false, false))
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	ctx := context.Background()
	planned, err := s.PlanRelease(ctx, releaseOf("greet"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateRelease(ctx, &buildv1.CreateReleaseRequest{Entities: releaseOf("greet").GetEntities(), Etag: planned.GetPlan().GetEtag()})
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeUnimplemented || reason != "PUBLISH_NOT_SERVED" || domain != refusal.Build || !strings.Contains(err.Error(), "//services/greet:custom.publish/release publishes") {
		t.Errorf("a release from a workspace that would publish: %v (%s, %s), want UNIMPLEMENTED, PUBLISH_NOT_SERVED, naming what publishes", err, reason, domain)
	}
	if listed, err := c.runs.ListBuilds(ctx, &buildv1.ListBuildsRequest{}); err != nil || len(listed.GetBuilds()) != 0 {
		t.Errorf("the refused release built %v (%v)", listed.GetBuilds(), err)
	}
	if tagsOf(t, clone) != "" || readAt(t, into, "greet_v1.0.0") != "" {
		t.Errorf("the refused release left the tags %q and published %q", tagsOf(t, clone), readAt(t, into, "greet_v1.0.0"))
	}
	if _, plain := releasedAlone(t, c, "log"); len(plain.GetUnits()[0].GetPublishes()) != 0 || tagsOf(t, remoteOf(t, clone)) != "log/v1.0.0" {
		t.Errorf("a release from the workspace of what nothing publishes was published as %v, with the tags %q", plain.GetUnits()[0].GetPublishes(), tagsOf(t, remoteOf(t, clone)))
	}
	given, _, _ := givenACheckoutWith(t, bringing{}, "release/1.0", publishingSource(t, into, false, false), 3)
	if _, published := releasedAlone(t, given, "greet"); len(published.GetUnits()[0].GetPublishes()) != 1 || readAt(t, into, "greet_v1.0.0") == "" {
		t.Errorf("a release from a checkout given the daemon was published as %v", published.GetUnits()[0].GetPublishes())
	}
}
