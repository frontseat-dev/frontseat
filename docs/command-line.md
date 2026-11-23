# The command line

`frontseat --help` and each command's own `--help` say what each command
does. This page holds what a program that runs `frontseat` relies on:
how it ends.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | the server refused it by name, or what was asked for did not succeed: a release that failed or was not pushed (`ship`), `query --why` that found no path, a program that was not run (`run`) |
| 2 | the command line was written wrongly (an unknown flag, an argument missing, flags that do not go together), what this machine keeps cannot be read, or the command line refused it of itself, asking no server: `build`, `test`, `run`, `query` or `ship` run outside a sandbox while a workspace is open, which says to run it in the workspace (`frontseat workspace ssh`) |
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
| outside a sandbox, with no workspace open | the directory it is run in: the workspace or repository it is part of | `build.yaml` in frontseat's configuration directory ([What a build daemon is told](build-settings.md)) |
| outside a sandbox, while a workspace is open | nothing: it is refused, exit 2, and says to run it in the workspace | |

A workspace is built from inside it: nothing builds one from outside,
and a control plane neither serves nor forwards the build API.
