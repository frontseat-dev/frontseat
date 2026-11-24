// Package dated says which file systems of Linux a build daemon trusts
// to date a file's changes themselves, for the specs that hold a stack
// to them.
package dated

// Linux are those file systems, each by the number statfs answers for
// it: where a workspace is on one, what its build daemon knows of its
// files is kept for the daemon started after it. It mirrors the build
// engine's own list (linuxDates, in
// build/engine/engine/source/filesystem.go), which a spec may not import
// and which says why each is on it; the architecture test holds the two
// to each other (TestTheFileSystemsASpecTrustsAreTheOnesABuildDaemonTrusts).
var Linux = map[uint32]string{
	0x0000ef53: "ext4",
	0x58465342: "xfs",
	0x9123683e: "btrfs",
	0x01021994: "tmpfs",
	0xf2f52010: "f2fs",
	0xca451a4e: "bcachefs",
	0x2fc12fc1: "zfs",
	0x794c7630: "overlayfs",
}

// Overlay is the one of them that keeps no file of its own: it gives the
// change time of the file system beneath it, and is trusted only as far
// as that one is.
const Overlay = 0x794c7630
