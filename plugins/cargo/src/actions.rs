use frontseat_plugin::prelude::*;

use crate::lock::{Crate, Fetching};
use crate::manifest::{Example, Lib};

/// The tool every action runs with: the Rust the repository pins, which
/// brings cargo, clippy and rustfmt with it.
pub(super) const RUST: &str = "rust";
/// The standard library a wasm module is linked against, pinned beside
/// Rust where a repository builds one.
pub(super) const WASM_STD: &str = "rust-std-wasm32-wasip1";
const WASM_TARGET: &str = "wasm32-wasip1";

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
    Action {
        downloads: vec![Download {
            url: archive(c),
            sha256: c.checksum.clone(),
            into: "~/.cargo/vendor/".into(),
            ..Default::default()
        }],
        files: vec![
            File {
                path: format!("{unpacked}.cargo-checksum.json"),
                content: format!("{{\"files\":{{}},\"package\":\"{}\"}}", c.checksum).into_bytes(),
                executable: false,
            },
            File {
                path: "~/.cargo/config.toml".into(),
                content: VENDORED.to_vec(),
                executable: false,
            },
        ],
        ..Action::fetch(
            format!("fetch/{}@{}", c.name, c.version),
            [unpacked.clone(), "~/.cargo/config.toml".into()],
        )
    }
}

/// A cargo command, as every action runs one: over the package in its
/// own directory, from its lock, reaching no registry but through the
/// replacement, and writing nothing of the workspace but into the
/// package's own target directory.
fn cargo(arguments: &[&str]) -> Command {
    Command {
        arguments: ["cargo"]
            .iter()
            .chain(arguments)
            .map(|a| a.to_string())
            .collect(),
        environment: [
            // cargo keeps what it builds in the workspace root's target
            // directory, above the package; here it keeps it in the
            // package's, where an output is found.
            ("CARGO_TARGET_DIR", "target"),
            // A worker builds once; what incremental compilation keeps
            // is never read again.
            ("CARGO_INCREMENTAL", "0"),
        ]
        .map(|(name, value)| (name.to_string(), value.to_string()))
        .into(),
        ..Default::default()
    }
}

/// The actions of a package. Its crates are fetched each on its own
/// where the lock allows (lock::Fetching), and the actions that build
/// wait for every one of them and read them from cargo's home; where it
/// does not, one fetch of the whole lock by cargo, through the proxy's
/// registry, which every action then names.
pub(super) fn actions(
    repo: &Repository,
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
            // through the proxy.
            let mut whole = Action {
                name: "fetch".into(),
                command: Some(cargo(&["fetch", "--locked", "--config", REPLACE])),
                inputs: vec![Input::set("package"), Input::dependencies("{root}")],
                tools: vec![RUST.into()],
                fetch: true,
                ..Default::default()
            };
            whole.outputs.push(Output {
                path: "~/.cargo/".into(),
                // A package of path dependencies alone fetches nothing.
                optional: true,
            });
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
    let action = |name: &str, arguments: &[&str]| Action {
        name: name.into(),
        command: Some(cargo(&[arguments, &replace].concat())),
        inputs: inputs.clone(),
        tools: vec![RUST.into()],
        ..Default::default()
    };
    let mut compile_arguments = vec!["build", "--locked", "--offline"];
    if repo.profile()? == Profile::Production {
        compile_arguments.push("--release");
    }
    let compile = action("compile", &compile_arguments);
    let mut test = action("test", &["test", "--locked", "--offline"]);
    test.test = true;
    // clippy's findings are read from what it prints, a line each, and
    // a warning fails the check as an error does.
    let mut lint = action(
        "lint",
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
    );
    lint.check = true;
    // rustfmt names what it would change and writes nothing: a check.
    // Its fix, cargo fmt, runs where the checkout is and nowhere else.
    // Formatting reads no dependency, so it reads nothing of a fetch.
    let mut format = action("format", &["fmt", "--", "--check"]);
    format.command = Some(cargo(&["fmt", "--", "--check"]));
    format.check = true;
    format.inputs = vec![Input::set("package"), Input::dependencies("{root}")];
    format.fix = Some(cargo(&["fmt"]));
    format.rewrites = vec!["**/*.rs".into()];
    let mut actions = fetches;
    actions.extend([compile, test, lint, format]);
    // A library built as a C-style dynamic library is a wasm module where
    // the repository pins the standard library for wasm: built in
    // release, as a module is shipped.
    if let Some(lib) = lib.filter(|lib| lib.crate_type.iter().any(|kind| kind == "cdylib")) {
        if repo.tool_version(WASM_STD).is_some() {
            let mut wasm = action(
                "wasm",
                &[
                    "build",
                    "--locked",
                    "--offline",
                    "--release",
                    "--target",
                    WASM_TARGET,
                ],
            );
            wasm.tools.push(WASM_STD.into());
            let name = lib
                .name
                .clone()
                .unwrap_or_else(|| package.replace('-', "_"));
            wasm.outputs.push(Output {
                path: format!("target/{WASM_TARGET}/release/{name}.wasm"),
                optional: false,
            });
            actions.push(wasm);
        }
    }
    // So are its examples built so, together, as the library is.
    let modules: Vec<&Example> = examples
        .iter()
        .filter(|example| example.crate_type.iter().any(|kind| kind == "cdylib"))
        .collect();
    if !modules.is_empty() && repo.tool_version(WASM_STD).is_some() {
        let mut wasm = action(
            "wasm-examples",
            &[
                "build",
                "--locked",
                "--offline",
                "--release",
                "--target",
                WASM_TARGET,
                "--examples",
            ],
        );
        wasm.tools.push(WASM_STD.into());
        wasm.outputs = modules
            .iter()
            .map(|example| Output {
                path: format!(
                    "target/{WASM_TARGET}/release/examples/{}.wasm",
                    example.name
                ),
                optional: false,
            })
            .collect();
        actions.push(wasm);
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
        "lint" => clippy(workspace, printed),
        "format" => unformatted(root, printed),
        _ => Vec::new(),
    }
}

