//! Executable contract fixture for separate compile and test dependencies.
//! The acceptance spec runs these declarations through the real Wasm host and grid.
#[path = "fixture/mod.rs"]
mod fixture;

use frontseat_plugin::{
    Action, Analysis, Code, Command, Coordinate, Description, Error, File, Handed, Input, InputSet,
    Origin, Output, Plugin, Produced, Project, Provide, Repository, Requirement,
};

struct RuntimeInputs;

fn coordinate(name: &str) -> Coordinate {
    Coordinate {
        ecosystem: "input-fixture".into(),
        name: name.into(),
    }
}

fn action(name: &str, arguments: &[&str], inputs: Vec<Input>, output: Option<&str>) -> Action {
    Action {
        name: name.into(),
        command: Some(Command {
            arguments: arguments.iter().map(|arg| arg.to_string()).collect(),
            ..Default::default()
        }),
        inputs,
        outputs: output
            .into_iter()
            .map(|path| Output {
                path: path.into(),
                optional: false,
            })
            .collect(),
        test: name == "test",
        ..Default::default()
    }
}

impl Plugin for RuntimeInputs {
    fn describe(&self) -> Description {
        fixture::describe("input-fixture")
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let (manifest, document) = fixture::manifest(repo, root, "input-fixture")?;
        let role = document
            .get("role")
            .and_then(|value| value.as_str())
            .unwrap_or("");
        let mut project = Project {
            root: root.into(),
            manifest: manifest.clone(),
            name: role.into(),
            kind: "input-fixture".into(),
            ..Default::default()
        };
        match role {
            "source" => {
                project.input_sets = vec![InputSet {
                    name: "data".into(),
                    inputs: vec![Input::source(":source.txt")],
                    ..Default::default()
                }];
                project.provides = vec![Provide {
                    coordinate: Some(coordinate("source")),
                    sources: "data".into(),
                    ..Default::default()
                }];
            }
            "runtime" => {
                project.actions = vec![action(
                    "compile",
                    &["cp", "=fixture.txt", "fixture.out"],
                    vec![Input::source("=fixture.txt")],
                    Some("fixture.out"),
                )];
                project.provides = vec![Provide {
                    coordinate: Some(coordinate("runtime")),
                    artifacts: vec![Produced {
                        action: "compile".into(),
                        output: "fixture.out".into(),
                    }],
                    ..Default::default()
                }];
            }
            "consumer" => {
                project.requires = ["source", "runtime"]
                    .map(|name| Requirement {
                        coordinate: Some(coordinate(name)),
                        origin: Some(Origin {
                            path: manifest.clone(),
                            line: 1,
                        }),
                        ..Default::default()
                    })
                    .to_vec();
                project.always_arranged = true;
                project.actions = vec![
                    action(
                        "compile",
                        &["cp", "deps/source/:source.txt", "compiled.out"],
                        vec![Input::dependency(coordinate("source"), "deps/{name}")],
                        Some("compiled.out"),
                    ),
                    action(
                        "test",
                        &["cmp", "compiled.out", "deps/runtime/fixture.out"],
                        vec![
                            Input::output("compiled.out"),
                            Input::dependency(coordinate("runtime"), "deps/{name}"),
                        ],
                        None,
                    ),
                ];
            }
            _ => {
                return Err(Error::new(
                    Code::FailedPrecondition,
                    "unknown-role",
                    "expected source, runtime or consumer",
                )
                .at(manifest))
            }
        }
        Ok(Analysis {
            projects: vec![project],
            ..Default::default()
        })
    }

    fn arrange(
        &self,
        _: &Repository,
        root: &str,
        actions: &[String],
        handed: &[Handed],
    ) -> Result<Vec<File>, Error> {
        let expected = match actions {
            [name] if name == "compile" => "source",
            [name] if name == "test" => "runtime",
            _ => "invalid-group",
        };
        if handed.len() != 1
            || handed[0].coordinate.as_ref() != Some(&coordinate(expected))
            || handed[0].into != format!("deps/{expected}")
        {
            return Err(Error::new(
                Code::FailedPrecondition,
                "wrong-handoff",
                "each action must be arranged with exactly its selected dependency",
            )
            .at(frontseat_plugin::repository::join(
                root,
                "input-fixture.toml",
            )));
        }
        Ok(Vec::new())
    }
}

frontseat_plugin::serve!(RuntimeInputs);
