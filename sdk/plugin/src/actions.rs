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
use crate::{
    Action, ActionKind, Build, Check, Command, Download, Fetch, File, Input, Output, Package,
    Program, Publish, SharedFetch, Test,
};

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
    #[must_use]
    pub fn env(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.environment.insert(name.into(), value.into());
        self
    }

    /// The same command, run in a directory relative to its project's.
    #[must_use]
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
    /// An action of a name that runs a command: a step of a build,
    /// reading nothing, with no tool and leaving nothing until it is told
    /// so.
    pub fn new(name: impl Into<String>, command: Command) -> Self {
        Self {
            name: name.into(),
            command: Some(command),
            kind: Some(ActionKind::Build(Build {})),
            ..Default::default()
        }
    }

    /// The same action, run with a tool the repository pins.
    #[must_use]
    pub fn tool(mut self, tool: impl Into<String>) -> Self {
        self.tools.push(tool.into());
        self
    }

    /// The same action, reading these besides what it reads.
    #[must_use]
    pub fn inputs(mut self, inputs: impl IntoIterator<Item = Input>) -> Self {
        self.inputs.extend(inputs);
        self
    }

    /// The same action, leaving this besides what it leaves.
    #[must_use]
    pub fn output(mut self, output: Output) -> Self {
        self.outputs.push(output);
        self
    }

    /// The same action, as one that publishes what a release shipped:
    /// no build runs it, and a release does, where the daemon is, once
    /// its tags are pushed. `environment` names the variables of where
    /// the daemon runs that its tool reads a token from, as `GH_TOKEN`.
    #[must_use]
    pub fn publishes<V: Into<String>>(mut self, environment: impl IntoIterator<Item = V>) -> Self {
        self.kind = Some(ActionKind::Publish(Publish {
            environment: environment.into_iter().map(Into::into).collect(),
        }));
        self
    }

    /// The same action, as a check: it says whether the project is as it
    /// should be, and what it found ([`crate::Plugin::report`]). A fix
    /// said before is kept.
    #[must_use]
    pub fn check(mut self) -> Self {
        if !matches!(self.kind, Some(ActionKind::Check(_))) {
            self.kind = Some(ActionKind::Check(Check::default()));
        }
        self
    }

    /// The same action, given the version of the release it is built
    /// for: the host fills what its command names of it
    /// ([`crate::version`]), in its arguments and the values of its
    /// environment. Say so only of the action that writes the version
    /// into what it makes, and name at least one: every other action
    /// stays the same action from one release to the next.
    #[must_use]
    pub fn versioned(mut self) -> Self {
        self.versioned = true;
        self
    }

    /// The same action, as one that makes what its project ships: each
    /// file it leaves is an artifact of the release that builds it, by
    /// the name the tool gave it. It leaves at least one, each sure to
    /// be left. Declare it for the production profile alone where it
    /// costs what a development build should not pay.
    #[must_use]
    pub fn package(mut self) -> Self {
        self.kind = Some(ActionKind::Package(Package {}));
        self
    }

    /// The same action, as a program to run and no step of a build: a
    /// run runs it, where the daemon is, and no build does. Name it
    /// `run/` and what it runs.
    #[must_use]
    pub fn program(mut self) -> Self {
        self.kind = Some(ActionKind::Program(Program {}));
        self
    }

    /// The same action, as one that runs tests.
    #[must_use]
    pub fn test(mut self) -> Self {
        if !matches!(self.kind, Some(ActionKind::Test(_))) {
            self.kind = Some(ActionKind::Test(Test::default()));
        }
        self
    }

    /// The same action, as a test that has nothing to do where an output
    /// it reads, which its action may pass without leaving, was not left:
    /// it is then not run, and ends as idle.
    #[must_use]
    pub fn idle_without_inputs(self) -> Self {
        let mut test = self.test();
        if let Some(ActionKind::Test(kind)) = test.kind.as_mut() {
            kind.idle_without_inputs = true;
        }
        test
    }

    /// The same action, as a fetch of what its project's lock names, with
    /// the ecosystem's own tool: the one kind of project action that
    /// reaches outside its sandbox.
    #[must_use]
    pub fn fetch(mut self) -> Self {
        self.kind = Some(ActionKind::Fetch(Fetch {}));
        self
    }

    /// The same action, as a check with a command that changes the files
    /// it found wrong where the checkout is, rewriting those the patterns
    /// select.
    #[must_use]
    pub fn fix<P: Into<String>>(
        mut self,
        command: Command,
        rewrites: impl IntoIterator<Item = P>,
    ) -> Self {
        self.kind = Some(ActionKind::Check(Check {
            fix: Some(command),
            rewrites: rewrites.into_iter().map(Into::into).collect(),
        }));
        self
    }

    /// A shared fetch: an action that brings what a lock names from
    /// outside, shared by every project of the repository that locks it
    /// alike, and leaving each of `outputs`, which it must. The plugin
    /// says how it fetches: a command, or what it downloads
    /// ([`Action::download`]), with the files it is told
    /// ([`Action::file`]):
    ///
    /// ```
    /// # use frontseat_plugin::prelude::*;
    /// let fetch = Action::shared_fetch("fetch/itoa@1.0.18", ["~/.cargo/vendor/itoa-1.0.18/"])
    ///     .download(Download { url: "https://static.crates.io/crates/itoa/1.0.18/download".into(), ..Default::default() });
    /// assert!(fetch.as_shared_fetch().is_some_and(|shared| shared.downloads.len() == 1));
    /// ```
    pub fn shared_fetch<O: Into<String>>(
        name: impl Into<String>,
        outputs: impl IntoIterator<Item = O>,
    ) -> Self {
        Self {
            name: name.into(),
            outputs: outputs.into_iter().map(Output::required).collect(),
            kind: Some(ActionKind::SharedFetch(SharedFetch::default())),
            ..Default::default()
        }
    }

    /// The same shared fetch, told a file: written into its directory,
    /// or its home (`~/...`), before it runs.
    #[must_use]
    pub fn file(mut self, file: File) -> Self {
        self.shared().files.push(file);
        self
    }

    /// The same shared fetch, bringing a download from outside.
    #[must_use]
    pub fn download(mut self, download: Download) -> Self {
        self.shared().downloads.push(download);
        self
    }

    /// What the action says of itself as a shared fetch, which it is
    /// made where it was not.
    fn shared(&mut self) -> &mut SharedFetch {
        if !matches!(self.kind, Some(ActionKind::SharedFetch(_))) {
            self.kind = Some(ActionKind::SharedFetch(SharedFetch::default()));
        }
        match self.kind.as_mut() {
            Some(ActionKind::SharedFetch(shared)) => shared,
            _ => unreachable!("the action was made a shared fetch"),
        }
    }

    /// What the action says as a test, where it is one.
    pub fn as_test(&self) -> Option<&Test> {
        match &self.kind {
            Some(ActionKind::Test(test)) => Some(test),
            _ => None,
        }
    }

    /// What the action says as a check, where it is one: its fix and
    /// what the fix may change.
    pub fn as_check(&self) -> Option<&Check> {
        match &self.kind {
            Some(ActionKind::Check(check)) => Some(check),
            _ => None,
        }
    }

    /// What the action says as a shared fetch, where it is one: its
    /// files and downloads.
    pub fn as_shared_fetch(&self) -> Option<&SharedFetch> {
        match &self.kind {
            Some(ActionKind::SharedFetch(shared)) => Some(shared),
            _ => None,
        }
    }

    /// What the action says as what publishes, where it publishes.
    pub fn as_publish(&self) -> Option<&Publish> {
        match &self.kind {
            Some(ActionKind::Publish(publish)) => Some(publish),
            _ => None,
        }
    }

    /// Whether the action is a fetch of its project's own lock.
    pub fn is_fetch(&self) -> bool {
        matches!(self.kind, Some(ActionKind::Fetch(_)))
    }

    /// Whether the action is a program to run.
    pub fn is_program(&self) -> bool {
        matches!(self.kind, Some(ActionKind::Program(_)))
    }

    /// Whether the action makes what its project ships.
    pub fn is_package(&self) -> bool {
        matches!(self.kind, Some(ActionKind::Package(_)))
    }
}

