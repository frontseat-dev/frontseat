package stage

import (
	"context"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/download"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// Staging only needs a reference to installed tools. This test does not execute
// a command; the API spec exercises the actual worker's source comparison.
type stagedInstall struct{}

func (stagedInstall) Execute(_ context.Context, action exec.Action) (exec.Result, error) {
	outputs := map[string]exec.Held{}
	for _, output := range action.Outputs {
		outputs[output] = exec.Held{Digest: exec.Digest{Hash: strings.Repeat("0", 64), Size: 1}, Directory: true}
	}
	return exec.Result{Outputs: outputs}, nil
}

// installed is a provisioner whose tools are installed already, on a
// platform of the workers'.
func installed() *tools.Provisioner {
	return &tools.Provisioner{Executor: stagedInstall{}, Platform: map[string]string{"OSFamily": "Linux", "ISA": "aarch64"}}
}

// fetching stages action over snapshot, its tools installed, with an
// asset service of its own; it returns the service and what was staged.
func fetching(t *testing.T, snapshot *source.Snapshot, action plan.Action) (*fetched, exec.Action) {
	t.Helper()
	service := &fetched{}
	staged, err := Of(t.Context(), snapshot, action, installed(), service, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service, staged
}

func TestHandedSourcesCannotOverwriteAtOneDestination(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"left/own.txt": "left", "right/own.txt": "right"})
	for _, destination := range []string{".deps/shared", ".deps/./shared", ".deps/other"} {
		action := plan.Action{
			Declared: &pluginv1.Action{Command: &pluginv1.Command{Arguments: []string{"cmp", ".deps/shared/own.txt", ".deps/other/own.txt"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
			Handed: []plan.Handed{
				{Root: "left", Patterns: []string{"own.txt"}, Into: ".deps/shared"},
				{Root: "right", Patterns: []string{"own.txt"}, Into: destination},
			},
		}
		staged, err := Of(t.Context(), snapshot, action, installed(), nil, nil)
		if destination == ".deps/other" {
			if err != nil || len(staged.Copies) != 2 {
				t.Fatalf("separate-placement control: %v %+v", err, staged.Copies)
			}
		} else if err == nil || !strings.Contains(err.Error(), "more than one dependency") {
			t.Errorf("lost a declared dependency at %q: copies=%+v, err=%v", destination, staged.Copies, err)
		}
	}
}

// What a plugin writes for an action is never put where a file it is
// given of the source is: one would replace the other without a word.
func TestNothingWrittenStandsInPlaceOfASourceFile(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"go.mod": "module a.com/x\n"})
	for _, at := range []string{"go.mod", "go.work"} {
		action := plan.Action{
			Sources:  []pattern.Group{{Patterns: []string{"go.mod"}}},
			Declared: &pluginv1.Action{Command: &pluginv1.Command{Arguments: []string{"true"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
			Arranged: []plan.Arranged{{Path: at, Content: []byte("go 1.21\n")}},
		}
		_, err := Of(t.Context(), snapshot, action, installed(), nil, nil)
		switch {
		case at == "go.work" && err != nil:
			t.Errorf("a file beside the source was refused: %v", err)
		case at == "go.mod" && (err == nil || !strings.Contains(err.Error(), "a file it is given")):
			t.Errorf("a file written over the source go.mod was not refused: %v", err)
		}
	}
}

// fetched stands in for the asset service: it answers each URL with a
// digest of the checksum asked for.
type fetched struct{ asked []string }

// unchecked is the digest the stand-in answers an unchecked ask with.
var unchecked = strings.Repeat("c4", 32)

func (f *fetched) Fetch(_ context.Context, url, sum string) (exec.Digest, error) {
	f.asked = append(f.asked, url+" "+sum)
	if sum == "" {
		sum = unchecked
	}
	return exec.Digest{Hash: sum, Size: 7}, nil
}

func (f *fetched) Forget() {}

// A shared fetch that downloads is given what the asset service fetched,
// by digest, beside its home, and a command of the host's that unpacks
// each into its home with no network; its files of the home are written
// there. Without an asset service, it is refused by name.
func TestADownloadingFetchIsGivenWhatWasFetchedAndUnpacksItOffline(t *testing.T) {
	snapshot := sourcetest.Taken(t, nil)
	sum := strings.Repeat("8f", 32)
	action := plan.Action{
		Declared: &pluginv1.Action{
			Name: "fetch/itoa@1.0.18", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: "~/.cargo/vendor/itoa-1.0.18/.cargo-checksum.json", Content: []byte("{}")}}, Downloads: []*pluginv1.Download{{Url: "https://static.crates.io/crates/itoa/1.0.18/download", Sha256: sum, Into: "~/.cargo/vendor/"}}}}, Outputs: []*pluginv1.Output{{Path: "~/.cargo/vendor/itoa-1.0.18/"}},
		},
	}
	service, staged := fetching(t, snapshot, action)
	if service.asked == nil || service.asked[0] != "https://static.crates.io/crates/itoa/1.0.18/download "+sum {
		t.Errorf("the asset service was asked %v", service.asked)
	}
	archive := ".frontseat/downloads/" + sum
	if held := staged.Held[archive]; held.Digest.Hash != sum || held.Directory {
		t.Errorf("held at %s: %+v; want the fetched file", archive, held)
	}
	if _, there := staged.Written[".frontseat/home/.cargo/vendor/itoa-1.0.18/.cargo-checksum.json"]; !there {
		t.Errorf("the checksum file is not written in the home: %v", keysOf(staged.Written))
	}
	script := strings.Join(staged.Arguments, " ")
	switch {
	case !strings.Contains(script, " "+placer()+" unpack "):
		t.Errorf("the command does not unpack with frontseat-place: %q", staged.Arguments)
	case !strings.Contains(script, " "+archive+" .frontseat/home/.cargo/vendor"):
		t.Errorf("the command does not name the archive and its directory: %q", staged.Arguments)
	case networked(staged.Arguments):
		t.Errorf("the command has the network, and needs none: %q", staged.Arguments)
	}
	if len(staged.Outputs) != 1 || staged.Outputs[0] != ".frontseat/home/.cargo/vendor/itoa-1.0.18" {
		t.Errorf("outputs %v", staged.Outputs)
	}
	// The control: the daemon has no asset service.
	_, err := Of(t.Context(), snapshot, action, installed(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--assets") {
		t.Errorf("without an asset service: %v; want a refusal naming --assets", err)
	}
}

func keysOf(written map[string]exec.Written) []string {
	out := make([]string, 0, len(written))
	for at := range written {
		out = append(out, at)
	}
	return out
}

// A shared fetch that cuts parts out of what it downloads asks for the
// whole unchecked, there being no checksum of it, is given it by the
// digest the asset service answered with, and runs a command of the
// host's that cuts each part out, checks it and keeps it in its home,
// with no network.
func TestAFetchThatCutsPartsIsGivenTheWholeUncheckedAndChecksEachPartOffline(t *testing.T) {
	snapshot := sourcetest.Taken(t, nil)
	address := "https://packages.wolfi.dev/os/aarch64/busybox-1.37.0-r50.apk"
	control, data := strings.Repeat("1a", 20), strings.Repeat("2b", 32)
	within := "~/.cache/dev.chainguard.go-apk/https%3A%2F%2Fpackages.wolfi.dev%2Fos/aarch64/busybox-1.37.0-r50/"
	action := plan.Action{
		Declared: &pluginv1.Action{
			Name: "fetch/packages.wolfi.dev/os/aarch64/busybox-1.37.0-r50", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Downloads: []*pluginv1.Download{{Url: address, Parts: []*pluginv1.Part{
				{Offset: 647, Length: 982, Sha1: control, Path: within + control + ".ctl.tar.gz"},
				{Offset: 1629, Length: 1301, Sha256: data, Path: within + data + ".dat.tar.gz"},
			}}}}}, Outputs: []*pluginv1.Output{{Path: within}},
		},
	}
	service, staged := fetching(t, snapshot, action)
	if len(service.asked) != 1 || service.asked[0] != address+" " {
		t.Errorf("the asset service was asked %q; want the address once, with no checksum", service.asked)
	}
	whole := ".frontseat/downloads/" + unchecked
	if held := staged.Held[whole]; held.Digest.Hash != unchecked || held.Directory {
		t.Errorf("held at %s: %+v; want what the asset service answered with", whole, held)
	}
	home := ".frontseat/home/.cache/dev.chainguard.go-apk/https%3A%2F%2Fpackages.wolfi.dev%2Fos/aarch64/busybox-1.37.0-r50/"
	script := strings.Join(staged.Arguments, " ")
	for _, want := range []string{
		" " + placer() + " cut ",
		" " + whole + " 647 982 sha1 " + control + " " + home + control + ".ctl.tar.gz " + address,
		" " + whole + " 1629 1301 sha256 " + data + " " + home + data + ".dat.tar.gz " + address,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the command lacks %q: %q", want, staged.Arguments)
		}
	}
	if strings.Contains(script, " unpack ") {
		t.Errorf("the command unpacks, and nothing is to be unpacked: %q", staged.Arguments)
	}
	if networked(staged.Arguments) {
		t.Errorf("the command has the network, and needs none: %q", staged.Arguments)
	}
}

