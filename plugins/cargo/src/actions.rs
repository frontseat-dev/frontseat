use frontseat_plugin::prelude::*;

use crate::lock::{Crate, Fetching};
use crate::manifest::{Example, Lib};

/// The tool every action runs with: the Rust the repository pins, which
/// brings cargo, clippy and rustfmt with it.
pub(super) const RUST: &str = "rust";
/// The option that has a library and the examples that are a C-style
/// dynamic library (`cdylib`) built to wasm32-wasip1 modules, beside
/// what is built for the worker.
pub(super) const WASM: &str = "wasm";
/// The standard library a wasm module is linked against, which the
/// repository pins beside Rust where it builds one.
const WASM_STD: &str = "rust-std-wasm32-wasip1";
const WASM_TARGET: &str = "wasm32-wasip1";
/// The checks of a package, by the names their findings are asked by.
const LINT: &str = "lint";
const FORMAT: &str = "format";

/// Where cargo reads crates from when the whole lock is fetched at once:
/// crates.io, replaced by the registry the worker names `frontseat`,
/// which is the proxy. Every action of such a package says so, since the
/// index cargo keeps is the replacement's. The worker names the registry;
/// the repository names nothing.
const REPLACE: &str = "source.crates-io.replace-with=\"frontseat\"";

/// Where cargo reads crates from when each is fetched on its own: the
/// directory of cargo's home the fetches unpacked them into, in place of
/// crates.io. Written as cargo's own configuration, in its home, by every
/// such fetch alike; a path there is relative to the home.
const VENDORED: &[u8] =
    b"[source.crates-io]\nreplace-with = \"vendored\"\n\n[source.vendored]\ndirectory = \".cargo/vendor\"\n";

/// Where crates.io serves a crate's archive: what rules_rust and reindeer
/// download each crate from. The grid's proxy stands in for it.
fn archive(c: &Crate) -> String {
    format!(
        "https://static.crates.io/crates/{}/{}/download",
        c.name, c.version
    )
}

/// The fetch of one crate: shared by every package of the repository
/// that locks it, it downloads the crate's archive by its checksum and
/// unpacks it into cargo's home, beside the checksum file cargo's
/// directory source reads to hold the crate to the lock. It runs no
/// tool: the host fetches through the asset service and unpacks.
fn fetch(c: &Crate) -> Action {
    let unpacked = format!("~/.cargo/vendor/{}-{}/", c.name, c.version);
    Action::shared_fetch(
        format!("fetch/{}@{}", c.name, c.version),
        [unpacked.clone(), "~/.cargo/config.toml".into()],
    )
    .download(Download {
        url: archive(c),
        sha256: c.checksum.clone(),
        into: "~/.cargo/vendor/".into(),
        ..Default::default()
    })
    .file(File {
        path: format!("{unpacked}.cargo-checksum.json"),
        content: format!("{{\"files\":{{}},\"package\":\"{}\"}}", c.checksum).into_bytes(),
        executable: false,
    })
    .file(File {
        path: "~/.cargo/config.toml".into(),
        content: VENDORED.to_vec(),
        executable: false,
    })
}

/// A cargo command, as every action runs one: over the package in its
/// own directory, from its lock, reaching no registry but through the
/// replacement, and writing nothing of the workspace but into the
/// package's own target directory.
fn cargo(arguments: &[&str]) -> Command {
    Command::new(["cargo"].iter().chain(arguments).copied())
        // cargo keeps what it builds in the workspace root's target
        // directory, above the package; here it keeps it in the
        // package's, where an output is found.
        .env("CARGO_TARGET_DIR", "target")
        // A worker builds once; what incremental compilation keeps is
        // never read again.
        .env("CARGO_INCREMENTAL", "0")
}

