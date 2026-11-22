//! Contract fixture: round-trip typed options through the real Wasm SDK.
//! This exercises the SDK; it is not an ecosystem build plugin.
use frontseat_plugin::{
    Analysis, Description, Error, File, Handed, Plugin, PluginOption, Repository, Value,
};

struct Options;

impl Plugin for Options {
    fn describe(&self) -> Description {
        Description {
            name: "options".into(),
            options: vec![
                PluginOption::new("int", "A whole number.", Value::Int(0)).entity_settable(),
                PluginOption::new("bool", "True or false.", Value::Bool(false)).entity_settable(),
                PluginOption::new("string", "Text.", Value::String(String::new()))
                    .entity_settable(),
                PluginOption::new("list", "A list of text.", Value::StringList(Vec::new()))
                    .entity_settable(),
            ],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, _root: &str) -> Result<Analysis, Error> {
        // A valid call reads source; invalid options must fail before this read.
        repo.read("contract.txt")?;
        Ok(Analysis::default())
    }

    fn arrange(
        &self,
        repo: &Repository,
        _root: &str,
        _actions: &[String],
        _handed: &[Handed],
    ) -> Result<Vec<File>, Error> {
        repo.read("contract.txt")?;
        let mut files = vec![
            File {
                path: "integer".into(),
                content: repo.int("int")?.to_string().into_bytes(),
                ..Default::default()
            },
            File {
                path: "boolean".into(),
                content: repo.bool("bool")?.to_string().into_bytes(),
                ..Default::default()
            },
            File {
                path: "string".into(),
                content: repo.string("string")?.as_bytes().to_vec(),
                ..Default::default()
            },
        ];
        for (i, value) in repo.string_list("list")?.iter().enumerate() {
            files.push(File {
                path: format!("item-{i}"),
                content: value.as_bytes().to_vec(),
                ..Default::default()
            });
        }
        Ok(files)
    }
}

frontseat_plugin::serve!(Options);
