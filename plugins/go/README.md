# Go plugin

It builds Go modules: it finds a module wherever a `go.mod` is, declares
the go command's actions for it (fetch, lint, format, compile, test, and
a program to run for each `package main`), arranges the modules it was
handed as a Go workspace, and reads what its checks found from what they
printed. One Rust crate produces one wasm module. It serves Describe,
Analyze, Arrange and Report; the rest of the contract it answers
`UNIMPLEMENTED`.

It is the largest plugin, not the place to start writing one: the small
example is `sdk/plugin/examples/make.rs`, and how a plugin is written,
built, enabled and tested is in `sdk/plugin/README.md`.

## Ownership

| File | Responsibility |
|---|---|
| `src/lib.rs` | Private module declarations and SDK `serve!` registration |
| `src/plugin.rs` | Composition of the plugin methods; `arrange` writes the `go.work` bytes for inferred local dependencies |
| `src/manifest.rs` | `go.mod`, dependency origins, and archive/metadata entries from `go.sum` |
| `src/inputs.rs` | Named source sets, embedded assets and test-data selection |
| `src/actions.rs` | Go fetch, metadata, lint, format, compile and test actions, a test for each package that has any, run with a program built apart; the format check's fix; profile choices; the findings `report` reads |
| `src/programs.rs` | Which directories are programs, read as go/build reads them and held to the go command by a differential test, and the action that runs each |
| `src/composition_tests.rs` | Native tests through the registered `served()` plugin |
| `testdata/` | Independent valid and malformed ecosystem fixtures |

`plugin.rs` composes the other modules. They use contract types and repository
services from `frontseat-plugin`, the Rust SDK in `sdk/plugin/`. They never call another
plugin or a server implementation.

The SDK owns dispatch, encoding, ABI negotiation, repository reads, scoped globs,
profile and option access, and structured errors. The host owns authoritative
contract validation and tracking/invalidation of repository reads. Go supplies
only ecosystem behavior. Do not copy common logic into a new plugin.

The plugin ABI uses `PluginOption` declarations with `String`, `Bool`, `Int` and
`StringList` values. The SDK rejects unknown, missing and mistyped options
before ecosystem code reads source. Integer values remain exact signed int64s;
lists retain individual entries, including empty strings and commas.

The context must name development or production explicitly. `repo.profile()`,
`repo.read()` and `repo.glob()` return `Result`; propagate errors with `?`.
Use `repo.text()` for UTF-8 source, `repo.toml()` for TOML and `repo.yaml::<T>()`
for typed YAML. These readers preserve absent files, track source reads, apply
shared validation/resource limits and return source diagnostics. YAML types
use Serde; common parser policy belongs to the SDK. Go does not replace bad
text bytes or treat a failed source operation as an absent file.

Return the SDK's `Error` with a canonical `Code`, a lower-kebab-case `reason`,
message and optional path/line/help. The SDK produces `ErrorInfo` and the shared
`Diagnostic`; the host validates them and binds the repository. Graph requests
and saved runs retain the same structured details. Error formatting and wire
encoding do not belong in a plugin.

## Served behavior

| Method | Go behavior | Error/control |
|---|---|---|
| Describe | Names the plugin, interests, `test-timeout`, `test-shards`, `tags` and `version` options | Shared SDK refuses incompatible ABI; method reads no source |
| Analyze | Finds a module, provides/requires coordinates with origins, named inputs and complete actions | Absent manifest yields no project; a malformed go.mod or go.sum is refused `invalid-manifest`, and a `replace` or a directive it does not read `directive-not-served` (`UNIMPLEMENTED`), each with its path, line and help |
| Arrange | Writes a workspace using the pinned Go version and replacements for handed modules | Missing tool pin (`unpinned-tool`) or invalid handover is refused; valid calls work afterwards |
| Report | Reads `go vet`'s `path:line[:col]: message` lines and `gofmt -l`'s files into findings | A line that names no file beneath the module is no finding |

The host resolves project requirements. Users never write their own dependency
graph. Each module the `go.sum` locks is fetched by a shared
action of its own (`fetch/<module>@<version>`, or `metadata/<module>@<version>`
where only its `go.mod` is locked), told that module's lines of the lock and
nothing of the project; Go fetches it through the proxy and verifies it
against those lines. A malformed `go.sum` line is refused where it is. Lint, format,
compile and test run offline. Lint and format are checks: `go vet` and
`gofmt -l` write nothing and fail on what they find, which `report` turns
into `go/vet` and `go/format` findings with their files and lines; format's
fix, `gofmt -w`, runs only where the checkout is, under `frontseat build
--fix`. A module's inputs stop at a nested module's `go.mod`. Development compilation disables optimization and inlining;
production uses Go's optimized compilation. Both trim worker paths.

