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
frontseat workspace credential github.com --username YOU < token.txt
```

The token is read from standard input and is never an argument. Give
one that reads only the repository it is for. `frontseat workspace
credential github.com --forget` lets go of it.

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
  runs in its one container: `docker logs frontseat-local` reads what
  they said.
- **A workspace is not what you expected.** `frontseat workspace list`
  lists yours, `frontseat workspace describe --workspace NAME` says what
  one holds, and `frontseat workspace delete NAME` ends one.

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
after a change to what the stack runs. It is `frontseat-local up`: it
builds the box image from this checkout every time and names it by what
it holds, so the stack runs this checkout's box whatever another checkout
built since; a stack another checkout started is refused, and `mise
stop` (`frontseat-local down`) stops it from any checkout. It also lays out the token the
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

At a terminal, `workspace open` leaves you in a shell in the workspace. Build there,
or from your own shell with `--workspace NAME`:

```sh
frontseat build --only compile --only lint --only generate-sdk --only generate-daemon --only generate-runner --only generate-source --only wasm --only depends-on
frontseat builds
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

What Frontseat does not build of itself yet still runs through mise: the
gates (`mise run check`, and `mise run test`, which runs the specs that
need the local stack), the generation of the contracts' Go and Rust
(`mise run protos`), the box image (`mise run build`), and the
documentation.

The stack mounts nothing of this checkout: it runs the images built from
it, and keeps what it keeps in its own volume (`frontseat-local`). Do not
restart another contributor's stack: `mise start` refuses one another
checkout started.

To preview documentation, run `mise run docs:serve` and open
`http://localhost:8000`. When finished with an unshared grid, run
`mise stop`, which stops the stack whichever checkout
started it; its one volume (`frontseat-local`) remains.

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
go run ./specs/cmd/gate test -no-stack -only server/internal/build -- -race -run TestName
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
