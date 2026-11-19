use frontseat_plugin::javascript::Package;
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

pub(crate) struct JestPlugin;

fn invalid(path: &str, message: impl Into<String>) -> Error {
    Error::invalid(
        "invalid-manifest",
        path,
        message,
        "Declare Jest as a direct dependency and scripts.test as jest; keep options in native Jest configuration. One configured suite is one atom.",
    )
}

impl Plugin for JestPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "jest".into(),
            // A testdata directory holds fixtures, as Go has it: what is
            // in one is no project, however it looks.
            interests: vec![
                "**/package.json".into(),
                "!**/node_modules/**".into(),
                "!**/testdata/**".into(),
            ],
            options: vec![PluginOption {
                name: "test-shards",
                default: Value::Int(1),
                entity_settable: true,
            }],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let manifest = join(root, "package.json");
        let Some(package) = repo.json_document::<Package>(&manifest)? else {
            return Ok(Analysis::default());
        };
        if !package.depends_on("jest") {
            return Ok(Analysis::default());
        }
        if package.workspaces.is_some() {
            return Err(invalid(
                &manifest,
                "Jest workspace installations are not supported yet",
            ));
        }
        if repo.int("test-shards")? != 1 {
            return Err(invalid(
                &manifest,
                "Jest runs one configured suite atom; test-shards must be 1",
            ));
        }
        if package.scripts.get("test").map(String::as_str) != Some("jest") {
            return Err(invalid(&manifest, "scripts.test must be jest; arbitrary scripts and command-line options are not supported"));
        }
        for hook in ["pretest", "posttest"] {
            if package.scripts.contains_key(hook) {
                return Err(invalid(
                    &manifest,
                    format!("the {hook} lifecycle is not supported yet"),
                ));
            }
        }
        let manager = package.manager(&manifest)?;
        let installation = package.installation();
        // Its own installation, which no lock gives a version.
        let requirement = Requirement {
            coordinate: Some(installation.clone()),
            origin: Some(Origin {
                path: manifest.clone(),
                line: 1,
            }),
            version: String::new(),
        };
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                name: package.name,
                kind: "jest-suite".into(),
                manifest,
                requires: vec![requirement],
                always_arranged: true,
                actions: vec![Action {
                    name: "test".into(),
                    command: Some(Command {
                        arguments: manager.script("test", &["--ci"]),
                        ..Default::default()
                    }),
                    inputs: vec![
                        Input::source("**"),
                        Input::source("!node_modules/**"),
                        Input::dependency(installation, "{root}"),
                    ],
                    tools: vec!["node".into()],
                    test: true,
                    ..Default::default()
                }],
                ..Default::default()
            }],
            ..Default::default()
        })
    }

    fn arrange(
        &self,
        repo: &Repository,
        root: &str,
        actions: &[String],
        handed: &[Handed],
    ) -> Result<Vec<File>, Error> {
        let manifest = join(root, "package.json");
        let package = repo
            .json_document::<Package>(&manifest)?
            .ok_or_else(|| invalid(&manifest, "the Jest package is missing"))?;
        if actions != ["test"] {
            return Err(invalid(
                &manifest,
                "only the declared Jest test action can be arranged",
            ));
        }
        // {root} is resolved by the host: another repository is refused there,
        // another directory is non-dot here. A name alone cannot substitute an
        // installation made from a different manifest, lock or tool pin.
        if handed.len() != 1
            || handed[0].coordinate.as_ref() != Some(&package.installation())
            || handed[0].into != "."
        {
            return Err(Error { reason: "missing-installation".into(), path: manifest,
                message: "Jest requires the installed dependencies of this package at its own root".into(),
                help: "Enable the npm plugin beside jest; the current adapter serves standalone npm installations.".into(),
                ..Default::default() });
        }
        Ok(Vec::new())
    }
}
