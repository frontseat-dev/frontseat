package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// waited asks a daemon to wait for what a build read to change, for at
// most a while: what it answered, or why it did not.
func waited(t *testing.T, c clients, id string, within time.Duration) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	said, err := c.runs.WaitSourceChanged(ctx, &buildv1.WaitSourceChangedRequest{BuildId: id})
	return said.GetSourceChanged().GetPaths(), err
}

// answers holds what a wait answered to be want; where want is nil, to
// be nothing within the while it was given.
func answers(t *testing.T, what string, changed []string, err error, want []string) {
	t.Helper()
	if want == nil && connect.CodeOf(err) != connect.CodeDeadlineExceeded || want != nil && (err != nil || !slices.Equal(changed, want)) {
		t.Errorf("%s: the wait answered %v (%v); want %v", what, changed, err, want)
	}
}

// built serves a workspace whose files have settled, with plugins,
// builds it once to its end, and returns the daemon, looking every
// millisecond, and the build. A file made beside the workspace is what
// the daemon is made of, as it looked before the daemon read it.
func built(t *testing.T, workspace string, config configuration, plugins func() ([]*pluginhost.Plugin, error)) (clients, *buildv1.Build) {
	t.Helper()
	settled(t, workspace)
	loaded, err := plugins()
	if err != nil {
		t.Fatal(err)
	}
	config.Workspace, config.Mounted, config.Executor = workspace, true, scripted{}
	c := serveWith(t, config, loaded)
	c.daemon.runs.every = time.Millisecond
	return c, wait(t, c.runs, create(t, c.runs).GetId())
}

// beside is files looked at beside a build at a time, and then read,
// as a daemon looks at what it is made of before it reads it, and says
// what it read.
func beside(t *testing.T, at time.Time, files ...string) *source.Looks {
	t.Helper()
	looks, err := source.Beside(t.Context(), at, files...)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		_, _ = looks.Read(file, func(r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		})
	}
	return looks
}

// goAndCatalog are the Go and Backstage plugins, what a workspace of
// one Go module is built with (oneModule).
func goAndCatalog() ([]*pluginhost.Plugin, error) {
	all, err := loaded()
	return all[:2], err
}

// A build of a workspace of Cargo's reads the workspace's
// .cargo/config.toml by name, beneath a directory whose name begins with
// a dot, which no listing takes: written again, and nothing else, it is
// what the wait answers. Control: a file in another such directory, which
// no build reads, written, and nothing is answered.
func TestAWaitNamesAFileTheBuildReadByNameBeneathADotDirectory(t *testing.T) {
	for name, tc := range map[string]struct {
		written string
		want    []string
	}{
		"cargo's configuration, written again": {".cargo/config.toml", []string{".cargo/config.toml"}},
		"a file no build reads, written":       {".idea/workspace.xml", nil},
	} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			testfiles.Tree(t, workspace, map[string]string{
				"Cargo.toml":          "[workspace]\nresolver = \"2\"\nmembers = [\"hello\"]\n",
				"Cargo.lock":          "version = 4\n\n[[package]]\nname = \"hello\"\nversion = \"0.1.0\"\n",
				"hello/Cargo.toml":    "[package]\nname = \"hello\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
				"hello/src/main.rs":   "fn main() {}\n",
				".cargo/config.toml":  "[build]\n",
				".idea/workspace.xml": "<project/>",
				"frontseat.yaml":      "plugins:\n  cargo: {}\n",
			})
			c, build := built(t, workspace, configuration{}, cargoLoaded)
			write(t, workspace, tc.written, "written again")
			changed, err := waited(t, c, build.GetId(), 2*time.Second)
			answers(t, tc.written+" written", changed, err, tc.want)
		})
	}
}

