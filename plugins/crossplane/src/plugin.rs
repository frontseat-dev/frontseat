use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

use crate::{actions, manifest, validated};

pub(crate) struct CrossplanePlugin;

impl Plugin for CrossplanePlugin {
    fn describe(&self) -> Description {
        Description {
            name: "crossplane".into(),
            // A package is found by its metadata, as `crossplane xpkg
            // build` finds it at its package root.
            interests: vec![format!("**/{}", manifest::FILE)],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let Some(configuration) = manifest::read(repo, root)? else {
            return Ok(Analysis::default());
        };
        let manifest = join(root, manifest::FILE);
        // Its tool is pinned, or the Configuration is refused here,
        // before anything is built.
        repo.pinned(actions::TOOL, &manifest)?;
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                kind: "crossplane-configuration".into(),
                name: configuration.name.clone(),
                manifest,
                actions: actions::of(repo, root, &configuration)?,
                ..Default::default()
            }],
            ..Default::default()
        })
    }

    fn report(
        &self,
        repo: &Repository,
        root: &str,
        action: &str,
        printed: &Printed,
    ) -> Result<Vec<Diagnostic>, Error> {
        match action {
            actions::VALIDATE => validated::findings(repo, root, printed),
            _ => Ok(Vec::new()),
        }
    }
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    fn fixture() -> TestHost {
        TestHost::new(
            Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/valid")).unwrap(),
        )
    }

    /// A directory that holds a Configuration's metadata is a project,
    /// named as the Configuration is; one that holds none is no project.
    #[test]
    fn a_configuration_is_a_project_named_as_it_is() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin
            .context(Profile::Development)
            .tool("crossplane", "2.5.0");
        let analysis = plugin.analyze(&fixture(), &context, &["app"]).answered();
        let project = &analysis.analyses[0].projects[0];
        assert_eq!(
            (
                project.root.as_str(),
                project.kind.as_str(),
                project.name.as_str(),
                project.manifest.as_str()
            ),
            (
                "app",
                "crossplane-configuration",
                "network",
                "app/crossplane.yaml"
            )
        );
        let none = plugin
            .analyze(
                &TestHost::new(Files::new().with("app/x.yaml", "a: 1\n")),
                &context,
                &["app"],
            )
            .answered();
        assert!(none
            .analyses
            .iter()
            .all(|analysis| analysis.projects.is_empty()));
    }

    /// A repository that pins no crossplane is refused when it is
    /// analysed, saying what to pin.
    #[test]
    fn a_repository_that_pins_no_crossplane_is_refused() {
        let plugin = TestPlugin::new(crate::served());
        let refused = plugin
            .analyze(&fixture(), &plugin.context(Profile::Development), &["app"])
            .refused("unpinned-tool");
        assert_eq!(refused.path, "app/crossplane.yaml");
    }

    #[test]
    fn it_takes_no_options_and_is_interested_in_package_metadata() {
        let described = crate::served().describe();
        assert_eq!(described.name, "crossplane");
        assert!(described.options.is_empty());
        assert_eq!(described.interests[0], "**/crossplane.yaml");
    }
}
