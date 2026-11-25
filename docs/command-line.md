# The command line

`frontseat --help` and each command's own `--help` say what each command
does. This page holds what a program that runs `frontseat` relies on:
how it ends.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | the server refused it by name, or what was asked for did not succeed: a release that failed or was not pushed (`ship`), `query --why` that found no path, a program that was not run (`run`) |
| 2 | the command line was written wrongly (an unknown flag, an argument missing, flags that do not go together), what this machine keeps cannot be read, or the command line refused it of itself, asking no server: `build`, `test`, `run`, `query` or `ship` run outside a sandbox while a workspace is selected, which says to run it in the workspace (`frontseat workspace ssh`) or to forget the selection (`frontseat workspace close`) |
| 3, 4, 8, … | a build's own exit code, as the build API names them (`build`, `test`, `build events`): 3 a test failed, 4 no tests were found (`NO_TESTS_FOUND`), 8 interrupted |
| 36 | the server refused who asked: no token, or a token it does not take (`UNAUTHENTICATED`, `PERMISSION_DENIED`) |
| 37 | the server did not answer (`UNAVAILABLE`, `DEADLINE_EXCEEDED`), or stopped answering before what was followed ended |
| 130 | interrupted (Ctrl-C, `SIGTERM`); nothing more is printed. `workspace forward` serves until it is interrupted, and so ends this way; so do `build --watch` and `test --watch`, which build until they are |
| 255 | `workspace ssh`: the SSH client could not get in |

`frontseat run` exits with its program's own exit code, so 1, 2, 36, 37
and 130 from it may be the program's.

What went wrong is said on standard error, after `frontseat:`: the
server's message, the reason it refused by (`[WORKSPACE_NOT_FOUND]`,
listed with each call in the API's contract), and what to do where the
command line knows.

## What a command that builds asks

`build`, `test`, `run`, `query` and `ship` work on what is where they
are run, and ask a build daemon `frontseat` starts for itself (this
program again, `frontseat daemon`), on the loopback, with that daemon's
own token; no control plane is asked. A daemon serves one directory,
and ends itself once it has had nothing to do for long enough.

| Where the command is run | What it builds | What the daemon is told |
|---|---|---|
| inside a workspace's sandbox | the workspace: its directory, the nearest above that holds `.frontseat/workspace.yaml`. A workspace that was told nothing to hold yet has none, and the command says so | `/var/lib/frontseat/build.yaml`, which the sandbox's supervisor wrote from what the control plane told it |
| outside a sandbox, with no workspace selected | the directory it is run in: the workspace or repository it is part of | `build.yaml` in frontseat's configuration directory ([What a build daemon is told](build-settings.md)) |
| outside a sandbox, while a workspace is selected | nothing: it is refused, exit 2, and says to run it in the workspace, or to forget the selection | |

A workspace is built from inside it: nothing builds one from outside,
and a control plane neither serves nor forwards the build API.

## Building again as files change

`frontseat build --watch` and `frontseat test --watch` build what they
were pointed at, and then again each time something a build reads
changed, until they are interrupted. They take what `build` and `test`
take (targets, `--only`, `--profile`), inside a workspace's sandbox and
outside one.

Each round is a build of its own, printed as `build` prints one. It
lists and reads the files itself, and runs what the change reaches: the
rest is answered from the cache. After a round, the command asks the
build daemon that built it to wait until anything that build listed or
read looks otherwise than the build held it to
(`BuildService.WaitSourceChanged`); the command looks at no file
itself. Before every round after the first, one line says which files
changed:

```
watching /work: it is built again each time a file of it changes, until interrupted
...
code/hello/main.go changed: building again
...
a.go, b.go, c.go and 4 more changed, 7 files: building again
```