// What a build listed is waited on as the build held it, and so is what
// the daemon is made of and the workspace's manifest: a file listed,
// written again at its size with its times set back; a file made in a
// directory listed; a part of the daemon written again; the manifest
// made. Each is answered by its name. Control: nothing written, nothing
// is answered.
func TestAWaitNamesWhatTheBuildListedAndWhatItIsMadeOf(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(t *testing.T, workspace, part string)
		want   func(workspace, part string) []string
	}{
		"nothing written": {func(*testing.T, string, string) {}, func(string, string) []string { return nil }},
		"a file listed, written again at its size with its times set back": {func(t *testing.T, workspace, _ string) {
			file := filepath.Join(workspace, "hello", "main.go")
			was, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			write(t, workspace, "hello/main.go", "package MAIN\n\nfunc main() {}\n")
			if err := os.Chtimes(file, was.ModTime(), was.ModTime()); err != nil {
				t.Fatal(err)
			}
		}, func(string, string) []string { return []string{"hello/main.go"} }},
		"a file made in a directory listed": {func(t *testing.T, workspace, _ string) {
			write(t, workspace, "hello/more.go", "package main\n")
		}, func(string, string) []string { return []string{"hello/more.go"} }},
		"a part of the daemon written again": {func(t *testing.T, _, part string) {
			testfiles.Write(t, part, "settings: other\n")
		}, func(_, part string) []string { return []string{part} }},
		"the workspace's manifest made": {func(t *testing.T, workspace, _ string) {
			write(t, workspace, manifest.File, "version: 1\n")
		}, func(workspace, _ string) []string {
			return []string{filepath.Join(workspace, filepath.FromSlash(manifest.File))}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			part := filepath.Join(t.TempDir(), "build.yaml")
			testfiles.Write(t, part, "settings: these\n")
			settled(t, filepath.Dir(part))
			workspace := oneModule(t)
			c, build := built(t, workspace, configuration{Made: beside(t, time.Now(), part)}, goAndCatalog)
			tc.change(t, workspace, part)
			changed, err := waited(t, c, build.GetId(), 2*time.Second)
			answers(t, name, changed, err, tc.want(workspace, part))
		})
	}
}

// A wait the caller gave up on returns at once, however long it was to
// wait before it looked again. Control: the same wait, not given up on,
// goes on.
func TestAWaitReturnsOnceItsCallerGivesUp(t *testing.T) {
	c, build := built(t, oneModule(t), configuration{}, goAndCatalog)
	c.daemon.runs.every = time.Hour
	service := &buildService{runs: c.daemon.runs}
	for name, after := range map[string]time.Duration{"given up on": 50 * time.Millisecond, "the control, not given up on": time.Hour} {
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(after, cancel)
		returned := make(chan error, 1)
		go func() {
			_, err := service.WaitSourceChanged(ctx, &buildv1.WaitSourceChangedRequest{BuildId: build.GetId()})
			returned <- err
		}()
		select {
		case err := <-returned:
			if after == time.Hour || !errors.Is(err, context.Canceled) {
				t.Errorf("%s: the wait returned %v", name, err)
			}
		case <-time.After(time.Second):
			if after != time.Hour {
				t.Errorf("%s: the wait went on for a second after", name)
			}
		}
		cancel()
	}
}

// A wait of a build no daemon made is refused by name, NOT_FOUND
// (BUILD_NOT_FOUND); of one whose read the daemon no longer keeps, the
// newest builds that ended keeping it, or that a daemon before it made,
// FAILED_PRECONDITION (BUILD_SOURCE_NOT_KEPT): a build made now reads
// the files as they are. Control: the newest build, kept, is waited on.
func TestAWaitOfABuildWhoseReadIsNotKeptIsRefusedByName(t *testing.T) {
	workspace, runs := oneModule(t), t.TempDir()
	config := configuration{Builds: runs}
	c, first := built(t, workspace, config, goAndCatalog)
	var newest *buildv1.Build
	for range readKept {
		newest = wait(t, c.runs, create(t, c.runs).GetId())
	}
	for name, tc := range map[string]struct {
		id     string
		code   connect.Code
		reason string
	}{
		"a build no daemon made":                        {"build_none", connect.CodeNotFound, frontseat.ReasonBuildNotFound},
		"a build older than the newest that keep it":    {first.GetId(), connect.CodeFailedPrecondition, frontseat.ReasonBuildSourceNotKept},
		"the control: the newest build, which keeps it": {newest.GetId(), connect.CodeDeadlineExceeded, ""},
	} {
		_, err := waited(t, c, tc.id, 300*time.Millisecond)
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != tc.code || reason != tc.reason {
			t.Errorf("%s: %v; want %v %s", name, err, tc.code, tc.reason)
		}
	}
	c.daemon.Close()
	again := serveWith(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}, Builds: runs}, nil)
	_, err := waited(t, again, newest.GetId(), 300*time.Millisecond)
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeFailedPrecondition || reason != frontseat.ReasonBuildSourceNotKept {
		t.Errorf("a build a daemon before it made: %v; want it refused, %s", err, frontseat.ReasonBuildSourceNotKept)
	}
}

