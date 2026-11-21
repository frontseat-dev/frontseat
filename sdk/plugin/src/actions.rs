//! Constructors for what a plugin declares: an action, the command it
//! runs and what it leaves.
//!
//! ```
//! use frontseat_plugin::prelude::*;
//!
//! let install = Action::new("install", Command::new(["npm", "ci", "--offline"]).env("CI", "1"))
//!     .tool("node")
//!     .inputs([Input::source("package.json"), Input::output("fetch")])
//!     .output(Output::optional("node_modules/"));
//! assert_eq!(install.tools, ["node"]);
//! ```
use crate::{Action, Command, Input, Output};

impl Command {
    /// A command of these arguments, the program first, in no
    /// environment of its own and run in its project's directory.
    pub fn new<A: Into<String>>(arguments: impl IntoIterator<Item = A>) -> Self {
        Self {
            arguments: arguments.into_iter().map(Into::into).collect(),
            ..Default::default()
        }
    }

    /// The same command, with a variable set in its environment.
    pub fn env(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.environment.insert(name.into(), value.into());
        self
    }

    /// The same command, run in a directory relative to its project's.
    pub fn directory(mut self, directory: impl Into<String>) -> Self {
        self.directory = directory.into();
        self
    }
}

impl Output {
    /// What an action must leave: it fails where it leaves none.
    pub fn required(path: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            optional: false,
        }
    }

    /// What an action may leave, or not.
    pub fn optional(path: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            optional: true,
        }
    }
}

impl Action {
    /// An action of a name that runs a command, reading nothing, with no
    /// tool and leaving nothing until it is told so.
    pub fn new(name: impl Into<String>, command: Command) -> Self {
        Self {
            name: name.into(),
            command: Some(command),
            ..Default::default()
        }
    }

    /// The same action, run with a tool the repository pins.
    pub fn tool(mut self, tool: impl Into<String>) -> Self {
        self.tools.push(tool.into());
        self
    }

    /// The same action, reading these besides what it reads.
    pub fn inputs(mut self, inputs: impl IntoIterator<Item = Input>) -> Self {
        self.inputs.extend(inputs);
        self
    }

    /// The same action, leaving this besides what it leaves.
    pub fn output(mut self, output: Output) -> Self {
        self.outputs.push(output);
        self
    }

    /// The same action, as a check: it says whether the project is as it
    /// should be, and what it found ([`crate::Plugin::report`]).
    pub fn check(mut self) -> Self {
        self.check = true;
        self
    }

    /// The same action, given the version of the release it is built
    /// for: the host fills what its command names of it
    /// ([`crate::version`]), in its arguments and the values of its
    /// environment. Say so only of the action that writes the version
    /// into what it makes, and name at least one: every other action
    /// stays the same action from one release to the next.
    pub fn versioned(mut self) -> Self {
        self.versioned = true;
        self
    }

    /// The same action, as one that makes what its project ships: each
    /// file it leaves is an artifact of the release that builds it, by
    /// the name the tool gave it. It leaves at least one, each sure to
    /// be left. Declare it for the production profile alone where it
    /// costs what a development build should not pay.
    pub fn package(mut self) -> Self {
        self.package = true;
        self
    }

    /// The same action, as one that runs tests.
    pub fn test(mut self) -> Self {
        self.test = true;
        self
    }

    /// The same check, with a command that changes the files it found
    /// wrong where the checkout is, rewriting those the patterns select.
    pub fn fix<P: Into<String>>(
        mut self,
        command: Command,
        rewrites: impl IntoIterator<Item = P>,
    ) -> Self {
        self.fix = Some(command);
        self.rewrites = rewrites.into_iter().map(Into::into).collect();
        self
    }

    /// A fetch: an action that brings what a lock names from outside,
    /// shared by every project of the repository that locks it alike, and
    /// leaving each of `outputs`, which it must. The plugin fills in how
    /// it fetches (its downloads, its command or the files it is told):
    ///
    /// ```
    /// # use frontseat_plugin::prelude::*;
    /// let downloads = vec![Download { url: "https://static.crates.io/crates/itoa/1.0.18/download".into(), ..Default::default() }];
    /// let fetch = Action { downloads, ..Action::fetch("fetch/itoa@1.0.18", ["~/.cargo/vendor/itoa-1.0.18/"]) };
    /// assert!(fetch.fetch && fetch.shared);
    /// ```
    pub fn fetch<O: Into<String>>(
        name: impl Into<String>,
        outputs: impl IntoIterator<Item = O>,
    ) -> Self {
        Self {
            name: name.into(),
            outputs: outputs.into_iter().map(Output::required).collect(),
            fetch: true,
            shared: true,
            ..Default::default()
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::{Action, Command, Input, Output};

    #[test]
    fn an_action_is_built_as_it_is_said() {
        let built = Action::new(
            "lint",
            Command::new(["tool", "lint"])
                .env("A", "1")
                .directory("sub"),
        )
        .tool("tool")
        .inputs([Input::set("sources")])
        .output(Output::optional("report/"))
        .check()
        .fix(Command::new(["tool", "fix"]), ["**/*.x"]);
        assert_eq!(
            built,
            Action {
                name: "lint".into(),
                command: Some(Command {
                    arguments: vec!["tool".into(), "lint".into()],
                    environment: [("A".to_string(), "1".to_string())].into(),
                    directory: "sub".into(),
                }),
                tools: vec!["tool".into()],
                inputs: vec![Input::set("sources")],
                outputs: vec![Output {
                    path: "report/".into(),
                    optional: true
                }],
                check: true,
                fix: Some(Command {
                    arguments: vec!["tool".into(), "fix".into()],
                    ..Default::default()
                }),
                rewrites: vec!["**/*.x".into()],
                ..Default::default()
            }
        );
        assert!(Action::new("test", Command::new(["t"])).test().test);
    }

    #[test]
    fn a_fetch_is_shared_and_leaves_what_it_must() {
        let fetch = Action::fetch("fetch/a", ["~/a/", "~/b"]);
        assert_eq!(
            fetch,
            Action {
                name: "fetch/a".into(),
                outputs: vec![
                    Output {
                        path: "~/a/".into(),
                        optional: false
                    },
                    Output {
                        path: "~/b".into(),
                        optional: false
                    },
                ],
                fetch: true,
                shared: true,
                ..Default::default()
            }
        );
    }
}
