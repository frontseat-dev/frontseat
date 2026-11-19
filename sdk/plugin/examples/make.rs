//! A plugin whole, as small as one is: it finds a project wherever a
//! `Makefile` is, runs `make test` as its tests and `make lint` as a
//! check, and reads what the check found from what it printed.
//!
//! Built as the plugins of this repository are:
//!
//! ```text
//! cargo build --release --target wasm32-wasip1 --example make
//! ```
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

struct Make;

/// What `make lint` finds, each printed `path:line[:column]: message`.
const LINT: Finding = Finding {
    rule_id: "make/lint",
    help: "Fix what `make lint` reports.",
};

impl Plugin for Make {
    fn describe(&self) -> Description {
        Description {
            name: "make".into(),
            interests: vec!["**/Makefile".into()],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let manifest = join(root, "Makefile");
        if repo.read(&manifest)?.is_none() {
            return Ok(Analysis::default());
        }
        let action = |name: &str| {
            Action::new(name, Command::new(["make", name]))
                .tool("make")
                .inputs([Input::source("**")])
        };
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                name: if root.is_empty() {
                    "root".into()
                } else {
                    root.into()
                },
                kind: "make-project".into(),
                manifest,
                actions: vec![action("test").test(), action("lint").check()],
                ..Default::default()
            }],
            ..Default::default()
        })
    }

    fn report(
        &self,
        _repo: &Repository,
        root: &str,
        action: &str,
        printed: &Printed,
    ) -> Result<Vec<Diagnostic>, Error> {
        if action != "lint" {
            return Ok(Vec::new());
        }
        Ok(String::from_utf8_lossy(&printed.stdout)
            .lines()
            .filter_map(Located::read)
            .filter_map(|at| LINT.at(root, at.path, at.line, at.message))
            .collect())
    }
}

frontseat_plugin::serve!(Make);
