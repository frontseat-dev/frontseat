package source

import "fmt"

// What a keeper saved is used by the daemon after it only for a
// repository on a file system known to date a file's changes itself:
// to keep, of each file, when anything of it last changed, by the
// kernel's clock, which nothing that writes the file can set. That time
// is what tells a file rewritten in place, as long as it was and with
// its times set back, from one left alone. A file system that keeps no
// such time answers when the file was written, which its writer sets,
// and such a rewrite is not seen there. One not known to keep it is
// taken to keep none: nothing saved is used of a repository on it, and
// nothing of it is saved (begin, Save).
//
// Only what outlasts the daemon is held to this. A running daemon's
// keeper goes by how a file looks on any file system, as it did before
// and as Bazel's and Buck2's do: that is not changed here.
//
// What the tools before do (their clones, at the commits named):
//
//   - Git (1891de4) holds its index to a file's change time, and has two
//     settings for where that cannot be trusted, each a person's to
//     set: core.trustctime=false, where something outside Git changes
//     it (crawlers, backups), and core.checkStat=minimal, where another
//     Git leaves no usable inode or sub-second time (statinfo.c,
//     match_stat_data; Documentation/config/core.adoc). It asks nothing
//     of the file system: a person who knows says.
//   - Bazel (94776af184) goes by change time, written time and inode
//     (FileContentsProxy.java, which notes that some file systems answer
//     the written time for the change time), guards what it uploads by
//     the change time alone (--guard_against_concurrent_changes,
//     RemoteOptions.java), and of the file system's type only warns
//     where its output base is on NFS, by statfs's magic
//     (blaze_util_linux.cc, WarnFilesystemType). Its server keeps what
//     it knows in memory, and a server started again reads again.
//   - Buck2 (903bfd7) keeps no file's look across a restart, and is told
//     of changes by a watcher. Sapling (5dc5632ab5b) names a file
//     system by statfs, the magic on Linux and the name on macOS
//     (eden/scm/lib/fsinfo), and decides by it what a working copy may
//     rely on (lib/vfs).
//
// Frontseat takes Sapling's way of asking and Bazel's caution, and
// turns Git's setting round: no person is asked, and what is not known
// to be trustworthy is not trusted.

// darwinDates are the file systems of macOS known to date a file's
// changes themselves, as statfs names them. On each, measured on macOS
// 26 (hfs on a disk image): utimensat given a written time long past
// leaves the change time at the kernel's own, and setattrlist asked to
// set it (ATTR_CMN_CHGTIME) changes nothing. On exfat and msdos, each
// on a disk image beside them, utimensat sets the change time to the
// written time it was given: they are not here, nor is anything reached over a network
// (nfs, smbfs, webdav) or served by a program (macfuse, virtiofs).
var darwinDates = map[string]bool{
	"apfs": true,
	"hfs":  true,
}

// linuxDates are the file systems of Linux known to date a file's
// changes themselves, each by the number statfs answers for it and
// named as the kernel names it. For every file system the kernel sets
// the change time of whatever changes a file's times to its own clock
// (Linux 6.12: fs/utimes.c asks ATTR_CTIME, fs/attr.c notify_change
// sets ia_ctime to now); these keep it, in their own setattr, on a
// disk or in memory that are the kernel's:
//
//   - ext2, ext3 and ext4, one number (ext4_setattr, setattr_copy);
//   - xfs (xfs_setattr_nonsize) and btrfs (btrfs_setattr);
//   - tmpfs (shmem_setattr), f2fs (f2fs/file.c, __setattr_copy) and
//     bcachefs (bcachefs/fs.c, bch2_setattr_copy);
//   - zfs, which is not the kernel's own (OpenZFS 2.2: zpl_setattr hands
//     ia_ctime to zfs_setattr, which stores it);
//   - overlayfs, which keeps no time of its own: it answers the time of
//     the file beneath, the upper one once a file is written
//     (overlayfs/inode.c: ovl_getattr; ovl_setattr changes the upper
//     file through notify_change). Not held: what is beneath cannot be
//     asked from inside a container, and an overlay whose upper
//     directory is on a file system that is not on this list is trusted
//     as one that is. It is here because a container's own files are
//     an overlay of its runtime's storage, which is as a rule on a
//     disk of the kernel's own, and a workspace's box is a container.
var linuxDates = map[uint32]string{
	0x0000ef53: "ext4",
	0x58465342: "xfs",
	0x9123683e: "btrfs",
	0x01021994: "tmpfs",
	0xf2f52010: "f2fs",
	0xca451a4e: "bcachefs",
	0x2fc12fc1: "zfs",
	0x794c7630: "overlayfs",
}