/// The actions of a package. Its crates are fetched each on its own
/// where the lock allows (lock::Fetching), and the actions that build
/// wait for every one of them and read them from cargo's home; where it
/// does not, one fetch of the whole lock by cargo, through the proxy's
/// registry, which every action then names.
pub(super) fn actions(
    repo: &Repository,
    manifest: &str,
    lib: Option<&Lib>,
    examples: &[Example],
    package: &str,
    fetching: &Fetching,
) -> Result<Vec<Action>, Error> {
    let (fetches, replace): (Vec<Action>, Vec<&str>) = match fetching {
        Fetching::Crates(crates) => (crates.iter().map(fetch).collect(), Vec::new()),
        Fetching::Whole => {
            // Fetches what the lock names into cargo's home, which is
            // handed on: the one action that reaches the registry,
            // through the proxy. A package of path dependencies alone
            // fetches nothing.
            let whole = Action::new("fetch", cargo(&["fetch", "--locked", "--config", REPLACE]))
                .tool(RUST)
                .inputs([Input::set("package"), Input::dependencies("{root}")])
                .output(Output::optional("~/.cargo/"))
                .fetch();
            (vec![whole], vec!["--config", REPLACE])
        }
    };
    // The package, and all each fetch left: what an action reads is
    // what it waits for.
    let inputs: Vec<Input> = [Input::set("package")]
        .into_iter()
        .chain(fetches.iter().map(|f| Input::output(&f.name)))
        .chain([Input::dependencies("{root}")])
        .collect();
    let action = |name: &str, arguments: &[&str]| {
        Action::new(name, cargo(&[arguments, &replace].concat()))
            .tool(RUST)
            .inputs(inputs.clone())
    };
    let mut compile_arguments = vec!["build", "--locked", "--offline"];
    if repo.profile()? == Profile::Production {
        compile_arguments.push("--release");
    }
    let compile = action("compile", &compile_arguments);
    let test = action("test", &["test", "--locked", "--offline"]).test();
    // clippy's findings are read from what it prints, a line each, and
    // a warning fails the check as an error does.
    let lint = action(
        LINT,
        &[
            "clippy",
            "--locked",
            "--offline",
            "--all-targets",
            "--message-format",
            "short",
            "--",
            "-D",
            "warnings",
        ],
    )
    .check();
    // rustfmt names what it would change and writes nothing: a check.
    // Its fix, cargo fmt, runs where the checkout is and nowhere else.
    // Formatting reads no dependency, so it reads nothing of a fetch.
    let format = Action::new(FORMAT, cargo(&["fmt", "--", "--check"]))
        .tool(RUST)
        .inputs([Input::set("package"), Input::dependencies("{root}")])
        .check()
        .fix(cargo(&["fmt"]), ["**/*.rs"]);
    let mut actions = fetches;
    actions.extend([compile, test, lint, format]);
    // A library built as a C-style dynamic library is a wasm module, and
    // so are its examples so built, where the option says so: built in
    // release, as a module is shipped, the examples together.
    if !repo.option_bool(WASM)? {
        return Ok(actions);
    }
    let wasm = |name: &str, more: &[&str]| {
        let arguments = [
            &[
                "build",
                "--locked",
                "--offline",
                "--release",
                "--target",
                WASM_TARGET,
            ],
            more,
        ]
        .concat();
        action(name, &arguments).tool(WASM_STD)
    };
    let cdylib = |kinds: &[String]| kinds.iter().any(|kind| kind == "cdylib");
    let modules: Vec<&Example> = examples
        .iter()
        .filter(|example| cdylib(&example.crate_type))
        .collect();
    let lib = lib.filter(|lib| cdylib(&lib.crate_type));
    if lib.is_some() || !modules.is_empty() {
        repo.pinned(WASM_STD, manifest)?;
    }
    if let Some(lib) = lib {
        let name = lib
            .name
            .clone()
            .unwrap_or_else(|| package.replace('-', "_"));
        // The module is what a crate that is a wasm module ships.
        actions.push(
            wasm("wasm", &[])
                .output(Output::required(format!(
                    "target/{WASM_TARGET}/release/{name}.wasm"
                )))
                .package(),
        );
    }
    if !modules.is_empty() {
        let mut examples = wasm("wasm-examples", &["--examples"]);
        examples.outputs = modules
            .iter()
            .map(|example| {
                Output::required(format!(
                    "target/{WASM_TARGET}/release/examples/{}.wasm",
                    example.name
                ))
            })
            .collect();
        actions.push(examples);
    }
    Ok(actions)
}

/// What a check found, read from what it printed.
pub(super) fn report(
    root: &str,
    workspace: &str,
    action: &str,
    printed: &Printed,
) -> Vec<Diagnostic> {
    match action {
        LINT => clippy(workspace, printed),
        FORMAT => unformatted(root, printed),
        _ => Vec::new(),
    }
}

/// clippy's short messages, one a line: a path relative to the workspace
/// root, a line, a column, a level and the message. What is not a
/// warning or an error is no finding.
fn clippy(workspace: &str, printed: &Printed) -> Vec<Diagnostic> {
    const CLIPPY: Finding = Finding {
        rule_id: "cargo/clippy",
        help: "Fix what clippy reports; its lints are explained at https://rust-lang.github.io/rust-clippy/.",
    };
    String::from_utf8_lossy(&printed.stderr)
        .lines()
        .filter_map(|line| {
            let at = Located::read(line)?;
            let (level, message) = at.message.split_once(": ")?;
            if !matches!(level, "warning" | "error") {
                return None;
            }
            CLIPPY.at(workspace, at.path, at.line, message.trim())
        })
        .collect()
}

/// The files rustfmt --check names, each with a diff it would make:
/// "Diff in <path>:<line>:". The path is the worker's full path; the
/// part from the project's directory on names the file here, where the
/// project is not the repository's root.
fn unformatted(root: &str, printed: &Printed) -> Vec<Diagnostic> {
    let help = Unformatted::fixed_by("cargo fmt");
    let format = Unformatted {
        rule_id: "cargo/format",
        message: "it is not formatted as rustfmt formats it",
        help: &help,
    };
    let mut seen = std::collections::BTreeSet::new();
    String::from_utf8_lossy(&printed.stdout)
        .lines()
        .filter_map(|line| {
            let (full, number) = line
                .strip_prefix("Diff in ")?
                .strip_suffix(':')?
                .rsplit_once(':')?;
            number.parse::<u32>().ok()?;
            if !seen.insert(full.to_string()) {
                return None;
            }
            let within = (!root.is_empty())
                .then(|| {
                    full.rsplit_once(&format!("/{root}/"))
                        .map(|(_, within)| within)
                })
                .flatten();
            // A file outside the project's directory is named whole,
            // with nowhere in the repository to point at.
            Some(
                within
                    .and_then(|within| format.file(root, within))
                    .unwrap_or_else(|| Diagnostic {
                        rule_id: format.rule_id.into(),
                        message: format!("{full} is not formatted as rustfmt formats it"),
                        source_location: None,
                        help: help.clone(),
                    }),
            )
        })
        .collect()
}
