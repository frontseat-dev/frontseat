//! Contract fixture for projects from two plugins sharing a source directory.
//! This is not an ecosystem plugin: its worker compares the source it was
//! handed with the same file declared in its own inputs.
#[path = "fixture/mod.rs"]
mod fixture;

use frontseat_plugin::Input;
use frontseat_plugin::{
    Action, Analysis, Command, Coordinate, Description, Error, File, Handed, InputSet, Origin,
    Plugin, Project, Provide, Repository, Requirement,
};

struct Cohabiting;

impl Plugin for Cohabiting {
    fn describe(&self) -> Description {
        fixture::describe("cohabiting")
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let (manifest, document) = fixture::manifest(repo, root, "cohabiting")?;
        let field = |name: &str| -> Result<String, Error> {
            document
                .get(name)
                .and_then(|value| value.as_str())
                .filter(|value| !value.is_empty())
                .map(String::from)
                .ok_or_else(|| Error {
                    reason: "missing-coordinate".into(),
                    message: format!("{name} must name a module"),
                    path: manifest.clone(),
                    ..Default::default()
                })
        };
        let provided = field("provides")?;
        let required = field("requires")?;
        let directory = document
            .get("dependency_directory")
            .and_then(|value| value.as_str())
            .unwrap_or(".frontseat/deps/{name}");
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                kind: "contract-fixture".into(),
                name: provided.clone(),
                manifest: manifest.clone(),
                provides: vec![Provide {
                    coordinate: Some(Coordinate {
                        ecosystem: "go".into(),
                        name: provided,
                    }),
                    sources: "declaration".into(),
                    ..Default::default()
                }],
                requires: vec![Requirement {
                    coordinate: Some(Coordinate {
                        ecosystem: "go".into(),
                        name: required.clone(),
                    }),
                    origin: Some(Origin {
                        path: manifest,
                        line: 1,
                    }),
                    ..Default::default()
                }],
                input_sets: vec![InputSet {
                    name: "declaration".into(),
                    inputs: vec![Input::source("cohabiting.toml")],
                    ..Default::default()
                }],
                actions: vec![Action {
                    name: "compare".into(),
                    command: Some(Command {
                        arguments: vec![
                            "cmp".into(),
                            "go.mod".into(),
                            format!(".frontseat/deps/{required}/go.mod"),
                        ],
                        ..Default::default()
                    }),
                    inputs: vec![Input::source("go.mod"), Input::dependencies(directory)],
                    ..Default::default()
                }],
                ..Default::default()
            }],
            ..Default::default()
        })
    }

    fn arrange(
        &self,
        _: &Repository,
        _: &str,
        _: &[String],
        _: &[Handed],
    ) -> Result<Vec<File>, Error> {
        // cmp reads the declared dependency placement directly.
        Ok(Vec::new())
    }
}

frontseat_plugin::serve!(Cohabiting);