// linuxUndated names the file systems of Linux most often met that are
// not known to date a file's changes themselves, so that each is said
// by its name; any other number is not trusted either, and is said as
// the number it is.
//
//   - msdos (vfat too) and exfat keep one time for both on the disk:
//     "ctime and mtime share the same on-disk field" (fat/misc.c).
//   - fuse answers whatever the program that serves it says, and
//     virtiofs is fuse to statfs (fuse/virtio_fs.c fills its superblock
//     with fuse_fill_super_common, FUSE_SUPER_MAGIC): it hands on the
//     time of a file system of another machine, which cannot be asked
//     what it is. ntfs-3g is fuse too.
//   - nfs, cifs, smb2 and 9p answer a server's time
//     (9p/vfs_inode_dotl.c takes st_ctime from the reply), kept for a
//     while by the client, of a file system that is not known. Bazel
//     warns of NFS for the same.
//   - ntfs, the kernel's own drivers: not looked into, so not trusted.
var linuxUndated = map[uint32]string{
	0x00004d44: "msdos",
	0x2011bab0: "exfat",
	0x65735546: "fuse",
	0x00006969: "nfs",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x01021997: "9p",
	0x5346544e: "ntfs",
	0x7366746e: "ntfs3",
}

// linuxFileSystem returns the name of the file system statfs answers a
// number for, the number itself where it has none here, and whether it
// is known to date a file's changes itself.
func linuxFileSystem(magic uint32) (name string, dates bool) {
	if name, dates = linuxDates[magic]; dates {
		return name, true
	}
	if name, known := linuxUndated[magic]; known {
		return name, false
	}
	return fmt.Sprintf("%#x", magic), false
}

// Unkept is a repository what a daemon knows of whose files is not kept
// for the daemon after it.
type Unkept struct {
	// Directory is where the repository is.
	Directory string
	// FileSystem is the file system it is on, as the system names it;
	// none where the system does not say.
	FileSystem string
}

// String says it, as a daemon says it in its log.
func (u Unkept) String() string {
	of := "which the system does not name"
	if u.FileSystem != "" {
		of = u.FileSystem
	}
	return fmt.Sprintf("what is known of the files of %s is not kept across restarts, because its file system (%s) is not known to date a file's changes itself: a daemon started again reads them all once", u.Directory, of)
}

// Unkept returns the repositories of the run that began last whose
// files are known only for as long as this daemon runs, each once: a
// repository is returned by the first call after the run that first
// held it there. A keeper that is saved nowhere says nothing: nothing
// of it is kept.
func (k *Keeper) Unkept() []Unkept {
	k.mu.Lock()
	defer k.mu.Unlock()
	untold := k.untold
	k.untold = nil
	return untold
}

// on returns the file system a directory is on and whether it is known
// to date a file's changes itself.
func (k *Keeper) on(directory string) (string, bool) {
	if k.fileSystem != nil {
		return k.fileSystem(directory)
	}
	return fileSystemOf(directory)
}

// dated learns which of the repositories of a run that begins are on a
// file system known to date a file's changes itself. Each place is
// asked once for as long as the daemon runs: this is on the way of
// every run. The keeper's lock is held.
func (k *Keeper) dated(places map[string]place) {
	for _, at := range places {
		if _, asked := k.dates[at]; asked {
			continue
		}
		fileSystem, dates := k.on(at.Directory)
		if k.dates == nil {
			k.dates = map[place]bool{}
		}
		k.dates[at] = dates
		if !dates {
			k.untold = append(k.untold, Unkept{Directory: at.Directory, FileSystem: fileSystem})
		}
	}
}
