use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{join, TomlTable};
use toml::Value;

use crate::actions::{actions, report, RUST};
use crate::inputs::input_sets;
use crate::lock::Lock;
use crate::manifest::{read, workspace_root};

pub(super) struct CargoPlugin;

/// What cargo reads of a workspace besides a package's own files, each
/// at the workspace's root: the configuration of cargo, rustfmt and
/// clippy. The workspace's manifest and lock are written for each run,
/// narrowed to the packages present (`arrange`).
const AT_THE_ROOT: &[&str] = &[
    ".cargo/config.toml",
    "rustfmt.toml",
    ".rustfmt.toml",
    "clippy.toml",
    ".clippy.toml",
];

impl Plugin for CargoPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "cargo".into(),
            // What cargo reads, and not what it writes: a target
            // directory is cargo's, whatever it holds.
            interests: [
                "**/Cargo.toml",
                "**/Cargo.lock",
                "**/*.rs",
                "**/.cargo/config.toml",
                "**/rustfmt.toml",
                "**/.rustfmt.toml",
                "**/clippy.toml",
                "**/.clippy.toml",
                "!**/target/**",
                // A testdata directory holds fixtures, as Go has it: what
                // is in one is no package, however it looks.
                "!**/testdata/**",
            ]
            .map(str::to_string)
            .to_vec(),
            ..Default::default()
        }
    }

    // Every action of a package in a workspace is run under the one
    // manifest and lock narrowed to the packages present: the package and
    // what it was handed, so the answer is the same whichever actions were
    // named. cargo loads every member a workspace names, so one that is not
    // there would stop the build; the rest of the root manifest, what the
    // members inherit and how they are built, is kept as it is. The lock is
    // cargo's own text with only the packages present and what they depend
    // on, which is what cargo would write for the narrowed workspace, so
    // `--locked` holds. A package in no workspace needs nothing written.
    fn arrange(
        &self,
        repo: &Repository,
        root: &str,
        _actions: &[String],
        handed: &[Handed],
    ) -> Result<Vec<File>, Error> {
        if repo.tool_version(RUST).is_none() {
            return Err(Error {
                reason: "unpinned-tool".into(),
                message: "the repository pins no rust, so a package cannot be built".into(),
                path: join(root, "Cargo.toml"),
                help: "Pin rust in mise.toml.".into(),
                ..Error::default()
            });
        }
        let Some(mut own) = read(repo, root)? else {
            return Err(Error {
                reason: "invalid-manifest".into(),
                message: "there is no Cargo.toml here".into(),
                path: join(root, "Cargo.toml"),
                help: "Add a Cargo.toml with a [package] table.".into(),
                ..Error::default()
            });
        };
        let Some(ws) = workspace_root(repo, root, &mut own)? else {
            return Ok(Vec::new());
        };
        if ws.dir == root {
            return Ok(Vec::new());
        }
        let manifest = join(&ws.dir, "Cargo.toml");
        let Some(table) = repo.toml(&manifest)? else {
            return Ok(Vec::new());
        };
        let lock = join(&ws.dir, "Cargo.lock");
        let Some(parsed) = Lock::read(repo, &lock)? else {
            return Err(Error {
                code: Code::FailedPrecondition,
                reason: "missing-lock".into(),
                message: format!("the workspace at {manifest} has no Cargo.lock, and a package is built from its lock alone"),
                path: join(root, "Cargo.toml"),
                help: "Run `cargo generate-lockfile` and commit Cargo.lock.".into(),
                ..Error::default()
            });
        };
        let mut present: Vec<String> = handed
            .iter()
            .filter_map(|each| each.coordinate.as_ref().map(|c| c.name.clone()))
            .collect();
        if let Some(package) = own.manifest.package.as_ref() {
            present.push(package.name.clone());
        }
        // Of the root manifest, what is the workspace's: not a package that
        // may be declared beside it, whose files are not here.
        let mut narrowed = TomlTable::new();
        for key in ["workspace", "profile", "patch", "replace"] {
            if let Some(value) = table.get(key) {
                narrowed.insert(key.into(), value.clone());
            }
        }
        let mut members: Vec<String> = handed
            .iter()
            .map(|each| normalized(&join(root, &each.into)))
            .chain([root.to_string()])
            .map(|dir| {
                dir.strip_prefix(&ws.dir)
                    .map_or(dir.clone(), |rest| rest.trim_start_matches('/').to_string())
            })
            .collect();
        members.sort();
        members.dedup();
        let Some(Value::Table(workspace)) = narrowed.get_mut("workspace") else {
            return Ok(Vec::new());
        };
        for key in ["members", "default-members", "exclude"] {
            workspace.remove(key);
        }
        workspace.insert(
            "members".into(),
            Value::Array(members.into_iter().map(Value::String).collect()),
        );
        let content = toml::to_string(&narrowed).map_err(|error| Error {
            reason: "invalid-manifest".into(),
            message: format!("the workspace manifest cannot be written back: {error}"),
            path: manifest.clone(),
            help: "Keep the workspace manifest to what cargo documents.".into(),
            ..Error::default()
        })?;
        let narrowed = parsed.narrowed(&present);
        Ok(vec![
            File {
                path: manifest,
                content: content.into_bytes(),
                executable: false,
            },
            File {
                path: lock,
                content: narrowed.into_bytes(),
                executable: false,
            },
        ])
    }

    fn report(
        &self,
        repo: &Repository,
        root: &str,
        action: &str,
        printed: &Printed,
    ) -> Result<Vec<Diagnostic>, Error> {
        let workspace = match read(repo, root)? {
            Some(mut own) => {
                workspace_root(repo, root, &mut own)?.map_or(root.to_string(), |ws| ws.dir)
            }
            None => root.to_string(),
        };
        Ok(report(root, &workspace, action, printed))
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let manifest = join(root, "Cargo.toml");
        let Some(mut own) = read(repo, root)? else {
            return Ok(Analysis::default());
        };
        // A manifest with no package is a workspace's root alone, which
        // is no project: its packages are.
        let Some(package) = own.manifest.package.take() else {
            return Ok(Analysis::default());
        };
        let lib = own.manifest.lib.take();
        let examples = std::mem::take(&mut own.manifest.example);
        let ws = workspace_root(repo, root, &mut own)?;
        if let Some(ws) = &ws {
            if ws.dir == root && !ws.workspace.members.is_empty() {
                return Err(Error {
                    code: Code::FailedPrecondition,
                    reason: "unsupported-manifest".into(),
                    message: "the package is also the root of a workspace with members, which is not built: its manifest would have to be both the package's and the workspace's narrowed to the packages present".into(),
                    path: manifest,
                    help: "Make the root a workspace alone, with the package as a member, or a package alone.".into(),
                    ..Error::default()
                });
            }
        }
        let ws_dir = ws.as_ref().map_or(root, |ws| ws.dir.as_str());
        let lock_path = join(ws_dir, "Cargo.lock");
        let Some(lock) = Lock::read(repo, &lock_path)? else {
            return Err(Error {
                code: Code::FailedPrecondition,
                reason: "missing-lock".into(),
                message: format!("the workspace at {} has no Cargo.lock, and a package is built from its lock alone", join(ws_dir, "Cargo.toml")),
                path: manifest,
                help: "Run `cargo generate-lockfile` and commit Cargo.lock.".into(),
                ..Error::default()
            });
        };
        // What the package depends on from outside, and how it is
        // fetched: each crate on its own, or the whole lock at once.
        let fetching = lock.fetching(std::slice::from_ref(&package.name));
        let mut workspace_files = Vec::new();
        if ws_dir != root {
            for name in AT_THE_ROOT {
                let path = join(ws_dir, name);
                if repo.read(&path)?.is_some() {
                    workspace_files.push(path);
                }
            }
        }
        let requires = own
            .path_dependencies(ws.as_ref().map(|ws| &ws.workspace))
            .into_iter()
            .map(|dependency| Requirement {
                // The version the lock holds the package at, which is the
                // one built.
                version: lock
                    .workspace_version(&dependency.name)
                    .unwrap_or_default()
                    .into(),
                coordinate: Some(Coordinate {
                    ecosystem: "cargo".into(),
                    name: dependency.name,
                }),
                origin: Some(Origin {
                    path: manifest.clone(),
                    line: dependency.line,
                }),
            })
            .collect();
        let project = Project {
            root: root.into(),
            kind: "cargo-package".into(),
            name: package.name.clone(),
            provides: vec![Provide {
                coordinate: Some(Coordinate {
                    ecosystem: "cargo".into(),
                    name: package.name.clone(),
                }),
                sources: "package".into(),
                ..Default::default()
            }],
            requires,
            actions: actions(repo, lib.as_ref(), &examples, &package.name, &fetching)?,
            input_sets: input_sets(repo, root)?,
            manifest,
            workspace_files,
            // In a workspace, the manifest and lock are written for every
            // run, narrowed to the packages present.
            always_arranged: ws_dir != root,
            ..Default::default()
        };
        Ok(Analysis {
            projects: vec![project],
            ..Default::default()
        })
    }
}

/// A path with its ".." and "." steps resolved, as a place in the repository.
fn normalized(path: &str) -> String {
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                parts.pop();
            }
            other => parts.push(other),
        }
    }
    parts.join("/")
}
