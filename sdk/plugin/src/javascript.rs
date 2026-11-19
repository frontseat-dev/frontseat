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
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Package {
    /// The package's name.
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
    /// Whether it is the root of npm workspaces, which no plugin builds
    /// yet: only whether it says any is read.
    pub workspaces: Option<IgnoredAny>,
    /// The package manager it names (`packageManager`), if it names one.
    pub package_manager: Option<String>,
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
