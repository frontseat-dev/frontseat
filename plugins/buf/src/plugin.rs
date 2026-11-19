use frontseat_plugin::prelude::*;

use crate::{actions, manifest};

pub(crate) struct BufPlugin;

impl Plugin for BufPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "buf".into(),
            // A testdata directory holds fixtures, as Go has it: what is
            // in one is no project, however it looks.
            interests: vec!["**/buf.yaml".into(), "!**/testdata/**".into()],
            generates_sources: true,
            ..Default::default()
        }
    }

    fn report(
        &self,
        _repo: &Repository,
        root: &str,
        action: &str,
        printed: &frontseat_plugin::Printed,
    ) -> Result<Vec<frontseat_plugin::Diagnostic>, Error> {
        Ok(match action {
            "format" => actions::unformatted(root, printed),
            _ => Vec::new(),
        })
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let Some(workspace) = manifest::read(repo, root)? else {
            return Ok(Analysis::default());
        };
        let (actions, generated_sources) = actions::of(&workspace);
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                kind: "buf-workspace".into(),
                name: if root.is_empty() {
                    "protobuf".into()
                } else {
                    root.into()
                },
                manifest: frontseat_plugin::repository::join(root, "buf.yaml"),
                actions,
                generated_sources,
                ..Default::default()
            }],
            ..Default::default()
        })
    }
}
