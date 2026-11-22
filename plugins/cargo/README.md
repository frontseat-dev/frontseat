# Cargo plugin

Builds Rust packages with cargo: the packages of a Cargo workspace, each
a project of its own, or a single package at a repository's root.

## What it reads

A `Cargo.toml` with a `[package]` table is a project. The nearest
manifest above it with a `[workspace]` table is its workspace root; a
package the workspace neither has as a member nor excludes is refused,
as cargo refuses to build it, and one it excludes is a package of its
own, at its own root, as cargo has it. A root manifest with no package is no project. The
project provides `cargo/<name>` and requires `cargo/<name>` for each
dependency found by path, including one inherited through
`[workspace.dependencies]`, with the line that says so. A package's
files are its directory, except `target/` and any package nested in it.

The workspace's `Cargo.lock`, `.cargo/config.toml`, `rustfmt.toml` and
`clippy.toml` are the project's workspace files, read where they are. A
workspace with no `Cargo.lock` is refused: a package is built from its
lock alone.

## What it runs

Every action runs cargo in the package's directory, with the Rust the
repository pins, from the lock, offline. Before any action, the plugin
writes the workspace manifest narrowed to the packages present: the
package and the dependencies it was handed, each at its own root.

Crates come from the lock alone. cargo cannot fetch one crate by itself,
so each crate of crates.io the package depends on, as `Cargo.lock` lists
it, is a shared fetch of its own, `fetch/<name>@<version>`, the same for
every package of the repository that locks it: it downloads the crate's
archive from `https://static.crates.io/crates/<name>/<version>/download`
by the lock's sha256 (`Action.downloads`; the host asks the grid's asset
service, which fetches through the proxy and checks the sha256) and
unpacks it into `~/.cargo/vendor/<name>-<version>/`, beside the
`.cargo-checksum.json` cargo's directory source reads, which names the
same sha256, and a `~/.cargo/config.toml` that replaces crates.io with
that directory. The archive is unpacked over no file that is there
already: a crate whose archive carries a `.cargo-checksum.json` of its
own is refused by its fetch (`tar: File exists`), since that file is
the lock's to write. The actions that build wait for every crate of the
package's closure of the lock and read them from there; cargo checks
each against the lock again (`--locked`). A change to one entry of the
lock fetches that crate again and no other; a change to the source
fetches nothing.

A lock that names a crate from a git repository or another registry, or
one with no checksum, is fetched whole by cargo instead, in one `fetch`
per package through the proxy's registry (`source.crates-io.replace-with`,
a registry the worker names), since only cargo fetches from those.

| Action | Command | Kind |
|---|---|---|
| `fetch/<name>@<version>` | the host downloads and unpacks the crate | shared fetch, one per crate of crates.io; hands on `~/.cargo/vendor/<name>-<version>/` and `~/.cargo/config.toml` |
| `fetch` | `cargo fetch --locked` | fetch of the whole lock, where a crate comes from git or another registry; hands on `~/.cargo` |
| `compile` | `cargo build --locked --offline`, `--release` in production | |
| `test` | `cargo test --locked --offline` | test |
| `lint` | `cargo clippy --all-targets -- -D warnings` | check; findings `cargo/clippy` with file and line |
| `format` | `cargo fmt -- --check` | check; findings `cargo/format`; fix `cargo fmt` over `**/*.rs` |
| `wasm` | `cargo build --release --target wasm32-wasip1` | where the option `wasm` is set and the library is a `cdylib`; output `target/wasm32-wasip1/release/<lib>.wasm`, which is what the crate ships: a package, and so an artifact of a release |
| `wasm-examples` | `cargo build --release --target wasm32-wasip1 --examples` | where the option `wasm` is set and an `[[example]]` is a `cdylib`; output `target/wasm32-wasip1/release/examples/<example>.wasm` for each such example |

## Options

| Option | Kind | Default | Set by | What it does |
|---|---|---|---|---|
| `wasm` | bool | false | the repository or a Component | Builds a library and the examples that are a `cdylib` to wasm32-wasip1 modules too (`wasm`, `wasm-examples`), with the `rust-std-wasm32-wasip1` the repository pins beside Rust; a package that has one, where the option is set and that standard library is not pinned, is refused (`unpinned-tool`). |

`target/` is never handed on: build scripts and proc macros run code of
what is built, so cargo's cache falls under the rule that such a cache
is not shared. Warm builds come from the grid's action cache.

## Not built yet

A lock is narrowed to a package by leaving entries out, each kept as
cargo wrote it. Where one member of the workspace turns on a feature of
a shared crate that brings that crate a dependency (`winnow`'s `std`
and `memchr`), the entry says so for every member, and a package that
does not turn it on is refused by cargo under `--locked`. Such a
workspace does not build yet.

A package that is also its workspace's root with members is refused as
not served (`package-at-a-workspace-root-not-served`): its manifest would have to be both the package's and the workspace's
narrowed one. A path dependency outside the repository, `[patch]` of a
path, and features chosen per build are not read. A git dependency, or
one from a registry other than crates.io, has the whole lock fetched by
cargo; a registry other than crates.io is not fetched crate by crate,
since its download address is not in the lock. This repository's own crates are built by it
(`specs/build/self_test.go`): the plugin SDK's contract types are
committed (`sdk/plugin/src/gen`), so its build runs no build script and
reads nothing outside the package. Each action compiles its dependencies from
the lock: the self-build spec took 405 s on 2026-11-08 with the Go
modules, the API and the five Rust packages, measured by `go test`.

Specs: `specs/build/cargo_test.go`. Conformance fixture:
`server/internal/pluginhost/cargo_test.go` over `testdata/`.
