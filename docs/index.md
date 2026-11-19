# Frontseat

Frontseat is a remote-first, agent-first build tool. This site is the
documentation Backstage shows for the repository that builds it.

## Where to start

- [CONTRIBUTING.md](https://github.com/frontseat-dev/frontseat/blob/main/CONTRIBUTING.md)
  says how to install the pinned tools, bring the local stack up, run the
  gates and have Frontseat build itself.
- [AGENTS.md](https://github.com/frontseat-dev/frontseat/blob/main/AGENTS.md)
  holds the rules every change keeps, each with what holds it; each part
  of the repository (`server/`, `local/`, `specs/`, `plugins/`) has one
  of the mistakes made there and the check that stops each.
- The contracts are in `api/`: the build, workspace, sandbox, credential
  and plugin APIs, each a proto package beside its catalog file.

## What the repository holds

| Where | What |
|---|---|
| `api/` | The contracts, and nothing else |
| `sdk/go`, `sdk/plugin`, `sdk/plugin-test` | The Go SDK, the Rust plugin SDK and its testkit |
| `server/` | Every server program: the daemon, the control plane, the sandbox service and the runner |
| `cli/` | The command line, `frontseat` |
| `local/` | The local stack, one container, and the box a workspace runs |
| `plugins/` | The plugins, each built to Wasm |
| `specs/` | The acceptance specs, the architecture test and the gate programs |

## Commands

| Command | What it does |
|---|---|
| `mise run build` | Builds Frontseat into `dist/`, and the box image |
| `mise start`, `mise stop` | Brings the local stack up from this checkout, and down |
| `mise run check` | What must always pass; needs no stack |
| `mise run test` | Everything, against the local stack |
| `mise run docs` | Builds this site into `site/` |