// patiently holds three waits in a row for what a build read to change
// to answer nothing.
func patiently(t *testing.T, what string, c clients, id string) {
	t.Helper()
	for i := range 3 {
		changed, err := waited(t, c, id, 300*time.Millisecond)
		answers(t, fmt.Sprintf("%s, wait %d", what, i+1), changed, err, nil)
	}
}

// A part of the daemon written the moment before the daemon started,
// looked at that moment, had not settled: it is held to what the daemon
// read of it, and read again once it has (source.Beside,
// source.Looks.Read), and three waits in a
// row, with nothing written since, answer nothing, where a part doubted
// and never read again would be answered by each, and the same daemon
// made again and again. Control: the part written again is answered.
func TestAPartOfTheDaemonWrittenAsItStartedIsReadAgainAndNotAnsweredByEveryWait(t *testing.T) {
	workspace := oneModule(t)
	part := filepath.Join(t.TempDir(), "build.yaml")
	testfiles.Write(t, part, "settings: these\n")
	info, err := os.Stat(part)
	if err != nil {
		t.Fatal(err)
	}
	made := beside(t, info.ModTime(), part)
	if made.Settled() {
		t.Fatal("a part looked at the moment it was written is not doubted")
	}
	c, build := built(t, workspace, configuration{Made: made}, goAndCatalog)
	patiently(t, "nothing written", c, build.GetId())
	testfiles.Write(t, part, "settings: other\n")
	changed, err := waited(t, c, build.GetId(), 2*time.Second)
	answers(t, "the control, the part written again", changed, err, []string{part})
}

// A part of the daemon written again with what it held, by a file of the
// same bytes moved over it, as an install again does, is no change: two
// waits running of the same build answer nothing, where each answered
// it before and the command built again for ever, each round finding
// the daemon it had, made of the same. Control: the part written again
// with other content is answered.
func TestAPartOfTheDaemonWrittenAgainWithWhatItHeldIsAnsweredByNoWait(t *testing.T) {
	workspace := oneModule(t)
	part := filepath.Join(t.TempDir(), "build.yaml")
	testfiles.Write(t, part, "settings: these\n")
	settled(t, filepath.Dir(part))
	c, build := built(t, workspace, configuration{Made: beside(t, time.Now(), part)}, goAndCatalog)
	testfiles.Again["moved over it"](t, part)
	for i := range 2 {
		changed, err := waited(t, c, build.GetId(), 500*time.Millisecond)
		answers(t, fmt.Sprintf("written again with what it held, wait %d", i+1), changed, err, nil)
	}
	testfiles.Write(t, part, "settings: those\n")
	changed, err := waited(t, c, build.GetId(), 2*time.Second)
	answers(t, "the control, the part written again with other content", changed, err, []string{part})
}

// held is a workspace that holds repositories, each of the files named
// beneath it, as its manifest records them, its files settled; and its
// manifest's path.
func held(t *testing.T, repositories map[string]map[string]string) (workspace, recorded string) {
	t.Helper()
	workspace = t.TempDir()
	var kept []record.Repository
	for name, files := range repositories {
		testfiles.Tree(t, filepath.Join(workspace, name), files)
		kept = append(kept, record.Repository{Name: name, Remote: "https://forge.example/acme/" + name + ".git", Commit: strings.Repeat("a", 40)})
	}
	if err := manifest.Write(workspace, manifest.Manifest{Repositories: kept}); err != nil {
		t.Fatal(err)
	}
	settled(t, workspace)
	return workspace, filepath.Join(workspace, filepath.FromSlash(manifest.File))
}

// serveHeld serves a workspace a manifest records, looking every
// millisecond.
func serveHeld(t *testing.T, workspace string, executor scripted) clients {
	t.Helper()
	plugins, err := goAndCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c := serveWith(t, configuration{Workspace: workspace, Executor: executor}, plugins)
	c.daemon.runs.every = time.Millisecond
	return c
}

