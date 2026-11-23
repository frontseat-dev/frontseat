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
| 130 | interrupted (Ctrl-C, `SIGTERM`); nothing more is printed |
| 255 | `workspace ssh`: the SSH client could not get in |

`frontseat run` exits with its program's own exit code, so 1, 2, 36, 37
and 130 from it may be the program's.

What went wrong is said on standard error, after `frontseat:`: the
server's message, the reason it refused by (`[WORKSPACE_NOT_FOUND]`,
listed with each call in the API's contract), and what to do where the
command line knows.
