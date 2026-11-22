use frontseat_plugin::javascript::Package;
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

pub(crate) struct JestPlugin;

fn invalid(path: &str, message: impl Into<String>) -> Error {
    Error::invalid("invalid-manifest", path, message, HELP)
}

/// The one action of a suite: its tests, which are what is arranged.
const TEST: &str = "test";

const HELP: &str = "Declare Jest as a direct dependency and scripts.test as jest; keep options in native Jest configuration. One configured suite is one piece.";

/// What a package says that Jest runs and this plugin does not run yet.
fn not_served(what: &str, path: &str, message: impl Into<String>) -> Error {
    Error::not_served(what, message).at(path).help(HELP)
}

impl Plugin for JestPlugin {
    fn describe(&self) -> Description {
        frontseat_plugin::javascript::description("jest")
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let manifest = join(root, "package.json");
        let Some(package) = repo
            .json::<Package>(&manifest)
            .map(|read| read.map(|document| document.value))?
        else {
            return Ok(Analysis::default());
        };
        if !package.depends_on("jest") {
            return Ok(Analysis::default());
        }
        if package.workspaces.is_some() {
            return Err(not_served(
                "workspaces",
                &manifest,
                "Jest workspace installations are not run yet",
            ));
        }
        if package.scripts.get("test").map(String::as_str) != Some("jest") {
            return Err(invalid(&manifest, "scripts.test must be jest; arbitrary scripts and command-line options are not supported"));
        }
        for hook in ["pretest", "posttest"] {
            if package.scripts.contains_key(hook) {
                return Err(not_served(
                    "test-hooks",
                    &manifest,
                    format!("the {hook} lifecycle is not run yet"),
                ));
            }
        }
        let script = package.script(&manifest, "test", &["--ci"])?;
        let installation = package.installation();
        // Its own installation, which no lock gives a version. The host
        // requires a requirement to say the line it is said on; no line
        // of package.json says it, and it is said of the file, from 1.
        let requirement = Requirement {
            coordinate: Some(installation.clone()),
            origin: Some(Origin {
                path: manifest.clone(),
                line: 1,
            }),
            version: String::new(),
        };
        let test = Action::new(TEST, Command::new(script))
            .tool("node")
            .inputs([
                Input::source("**"),
                Input::source("!node_modules/**"),
                Input::dependency(installation, "{root}"),
            ])
            .test();
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                name: package.name,
                kind: "jest-suite".into(),
                manifest,
                requires: vec![requirement],
                always_arranged: true,
                actions: vec![test],
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
        handed: &[Handoff],
    ) -> Result<Vec<File>, Error> {
        let manifest = join(root, "package.json");
        let package = repo
            .json::<Package>(&manifest)?
            .map(|document| document.value)
            .ok_or_else(|| invalid(&manifest, "the Jest package is missing"))?;
        if actions != [TEST] {
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
            return Err(Error::invalid("missing-installation", &manifest,
                "Jest requires the installed dependencies of this package at its own root",
                "Enable the npm plugin beside jest; the current adapter serves standalone npm installations."));
        }
        Ok(Vec::new())
    }
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    const PACKAGE: &str =
        r#"{"name":"web","scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.2"}}"#;

    /// What a package says that Jest runs and this plugin does not run
    /// yet is refused as not served, each by its own reason; a test
    /// script that is not jest, and a name that is blank, are refused as
    /// invalid; the control is a
    /// package whose test script is jest, which is a suite. It takes no
    /// option: one configured suite is one piece, never cut into shards.
    #[test]
    fn what_jest_runs_and_is_not_run_yet_is_refused_as_not_served() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let analyze = |package: String| {
            let host = TestHost::new(Files::new().with("app/package.json", package));
            plugin.analyze(&host, &context, &["app"])
        };
        analyze(PACKAGE.into()).answered();
        for (from, to, reason) in [
            (
                r#""name":"web""#,
                r#""name":"web","workspaces":["packages/*"]"#,
                "workspaces-not-served",
            ),
            (
                r#""name":"web""#,
                r#""name":"web","packageManager":"pnpm@11""#,
                "package-manager-not-served",
            ),
            (
                r#""test":"jest""#,
                r#""test":"jest","pretest":"node setup.js""#,
                "test-hooks-not-served",
            ),
            (
                r#""test":"jest""#,
                r#""test":"jest","posttest":"node finish.js""#,
                "test-hooks-not-served",
            ),
        ] {
            let refused = analyze(PACKAGE.replace(from, to)).refused(reason);
            assert_eq!(
                (refused.code, refused.path.as_str()),
                (Code::Unimplemented, "app/package.json")
            );
        }
        analyze(PACKAGE.replace(r#""test":"jest""#, r#""test":"jest --ci""#))
            .refused("invalid-manifest");
        analyze(PACKAGE.replace(r#""name":"web""#, r#""name":"""#)).refused("invalid-json");
        assert!(plugin.describe().options.is_empty());
        let host = TestHost::new(Files::new().with("app/package.json", PACKAGE));
        plugin
            .analyze(
                &host,
                &context.option("test-shards", Value::Int(1)),
                &["app"],
            )
            .refused("invalid-option");
    }
}
