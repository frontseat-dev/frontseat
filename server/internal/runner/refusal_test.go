package runner

import (
	"context"
	"strings"
	"testing"

	bbrunner "github.com/buildbarn/bb-remote-execution/pkg/proto/runner"
	"google.golang.org/protobuf/encoding/protowire"
)

// marked is in every value these refusals are given: any client of the
// grid writes an action's paths, its command and what is to be started
// beside it, and a value may hold a token.
const marked = "tok3n"

// unrepeated fails the test where what was refused was not, or where the
// refusal repeats the value it refused.
func unrepeated(t *testing.T, what string, err error) {
	t.Helper()
	switch {
	case err == nil:
		t.Errorf("%s was allowed", what)
	case strings.Contains(err.Error(), marked):
		t.Errorf("the refusal of %s repeats it: %v", what, err)
	}
}

// A refusal says what kind of thing was refused and why, never the
// value: not a path of a request, the way up of a command, nor a
// property of a program's platform. The control of each is the same
// thing well formed, which is allowed.
func TestARefusalRepeatsNothingAClientOfTheGridWrote(t *testing.T) {
	if _, err := pathsOf(request(good())); err != nil {
		t.Fatalf("the worker's own request: %v", err)
	}
	_, err := pathsOf(request(with(4, "../"+marked)))
	unrepeated(t, "a request that names no directory", err)
	_, err = pathsOf(request(with(5, "166/"+marked)))
	unrepeated(t, "a path outside the request's directory", err)
	_, err = pathsOf(request(map[protowire.Number]string{4: marked + "/../stdout", 5: marked + "/stderr", 6: marked + "/root", 7: marked + "/tmp", 8: marked + "/server_logs"}))
	unrepeated(t, "a path that climbs in a directory of the client's naming", err)

	const confiners = "/bb/frontseat-action-"
	root, dir := "/slot/build/x/root", "/slot/build/x/root/repo/module"
	if err := confined(confiners, []string{confiners + aForm, "{}", "../..", "go"}, dir, root); err != nil {
		t.Fatalf("a confined command: %v", err)
	}
	unrepeated(t, "a way up that is no way up", confined(confiners, []string{confiners + aForm, "{}", "../" + marked, "go"}, dir, root))

	if _, err := within("/build", "x/root"); err != nil {
		t.Fatalf("a path beneath its directory: %v", err)
	}
	_, err = within("/build", "../"+marked)
	unrepeated(t, "a path that leaves its directory", err)

	confinedRunner := &Confined{Build: t.TempDir()}
	if _, err := confinedRunner.CheckReadiness(context.Background(), &bbrunner.CheckReadinessRequest{}); err != nil {
		t.Fatalf("the build directory itself: %v", err)
	}
	_, err = confinedRunner.CheckReadiness(context.Background(), &bbrunner.CheckReadinessRequest{Path: marked})
	unrepeated(t, "a path that is not in the build directory", err)
	_, err = confinedRunner.output(marked + "/stdout")
	unrepeated(t, "an output file in a directory that is not there", err)

	r := &runner{image: busybox}
	if err := r.checkDescription(apiDescription(t)); err != nil {
		t.Fatalf("a program the daemon would write: %v", err)
	}
	program := apiDescription(t)
	program.Services[0].Program.Platform[marked] = "x"
	unrepeated(t, "a program of a platform property that is none", r.checkDescription(program))
}
