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
| 130 | interrupted (Ctrl-C, `SIGTERM`); nothing more is printed. `workspace forward` serves until it is interrupted, and so ends this way |
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
