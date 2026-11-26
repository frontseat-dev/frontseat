//! Native package metadata shared by package-manager and framework plugins.
//! Installation layout and test semantics remain with their owning plugins.

use crate::{Coordinate, Error};
use serde::{de::IgnoredAny, Deserialize};
use std::collections::BTreeMap;

/// What a plugin that reads JavaScript packages is, by its name: one
/// interested in each `package.json`, and in none of what a package
/// manager installed.
pub fn description(name: &str) -> crate::Description {
    crate::Description {
        name: name.into(),
        interests: vec!["**/package.json".into(), "!**/node_modules/**".into()],
        ..Default::default()
    }
}

/// A package's `package.json`, as far as the plugins that build and
/// test JavaScript read it.
#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Package {
    /// The package's name: text other than white space, as npm requires
    /// one.
    #[serde(deserialize_with = "named")]
    pub name: String,
    /// Its scripts, each by its name: `"test": "jest"`.
    #[serde(default)]
    pub scripts: BTreeMap<String, String>,
    /// What it depends on to run, each name with the range it asks for.
    #[serde(default)]
    pub dependencies: BTreeMap<String, String>,
    /// What it depends on to be built and tested.
    #[serde(default)]
    pub dev_dependencies: BTreeMap<String, String>,
    /// The workspaces it is the root of: the globs of its members'
    /// directories, as npm and Yarn both write them.
    pub workspaces: Option<Workspaces>,
    /// The package manager it names (`packageManager`), if it names one.
    pub package_manager: Option<String>,
}

/// The members of a package's workspaces, as `package.json` writes
/// them: a list of globs, or the same list as `packages` beside what
/// Yarn 1 alone reads (`nohoist`), which is not read.
#[derive(Clone, Debug, Deserialize)]
#[serde(untagged)]
pub enum Workspaces {
    /// `"workspaces": ["packages/*"]`.
    Globs(Vec<String>),
    /// `"workspaces": {"packages": ["packages/*"]}`.
    Packages {
        /// The globs.
        packages: Vec<String>,
        /// What Yarn 1 alone reads.
        #[serde(default)]
        nohoist: Option<IgnoredAny>,
    },
}

impl Workspaces {
    /// The globs of the members' directories, as written.
    pub fn globs(&self) -> &[String] {
        match self {
            Workspaces::Globs(globs)
            | Workspaces::Packages {
                packages: globs, ..
            } => globs,
        }
    }

    /// The patterns that select each member's `package.json` beneath the
    /// workspace's root: each glob, a negation kept as one, with `./`
    /// left off as the package managers leave it.
    pub fn manifests(&self) -> Vec<String> {
        self.globs()
            .iter()
            .map(|glob| {
                let (not, glob) = glob
                    .strip_prefix('!')
                    .map_or(("", glob.as_str()), |glob| ("!", glob));
                let glob = glob
                    .strip_prefix("./")
                    .unwrap_or(glob)
                    .trim_end_matches('/');
                format!("{not}{glob}/package.json")
            })
            .collect()
    }
}

/// What a plugin that installs a package's dependencies finds at `root`:
/// one project, of the plugin's `kind`, named for the package and
/// providing its installation as what the action `install` leaves at
/// each of `installed`.
pub fn installs(
    root: &str,
    package: &Package,
    kind: &str,
    (install, installed): (&str, &[String]),
    actions: Vec<crate::Action>,
) -> crate::Analysis {
    let project = crate::Project {
        root: root.into(),
        provides: vec![crate::Provision {
            coordinate: Some(package.installation()),
            artifacts: installed
                .iter()
                .map(|output| crate::OutputRef {
                    action: install.into(),
                    output: output.clone(),
                })
                .collect(),
            ..Default::default()
        }],
        name: package.name.clone(),
        kind: kind.into(),
        manifest: crate::repository::join(root, "package.json"),
        actions,
        ..Default::default()
    };
    crate::Analysis {
        projects: vec![project],
        ..Default::default()
    }
}

/// The lockfile of a Yarn install, beside the root's `package.json`.
pub const YARN_LOCK: &str = "yarn.lock";
/// The lockfile of a pnpm install, beside the root's `package.json`.
pub const PNPM_LOCK: &str = "pnpm-lock.yaml";

/// A package manager that installs a workspace from its root, by one
/// lockfile there, as each plugin that installs is told apart from
/// npm's, which installs each package by its own `package-lock.json`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Manager {
    /// Yarn: `yarn.lock`, its workspaces in the root's `package.json`.
    Yarn,
    /// pnpm: `pnpm-lock.yaml`, its workspaces in `pnpm-workspace.yaml`.
    Pnpm,
}

impl Manager {
    /// Its lockfile's name.
    pub fn lock(self) -> &'static str {
        match self {
            Manager::Yarn => YARN_LOCK,
            Manager::Pnpm => PNPM_LOCK,
        }
    }

    /// Its name, as a plugin that installs with it is listed.
    pub fn name(self) -> &'static str {
        match self {
            Manager::Yarn => "yarn",
            Manager::Pnpm => "pnpm",
        }
    }
}

/// The install of a workspace that serves a package: its manager, and
/// its root's directory.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Install {
    /// What installs it.
    pub manager: Manager,
    /// The workspace's root, where its lockfile is.
    pub root: String,
}

