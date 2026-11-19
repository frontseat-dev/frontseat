use std::collections::BTreeMap;

use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{join, line_of};
use serde::Deserialize;
use toml::Spanned;

/// Declares a struct of what a manifest table holds, given with its own
/// fields, and adds the three kinds of dependency cargo reads in it:
/// `dependencies`, `dev-dependencies` and `build-dependencies`, each by
/// name and with where it is written. The top of a manifest and a
/// `[target.…]` table hold the same three, and are declared through this
/// so the three are written once; serde's `flatten` would read them
/// without their spans.
macro_rules! with_dependencies {
    (
        $(#[$meta:meta])*
        $vis:vis struct $name:ident {
            $($(#[$field_meta:meta])* $field_vis:vis $field:ident: $ty:ty,)*
        }
    ) => {
        $(#[$meta])*
        $vis struct $name {
            $($(#[$field_meta])* $field_vis $field: $ty,)*
            #[serde(default)]
            pub(super) dependencies: BTreeMap<String, Spanned<Dependency>>,
            #[serde(default)]
            pub(super) dev_dependencies: BTreeMap<String, Spanned<Dependency>>,
            #[serde(default)]
            pub(super) build_dependencies: BTreeMap<String, Spanned<Dependency>>,
        }
    };
}

with_dependencies! {
    /// What a Cargo.toml says that this plugin acts on: whether it is a
    /// package, whether it is a workspace's root, what it depends on by
    /// path, and what its library is built as. The rest is cargo's, which
    /// reads the same file when it builds.
    #[derive(Deserialize, Default)]
    #[serde(rename_all = "kebab-case")]
    pub(super) struct Manifest {
        pub(super) package: Option<Package>,
        pub(super) workspace: Option<Workspace>,
        /// The dependencies of some targets only, `[target.'cfg(unix)']` or
        /// `[target.x86_64-unknown-linux-gnu]`, by the target cargo reads.
        #[serde(default)]
        pub(super) target: BTreeMap<String, Target>,
        pub(super) lib: Option<Lib>,
        /// Its examples, `[[example]]`: one built as a C-style dynamic
        /// library is a wasm module too.
        #[serde(default)]
        pub(super) example: Vec<Example>,
    }
}

with_dependencies! {
    /// What a `[target.…]` table holds of what a package depends on: the
    /// three kinds of dependency, as at the top of the manifest.
    #[derive(Deserialize, Default)]
    #[serde(rename_all = "kebab-case")]
    pub(super) struct Target {}
}

#[derive(Deserialize, Default)]
pub(super) struct Package {
    pub(super) name: String,
}

#[derive(Deserialize, Default)]
#[serde(rename_all = "kebab-case")]
pub(super) struct Workspace {
    #[serde(default)]
    pub(super) members: Vec<String>,
    #[serde(default)]
    pub(super) exclude: Vec<String>,
    #[serde(default)]
    pub(super) dependencies: BTreeMap<String, Dependency>,
}

#[derive(Deserialize, Default)]
#[serde(rename_all = "kebab-case")]
pub(super) struct Lib {
    pub(super) name: Option<String>,
    #[serde(default)]
    pub(super) crate_type: Vec<String>,
}

/// An example of a package, as its `[[example]]` table names it.
#[derive(Deserialize, Default)]
#[serde(rename_all = "kebab-case")]
pub(super) struct Example {
    pub(super) name: String,
    #[serde(default)]
    pub(super) crate_type: Vec<String>,
}

/// A dependency as cargo writes one: a version alone, or a table.
#[derive(Deserialize)]
#[serde(untagged)]
pub(super) enum Dependency {
    /// A version alone is cargo's to read: it names no path.
    Version(#[allow(dead_code)] String),
    Detailed(Detailed),
}

#[derive(Deserialize, Default)]
#[serde(rename_all = "kebab-case")]
pub(super) struct Detailed {
    pub(super) path: Option<String>,
    pub(super) workspace: Option<bool>,
    pub(super) package: Option<String>,
}

impl Dependency {
    /// The path a dependency is found at, if it is one found by path,
    /// resolved through the workspace's dependencies where it inherits.
    fn path<'a>(&'a self, key: &str, workspace: Option<&'a Workspace>) -> Option<&'a str> {
        match self {
            Dependency::Version(_) => None,
            Dependency::Detailed(detailed) => match (&detailed.path, detailed.workspace) {
                (Some(path), _) => Some(path),
                (None, Some(true)) => workspace
                    .and_then(|workspace| workspace.dependencies.get(key))
                    .and_then(|inherited| inherited.path(key, None)),
                _ => None,
            },
        }
    }

    /// The package's own name, where the key is not it.
    fn package<'a>(&'a self, key: &'a str, workspace: Option<&'a Workspace>) -> &'a str {
        let renamed = match self {
            Dependency::Detailed(Detailed {
                package: Some(package),
                ..
            }) => Some(package.as_str()),
            Dependency::Detailed(Detailed {
                workspace: Some(true),
                ..
            }) => workspace
                .and_then(|workspace| workspace.dependencies.get(key))
                .and_then(|inherited| match inherited {
                    Dependency::Detailed(Detailed {
                        package: Some(package),
                        ..
                    }) => Some(package.as_str()),
                    _ => None,
                }),
            _ => None,
        };
        renamed.unwrap_or(key)
    }
}

/// A package's manifest as read, with its text for the lines things are on.
pub(super) struct Read {
    pub(super) manifest: Manifest,
    text: String,
}

/// Reads a Cargo.toml, or says it is not there.
pub(super) fn read(repo: &Repository, dir: &str) -> Result<Option<Read>, Error> {
    let Some(typed) = repo.toml_document::<Manifest>(&join(dir, "Cargo.toml"))? else {
        return Ok(None);
    };
    Ok(Some(Read {
        manifest: typed.value,
        text: typed.text,
    }))
}

/// A dependency found by path: the package it is, where it is in the
/// repository, and the line that says so.
pub(super) struct PathDependency {
    pub(super) name: String,
    pub(super) line: i32,
}

impl Read {
    /// The dependencies of every kind that are found by path, for every
    /// target as for some, with the workspace's dependencies to resolve
    /// the inherited ones through. Cargo builds a dependency of some
    /// targets where the target is; the graph holds it wherever it is
    /// named.
    pub(super) fn path_dependencies(&self, workspace: Option<&Workspace>) -> Vec<PathDependency> {
        let mut out = Vec::new();
        let tables = [
            &self.manifest.dependencies,
            &self.manifest.dev_dependencies,
            &self.manifest.build_dependencies,
        ]
        .into_iter()
        .chain(self.manifest.target.values().flat_map(|target| {
            [
                &target.dependencies,
                &target.dev_dependencies,
                &target.build_dependencies,
            ]
        }));
        for table in tables {
            for (key, spanned) in table {
                let dependency = spanned.get_ref();
                if dependency.path(key, workspace).is_none() {
                    continue;
                }
                out.push(PathDependency {
                    name: dependency.package(key, workspace).to_string(),
                    line: line_of(&self.text, Some(spanned.span().start)),
                });
            }
        }
        out
    }
}

/// The root of the workspace a package is in: the nearest manifest above
/// it with a [workspace] table, or its own where it has one, or none.
/// A package the nearest workspace leaves out is refused, as cargo
/// refuses to build it.
pub(super) struct WorkspaceRoot {
    pub(super) dir: String,
    pub(super) workspace: Workspace,
}

pub(super) fn workspace_root(
    repo: &Repository,
    root: &str,
    own: &mut Read,
) -> Result<Option<WorkspaceRoot>, Error> {
    if let Some(workspace) = own.manifest.workspace.take() {
        return Ok(Some(WorkspaceRoot {
            dir: root.to_string(),
            workspace,
        }));
    }
    let mut dir = root;
    while let Some((parent, _)) = dir
        .rsplit_once('/')
        .or((!dir.is_empty()).then_some(("", dir)))
    {
        dir = parent;
        let Some(mut above) = read(repo, dir)? else {
            if dir.is_empty() {
                break;
            }
            continue;
        };
        let Some(workspace) = above.manifest.workspace.take() else {
            if dir.is_empty() {
                break;
            }
            continue;
        };
        let manifests = workspace
            .members
            .iter()
            .map(|member| join(member, "Cargo.toml"))
            .collect::<Vec<_>>();
        let members = repo.glob(
            dir,
            &manifests.iter().map(String::as_str).collect::<Vec<_>>(),
        )?;
        let within = root
            .strip_prefix(dir)
            .map_or(root, |rest| rest.trim_start_matches('/'));
        let excluded = workspace.exclude.iter().any(|excluded| {
            within == excluded.trim_end_matches('/')
                || within.starts_with(&format!("{}/", excluded.trim_end_matches('/')))
        });
        if !members.contains(&join(root, "Cargo.toml")) || excluded {
            return Err(Error {
                code: frontseat_plugin::Code::FailedPrecondition,
                reason: "not-a-member".into(),
                message: format!("the package is beneath the workspace at {} and is not one of its members, so cargo will not build it", join(dir, "Cargo.toml")),
                path: join(root, "Cargo.toml"),
                help: "Add the package to the workspace's members, or exclude it.".into(),
                ..Error::default()
            });
        }
        return Ok(Some(WorkspaceRoot {
            dir: dir.to_string(),
            workspace,
        }));
    }
    Ok(None)
}
