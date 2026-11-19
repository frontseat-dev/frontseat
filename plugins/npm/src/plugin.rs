use frontseat_plugin::prelude::*;

pub(crate) struct NpmPlugin;

impl Plugin for NpmPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "npm".into(),
            // A testdata directory holds fixtures, as Go has it: what is
            // in one is no project, however it looks.
            interests: vec![
                "**/package.json".into(),
                "!**/node_modules/**".into(),
                "!**/testdata/**".into(),
            ],
            ..Default::default()
        }
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
                manifest: frontseat_plugin::repository::join(root, "package.json"),
                actions: crate::actions::of(),
                ..Default::default()
            }],
            ..Default::default()
        })
    }
}