// A workspace's manifest written the moment before a build, looked at
// as the build began, that moment by the daemon's clock, had not
// settled: it is held to what the build read of it, and read again
// once it has (source.Beside, source.Looks.Read), and three waits in a row answer nothing, where a
// manifest doubted and never read again would be answered once by each
// build. Control: the manifest written again is answered.
func TestAManifestWrittenAsABuildBeganIsReadAgainAndNotAnsweredByTheWait(t *testing.T) {
	workspace, recorded := held(t, map[string]map[string]string{"code": {"hello/go.mod": "module example.com/hello\n\ngo 1.27\n", "hello/main.go": "package main\n\nfunc main() {}\n"}})
	again, _, err := manifest.Read(workspace, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(workspace, again); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(recorded)
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := goAndCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c := serveWith(t, configuration{Workspace: workspace, Executor: scripted{}, Now: func() time.Time { return info.ModTime() }}, plugins)
	c.daemon.runs.every = time.Millisecond
	build := wait(t, c.runs, create(t, c.runs).GetId())
	c.daemon.runs.mu.Lock()
	ran := c.daemon.runs.byID[build.GetId()]
	c.daemon.runs.mu.Unlock()
	if looks := ran.readLooks(nil); looks == nil || looks.Settled() {
		t.Fatal("the manifest, looked at the moment it was written, is not doubted")
	}
	patiently(t, "nothing written", c, build.GetId())
	again.Repositories[0].Commit = strings.Repeat("b", 40)
	if err := manifest.Write(workspace, again); err != nil {
		t.Fatal(err)
	}
	changed, err := waited(t, c, build.GetId(), 2*time.Second)
	answers(t, "the control, the manifest written again", changed, err, []string{recorded})
}

// A build of a workspace one of whose repositories cannot be listed, a
// directory of it being one its owner may not read, fails before it
// takes its snapshot, and is waited on as what could be listed then
// (source.Unread): three waits in a row answer nothing, as the build
// would fail again. Controls: a file of the repository listed, written,
// is answered; and, of the build after, which failed as it did, the
// directory made readable.
func TestABuildThatCouldNotListARepositoryIsWaitedOnPatiently(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads what it may not")
	}
	workspace, _ := held(t, map[string]map[string]string{"a": {"main.go": "package main\n"}, "b": {"main.go": "package main\n", "locked/secret.go": "package locked\n"}})
	locked := filepath.Join(workspace, "b", "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	c := serveHeld(t, workspace, scripted{})
	failed := func() string {
		t.Helper()
		build := wait(t, c.runs, create(t, c.runs).GetId())
		if build.GetState() != buildv1.Build_STATE_FAILED {
			t.Fatalf("a build of a repository that cannot be listed ended %v", build.GetState())
		}
		return build.GetId()
	}
	first := failed()
	patiently(t, "nothing written", c, first)
	write(t, workspace, "a/main.go", "package main // more\n")
	changed, err := waited(t, c, first, 2*time.Second)
	answers(t, "the control, a file of the repository listed, written", changed, err, []string{"a/main.go"})
	after := failed()
	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err = waited(t, c, after, 2*time.Second)
	answers(t, "the control, the directory made readable", changed, err, []string{"b/locked/secret.go", "b/main.go"})
}

// A wait for what a build read to change waits for the build to end
// first, all it read being known only then: a file made while the build
// runs is not answered before the build has ended, and is answered once
// it has, as the control.
func TestAWaitAnswersOnlyOnceTheBuildHasEnded(t *testing.T) {
	workspace := oneModule(t)
	settled(t, workspace)
	gate, reached := make(chan struct{}), make(chan struct{}, 1)
	plugins, err := goAndCatalog()
	if err != nil {
		t.Fatal(err)
	}
	c := serveWith(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{gate: gate, reached: reached}}, plugins)
	c.daemon.runs.every = time.Millisecond
	id := create(t, c.runs).GetId()
	// An action of the build waits at the gate: the build has listed
	// what it reads.
	<-reached
	type answer struct {
		changed []string
		err     error
	}
	answered := make(chan answer, 1)
	go func() {
		changed, err := waited(t, c, id, 30*time.Second)
		answered <- answer{changed, err}
	}()
	write(t, workspace, "hello/more.go", "package main\n")
	select {
	case got := <-answered:
		t.Fatalf("the wait answered %v (%v) while the build ran", got.changed, got.err)
	case <-time.After(500 * time.Millisecond):
	}
	close(gate)
	got := <-answered
	// Answered only once the build had ended: described after the
	// answer, and before anything is waited for, it has.
	described, err := c.runs.DescribeBuild(t.Context(), &buildv1.DescribeBuildRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	if state := described.GetBuild().GetState(); state == buildv1.Build_STATE_RUNNING || state == buildv1.Build_STATE_UNSPECIFIED {
		t.Errorf("the wait was answered while the build was %v", state)
	}
	answers(t, "the control, the build ended", got.changed, got.err, []string{"hello/more.go"})
}