A module's tests are an action for each package that has test files the go
command builds on a worker, `test/<directory>`, and `test/.` for the package
at the module's root. `test-build` builds their test programs together (`go
test -c -trimpath`), and each test runs its own program in its package's
directory as the go command runs it (`-test.paniconexit0`), given that
program, every file of its package's directory and its `testdata`, and
the modules its module's compile is given, for a test that runs the go
command itself. A test that opens a file anywhere else does not find it,
unless its Component's catalog file names it (`spec.frontseat.test.inputs`,
a path from the repository's root written `/…`). A directory with tests is handed to the
go command as a pattern where no package this reading sees is beneath it,
so one it has nothing to build of is passed by, and outright where
packages are beneath it, so that it never reaches one analysis does not
see (generated source); what could write two test programs of one name is
built by an action each (`test-build-1`). A module with
no test file has no test action. Which packages have tests on a worker is
the go command's to say there: a test whose program it did not build, as
for tests of another system or behind a tag nobody gave, is not run and
ends idle. Refused by name: a directory with test files whose name the go
command would refuse too. Every test program is started with
`test-timeout`, a length of time as the go command writes one (`90s`,
`1h30m`; `0` is no limit): one it would not read, or less than nothing, is
refused where the module is read (`invalid-test-timeout`). Where
`test-shards` is more than 1, a package's test is cut into that many actions
at most (`test/<directory>@1ofN`), each running a share of the package's
tests by name and the last everything the others were not told to. Not
served: shards of a package whose tests are one function, as a Ginkgo
suite's are.

What a module ships is its programs, made for each platform its entity
ships for (`spec.frontseat.platforms` in its catalog file): for the
production profile, an action `package/<program>-<os>-<arch>` for each
program and platform, the go command building that program for that
system (`GOOS`, `GOARCH`), which leaves `_package/<program>-<os>-<arch>`:
an artifact of a release. Without cgo, a program is made for any system
from any worker. Not served: which directories hold a program is read
for Linux, as for everything else of a module, so a program that has no
files for a platform its entity names fails its package, and one that
has files for another system alone is not packaged; `.exe` is not added
for Windows; each package compiles the module anew.

A module's programs say the version they were released at where the
`version` option names the variables that hold it, each
`<package>.<variable>=<what>`, a string variable of a package by its
import path (`main` for a program's own) and what of the release it is
set to: `version`, `tag`, `commit` or `unit`.

```yaml
plugins:
  go:
    version:
      - example.com/greet/internal/build.Version=version
      - main.commit=commit
```

The compile is then versioned (`-ldflags=-X …`), and it alone: the host
fills in what a release planned, and `0.0.0-dev` and nothing for any
other build. The linker sets a variable where its package is linked in
and says nothing where it is not, so one list serves a repository. A
Component's catalog file may set a list of its own, which is then the
whole list for its modules, in place of the repository's. Refused by
name (`invalid-version-variable`): what names no variable of a package,
what is set to nothing a release has, and a variable set twice.

A module is compiled by one action, so `main.<variable>` is set in every
program of the module alike, and each is given what the release plans
for the Component that owns the module: a program of another Component
nested in the module's directory says the module's owner's version.

Pass a literal directory separately from glob patterns. `repo.glob(root,
patterns)` delegates selection to the host snapshot; it also records the scope
for dependency replay. A name such as `pkg[1]` is not a pattern.

## Tests

Its own logic is tested natively, beside each module, and through
`served()` with the testkit (`frontseat-plugin-test`) in
`src/composition_tests.rs`. The built module is held to the real-wasm
conformance harness every plugin is (`server/internal/pluginhost`,
`conformance_test.go`), with the fixtures in `testdata/`; what it builds
on the grid is held by the API specs below.

## Capability evidence

| Capability | API spec |
|---|---|
| Go source, assembly, embeds, literal project names, test fixtures | `specs/build/go_test.go` |
| A test for each package, run again only where a change reached it | `specs/build/tests_test.go` |
| Local dependency inference and handover | `specs/build/requires_test.go` |
| Locked archive/metadata fetch and checksum refusal | `specs/build/dependencies_test.go` |
| Profiles and plugin options | `specs/build/profiles_test.go`, `configuration_test.go` |
| Structured plugin diagnostics through graph and run APIs | `specs/build/diagnostics_test.go` |
| Programs consumed by other actions | `specs/build/programs_test.go` |
| A program for each `package main`, run where the daemon is (`run/<name>`) | `specs/sandbox/runs_test.go` |
| Real client/server module builds and input invalidation | `specs/build/modules_test.go` |
| Confinement and input immutability | `specs/build/safety_test.go` |

Run `mise run check` for native, Wasm conformance, architecture and budget checks.
Run `mise run test` with the local stack for API/grid proofs. Acceptance tests
use standard Go tests with Gomega assertions. Independent build cases run
concurrently with bounded admission; the acceptance harness itself still runs
on the host while its builds execute on the grid.

Every other plugin (apko, Backstage, Buf, Cargo, container, Jest, npm,
postgres, release) is held to the same real-wasm conformance harness; each
README says what it serves.
