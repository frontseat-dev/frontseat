package reapi_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/access"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
)

// A grid that is not this machine's own asks who calls. A daemon given the
// authorities it is known by and a token it admits runs an action on it,
// through stock Buildbarn configured as such a grid's door is: TLS, and a
// bearer token a key signed. The same door admits no daemon that bears
// none, or one another key signed.
func TestAnActionRunsOnAGridThatAsksWhoTheDaemonIs(t *testing.T) {
	door := testgrid.NewDoor(t)
	grid, err := reapi.Dial(t.Context(), reapi.Config{Address: door.Address, Access: access.Config{Authorities: door.Authorities, Token: door.Token(t)}})
	if err != nil {
		t.Fatal(err)
	}
	defer grid.Close()
	done, err := confining{grid}.Execute(context.Background(), shell(root(t, map[string]string{"greeting.txt": "through the door\n"}), "cat greeting.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if done.ExitCode != 0 || done.Cached || !strings.Contains(string(done.Stdout), "through the door") {
		t.Errorf("exit %d, cached %v, stdout %q, stderr %q", done.ExitCode, done.Cached, done.Stdout, done.Stderr)
	}
	for name, config := range map[string]access.Config{
		"bearing no token":               {Authorities: door.Authorities},
		"bearing one another key signed": {Authorities: door.Authorities, Token: door.Forged(t)},
		"bearing one that ran out":       {Authorities: door.Authorities, Token: door.Expired(t)},
		"bearing one for another grid":   {Authorities: door.Authorities, Token: door.Misdirected(t)},
	} {
		refused, err := reapi.Dial(t.Context(), reapi.Config{Address: door.Address, Access: config})
		if err == nil {
			refused.Close()
		}
		if !reapi.NotAdmitted(err) {
			t.Errorf("a daemon %s: %v; want it not admitted", name, err)
		}
	}
	// Not known by the system's authorities, the door is not the grid.
	if unknown, err := reapi.Dial(t.Context(), reapi.Config{Address: door.Address, Access: access.Config{TLS: true}}); err == nil {
		unknown.Close()
		t.Error("a door of an authority the system does not know was taken for the grid")
	}
}

// A token lives for minutes and a build for longer. A grid that stops
// admitting a daemon while it builds, its token having run out, says so
// of the action it was asked for and names the file, never what it held;
// nothing is kept of that action, and with the token renewed it runs.
func TestAnActionAskedForWithATokenThatRanOutIsNotAdmittedAndRunsOnceItIsRenewed(t *testing.T) {
	door := testgrid.NewDoor(t)
	token := door.Token(t)
	grid, err := reapi.Dial(t.Context(), reapi.Config{Address: door.Address, Access: access.Config{Authorities: door.Authorities, Token: token}})
	if err != nil {
		t.Fatal(err)
	}
	defer grid.Close()
	replace := func(with string) {
		t.Helper()
		// As a writer of one must: the file whole, moved into place.
		if err := os.Rename(with, token); err != nil {
			t.Fatal(err)
		}
	}
	remembering := &exec.Remembering{Executor: confining{grid}}
	action := shell(root(t, map[string]string{"greeting.txt": "after the token was renewed\n"}), "cat greeting.txt")
	ranOut := door.Expired(t)
	held, err := os.ReadFile(ranOut)
	if err != nil {
		t.Fatal(err)
	}
	replace(ranOut)
	_, err = remembering.Execute(context.Background(), action)
	if !reapi.NotAdmitted(err) || !strings.Contains(err.Error(), "did not admit this daemon") || !strings.Contains(err.Error(), token) {
		t.Fatalf("with a token that ran out: %v", err)
	}
	if strings.Contains(err.Error(), strings.TrimSpace(string(held))) {
		t.Errorf("the refusal repeats the token: %v", err)
	}
	replace(door.Token(t))
	done, err := remembering.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if done.ExitCode != 0 || done.Cached || !strings.Contains(string(done.Stdout), "after the token was renewed") {
		t.Errorf("exit %d, cached %v, stdout %q, stderr %q", done.ExitCode, done.Cached, done.Stdout, done.Stderr)
	}
}
