//! What an action's command writes for the host to fill: each stands
//! for one thing of the release the action is built for, in its
//! arguments or in a value of its environment. A versioned action
//! ([`crate::Action::versioned`]) is given the first four; what
//! publishes ([`crate::Action::publishes`]) is given all eight.
//!
//! ```
//! use frontseat_plugin::{placeholders, Action, Command};
//!
//! let pack = Action::new(
//!     "pack",
//!     Command::new(["tool", "pack"]).env("PACKAGE_VERSION", placeholders::VERSION),
//! )
//! .versioned();
//! assert!(pack.versioned);
//! ```
//!
//! Only the build a release makes for production is given what was
//! planned; every other build is given "0.0.0-dev" for the version and
//! nothing for the rest, the same each time, so that the action is
//! built once. A tool that reads its version from a file is given it by
//! the action's command, which writes that file from one of these.
//!
//! What is filled in is data: a tag is whatever the repository's catalog
//! says a tag is. Give each to the tool as an argument or an environment
//! value of its own, as above, and never inside text a shell reads as a
//! program.

/// The version released: "1.4.0".
pub const VERSION: &str = "{frontseat.version}";
/// The tag the release is given: "greet/v1.4.0".
pub const TAG: &str = "{frontseat.tag}";
/// The commit the release is planned at.
pub const COMMIT: &str = "{frontseat.commit}";
/// The unit released, as an entity: "component:lab/greet".
pub const UNIT: &str = "{frontseat.unit}";
/// What publishes is given, as an argument by itself: each file the
/// unit's packages left, an argument each, in the order of their names.
pub const ARTIFACTS: &str = "{frontseat.artifacts}";
/// What publishes is given: a file that holds the unit's release notes.
pub const NOTES: &str = "{frontseat.notes}";
/// What publishes is given: where the unit's repository was brought
/// from, as its version control system names it.
pub const REMOTE: &str = "{frontseat.remote}";
/// What publishes is given: "true" where the release is a prerelease,
/// "false" where it is not.
pub const PRERELEASE: &str = "{frontseat.prerelease}";
