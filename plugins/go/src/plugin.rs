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
            // What the go command looks at. A directory named testdata
            // is a fixture's, whatever it holds: the SDK leaves it out.
            interests: ["**/go.mod", "**/go.sum", "**/*.go"]
                .map(str::to_string)
                .to_vec(),
            options: vec![
                PluginOption::new(
                    "test-timeout",
                    "How long each test action may run, as the go command writes a duration (10m).",
                    "10m",
                )
                .entity_settable(),
                PluginOption::new(
                    "test-shards",
                    "Into how many actions each package's tests are cut at most, each running a share.",
                    1,
                )
                .entity_settable(),
                PluginOption::new(
                    "tags",
                    "The build tags a module is linted, compiled and tested with.",
                    Vec::<String>::new(),
                )
                .entity_settable(),
                // Each "<package>.<variable>=<what>": a string variable of
                // a package, by its import path, and what of the release
                // it is set to when the program is linked.
                PluginOption::new(
                    "version",
                    "Variables a module's programs say their version by, each <package>.<variable>=<version|tag|commit|unit>.",
                    Vec::<String>::new(),
                )
                .entity_settable(),
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
        handed: &[Handoff],
    ) -> Result<Vec<File>, Error> {
        let manifest = join(root, "go.mod");
        let version = repo.pinned("go", &manifest)?;
        // Only the action's project is a main module. Every inferred dependency
        // is a local replacement, including transitive requirements. A use entry
        // alone may still make Go read an unpublished required version's go.mod.
        let mut work = format!("go {version}\n\nuse .\n");
        if !handed.is_empty() {
            work.push_str("\nreplace (\n");
            for each in handed {
                let coordinate = each.coordinate.as_ref().ok_or_else(|| {
                    Error::invalid(
                        "invalid-handover",
                        &manifest,
                        "a handed Go module has no coordinate",
                        "Hand a module with its Go coordinate.",
                    )
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
            &crate::actions::Found {
                packages: &read.packages,
                nested: &read.nested,
                tested: &read.tested,
                programs: &directories,
            },
        )?;
        actions.extend(crate::programs::programs(&module.path, &directories));
        let project = Project {
            root: root.into(),
            kind: "go-module".into(),
            name: module.path.clone(),
            // A module is required by its path, and whatever requires it
            // is handed its source: the go command builds a module it
            // requires from source.
            provides: vec![Provision {
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
