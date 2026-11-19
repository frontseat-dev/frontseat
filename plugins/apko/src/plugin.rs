use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

use crate::{actions, config, lock};

pub(crate) struct ApkoPlugin;

impl Plugin for ApkoPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "apko".into(),
            // An image is found by its lock: a configuration with no lock
            // is a fragment, or an image nobody has locked, and neither
            // can be built without asking a package index what is newest.
            interests: vec![format!("**/{}", lock::FILE)],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let Some(lock) = lock::read(repo, root)? else {
            return Ok(Analysis::default());
        };
        config::check(repo, &join(root, &lock.config))?;
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                kind: "apko-image".into(),
                name: if root.is_empty() {
                    "image".into()
                } else {
                    root.into()
                },
                manifest: join(root, lock::FILE),
                actions: actions::of(&lock),
                ..Default::default()
            }],
            ..Default::default()
        })
    }
}
