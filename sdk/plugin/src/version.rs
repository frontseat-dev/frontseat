//! What a versioned action's command writes for the host to fill
//! ([`crate::Action::versioned`]): each stands for one thing of the
//! release the action is built for, in its arguments or in a value of
//! its environment.
//!
//! ```
//! use frontseat_plugin::{version, Action, Command};
//!
//! let pack = Action::new(
//!     "pack",
//!     Command::new(["tool", "pack"]).env("PACKAGE_VERSION", version::VERSION),
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
