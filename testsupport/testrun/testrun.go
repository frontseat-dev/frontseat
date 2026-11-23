// Package testrun names what a test makes on a provider other runs
// share, a container runtime or a gateway: by an id no other run draws
// (ID), and labelled with the run that made it (Label, This), which is
// what a person finds a crashed run's leftovers by, to end them by hand.
package testrun

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"sync"
	"testing"
)

// ID is an id of the name given and a token no other run draws: a
// driver's tests share its provider with every other checkout and
// session, which may be running the same test at the same moment, and an
// id a test spells alone may be another's sandbox. The token is eight
// hexadecimal digits, so that the name with it is still one a driver
// takes where the name is.
func ID(t testing.TB, name string) string {
	t.Helper()
	token := make([]byte, 4)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	return name + "-" + hex.EncodeToString(token)
}

// Label is the label everything a test makes on a shared provider
// carries (a driver's sandbox, a test's stack), with
// the run that made it (This) as its value: a run that crashed leaves
// its sandboxes, which nothing ends by itself, and this is what a person
// finds them by, to end them by hand. Nothing ends another run's.
const Label = "frontseat.dev/test-run"

// This is the run of this process: its process id and a token no other
// run draws, as a label's value may be written.
var This = sync.OnceValue(func() string {
	token := make([]byte, 4)
	if _, err := rand.Read(token); err != nil {
		panic(err)
	}
	return "p" + strconv.Itoa(os.Getpid()) + "-" + hex.EncodeToString(token)
})
