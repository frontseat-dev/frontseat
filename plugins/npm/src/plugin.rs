use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

pub(crate) struct NpmPlugin;

impl Plugin for NpmPlugin {
    fn describe(&self) -> Description {
        frontseat_plugin::javascript::description("npm")
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let Some(package) = crate::manifest::read(repo, root)? else {
            return Ok(Analysis::default());
        };
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                provides: vec![Provide {
                    coordinate: Some(package.installation()),
                    artifacts: vec![Produced {
                        action: "install".into(),
                        output: "node_modules/".into(),
                    }],
                    ..Default::default()
                }],
                name: package.name,
                kind: "npm-package".into(),
                manifest: join(root, "package.json"),
                actions: crate::actions::of(),
                ..Default::default()
            }],
            ..Default::default()
        })
    }
}
