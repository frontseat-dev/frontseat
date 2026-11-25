# What a build daemon is told

Every command that works on what is where it is run, `frontseat build`,
`test`, `run`, `query` and `ship`, and those that list or read what one
of them made (`build list`, `build logs`), is served by a build daemon,
which the command starts for the directory it is run in. The daemon
knows that directory and one file of settings: its plugins, the grid its
actions run on, the platform of the grid's workers, and what a publish
may be given.

| Where | The file |
|---|---|
| in a workspace's sandbox | `/var/lib/frontseat/build.yaml`, written by the sandbox's supervisor from what the control plane told it |
| anywhere else: a machine, a job | `build.yaml` in `$XDG_CONFIG_HOME/frontseat` (else `~/.config/frontseat`) |

```yaml
version: 1
# A directory: every <name>.wasm in it is loaded.
plugins: /opt/frontseat/plugins
grid:
  address: grid.example:8980
  instance: frontseat/p/b
  # The grid's asset service, which fetches what a build downloads.
  assets: assets.example:8983
  # A PEM file of the authorities the grid is known by; with it the
  # grid is asked over TLS. `tls: true` asks over TLS by the system's.
  authorities: /etc/fleet-ca/ca.crt
  # A file holding the token the grid admits the daemon by, read again
  # for every call. It is given with authorities.
  token: /run/secrets/grid-token
  # Which proxy of the grid's stands in for a public site a download
  # names: the first that matches, in the order given.
  rewrites:
    - from: '^https://static\.crates\.io/crates/([^/]+)/([^/]+)/download$'
      to: 'http://mirror:8080/cargo/$1/$2/download'
# What the grid's workers are: every action asks for these.
platform:
  OSFamily: linux
  ISA: x86-64
  container-image: docker://frontseat.dev/workers/v1
publish:
  # Variables of where the daemon runs that what publishes a release
  # may be given, by name.
  environment: [GH_TOKEN]
```

Only `version`, `plugins` and `grid.address` must be said. Told no
platform, the daemon asks for Linux on its machine's architecture; told
nothing of TLS, it asks the grid in the clear, as the local stack's is
asked.

The file is read strictly, by every command and when the daemon starts:
a key that is not listed here, another version, a rewrite that is no
regular expression or plugins that are not there is a mistake, and
nothing is built. It is one document of no more than a mebibyte, written
plainly: an anchor, an alias, a merge key (`<<`) and a tag are refused,
`version` is the integer `1`, `grid.tls` is `true` or `false`, and every
other value is a text on one line, so one that would be read as a number,
a truth or nothing is written in quotes. `grid.address` and `grid.assets`
are each a host and a port. In the directory of plugins a `<name>.wasm`
is a file: a link or a directory of that name is refused. A path is from
the file's own directory. The file holds no secret: a token is the path
of its file.

The file says which plugins run and which grid a token is sent to, so it
is yours alone to write: a command refuses one that its group or others
may write, one that is a link, and one in a directory they may write
(`chmod 600` the file, `chmod 700` its directory). In a workspace's
sandbox the supervisor that writes it and the command run as one user.

A daemon is replaced by the next command when the content of what it is
made of has changed: its program, its plugins, its settings, the
authorities its grid is known by, or what a publish may be given, the
variables `publish.environment` names as they are where the command is
run. Each is compared by content, read whole by every command, never by
a file's time or size. The daemon that was there is asked to stop and
another is started, unless it has something in hand, a build, a run or a
release: that one is left, and the command ends, saying in what the
daemon differs. Wait for what it has in hand, or cancel it from where it
was started. A daemon with nothing to do for three hours ends itself.

What a daemon keeps, its builds, what it knew of the directory's files
when it ended (`files`, so that the daemon started after it reads only
the files that have changed since) and what it says of where it serves
(`daemon.json`: its address on the loopback and the token it is asked
with, its user's alone to read), is in a directory named for the
directory it builds: beneath `build/` beside the file, and in a
workspace's sandbox beneath `/var/lib/frontseat/build`. The file is
believed only while the daemon holds that directory's lock
(`daemon.lock`): what a daemon that died left there is removed by the
next command, which starts one. What a daemon printed is beside it
(`daemon.log`), begun anew once it is over four mebibytes. What a daemon
knew of a repository's files is kept for the daemon after it only where
the repository is on a file system known to date a file's changes
itself, which is what tells a file rewritten with its times set back
from one left alone: APFS and HFS+ on macOS; ext4, XFS, Btrfs, tmpfs,
F2FS, bcachefs, ZFS and an overlay on Linux. On any other (FAT, exFAT,
a network or FUSE mount, virtiofs, 9p, or one the system does not name)
nothing of it is kept, a daemon started again reads every file once,
and the daemon says so in `daemon.log`, once for each repository. In a sandbox
the command line starts the daemon as it does anywhere, for the
workspace's directory: its supervisor writes the file, removes it where
it is told no grid, and builds nothing.

The directory a command builds is the nearest above where it is run that
holds a workspace's manifest or a repository's `frontseat.yaml`, and is
yours: one that another user owns is passed over, as git passes over a
repository that is not its user's.

In a sandbox the file is the sandbox's user's to write, as everything
beneath `/var/lib/frontseat` is. The supervisor writes it when it
starts, which holds against an accident and not against a program in
the sandbox, which may write another or start a daemon told otherwise.
A sandbox is one trust domain: whatever runs in it reaches what its
build daemon reaches, the grid among it, and no credential of the grid
or of a forge is kept in it.
