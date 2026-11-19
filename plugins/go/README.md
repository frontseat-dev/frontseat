# Go reference plugin

This is the reference for implementing a Frontseat plugin. It discovers Go
modules, declares actions for the Go tool and arranges inferred local source
dependencies. One Rust crate produces one Wasm module. The current contract is
Describe, Analyze and Arrange; it does not claim every capability of the old
repository.

## Ownership

| File | Responsibility |
|---|---|
| `src/lib.rs` | Private module declarations and SDK `serve!` registration |
| `src/plugin.rs` | Composition of the plugin methods; `arrange` writes the `go.work` bytes for inferred local dependencies |
| `src/manifest.rs` | `go.mod`, dependency origins, and archive/metadata entries from `go.sum` |
| `src/inputs.rs` | Named source sets, embedded assets and test-data selection |
| `src/actions.rs` | Go fetch, metadata, lint, format, compile and test actions, a test for each package that has any, run with a program built apart; the format check's fix; profile choices |
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
| Describe | Names the plugin, interests, `test-timeout`, `test-shards` and `tags` options | Shared SDK refuses incompatible ABI; method reads no source |
| Analyze | Finds a module, provides/requires coordinates with origins, named inputs and complete actions | Absent manifest yields no project; malformed or unsupported directives yield coded diagnostics with paths/help |
| Arrange | Writes a workspace using the pinned Go version and replacements for handed modules | Missing tool pin or invalid handover is refused; valid calls work afterwards |

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
command would refuse too. Where
`test-shards` is more than 1, a package's test is cut into that many actions
at most (`test/<directory>@1ofN`), each running a share of the package's
tests by name and the last everything the others were not told to. Not
served: shards of a package whose tests are one function, as a Ginkgo
suite's are.

Pass a literal directory separately from glob patterns. `repo.glob(root,
patterns)` delegates selection to the host snapshot; it also records the scope
for dependency replay. A name such as `pkg[1]` is not a pattern.

## Tests a new plugin must supply

1. Add the crate to the root Cargo workspace and depend on `sdk/plugin/`. Use `serve!`.
2. Keep ecosystem unit tests beside their module. Exercise composition through
   `served()` with a test host; do not recreate ABI dispatch in the plugin.
3. Put input fixtures under the plugin's `testdata/` directory. Register them in
   `server/internal/pluginhost/conformance_test.go`. Expectations are
   independent typed declarations, required host reads, and exact arranged bytes.
4. Supply valid, absent and malformed Analyze sources, plus valid and invalid
   Arrange requests. An empty arrangement is an explicit empty expected slice.
   Every plugin runs the same real-Wasm suite; there are no per-plugin skip flags.
5. Add API acceptance specs under `specs/build/` that execute the actual declared
   work on the grid. A native composition test cannot prove hermetic execution.

The common harness owns setup, assertions and cleanup. It checks Describe,
Analyze and Arrange, deterministic answers, recovery after errors/cancellation,
concurrent calls and tracked reads. Inventory controls reject an unregistered
crate and missing method/error coverage. Shared SDK tests separately exercise
wire encoding, dispatch, repository access and context interpretation.
The harness also rejects unspecified/unknown profiles before source access and
injects failures at each declared read/glob, including optional absent files.

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

Remediation skills are not implemented yet. The
other plugins (Backstage, Buf, Cargo, apko, release, container, postgres)
are held to the same real-Wasm conformance harness; each README says what
it serves.
