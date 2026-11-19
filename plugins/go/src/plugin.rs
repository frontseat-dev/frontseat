use crate::{
    actions::actions,
    inputs::input_sets,
    manifest::{locked, Module},
};
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

pub(super) struct GoPlugin;

impl Plugin for GoPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "go".into(),
            // What the go command looks at, and not what it leaves out:
            // a directory named testdata is a fixture's, whatever it holds.
            interests: ["**/go.mod", "**/go.sum", "**/*.go", "!**/testdata/**"]
                .map(str::to_string)
                .to_vec(),
            options: vec![
                // How long a module's tests may run, as the go command writes
                // a duration.
                PluginOption {
                    name: "test-timeout",
                    default: Value::String("10m".into()),
                    entity_settable: true,
                },
                // Into how many actions a package's tests are cut at most,
                // each running a share of them.
                PluginOption {
                    name: "test-shards",
                    default: Value::Int(1),
                    entity_settable: true,
                },
                // The build tags a module is linted, compiled and tested with.
                PluginOption {
                    name: "tags",
                    default: Value::StringList(Vec::new()),
                    entity_settable: true,
                },
            ],
            ..Default::default()
        }
    }

    // Every action of a module that was handed modules is run in the one
    // workspace of them, so the answer is the same whichever were named: a
    // Go workspace of the module and the modules it was handed, so that the
    // go command builds them from where they were placed. It is written for
    // the Go the repository pins, which is no older than any module needs.
    fn arrange(
        &self,
        repo: &Repository,
        root: &str,
        _actions: &[String],
        handed: &[Handed],
    ) -> Result<Vec<File>, Error> {
        let Some(version) = repo.tool_version("go") else {
            return Err(Error {
                reason: "unpinned-tool".into(),
                message:
                    "the repository pins no go, so a workspace of its modules cannot be written"
                        .into(),
                path: join(root, "go.mod"),
                help: "Pin go in mise.toml.".into(),
                ..Error::default()
            });
        };
        // Only the action's project is a main module. Every inferred dependency
        // is a local replacement, including transitive requirements. A use entry
        // alone may still make Go read an unpublished required version's go.mod.
        let mut work = format!("go {version}\n\nuse .\n");
        if !handed.is_empty() {
            work.push_str("\nreplace (\n");
            for each in handed {
                let coordinate = each.coordinate.as_ref().ok_or_else(|| Error {
                    reason: "invalid-handover".into(),
                    message: "a handed Go module has no coordinate".into(),
                    path: join(root, "go.mod"),
                    help: "Hand a module with its Go coordinate.".into(),
                    ..Error::default()
                })?;
                work.push_str(&format!("\t{} => ./{}\n", coordinate.name, each.into));
            }
            work.push_str(")\n");
        }
        Ok(vec![File {
            path: join(root, "go.work"),
            content: work.into_bytes(),
            ..Default::default()
        }])
    }

    fn report(
        &self,
        _repo: &Repository,
        root: &str,
        action: &str,
        printed: &Printed,
    ) -> Result<Vec<Diagnostic>, Error> {
        Ok(crate::actions::report(root, action, printed))
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let manifest = join(root, "go.mod");
        let Some(raw) = repo.text(&manifest)? else {
            return Ok(Analysis::default());
        };
        let module = Module::parse(&manifest, &raw)?;
        let locked = locked(repo, root)?;
        let read = input_sets(repo, root)?;
        let (input_sets, directories) = (read.sets, read.programs);
        let mut actions = actions(
            repo,
            &locked,
            root,
            &module.path,
            &read.packages,
            &read.nested,
            &read.tested,
        )?;
        actions.extend(crate::programs::programs(&module.path, &directories));
        let project = Project {
            root: root.into(),
            kind: "go-module".into(),
            name: module.path.clone(),
            // A module is required by its path, and whatever requires it
            // is handed its source: the go command builds a module it
            // requires from source.
            provides: vec![Provide {
                coordinate: Some(Coordinate {
                    ecosystem: "go".into(),
                    name: module.path,
                }),
                sources: "module".into(),
                ..Default::default()
            }],
            requires: module
                .requires
                .into_iter()
                .map(|required| Requirement {
                    coordinate: Some(Coordinate {
                        ecosystem: "go".into(),
                        name: required.path,
                    }),
                    origin: Some(Origin {
                        path: manifest.clone(),
                        line: required.line,
                    }),
                    version: required.version,
                })
                .collect(),
            actions,
            input_sets,
            manifest,
            ..Default::default()
        };
        Ok(Analysis {
            projects: vec![project],
            ..Default::default()
        })
    }
}