/// clippy's short messages, one a line: a path relative to the workspace
/// root, a line, a column, a level and the message.
fn clippy(workspace: &str, printed: &Printed) -> Vec<Diagnostic> {
    String::from_utf8_lossy(&printed.stderr)
        .lines()
        .filter_map(|line| {
            let (path, rest) = line.split_once(':')?;
            let (number, rest) = rest.split_once(':')?;
            let (_column, rest) = rest.split_once(':')?;
            let number: i32 = number.trim().parse().ok()?;
            let (level, message) = rest.trim_start().split_once(": ")?;
            if !matches!(level, "warning" | "error") || path.is_empty() || path.starts_with('/') || path.split('/').any(|part| part == ".." || part.is_empty()) {
                return None;
            }
            Some(Diagnostic {
                rule_id: "cargo/clippy".into(),
                message: message.trim().into(),
                source_location: Some(SourceLocation {
                    repository: String::new(),
                    path: frontseat_plugin::repository::join(workspace, path),
                    line: number,
                }),
                help: "Fix what clippy reports; its lints are explained at https://rust-lang.github.io/rust-clippy/.".into(),
            })
        })
        .collect()
}

/// The files rustfmt --check names, each with a diff it would make:
/// "Diff in <path>:<line>:" or, from an older rustfmt, "Diff in <path>
/// at line <line>:". The path is the worker's full path; the part from
/// the project's directory on names the file here, where the project is
/// not the repository's root.
fn unformatted(root: &str, printed: &Printed) -> Vec<Diagnostic> {
    const FORMAT: Unformatted = Unformatted {
        rule_id: "cargo/format",
        message: "it is not formatted as rustfmt formats it",
        help: "Run `frontseat build --fix`, which runs cargo fmt where the checkout is.",
    };
    let mut seen = std::collections::BTreeSet::new();
    String::from_utf8_lossy(&printed.stdout)
        .lines()
        .filter_map(|line| {
            let rest = line.strip_prefix("Diff in ")?;
            let full = if let Some((path, _)) = rest.split_once(" at line ") {
                path
            } else {
                let without_colon = rest.strip_suffix(':')?;
                let (path, number) = without_colon.rsplit_once(':')?;
                number.parse::<u32>().ok()?;
                path
            };
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
                    .and_then(|within| FORMAT.file(root, within))
                    .unwrap_or_else(|| Diagnostic {
                        rule_id: FORMAT.rule_id.into(),
                        message: format!("{full} is not formatted as rustfmt formats it"),
                        source_location: None,
                        help: FORMAT.help.into(),
                    }),
            )
        })
        .collect()
}
