# Contributing to Frontseat

Frontseat is in preview: it is under active development, and no release
of it has been published yet. Reproducible bug reports, documentation
corrections and focused changes are useful contributions. This is the
getting-started guide for contributors. Each package says what it is for
in its doc comment; read it before changing the package.

## Quick start

You need:

- Git.
- [mise](https://mise.jdx.dev/), activated in your shell (`mise
  activate`, as its documentation says for your shell). mise installs
  the Go, Rust and other tools this repository pins, and puts
  `dist/bin`, where `mise run build` writes `frontseat`, on your `PATH`
  while you are in this repository. Without activation `frontseat` is
  not found: run it as `mise x -- frontseat …`.
- A native C compiler and linker with development headers: Xcode
  Command Line Tools on macOS, your distribution's build toolchain on
  Linux. Rust build scripts and the Go race detector need it.
- A container runtime answering `docker` that runs Linux containers:
  OrbStack or Docker Desktop. On a Linux machine's own Docker the grid
  is published on the loopback, which the address the runtime gives
  that machine does not reach, so a workspace does not reach the grid
  there. `mise run check` needs none.

Then, in order:

```sh
git clone https://github.com/frontseat-dev/frontseat.git   # or your fork
cd frontseat
mise trust            # let mise read this repository's mise.toml
mise install          # install the pinned tools
mise run check        # every check that needs no containers
mise start            # build Frontseat into dist/ and bring the local stack up
git switch -c your-change
git push -u origin your-change
frontseat workspace open    # a workspace of the branch you pushed, and a shell in it
frontseat test              # there: build what the tests need and run them on the grid
mise stop             # from your own shell: bring the local stack down
```

`mise tasks` lists every task with what it does.

A workspace fetches your branch from its remote and never reads your
checkout, so push before you open it. Where the remote asks for a
credential to fetch from, as a private fork does, give the control plane
one before `frontseat workspace open`:

```sh
frontseat credential set github.com --username YOU < token.txt
```

The token is read from standard input and is never an argument. Give
one that reads only the repository it is for. `frontseat credential
list` lists yours by host, and `frontseat credential delete github.com`
lets go of it.

## When something goes wrong

- **`frontseat: command not found`.** mise is not activated in this
  shell, or nothing was built yet: `mise run build` (or `mise start`)
  writes `dist/bin/frontseat`, and `mise x -- frontseat …` runs it
  without activation.
- **`mise start` stops before it builds anything.** It first asks
  whether the stack can be started from this checkout (`mise run ready`
  asks that alone): the container runtime must answer, and no stack
  another checkout started may be up. Start the runtime; or stop the
  other stack with `mise stop`, from any checkout, and start again.
- **A `frontseat` command says the server does not answer.** The local
  stack is not up: `mise start`.
- **`mise run test` fails at once naming the local stack.** It runs
  against the stack and none answers: `mise start` first. `mise run
  check` needs none.
- **Something in the stack misbehaves.** Every service of the stack
  runs in its one container: `docker logs frontseat-stack` reads what
  they said.
- **A workspace is not what you expected.** `frontseat workspace list`
  lists yours, `frontseat workspace describe NAME` says what
  one holds, and `frontseat workspace delete NAME` ends one.
- **`frontseat build` says a workspace is open.** The workspace you
  last opened is selected on this machine, and is built from inside it:
  `frontseat workspace ssh -- frontseat build`. To build the directory
  you are in instead, `frontseat workspace close` forgets the selection
  here and ends nothing; `frontseat workspace open NAME` selects the
  workspace again.

## Getting started as a contributor, in full

`mise run check` runs the checks that need no containers. Everything
else needs the local stack: one container of your container runtime,
given what nested containers need and nothing of your machine. `mise
start` builds Frontseat and starts the stack, which runs what was built,
and `mise run test` runs the full suite against it:

```sh
mise start
mise run test
```

`mise start` builds what changed and has the stack run it; run it again
after a change to what the stack runs. It is `frontseat-stack up`: it
builds the box image from this checkout every time and names it by what
it holds, so the stack runs this checkout's box whatever another checkout
built since; a stack another checkout started is refused, and `mise
stop` (`frontseat-stack down`) stops it from any checkout. It also lays out the token the
stack's control plane knows you by, in `$XDG_CONFIG_HOME/frontseat/local/token` (else `~/.config/frontseat/local/token`),
which the command line and the specs that ask the stack present; the
control plane answers nothing without it, and serves the sandbox API to
no one.

## Bootstrap with mise, then let Frontseat build itself

mise is the bootstrap, not the build system. It installs the pinned
tools, builds Frontseat once from this checkout into `dist/` (whose
`bin` mise puts on your `PATH` here, where it is activated) together
with the box image workspaces run,
and starts the local stack: `mise start` does all three. From there
Frontseat builds Frontseat.

Commit and push your branch, then open its workspace from the checkout.
A workspace holds what the remote holds: commits you have not pushed and
files you have not committed are not brought.

```sh
git push -u origin your-change
frontseat workspace open
```

At a terminal, `workspace open` leaves you in a shell in the workspace
(`--no-shell` opens it and returns). Build there: what builds a
workspace is run in it, in that shell or as one command from your own
(`frontseat workspace ssh -- frontseat build`). `frontseat workspace
delete NAME` ends it, and asks first:

```sh
frontseat build --only compile --only lint --only generate --only package --only depends-on
frontseat build list
```

The build is held to the actions of those names because they are what
Frontseat builds of itself today, as below; pointed at the whole
repository with no `--only`, it asks for more than that.

`frontseat test` builds what the tests need and runs the test actions of
what it is pointed at, and exits 4 where it points at none. Pointed at
this repository, it runs every test of it on the grid but those that
need the local stack itself, which say so and are skipped there
(`specs/build/self_test.go` holds it).
`frontseat query` says what the workspace is made of, and with `--why`
why one entity depends on another, and at which version its lock
resolves each requirement.
`frontseat version` says which release this frontseat is. Built by mise
or by `frontseat build` it says `0.0.0-dev`; the build a release makes
(`frontseat ship`) writes in the version and the commit it is released
at.

That is what Frontseat builds of itself today, and what the self-build
spec proves (`specs/build/self_test.go`): every Go module compiled and
linted, the API's Go generated by Buf into the Go SDK and the server and
held to the committed copies, the plugin SDK and every plugin compiled
and built to Wasm, and each catalog file's `dependsOn` held to what the
source says. Where a catalog file is out of step, `frontseat build --fix --only
depends-on` writes it in the workspace; commit and push it from there.

The portal (`portal/`) is a Backstage repository, each of its packages a
Component of its own (`portal-app`, `portal-backend`): Frontseat installs
it with Yarn (`plugins/yarn`) and compiles, lints and tests it with the
Backstage CLI (`plugins/backstage`), one `tsc` over it and each package by
its role, so the build above builds it too. Its Backstage release is
1.55.0, every `@backstage/` package it uses pinned to that release in its
root's `resolutions`, as `backstage-cli versions:bump --release 1.55.0`
pins them; raise them together. Its backend ships what its build packed,
`dist/bundle.tar.gz` and `dist/skeleton.tar.gz`, the same for every
platform; an image installs it from them, as Backstage's own recipe does,
and nothing builds that image yet. Run it by hand with `yarn install`, then
`yarn start`, in `portal/`, with the Node and Yarn `mise.toml` pins.

What Frontseat does not build of itself yet still runs through mise: the
gates (`mise run check`, and `mise run test`, which runs the specs that
need the local stack), the generation of the contracts' Go and Rust
(`mise run protos`), the box image (`mise run build`), and the
documentation.

The stack mounts nothing of this checkout: it runs the images built from
it, and keeps what it keeps in its own volume (`frontseat-stack`). Do not
restart another contributor's stack: `mise start` refuses one another
checkout started.

To preview documentation, run `mise run docs:serve` and open
`http://localhost:8000`. When finished with an unshared grid, run
`mise stop`, which stops the stack whichever checkout
started it; its one volume (`frontseat-stack`) remains.

## Make a focused change

1. Read the relevant contract in `api/` and the package documentation.
   Reuse existing SDK and host facilities.
2. For behavior changes, research how the build tools that came before
   (Bazel, Buck2, Pants, Please, Nx, moon, bob) and the ecosystem's own
   rules solve it. Record source revisions, limitations and why the chosen
   approach fits Frontseat in the PR.
3. Write the acceptance spec first and see it fail for the intended reason.
   Specs under `specs/` exercise the public API and real services. Refusal
   cases need valid controls; a test that passes because the grid was
   unavailable proves nothing. Documentation-only edits use documentation
   and claim checks rather than invented runtime tests.
4. Implement in the package that owns the behavior. Keep tests beside
   their owner, share common plugin logic through the SDK, and include
   malformed, cancelled and concurrent cases where relevant.
5. Update user documentation. A hot build path needs measured time/allocation budgets. Do not make
   performance claims without the command and results that support them.

### Upgrade a Go dependency

Every module requires a dependency at the version the workspace selects,
and its go.sum locks it: Frontseat builds each module from what its own
go.sum locks, offline, where `go.work` would quietly take the newer
version. `go get`, `go mod tidy` and `go work sync` fail in a module that
requires a sibling, whose `v0.0.0` only `go.work` resolves, so a version
is raised by hand in every module whose go.mod requires it:

```sh
go mod download -json golang.org/x/oauth2@v0.38.0              # prints its Sum and GoModSum
go mod edit -require=golang.org/x/oauth2@v0.38.0 cli/go.mod    # and each other go.mod that requires it
```

and each of those go.sums is given both `golang.org/x/oauth2 v0.38.0/go.mod
<GoModSum>` and `golang.org/x/oauth2 v0.38.0 <Sum>`, whether its go.mod
requires it directly or `// indirect`: since Go 1.17 an indirect
requirement is a module whose packages the build imports too. What the
new version requires may have to be raised in turn. The architecture
test names each module that requires another version than the workspace
selects, each go.mod or archive line a go.sum lacks of what its own
go.mod requires, any `exclude` or `retract`, and each go.mod line
specs/go.sum lacks of what `go mod graph` reads over the workspace
(specs/go.sum locks the whole workspace's graph, since the test reads it
with nothing else fetched; a dependency added to one module alone needs
its go.mod lines there too). That last is measured, not foretold: the
test runs `go mod graph` with `GOPROXY=off` over a module cache that
holds only the go.mod files specs/go.sum locks, copied from your module
cache, with none of your own go settings (`GOENV=off`, no `GOPRIVATE`),
and names each one the go command could not read. Where the go command
needs one specs/go.sum locks and your module cache lacks it, it says to
run `go mod download` in specs:

```sh
go run ./specs/cmd/gate test -no-stack -only specs/architecture -- -run 'TestEveryModuleRequiresWhatTheWorkspaceSelects|TestEachGoSumLocksWhatItsGoModRequires|TestSpecsGoSumLocksTheGoModOfEveryModuleInTheGraph|TestNoModuleExcludesOrRetracts'
```

It does not check what a module's own graph reads beyond its go.mod:
the go.mod line of a version a dependency requires in turn, newly
reached by the upgrade, which that module's go.sum must lock too. That a
go.sum locks everything its module's build reads is proven only by the
self-build against the stack (`TestRunsThisRepositorysOwnTestsOnTheGrid`).

## Validate the settled change

Batch known fixes before running the gates:

```sh
mise run check
mise run test
```

`check` needs no containers. `test` needs the local stack up (`mise
start`). The Go tests run with the race detector. Read the
command's exit status and the native Go per-test results. `mise run
test` fails before it runs anything where no local stack answers, and
under it and `mise run go:test` a test that needs the stack fails where
the stack is missing; only `go test` run by hand skips it, and skipped
integration coverage is not a successful full validation. Report any
unrun checks and their reason.

`mise run go:test -- ARGS` runs the tests you choose against the stack.
With no stack up, run them as `check` does, over the wasm modules of this
checkout and with every test that needs the stack skipped:

```sh
go run ./specs/cmd/gate test -no-stack -only build/daemon/build -- -race -run TestName
```

`-only` takes a directory of the repository, and what follows `--` is
go test's own.

For documentation changes, run `mise run docs`. Keep docs renderable by
the TechDocs image from the checkout alone.

## Report a problem or suggest a change

Use this repository's issue tracker for ordinary bugs and feature requests.
Include the commit, host architecture, container runtime, command, expected
result and actual result. A small reproduction and the relevant run
diagnostics are more useful than a large log dump. Remove tokens and
private source from anything you share. Report a security issue privately,
as [SECURITY.md](SECURITY.md) says, never in a public issue.

For a substantial feature, explain the use case and discuss the approach
before implementing it. New dependencies, frameworks, formats and changes
to repository rulings require maintainer agreement. API, plugin ABI and
CLI changes must be described before their implementation.

## Send a pull request

Keep the PR focused on one use case. Explain the problem, resulting
behavior, relevant contract changes and validation results. Link the
issue if there is one, include reproduction steps, and say what remains
unsupported or untested. Describe documentation-only changes in terms of
what readers can now find or do.

Someone who did not write the change must review it.
Resolve the findings before treating it as done. Maintainers decide when
to merge; do not bundle unrelated cleanup or changes to other repositories.
Agents must not commit or push unless the owner asks.

## License

Frontseat uses the [Apache License 2.0](LICENSE). Preserve third-party
notices and identify the origin and license of any added code or dependency.
Dependency licensing must meet the repository's rules. Contributor
agreement/sign-off policy is still an owner decision; no additional
process is established by this guide.