#[cfg(test)]
mod tests {
    use crate::{Action, ActionKind, Check, Command, Input, Output, SharedFetch, Test};

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
                kind: Some(ActionKind::Check(Check {
                    fix: Some(Command {
                        arguments: vec!["tool".into(), "fix".into()],
                        ..Default::default()
                    }),
                    rewrites: vec!["**/*.x".into()],
                })),
                ..Default::default()
            }
        );
        assert!(Action::new("test", Command::new(["t"]))
            .test()
            .as_test()
            .is_some());
        assert_eq!(
            Action::new("test", Command::new(["t"]))
                .idle_without_inputs()
                .kind,
            Some(ActionKind::Test(Test {
                idle_without_inputs: true
            }))
        );
    }

    #[test]
    fn an_action_is_of_one_kind_the_last_said() {
        let plain = Action::new("compile", Command::new(["c"]));
        assert!(matches!(plain.kind, Some(ActionKind::Build(_))));
        let fixed = Action::new("format", Command::new(["f"]))
            .fix(Command::new(["f", "-w"]), ["*"])
            .check();
        assert!(fixed.as_check().is_some_and(|check| check.fix.is_some()));
        let package = fixed.package();
        assert!(package.is_package() && package.as_check().is_none());
    }

    #[test]
    fn a_shared_fetch_leaves_what_it_must() {
        let fetch = Action::shared_fetch("fetch/a", ["~/a/", "~/b"]);
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
                kind: Some(ActionKind::SharedFetch(SharedFetch::default())),
                ..Default::default()
            }
        );
    }
}
