// Package image is the local stack's one image, beside what it is built
// of (Dockerfile, rootfs): the files of it a program carries in itself,
// since it runs where the repository may not be. What they say is read
// by stack/local.
package image

import (
	_ "embed"
)

// Runner is the file that names the image actions run in, as the stack's
// image holds it (rootfs/etc/frontseat/runner-image).
//
//go:embed rootfs/etc/frontseat/runner-image
var Runner []byte

// Slots is the file that says how many actions the grid's worker runs at
// once, each a worker to the scheduler (rootfs/etc/frontseat/slots), and
// Services the one that says how many tests with services it runs
// beside them (rootfs/etc/frontseat/services), each a worker too.
//
//go:embed rootfs/etc/frontseat/slots
var Slots []byte

//go:embed rootfs/etc/frontseat/services
var Services []byte
