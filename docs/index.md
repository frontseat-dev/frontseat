# Frontseat

Frontseat is a build orchestrator: it builds only what changed, on a
build grid that speaks Bazel's Remote Execution API, with the compilers,
linters and test runners a repository already uses, and it gives each
person and each agent an isolated workspace to build and test in. It is
in preview.

This site is the documentation Backstage shows for the repository that
builds Frontseat. It is for people who change Frontseat.

## How to start

[CONTRIBUTING.md](https://github.com/frontseat-dev/frontseat/blob/main/CONTRIBUTING.md)
has the quick start. In short, with [mise](https://mise.jdx.dev/) and a
container runtime installed:

```sh
mise trust && mise install   # the pinned tools
mise run check               # every check that needs no containers
mise start                   # build Frontseat and start the local stack
frontseat workspace open     # a workspace of the branch you pushed
frontseat test               # run its tests on the grid
```

## Where the reference is

- **Configuration**: everything a repository writes for Frontseat, in
  `frontseat.yaml` and under `spec.frontseat` of its catalog files, is
  in the [configuration reference](configuration.md).
- **The rules every change keeps** are in
  [AGENTS.md](https://github.com/frontseat-dev/frontseat/blob/main/AGENTS.md),
  each with the test that holds it. `build/`, `stack/`, `specs/` and
  `plugins/` each have an `AGENTS.md` of the mistakes made there and the
  check that stops each.
- **The APIs** are the proto packages in `api/`: the build, workspace,
  sandbox, credential and plugin contracts, each beside its catalog
  file. Their comments are the reference.
- **The command line** documents itself: `frontseat --help`, and
  `--help` on each command.
- **Each package** says what it is for in its doc comment.
- **Security**: what Frontseat confines, what is not held yet, and how
  to report a vulnerability are in
  [SECURITY.md](https://github.com/frontseat-dev/frontseat/blob/main/SECURITY.md).

## What the repository holds

| Where | What |
|---|---|
| `api/` | The contracts, and nothing else |
| `sdk/go`, `sdk/plugin`, `sdk/plugin-test` | The Go SDK, the Rust plugin SDK and its testkit |
| `cmd/` | Every server program, each a thin `main`: the control plane (`frontseat-server`), the local stack (`frontseat-stack`), a box's supervisor (`frontseat-box`), the runner and the confiner. The build daemon is the command line's own (`frontseat daemon`, in `cli/`) |
| `build/` | The build: its engine, and the build API served over a directory |
| `workspace/` | Workspaces: the control plane, and what fills a workspace's directory |
| `sandbox/`, `grid/` | The sandbox service and its drivers; the grid's runner |
| `core/`, `common/`, `testsupport/` | What every part is built on, what some parts share, and what tests are helped by |
| `cli/` | The command line, `frontseat` |
| `stack/` | The local stack, one container, and the box image a workspace runs |
| `plugins/` | The plugins, each built to Wasm |
| `specs/` | The acceptance specs, the architecture test and the gate programs |

## Commands

`mise tasks` lists every task with what it does. The ones used most:

| Command | What it does |
|---|---|
| `mise run build` | Builds Frontseat into `dist/`, and the box image |
| `mise start`, `mise stop` | Starts the local stack from this checkout, and stops it |
| `mise run check` | Everything that must pass and needs no containers |
| `mise run test` | Every test, against the local stack |
| `mise run go:test -- ARGS` | Chosen Go tests, with the plugins they load built first |
| `mise run docs` | Builds this site into `site/` |
