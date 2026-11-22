//! Contract fixture: round-trip typed options through the real Wasm SDK.
//! This exercises the SDK; it is not an ecosystem build plugin.
use frontseat_plugin::{
    Analysis, Description, Error, File, Handoff, Plugin, PluginOption, Repository, Value,
};

struct Options;

impl Plugin for Options {
    fn describe(&self) -> Description {
        Description {
            name: "options".into(),
            options: vec![
                PluginOption {
                    name: "int",
                    default: Value::Int(0),
                    entity_settable: true,
                },
                PluginOption {
                    name: "bool",
                    default: Value::Bool(false),
                    entity_settable: true,
                },
                PluginOption {
                    name: "string",
                    default: Value::String(String::new()),
                    entity_settable: true,
                },
                PluginOption {
                    name: "list",
                    default: Value::StringList(Vec::new()),
                    entity_settable: true,
                },
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
        _handed: &[Handoff],
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
