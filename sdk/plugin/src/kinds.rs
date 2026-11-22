//! The kinds plugins name their actions by, so that a run asked for one
//! (`--only lint`) is given every action of it, whichever plugin declared
//! it. An action is named a kind, or a kind, a slash and what piece of
//! it it is: `generate/sdk`, `package/greet-linux-amd64`.
//!
//! ```
//! use frontseat_plugin::{kinds, Action, Command};
//!
//! let lint = Action::new(kinds::LINT, Command::new(["tool", "lint"])).check();
//! let sdk = Action::new(format!("{}/sdk", kinds::GENERATE), Command::new(["tool", "generate"]));
//! assert_eq!((lint.name.as_str(), sdk.name.as_str()), ("lint", "generate/sdk"));
//! ```

/// What fetches what a project's lock names from outside.
pub const FETCH: &str = "fetch";
/// What checks the source for what its tool finds.
pub const LINT: &str = "lint";
/// What checks the source is formatted as its tool formats it.
pub const FORMAT: &str = "format";
/// What compiles the source.
pub const COMPILE: &str = "compile";
/// What runs tests.
pub const TEST: &str = "test";
/// What makes what a project ships.
pub const PACKAGE: &str = "package";
/// A program to run.
pub const RUN: &str = "run";
/// What publishes what a release shipped.
pub const PUBLISH: &str = "publish";
/// What generates source from a description of it.
pub const GENERATE: &str = "generate";