| What | How |
|---|---|
| What is watched | what the round's build listed and read: the files of the directory as a build lists them (not what `.frontseatignore` names, nor what is in a directory whose name begins with a dot, as `.git`), the links the listing followed, every file the build read by name that no listing takes (a plugin's `.cargo/config.toml`), there or not, read or not, each repository's `.frontseatignore`, listed or not, the workspace's manifest, and what the build daemon is made of: its settings, the plugins they load and the directory they are in, where a plugin added or removed counts, the authorities they name and the program. A file in a directory whose name begins with a dot that no build read (`.git/index`, `.idea/workspace.xml`) starts no round |
| What counts as a change | a file that does not look as the build held it: its size, when it was written, when anything of it last changed, which file of the disk it is, and its mode; a file made, removed or moved. It is what the build daemon itself goes by to read a file again. Each link a build follows is looked at so too. A directory is not: a file made in one the build listed, or removed, counts by its name, and one `.frontseatignore` names does not, nor does a file made and removed again after the build listed its directory, which the build never read, nor a file the build itself rewrote (`--fix`). A file the build asked for by name and could not read (one it may not read, or one saved as it was read) counts once it looks otherwise than it did then; one it could not look at (a link out of the repository, a directory on the way that may not be looked in) or that was no file (a directory, a pipe) counts once it is anything else, not there among it. A file of the directory touched with nothing of it changed counts, since how it looks is all that is asked of it, and its round is answered from the cache; what the daemon is made of and the manifest do not (below). A file the build read within the moment its times are told in after it was written is read again once that moment has passed, and counts where it holds otherwise than the build read it |
| When a round starts | once two looks, a quarter of a second apart, are the same and every file and link was written longer ago than the moment its times are told in, which is the build daemon's own rule for a file read before it settled: a save or a checkout still under way is waited out. A file said to have changed at a time still to come (a mount whose clock is ahead) never settles by waiting: it keeps nothing else from starting a round, and where the build read it, it is read again each time the rest has settled |
| A change while a round is under way | the round is followed to its end, and one round follows it: each round's build holds every file to how it looked when the build began, or when it read it, so whatever is written from then on starts the round after. A round is never cancelled for a change: what it ran is in the cache for the next, a build reads a file that changed under it as a failure that names the file, never as the file from two moments, and a round cancelled at every keystroke would say nothing while someone types |
| A round that failed | waits for the next change, as one that passed does: a build that failed and a test that failed. A build that could not list a repository (a directory of it that may not be read) waits for a file of the others to change, or for that repository to be listed again. A build the daemon refused of the directory as it is (a workspace being filled) built nothing to wait on: it is said, and asked for again a second later, until it is built |
| The daemon replaced | each round asks the daemon there is then: where its settings, its plugins or the program were written, the wait answers naming them, and the round finds or starts the one there is, as any command does. Waiting keeps the daemon from ending itself for having nothing to do, and does not keep another command from replacing it: the wait then fails, and the round after asks the daemon there is then, with nothing written. A daemon found as it was ending refuses the build by name (`DAEMON_ENDING`) and builds nothing: the round is asked again at once, of the daemon there is by then, up to three times running. A daemon keeps what a build read for the four newest builds that ended, and no daemon started again keeps it: a wait it cannot answer (`BUILD_SOURCE_NOT_KEPT`) is a round, with nothing written |
| What waiting costs | the daemon looks again at what the build listed and read four times a second, and never for more than a tenth of the time: of this repository, 8 ms a look on an Apple M4 Max, about 3% of one core (`BenchmarkAnIdleWait`). It keeps what a build listed and read for each build under way and the four newest that ended, and each wait under way a copy to hold it against: of this repository's 1,273 files, 480 KB a build and 185 KB a wait (`BenchmarkWhatAWaitKeeps`) |
| How it ends | interrupted, exit 130, at once, whether a round is under way or the daemon is waiting; or with the code of what could not be asked: a daemon that did not answer, or was ending three times running (37), one that has a build in hand and is not the one this command would start (1); a wait that could not be answered twice running ends it with the code of why |
| What it does not go with | `--fix`, which writes the files a watch builds again for: refused, exit 2 |

What the daemon is made of, and the workspace's manifest, are held by
one rule: such a file counts exactly when what it holds now differs
from bytes the daemon or a build used of it. Each is looked at as it
is opened to be read, and nothing is waited for; what is kept of it is
the SHA-256 of exactly the bytes read, whatever was done to the file
as it was read. It is read again where it looks otherwise, where it
was written within the moment before it was read (once that moment has
passed), where two readings used different bytes of it (which always
counts), and, on a disk whose clock is ahead, whenever the rest has
settled. Written again with what was read (touched, its mode changed,
copied over, a file of the same bytes moved over it, a plugin installed
again), during the read or after, it is read again once, by the wait
that finds it, and starts no round; removed, made unreadable, no
longer a file or holding otherwise, it does. One that could not be
opened counts once it looks otherwise. One whose reading failed used
no bytes that are known: it counts once it looks otherwise, or once
it has settled where it was read before it had, and the round after
reads it again, so it starts one round, not one after another. What the daemon is made
of is what it read when it started, once each: the directory of
plugins as it listed it, so a plugin added after it started counts.

What it costs: a file a build read whose disk says it changed at a
time still to come (a disk whose clock is ahead) is read again whole
about twice a second while a wait lasts; the daemon's own program is
tens of megabytes.

Not held: on a file system that does not date a file's changes itself
(FAT, exFAT, some network mounts) a file written again at its size with
its times set back looks as it did; and the daemon's program is held
as the daemon read its file, while what runs is what the system loaded
when it started.

## Which workspace is selected

Opening a workspace (`workspace open`, `workspace create`) selects it
on this machine: a `workspace` command told no name asks it, and
`build`, `test`, `run`, `query` and `ship` are run in it, and refused
outside it. The selection is this machine's alone, kept in
`context.json` in frontseat's configuration directory.

| What forgets it | What it does besides |
|---|---|
| `frontseat workspace close` | nothing: no control plane is asked, and the workspace goes on. `workspace open NAME` selects it again. A `context.json` the command line cannot read is removed |
| `frontseat workspace delete NAME`, of the workspace selected | ends the workspace; the selection is forgotten too where the control plane answers that there is no such workspace |
| opening another workspace | selects that one |

A workspace last opened of another control plane than the one the
command line asks now is none selected here: `build` and the rest work
on the directory, and a `workspace` command told no name says whose it
is. Two addresses are one control plane where they are one origin: the
scheme and the host in whatever case, with or without a scheme's own
port (80 of `http`, 443 of `https`). Nothing is resolved: `localhost`
and `127.0.0.1` are two.

A command that builds this machine's directory reads neither the
control plane's address nor its token. Where a server did not answer
(37) or refused who asked (36), the command says which it asked: the
control plane, or the build daemon of the directory, with the file that
holds what that daemon printed.

Inside a sandbox is where the sandbox's supervisor runs: the command
line knows it by the sandbox's lock (`/var/lib/frontseat/box.lock`),
which names the workspace and which the supervisor holds for as long as
it runs. A file of that name that no one holds, left by an earlier
program, is not a sandbox.

## What a carried port is open to

`workspace forward` listens on this machine's loopback and nowhere
else, so nothing on another machine reaches it. Every program of this
machine does, whoever runs it: while the command runs, any of them
reaches the workspace's port through it, and is asked nothing. What the
port serves is what decides who may use it.

A workspace is carried 64 connections at once, its shells among them;
one more is refused (`TUNNELS_EXHAUSTED`) until one has ended. A port
nothing listens on in the sandbox is refused too (`PORT_UNREACHED`),
and a workspace that is in no sandbox yet (`WORKSPACE_NOT_READY`).
