package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
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

// publishingSource writes, into a remote of releasingClients, greet
// with what it ships and an action that publishes it, into a directory.
func publishingSource(t *testing.T, into string, fails bool, atRoot bool) func(remote string) {
	failing := ""
	if fails {
		failing = "        environment: [RELEASE_TOKEN, OTHER_TOKEN, FAILS]\n"
	}
	publish := "      release:\n        command: [sh, -c, '" + publisher + "', publish, " + into + ", '{frontseat.tag}', '{frontseat.version}', '{frontseat.notes}', '{frontseat.remote}', 'prerelease={frontseat.prerelease}', '{frontseat.artifacts}']\n        publish: true\n"
	if !fails {
		publish += "        environment: [RELEASE_TOKEN, OTHER_TOKEN]\n"
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
	c, clone := releasingClients(t, bringing{}, "release/1.0", publishingSource(t, into, false, false))
	c.daemon.releases.graph.runs.given = []string{"RELEASE_TOKEN"}
	// A build is of everything, and publishes nothing.
	if ended := wait(t, c.runs, create(t, c.runs).GetId()); ended.GetExitCode().GetCode() != 0 || readAt(t, into, "greet_v1.0.0") != "" {
		t.Fatalf("a build ended %s, and published %q", ended.GetExitCode().GetName(), readAt(t, into, "greet_v1.0.0"))
	}
	refusal := ""
	if pointed, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{Targets: []string{"//services/greet:custom.publish/release"}}); err != nil {
		refusal = err.Error()
	} else if ended := wait(t, c.runs, pointed.GetBuild().GetId()); ended.GetExitCode().GetCode() != 0 {
		refusal = ended.GetFailure()
	}
	if !strings.Contains(refusal, "publishes, which no build runs") || readAt(t, into, "greet_v1.0.0") != "" {
		t.Errorf("a build pointed at what publishes says %q, and published %q", refusal, readAt(t, into, "greet_v1.0.0"))
	}
	s, release := releasedAlone(t, c, "greet")
	published := release.GetUnits()[0].GetPublished()
	if len(published) != 1 || published[0].GetLabel() != "//services/greet:custom.publish/release" || published[0].GetFailure() != "" {
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
		if !strings.Contains(rest, artifact.GetSha256()+"  "+artifact.GetName()) {
			t.Errorf("the sums do not hold %s as the release lists it:\n%s", artifact.GetName(), rest)
		}
	}
	if tagsOf(t, remoteOf(t, clone)) != "greet/v1.0.0" {
		t.Errorf("the remote has the tags %q", tagsOf(t, remoteOf(t, clone)))
	}
	// log has nothing that publishes: its release is as it ever was.
	if plain := releaseEnded(t, s, releaseBegun(t, s, "log").GetId()); plain.GetState() != buildv1.Release_STATE_SUCCEEDED || len(plain.GetUnits()[0].GetPublished()) != 0 || readAt(t, into, "log_v1.0.0") != "" {
		t.Errorf("a release of a unit with nothing that publishes ended %v (%s), published %v", plain.GetState(), plain.GetFailure(), plain.GetUnits()[0].GetPublished())
	}
}

// A publish that fails fails its release, which says which on the unit
// with what the tool printed, whatever it was given of where the daemon
// runs left out; the tags stay pushed.
func TestAPublishThatFailsFailsTheReleaseAndItsTagsStay(t *testing.T) {
	into := t.TempDir()
	t.Setenv("RELEASE_TOKEN", "a-token-of-this-tests")
	t.Setenv("FAILS", "yes")
	c, clone := releasingClients(t, bringing{}, "release/1.0", publishingSource(t, into, true, false))
	c.daemon.releases.graph.runs.given = []string{"RELEASE_TOKEN", "FAILS"}
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_FAILED || !strings.Contains(release.GetFailure(), "//services/greet:custom.publish/release of greet could not be published; every tag is pushed") {
		t.Fatalf("the release ended %v (%s)", release.GetState(), release.GetFailure())
	}
	published := release.GetUnits()[0].GetPublished()
	if len(published) != 1 || !strings.Contains(published[0].GetFailure(), "exit status 3") || !strings.Contains(published[0].GetFailure(), "refused …") {
		t.Errorf("the unit says it was published as %v", published)
	}
	if whole := release.String(); strings.Contains(whole, "a-token-of-this-tests") {
		t.Errorf("the release repeats the token: %s", whole)
	}
	if tagsOf(t, remoteOf(t, clone)) != "greet/v1.0.0" {
		t.Errorf("the remote has the tags %q, want the release's tag still", tagsOf(t, remoteOf(t, clone)))
	}
}

// What publishes and is owned by no entity, as at a repository's root,
// publishes each unit of its repository that moves, one at a time, each
// with its own artifacts and under its own tag.
func TestAPublishNoEntityOwnsPublishesEachUnitOfItsRepository(t *testing.T) {
	into := t.TempDir()
	c, _ := releasingClients(t, bringing{}, "release/1.0", publishingSource(t, into, false, true))
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
		if len(unit.GetPublished()) != 1 || unit.GetPublished()[0].GetLabel() != "//services:custom.publish/release" || unit.GetPublished()[0].GetFailure() != "" {
			t.Errorf("%s says it was published as %v", unit.GetEntity().GetName(), unit.GetPublished())
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
	c, clone := releasingClients(t, bringing{}, "release/1.0", source("      release:\n        command: [sh, -c, 'exit 0']\n        publish: true\n"))
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_FAILED || !strings.Contains(release.GetFailure(), "//services/greet:custom.pack left what cannot be published: an artifact is named checksums.txt") || !strings.Contains(release.GetFailure(), "nothing is tagged") || tagsOf(t, clone) != "" {
		t.Errorf("the release ended %v (%s) with the tags %q", release.GetState(), release.GetFailure(), tagsOf(t, clone))
	}
	control, _ := releasingClients(t, bringing{}, "release/1.0", source(""))
	if _, shipped := releasedAlone(t, control, "greet"); len(shipped.GetUnits()[0].GetArtifacts()) != 1 {
		t.Errorf("the control shipped %v", shipped.GetUnits()[0].GetArtifacts())
	}
}
