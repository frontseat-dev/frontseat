# Mistakes made in plugins/ and the plugin SDK

The mistakes made in the plugins, the plugin SDK and the plugin
contract (`sdk/plugin`) and the testkit (`sdk/plugin-test`), each with
the check that stops it. The rules every change keeps are in the root
`AGENTS.md`. Run the check; do not trust that you would not make the
mistake.

## The SDK and the contract

| The mistake | The check |
|---|---|
| An integer option passed through a floating-point value, and list entries were joined into text, so distinct values became one. | Defaults, context, SDK access and graph output use one typed union; test int64 limits, values above 2^53, empty values and lists holding commas through real Wasm and the API. |
| A plugin's error kept its location and help only as text, and clients and saved runs lost the structured reason. | Test the same status, reason, source and remediation through the Wasm host, the graph API and a saved run across a daemon restart (`TestAPluginDiagnosticOutlivesItsDaemon`, `TestAPluginFailureRetainsStructuredDetailsThroughTheGraphAPI`), and refuse malformed details beside valid controls. |
| A lossless YAML editor was taken for a strict reader: bad escapes and tags passed, and aliases expanded without a bound. | The SDK reads with a strict semantic reader; exercise syntax, scalar typing and bounded alias expansion through real Wasm, with valid controls. |
| An ASCII identity was Unicode-folded before validation, so Go accepted what the Rust SDK refused. | Run identical ASCII, Unicode and length cases in both runtimes; canonicalization keeps invalid input for refusal. |
| A glob library was taken to match as the host does, and differed on seventeen cases; the contract said three things of a pattern and the host did whatever doublestar does. | The pattern language is said whole in `host.proto`, and what doublestar makes of a pattern that no rule says is left out of it: both matchers refuse it (`pattern.Spelled`, the testkit's `spelled`), the host refuses an answer that holds one (`TestAPluginWhoseInterestsAreNoPatternsIsRefused`). `frontseat-plugin-test`'s matcher is held to Go's by one table both run (`sdk/plugin-test/testdata/select.json`, `server/internal/pattern/select_test.go`); a case either disagrees on is added there, with Go's answer. |
| A refusal's reason was spelled two ways in one plugin's tests: as written past the testkit, upper case through it; and the testkit decoded a refusal with a copy of the SDK's error. | The SDK decodes what it encodes (`Error::from_status`, `a_refusal_is_read_back_as_it_was_written`); the testkit answers that `Error`, and `refused` takes the reason as the plugin writes it. |
| What every plugin does alike was written in each: the fixture exclusion in seven plugins' interests, nested projects, the unpinned tool, a finding's path, a printed `path:line: message`. | It is the SDK's (`repository::NOT_FIXTURES`, which `describe` adds to every plugin's interests; `Repository::nested`, `Repository::pinned`, `diagnostics::Finding`, `diagnostics::Located`); `git grep -n '"!\*\*/testdata/\*\*"' -- plugins` finds none. |

## The plugins

| The mistake | The check |
|---|---|
| A plugin made npm's cache and installation required outputs, though npm makes neither when every optional dependency excludes the platform. | Let npm choose, make those outputs optional, and try empty and platform-excluded locks beside an installed dependency (`specs/build/jest_test.go`). |
| A framework was run directly, without the environment its native script gives it. | Run the package manager's own entrypoint, and prove a setup hook sees its script's variables (`specs/build/testdata/jest/global-setup.cjs`). |
| npm was assumed to refuse every malformed checksum, and installed for garbage, whitespace and unknown algorithms. | Check the checksum's shape with the SDK's strict decoder before npm sees it, with wrong, malformed, empty and unknown controls (`TestNpmTakesOnlyALockIntegrityItCanVerify`). |
| An installation handoff used a workspace-wide package name, which could take another repository's lock. | A framework asks for `{root}` and requires the placement `.` (`TestProvidedArtifactsUseTheExistingGraphAndRootPlacement`, `TestJestKeepsInvocationSemanticsAndRequiresItsOwnInstallation`). |
| Catalog graph and rewrite paths read YAML differently: aliases hid dependencies and editing an anchor changed other fields. | Exercise aliases, merges and both dependency directions through the readers, rewrites and the build API, with bounded traversal. |
| A catalog plugin worked in the test daemon and was missing from the box. | Exercise discovery, refusal, repair and a build through the real sandbox API; the box carries every plugin. |
| A plugin's test harness accepted empty declarations or harmless commands in place of builds. | Fixtures specify Describe, whole project and action messages, host reads and arranged bytes on their own, and a mutation of each fails. |
| Each plugin's tests wrote a host of their own, globbing as the author guessed and calling the plugin past the SDK's dispatch. | Test through `frontseat-plugin-test` (`sdk/plugin-test`): `TestHost` globs as the host does and `TestPlugin` asks through the SDK's dispatch; `git grep -n 'impl Host for' -- plugins sdk` finds only `WasmHost` and `TestHost`. |
| A formatter was asked to change a file on a worker, where inputs are read-only. | On the grid a check only checks; what writes source runs where the checkout is, proven with the real tool in the sandbox (`specs/sandbox/checks_test.go`); read bb-remote-execution's `blob_access_cas_file_factory.go` before assuming a worker lets a tool write. |
| A lock's tables were cut assuming `[metadata]` comes last. | Bound a span by the next span in position order, and test an order the format allows and the tool never writes. |
| A plugin's refusal advised what it would then refuse (cargo's "or exclude it"), and its answers included servers that could never become ready. | Each accepted form is a fixture whose answer is whole, each refused one is refused by its reason (`postgresFixture`), and a refusal's own help is followed and accepted (`a_package_the_workspace_excludes_is_built_as_a_package_of_its_own`). |
| The Cargo plugin built every `cdylib` to wasm where the repository pinned wasm's standard library: this repository's own build, guessed from a pin. | What a plugin builds beyond its ecosystem's own is an option the repository sets (cargo's `wasm`), with the option unset as the control. |
| A number a repository sets was cut to the module's 32-bit word by `as`. | Convert what crosses into a module with `try_from`, held by a test through the wasm (`TestAShardCountPastAModulesWordIsAsManyAsThereAreTests`). |
| A Go test program was started bare, where `go test` adds `-test.paniconexit0`, and a test that called `os.Exit(0)` passed. | Start what a tool builds as the tool does (`go test -x`), and hold each difference by a spec (`A test that ends its program as if all had passed has not passed`). |
| The go command's rule for which files it builds was written again from its documentation, disagreed with `go list`, and recursed without bound. | Port a rule from the original's source and hold it to the original by a differential run with hard files (`differs_from_the_go_command_nowhere`, `TRICKY`); bound whatever recurses over a repository's file. |
| What the go command decides on the worker was foretold from source, and review after review found a package left untested without a word. | Let the tool decide: name every candidate by a plain rule, make what it may leave optional, and idle where nothing was left (`Action.idle_without_inputs`); stop at the second review that finds the same kind of defect, and change the design. |
