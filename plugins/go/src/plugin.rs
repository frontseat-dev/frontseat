use crate::{
    actions::actions,
    inputs::input_sets,
    manifest::{locked, Module},
};
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

pub(super) struct GoPlugin;

/// The options a repository or an entity sets, by name: each is said
/// here once, where it is declared and where it is read.
pub(super) const TEST_TIMEOUT: &str = "test-timeout";
pub(super) const TEST_SHARDS: &str = "test-shards";
pub(super) const TAGS: &str = "tags";
pub(super) const LINK_VARIABLES: &str = "link-variables";

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
                // How long each action that runs tests may take, a
                // package's tests or one shard of them: every test
                // program is started with it, and 0 is no limit, as it
                // is the go command's. The host reads it as a duration.
                PluginOption::new(
                    TEST_TIMEOUT,
                    "How long each test action may run, as 90s, 10m or 1h30m; 0 is no limit.",
                    std::time::Duration::from_secs(600),
                )
                .entity_settable(),
                PluginOption::new(
                    TEST_SHARDS,
                    "Into how many actions each package's tests are cut at most, each running a share.",
                    1,
                )
                .entity_settable()
                .at_least(1),
                PluginOption::new(
                    TAGS,
                    "The build tags a module is linted, compiled and tested with.",
                    Vec::<String>::new(),
                )
                .entity_settable(),
                // A string variable of a package, by its import path and
                // name, and what of the release it is set to when the
                // program is linked.
                PluginOption::new(
                    LINK_VARIABLES,
                    "The variables a module's programs say their release by, each <package>.<variable> set to version, tag, commit or unit.",
                    std::collections::BTreeMap::<String, String>::new(),
                )
                .entity_settable()
                .one_of(&["version", "tag", "commit", "unit"]),
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