// A shared fetch of what its lock holds whole, a signing key, has no
// command and downloads nothing: it asks the asset service for nothing,
// its file is written in its home, where its output is, and it runs the
// host's command, which places nothing, with no network.
func TestAFetchOfWhatItsLockHoldsWholeLeavesItsFilesAndReachesNothing(t *testing.T) {
	snapshot := sourcetest.Taken(t, nil)
	const within = "~/.cache/dev.chainguard.go-apk/https%3A%2F%2Fpackages.wolfi.dev%2F/os/wolfi-signing.rsa.pub/"
	action := plan.Action{
		Declared: &pluginv1.Action{
			Name: "fetch/packages.wolfi.dev/os/wolfi-signing.rsa.pub", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: within + "locked.etag", Content: []byte("key")}}}}, Outputs: []*pluginv1.Output{{Path: within}},
		},
	}
	provisioner := installed()
	service := &fetched{}
	// With no asset service at all, too: it downloads nothing.
	for _, downloads := range []download.Fetcher{service, nil} {
		staged, err := Of(t.Context(), snapshot, action, provisioner, downloads, nil)
		if err != nil {
			t.Fatal(err)
		}
		home := ".frontseat/home/.cache/dev.chainguard.go-apk/https%3A%2F%2Fpackages.wolfi.dev%2F/os/wolfi-signing.rsa.pub"
		if written, there := staged.Written[home+"/locked.etag"]; !there || string(written.Content) != "key" {
			t.Errorf("the key is not written in the home: %v", keysOf(staged.Written))
		}
		if len(staged.Outputs) != 1 || staged.Outputs[0] != home {
			t.Errorf("outputs %v", staged.Outputs)
		}
		if networked(staged.Arguments) {
			t.Errorf("the command has the network, and needs none: %q", staged.Arguments)
		}
		if command := staged.Arguments[3:]; !slices.Equal(command, []string{placer(), "unpack"}) {
			t.Errorf("the command is %q; want frontseat-place told nothing to place", command)
		}
	}
	if len(service.asked) != 0 {
		t.Errorf("the asset service was asked %q", service.asked)
	}
}

// placer is frontseat-place as the daemon names it, of its form.
func placer() string { return download.Unpacking()[0] }

// networked says whether a staged command reaches the network: what
// frontseat-action is told, its second word, says so.
func networked(arguments []string) bool {
	return len(arguments) < 2 || strings.Contains(arguments[1], `"network":true`)
}
