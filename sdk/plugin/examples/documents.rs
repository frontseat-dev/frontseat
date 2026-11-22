//! Real-Wasm fixture for shared document readers; no ecosystem build behavior.
use frontseat_plugin::{Analysis, Description, Error, File, Handoff, Plugin, Repository};

struct Documents;

impl Plugin for Documents {
    fn describe(&self) -> Description {
        Description {
            name: "documents".into(),
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        repo.text(root)?;
        Ok(Analysis::default())
    }

    fn arrange(
        &self,
        repo: &Repository,
        root: &str,
        _: &[String],
        _: &[Handoff],
    ) -> Result<Vec<File>, Error> {
        let output = if root.ends_with(".toml") {
            repo.toml_table(root)?.map(|table| format!("{table:?}"))
        } else {
            repo.yaml_stream::<serde_json::Value>(root)
                .map(|read| read.map(|document| document.value))?
                .map(|documents| format!("{documents:?}"))
        };
        Ok(output
            .into_iter()
            .map(|value| File {
                path: "parsed".into(),
                content: value.into_bytes(),
                ..Default::default()
            })
            .collect())
    }
}

frontseat_plugin::serve!(Documents);
