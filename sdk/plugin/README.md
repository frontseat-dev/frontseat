# frontseat-plugin

What a Frontseat plugin is written against in Rust.

A plugin teaches Frontseat an ecosystem. It is a WebAssembly module that
Frontseat asks questions of: what it is, which projects a repository
holds and what each one builds, tests and checks, and what a check
found. It reads the repository only through what Frontseat serves it,
and runs nothing itself: it declares actions, and Frontseat runs them on
the grid. The contract between the two is the protobuf in
[`api/plugin-proto`](../../api/plugin-proto) and its
[ABI document](../../api/plugin-proto/ABI.md); this crate implements it
once, so a plugin is a type that implements `Plugin`.

## A plugin in five minutes

[`examples/make.rs`](examples/make.rs) is a plugin whole, and is what
follows. It finds a project wherever a `Makefile` is, runs `make test`
as its tests and `make lint` as a check, and reads what the check found
from what it printed:

```rust
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

struct Make;

/// What `make lint` finds, each printed `path:line[:column]: message`.
const LINT: Finding = Finding {
    rule_id: "make/lint",
    help: "Fix what `make lint` reports.",
};

impl Plugin for Make {
    fn describe(&self) -> Description {
        Description {
            name: "make".into(),
            interests: vec!["**/Makefile".into()],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let manifest = join(root, "Makefile");
        if repo.read(&manifest)?.is_none() {
            return Ok(Analysis::default());
        }
        let action = |name: &str| {
            Action::new(name, Command::new(["make", name]))
                .tool("make")
                .inputs([Input::source("**")])
        };
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                name: if root.is_empty() {
                    "root".into()
                } else {
                    root.into()
                },
                kind: "make-project".into(),
                manifest,
                actions: vec![action("test").test(), action("lint").check()],
                ..Default::default()
            }],
            ..Default::default()
        })
    }

    fn report(
        &self,
        _repo: &Repository,
        root: &str,
        action: &str,
        printed: &Printed,
    ) -> Result<Vec<Diagnostic>, Error> {
        if action != "lint" {
            return Ok(Vec::new());
        }
        Ok(String::from_utf8_lossy(&printed.stdout)
            .lines()
            .filter_map(Located::read)
            .filter_map(|at| LINT.at(root, at.path, at.line, at.message))
            .collect())
    }
}

frontseat_plugin::serve!(Make);
```

- `describe` says what the plugin is, and its interests: patterns, in
  the language `host.proto` says, of the files it reads. Frontseat asks
  `analyze` only of a directory that holds one. The SDK leaves every
  `testdata` directory out: what is in one is a fixture, and no project.
- `analyze` reads through the `Repository` and answers the projects
  under a root, each with its actions. A project has a name, at the
  repository's root too, where its directory's is empty: Frontseat
  refuses one without. An action runs a command with the
  tools the repository pins (`mise.toml`), reads the inputs it names and
  leaves the outputs it names, and nothing else.
- `report` reads a check's findings from what it printed.
  `diagnostics::Located` reads the `path:line[:column]: message` most
  tools print, and `diagnostics::Finding` makes each a finding of a file
  of the project.
- A refusal is an `Error` with a reason: `Error::invalid` for a file that
  is not as the plugin reads one, `Error::not_served` for what is valid
  and the plugin does not build yet, `Error::new` for the rest.
- `serve!` makes the type the module's plugin.

What the plugin does not serve it leaves: the trait's other methods
answer that it serves none of it.

## Build it

A plugin is a library crate built as a `cdylib`, for `wasm32-wasip1`:

```toml
[lib]
crate-type = ["cdylib"]

[dependencies]
frontseat-plugin = "0.0.0"
```

```text
rustup target add wasm32-wasip1
cargo build --release --target wasm32-wasip1
```

The module is `target/wasm32-wasip1/release/<crate>.wasm`.

## Enable it

The daemon loads a module it is given (`frontseatd --plugin make.wasm`,
or every `<name>.wasm` of a directory with `--plugins`), and a
repository enables a plugin by its name in its `frontseat.yaml`, with
the options it sets:

```yaml
plugins:
  make: {}
```

## Test it

[`frontseat-plugin-test`](../plugin-test) asks a plugin as Frontseat
asks it, on the machine that builds it, over a repository held in
memory: see its README.

## What is in the crate

| Where | What |
|---|---|
| `Plugin`, `Description`, `serve!` | what a plugin implements and says of itself |
| `Repository` | the repository, read: files, globs, typed TOML, YAML and JSON, options, pinned tools |
| `Action`, `Command`, `Output`, `Input` | what an action is, with constructors |
| `version` | what a versioned action's command writes for the host to fill: a release's version, tag, commit and unit |
| `Action::package`, `Repository::platforms` | what a project ships: the action that makes it, and the platforms its entity ships for |
| `Error` | a refusal, with its reason, file, line and help; a `std::error::Error`, printed as a person reads it |
| `diagnostics` | findings read from what a check printed |
| `repository` | paths: `join`, `beneath`, `resolve`, `plain`, `literal` |
| `checksum`, `entity`, `javascript` | what several plugins read alike |
| `wire`, `abi` | the contract's messages as generated, and the module's half of the ABI: what `serve!` and the testkit use |

The examples other than `make` are fixtures of the contract's
conformance tests, not plugins to start from.
