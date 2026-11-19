//! Native package metadata shared by package-manager and framework plugins.
//! Installation layout and test semantics remain with their owning plugins.

use crate::{Coordinate, Error};
use serde::{de::IgnoredAny, Deserialize};
use std::collections::BTreeMap;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Package {
    pub name: String,
    #[serde(default)]
    pub scripts: BTreeMap<String, String>,
    #[serde(default)]
    pub dependencies: BTreeMap<String, String>,
    #[serde(default)]
    pub dev_dependencies: BTreeMap<String, String>,
    pub workspaces: Option<IgnoredAny>,
    pub package_manager: Option<String>,
}

impl Package {
    /// The installed environment of this package. A framework must also
    /// require placement at this package's own repository and root.
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

    /// Select the supported native script entrypoint. More package managers
    /// need their own preparation adapter and invocation proof before use.
    pub fn manager(&self, manifest: &str) -> Result<Manager, Error> {
        if self.package_manager.is_some() {
            return Err(Error { reason: "invalid-manifest".into(), path: manifest.into(),
                message: "an explicit packageManager selection is not supported yet".into(),
                help: "The current adapter serves standalone npm packages using the npm bundled with pinned Node.".into(),
                ..Default::default() });
        }
        Ok(Manager::Npm)
    }
}

pub enum Manager {
    Npm,
}

impl Manager {
    /// Native script invocation retains lifecycle environment and local bins.
    pub fn script(&self, name: &str, arguments: &[&str]) -> Vec<String> {
        match self {
            Self::Npm => ["npm", "run", name, "--"]
                .into_iter()
                .chain(arguments.iter().copied())
                .map(String::from)
                .collect(),
        }
    }
}
