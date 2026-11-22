# The command line

`frontseat --help` and each command's own `--help` say what each command
does. This page holds what a program that runs `frontseat` relies on:
what it writes, and how it ends.

## `--json`

Plain lines are for people, and may change between releases. A program
gives `--json` (before the program, for `run`): each resource a command
prints is then the API's own message, one to a line, as protojson writes
it, with proto field names and enums by name. There is no second
schema: the message is the one the contract (`api/`) defines.

| Command | Each line |
|---|---|
| `workspace list` | `frontseat.workspace.v1alpha1.Workspace` |
| `workspace describe`, `add`, `open`, `create` | `Workspace`, as it is once it can be worked in (`open` and `create` go on into no shell, unless `--shell`) |
| `workspace delete` | nothing |
| `build`, `test`, `build events` | `frontseat.build.v1alpha1.BuildEvent`, to the one that says how the build ended (a failed action's log is not inlined: `build logs` reads it) |
| `build list`, `build describe` | `Build` |
| `build actions` | `ActionResult` |
| `build logs`, `run logs` | `frontseat.type.v1alpha1.LogLine` |
| `run` | its build's `BuildEvent`s, its program's `LogLine`s, then the `Run` as it ended |
| `run list`, `run describe` | `Run` |
| `ship --dry-run` | `ReleasePlan`: its `etag` is what `ship --etag` takes |
| `ship` | the `ReleasePlan` (unless `--etag`), its build's `BuildEvent`s, then the `Release` as it ended |
| `credential list` | `frontseat.credential.v1alpha1.Credential`, its `secret` always empty |
| `query` | the one exception: one line of query's own answer, whose names are kept |

A failure is one line of JSON on standard error:

```json
{"code":"not_found","reason":"WORKSPACE_NOT_FOUND","domain":"workspace.frontseat.dev","message":"…","exit":1}
```

`code` and `reason` are there where a server refused; `exit` is the
code the command exits with.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | the server refused it by name, or what was asked for did not succeed: a release that failed or is unpublished (`ship`), `query --why` that found no path, a program that was not run (`run`) |
| 2 | the command line was written wrongly (an unknown flag, an argument missing, flags that do not go together), or what this machine keeps cannot be read |
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