/// The install that serves the package at `root`, if one does: `root`'s
/// own where its directory holds a Yarn or pnpm lockfile, or that of the
/// nearest directory above it that holds one. Beneath a Yarn lockfile,
/// only a package the root's `workspaces` list is the install's: Yarn
/// takes the nearest lockfile above a package for its project's, so one
/// it does not list is served by none, and npm's where it has a lock of
/// npm's. Beneath a pnpm lockfile every package is taken for pnpm's,
/// without reading `pnpm-workspace.yaml`: reading YAML costs every call of
/// a plugin that links its reader, npm's among them, about 340
/// allocations (`TestBudgetOfNpmAnalysis`), and a repository installs
/// with one manager. A repository installs with one of npm, Yarn and pnpm, and lists
/// its one plugin; each refuses a package another installs, by name.
pub fn install_of(repo: &crate::Repository, root: &str) -> Result<Option<Install>, Error> {
    use crate::repository::join;
    // A lockfile that is not there costs nothing to read; one that is,
    // is the install's, which serves the package then.
    let locked = |directory: &str| -> Result<Option<Manager>, Error> {
        for manager in [Manager::Yarn, Manager::Pnpm] {
            if repo.read(&join(directory, manager.lock()))?.is_some() {
                return Ok(Some(manager));
            }
        }
        Ok(None)
    };
    if let Some(manager) = locked(root)? {
        return Ok(Some(Install {
            manager,
            root: root.into(),
        }));
    }
    /// Only what says a root's workspaces is read of it: a root of no
    /// name is a root all the same, and its manifest is not this
    /// package's.
    #[derive(Deserialize)]
    struct Root {
        workspaces: Option<Workspaces>,
    }
    let manifest = join(root, "package.json");
    let mut above = root;
    while !above.is_empty() {
        above = above.rsplit_once('/').map_or("", |(parent, _)| parent);
        let Some(manager) = locked(above)? else {
            continue;
        };
        let install = Install {
            manager,
            root: above.into(),
        };
        if manager == Manager::Pnpm {
            return Ok(Some(install));
        }
        let Some(workspaces) = repo
            .json::<Root>(&join(above, "package.json"))?
            .and_then(|document| document.value.workspaces)
        else {
            return Ok(None);
        };
        let patterns = workspaces.manifests();
        let patterns: Vec<&str> = patterns.iter().map(String::as_str).collect();
        let member = !patterns.is_empty() && repo.glob(above, &patterns)?.contains(&manifest);
        return Ok(member.then_some(install));
    }
    Ok(None)
}

/// A package another plugin's manager installs, refused by the plugin
/// `listed` asked about it: by that manager's name (`yarn-not-served`),
/// at its lockfile, saying which plugin to list in its place.
pub fn installed_by_another(install: &Install, listed: &str) -> Error {
    let manager = install.manager.name();
    Error::not_served(manager, format!("this package is installed with {manager}"))
        .at(crate::repository::join(
            &install.root,
            install.manager.lock(),
        ))
        .help(format!(
            "List the {manager} plugin in frontseat.yaml in place of {listed}."
        ))
}

/// Reads a package's name, refusing one that is empty or white space
/// alone as a mistake of the file, without repeating what was written.
/// The line said is where the JSON reader stopped: the name's own where
/// a key follows it.
fn named<'de, D: serde::Deserializer<'de>>(deserializer: D) -> Result<String, D::Error> {
    let name = String::deserialize(deserializer)?;
    if name.trim().is_empty() {
        return Err(serde::de::Error::custom(
            "the package's name is empty: name it, as npm requires",
        ));
    }
    Ok(name)
}

impl Package {
    /// The installed environment of this package: what the plugin that
    /// installs it provides, and a framework that runs it requires,
    /// placed at this package's own root. Its ecosystem,
    /// `javascript-install`, is the one these plugins agree on.
    pub fn installation(&self) -> Coordinate {
        Coordinate {
            ecosystem: "javascript-install".into(),
            name: self.name.clone(),
        }
    }

    /// A dependency declared directly by the native package manifest.
    pub fn depends_on(&self, name: &str) -> bool {
        self.dependencies.contains_key(name) || self.dev_dependencies.contains_key(name)
    }

    /// The command that runs one of its scripts with arguments, as its
    /// package manager runs it, keeping the script's environment and the
    /// package's own programs; or a refusal where it names a package
    /// manager, since npm, which pinned Node brings, is the only one
    /// served.
    pub fn script(
        &self,
        manifest: &str,
        name: &str,
        arguments: &[&str],
    ) -> Result<Vec<String>, Error> {
        if self.package_manager.is_some() {
            return Err(Error::not_served(
                "package-manager",
                "a packageManager the package names is not served yet",
            )
            .at(manifest)
            .help("Leave packageManager out: the npm pinned Node brings runs the package."));
        }
        Ok(["npm", "run", name, "--"]
            .into_iter()
            .chain(arguments.iter().copied())
            .map(String::from)
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use super::Package;

    /// A name that is empty or white space alone is refused at its line
    /// where a key follows it, saying nothing of what was written; the
    /// control is a name.
    #[test]
    fn a_package_is_named() {
        let package = |name: &str| {
            serde_json::from_str::<Package>(&format!(
                "{{\n  \"name\": \"{name}\",\n  \"version\": \"1.0.0\"\n}}"
            ))
        };
        assert_eq!(
            package("web").map(|package| package.name).ok().as_deref(),
            Some("web")
        );
        for blank in ["", " \\t\\n "] {
            let refused = package(blank).expect_err("a blank name is refused");
            assert_eq!(refused.line(), 2, "{blank:?}");
            assert!(
                refused
                    .to_string()
                    .starts_with("the package's name is empty"),
                "{refused}"
            );
        }
    }
}
