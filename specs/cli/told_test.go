package e2e

import (
	"encoding/json"
	"testing"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testbuild"
)

// What a test reads of a build daemon, to reach it in a box from
// outside (testbuild.Told), is what a build daemon says of itself
// (life.Info): testbuild is beneath the daemon and cannot name its
// type, so the two are held to each other here, by what one writes and
// the other reads. A field the daemon named otherwise would be read as
// nothing.
func TestWhatATestReadsOfABuildDaemonIsWhatOneSaysOfItself(t *testing.T) {
	said := life.Info{Address: "127.0.0.1:4711", Token: "a-token", Version: "1", Settings: "s", Directory: "/work", Environment: "e", PID: 7}
	written, err := json.Marshal(said)
	if err != nil {
		t.Fatal(err)
	}

	read, port, err := testbuild.Read(string(written))

	if err != nil {
		t.Fatalf("what a build daemon says of itself is not read: %v", err)
	}
	if want := (testbuild.Told{Address: said.Address, Token: said.Token, Directory: said.Directory}); read != want || port != 4711 {
		t.Errorf("of %s a test reads %+v at the port %d, want %+v at 4711", written, read, port, want)
	}
}
