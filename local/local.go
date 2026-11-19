// Package local says what the local stack, one container of local/image,
// is to whoever builds on it, from the stack's own files: the platform
// its workers advertise, and what a sandbox on this machine is told of
// it. The local stack's control plane (frontseat-local) and the test
// support carry the files in themselves (Embedded), since they run where
// the repository may not be; the box reads them from the checkout it is
// built from (Read). A program started at a checkout finds it there
// (Checkout), never above.
//
// The image actions run in is written once, in the file the stack's
// image holds (local/image/rootfs/etc/frontseat/runner-image): the pool
// makes every runner sandbox of it, the worker advertises it, and it is
// read from there here.
package local

import (
	_ "embed"
)

//go:embed image/rootfs/etc/frontseat/runner-image
var runner []byte

// Embedded are the stack's files the program was built with.
var Embedded = Files{Runner: runner}
