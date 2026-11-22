use crate::{Analysis, File, Handed, Repository};

/// A failure a plugin reports with what a reader needs to act on it.
///
/// It is made with a reason, always: [`Error::new`], or one of the
/// shapes every plugin answers alike, [`Error::invalid`] for a file that
/// is not as the plugin reads one and [`Error::not_served`] for what is
/// valid and the plugin does not build yet. Where it is about a file, and
/// what to do about it, is added to it:
///
/// ```
/// use frontseat_plugin::{Code, Error};
///
/// let refused = Error::new(Code::FailedPrecondition, "missing-lock", "there is no lock")
///     .at_line("app/Cargo.toml", 3)
///     .help("Commit Cargo.lock.");
/// assert_eq!((refused.reason.as_str(), refused.line), ("missing-lock", 3));
/// ```
#[derive(Clone, Debug, PartialEq)]
pub struct Error {
    /// Canonical RPC status.
    pub code: crate::Code,
    /// A plugin-qualified reason, written by authors as lower-case
    /// words of `[a-z0-9]` joined by single hyphens, the first begun
    /// with a letter: `invalid-manifest`. A client is told it upper
    /// case, `INVALID_MANIFEST`.
    pub reason: String,
    /// What went wrong. It is never empty.
    pub message: String,
    /// The file it is about, relative to the repository's root and
    /// within it; empty for none.
    pub path: String,
    /// The line in the file, from 1; 0 for none.
    pub line: i32,
    /// What to do about it.
    pub help: String,
}

/// The type URLs of the two details a failure carries.
const ERROR_INFO: &str = "type.googleapis.com/google.rpc.ErrorInfo";
const DIAGNOSTIC: &str = "type.googleapis.com/frontseat.type.v1alpha1.Diagnostic";

impl Error {
    /// A failure of this code, for this reason, saying what went wrong.
    pub fn new(code: crate::Code, reason: &str, message: impl Into<String>) -> Self {
        Self {
            code,
            reason: reason.into(),
            message: message.into(),
            path: String::new(),
            line: 0,
            help: String::new(),
        }
    }

    /// A file of the repository that is not as the plugin reads one: a
    /// source prerequisite failure (`FailedPrecondition`) with the
    /// plugin's reason, the file, what is wrong with it and what to do.
    ///
    /// ```
    /// # use frontseat_plugin::Error;
    /// Error::invalid("invalid-manifest", "app/package.json", "the name is missing", "Name the package.");
    /// ```
    pub fn invalid(
        reason: &str,
        path: &str,
        message: impl Into<String>,
        help: impl Into<String>,
    ) -> Self {
        Self::new(crate::Code::FailedPrecondition, reason, message)
            .at(path)
            .help(help)
    }

    /// What is valid, and the plugin does not build yet: `UNIMPLEMENTED`,
    /// for the reason `<what>-not-served`, as every server of frontseat
    /// refuses what it does not serve. A client tells it from a file that
    /// is wrong by its code. `what` names what was asked about, never a
    /// method: answered to Report, `report` would say the plugin serves
    /// no Report, and its findings would be lost without a word.
    ///
    /// ```
    /// # use frontseat_plugin::{Code, Error};
    /// let refused = Error::not_served("workspaces", "npm workspaces are not built yet")
    ///     .at("app/package.json");
    /// assert_eq!((refused.code, refused.reason.as_str()), (Code::Unimplemented, "workspaces-not-served"));
    /// ```
    pub fn not_served(what: &str, message: impl Into<String>) -> Self {
        Self::new(
            crate::Code::Unimplemented,
            &format!("{what}-not-served"),
            message,
        )
        .help("It is valid, and this plugin does not build it yet.")
    }

    /// The same failure, about a file of the repository.
    pub fn at(mut self, path: impl Into<String>) -> Self {
        self.path = path.into();
        self
    }

    /// The same failure, about a line of a file of the repository, from 1.
    pub fn at_line(self, path: impl Into<String>, line: i32) -> Self {
        Self {
            line,
            ..self.at(path)
        }
    }

    /// The same failure, saying what to do about it.
    pub fn help(mut self, help: impl Into<String>) -> Self {
        self.help = help.into();
        self
    }

    /// The rule of the host's this failure breaks, if it breaks one. The
    /// host refuses a failure that is not a failure's code, a reason
    /// written as [`Error::reason`] says, a message, and a file of the
    /// repository where it names one; `sdk/plugin-test/testdata/failures.json`
    /// is the table of cases the host and this run alike.
    fn broken(&self) -> Option<&'static str> {
        let mut words = self.reason.split('-');
        let word = |word: &str| {
            !word.is_empty()
                && word
                    .bytes()
                    .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit())
        };
        let begun = self
            .reason
            .as_bytes()
            .first()
            .is_some_and(u8::is_ascii_lowercase);
        if self.code == crate::Code::Ok {
            Some("its code is OK, which is no failure")
        } else if !begun || !words.all(word) {
            Some("its reason is not lower-case words of [a-z0-9] joined by single hyphens, begun with a letter")
        } else if self.message.is_empty() {
            Some("it has no message")
        } else if self.path.is_empty() {
            None
        } else if crate::repository::resolve("", &self.path).is_none() {
            Some("its path is not a file of the repository, relative to its root")
        } else if self.line < 0 {
            Some("its line is below zero")
        } else {
            None
        }
    }

    /// Encodes the failure as a module answers one: the status, with its
    /// reason (`ErrorInfo`) and where it is and what to do (`Diagnostic`).
    ///
    /// A failure the host would refuse is not sent: the host would say
    /// only that the plugin's failure was refused, and what the plugin
    /// meant to say would be lost. In its place goes an `INTERNAL`
    /// failure for the reason `invalid-failure`, which says the rule
    /// broken and the message the plugin wrote.
    pub fn into_status(self, plugin: &str) -> crate::google::rpc::Status {
        use crate::frontseat::r#type::v1alpha1::{Diagnostic, SourceLocation};
        use crate::google::rpc::{ErrorInfo, Status};
        use prost::Message;
        if let Some(rule) = self.broken() {
            let said = if self.message.is_empty() {
                "It said nothing.".to_string()
            } else {
                format!("It said: {}", self.message)
            };
            return Self::new(
                crate::Code::Internal,
                "invalid-failure",
                format!("the plugin's failure is not one the host accepts: {rule}. {said}"),
            )
            .help("Write the failure as frontseat_plugin::Error says one is written.")
            .into_status(plugin);
        }
        let mut message = self.message.clone();
        if self.line > 0 {
            message = format!("line {}: {message}", self.line);
        }
        if !self.path.is_empty() {
            message = format!("{}: {message}", self.path);
        }
        if !self.help.is_empty() {
            // One stop between them, whether the message ends in one or not.
            message = format!("{}. {}", message.trim_end_matches('.'), self.help);
        }
        let info = ErrorInfo {
            reason: self.reason.to_ascii_uppercase().replace('-', "_"),
            domain: format!("{plugin}.plugins.frontseat.dev"),
            metadata: Default::default(),
        };
        let diagnostic = Diagnostic {
            rule_id: format!("{plugin}/{}", self.reason),
            message: self.message,
            source_location: (!self.path.is_empty()).then_some(SourceLocation {
                repository: String::new(),
                path: self.path,
                line: self.line,
            }),
            help: self.help,
        };
        Status {
            code: self.code as i32,
            message,
            details: vec![
                prost_types::Any {
                    type_url: ERROR_INFO.into(),
                    value: info.encode_to_vec(),
                },
                prost_types::Any {
                    type_url: DIAGNOSTIC.into(),
                    value: diagnostic.encode_to_vec(),
                },
            ],
        }
    }

    /// The failure a status says, as [`Error::into_status`] encodes one:
    /// its reason as its author wrote it, `invalid-manifest`, and its
    /// message without where it is or what to do. A status with no
    /// details of a plugin's failure is read as far as it goes: its code,
    /// its message, and the reason its `ErrorInfo` says, if it has one.
    pub fn from_status(status: crate::google::rpc::Status) -> Self {
        use crate::frontseat::r#type::v1alpha1::Diagnostic;
        use crate::google::rpc::ErrorInfo;
        use prost::Message;
        let code = crate::Code::try_from(status.code).unwrap_or(crate::Code::Unknown);
        let mut error = Self::new(code, "", status.message);
        for detail in status.details {
            match detail.type_url.as_str() {
                ERROR_INFO if error.reason.is_empty() => {
                    if let Ok(info) = ErrorInfo::decode(detail.value.as_slice()) {
                        error.reason = info.reason.to_ascii_lowercase().replace('_', "-");
                    }
                }
                DIAGNOSTIC => {
                    if let Ok(diagnostic) = Diagnostic::decode(detail.value.as_slice()) {
                        if let Some((_, reason)) = diagnostic.rule_id.split_once('/') {
                            error.reason = reason.into();
                        }
                        error.message = diagnostic.message;
                        error.help = diagnostic.help;
                        if let Some(at) = diagnostic.source_location {
                            error.path = at.path;
                            error.line = at.line;
                        }
                    }
                }
                _ => {}
            }
        }
        error
    }
}

/// What a plugin is.
#[derive(Default)]
pub struct Description {
    /// The plugin's name, lower-case `[a-z0-9-]`.
    pub name: String,
    /// Patterns of the files it reads, relative to a repository, in the
    /// pattern language `host.proto` says. The SDK adds one that leaves
    /// out every fixture ([`crate::repository::NOT_FIXTURES`]) to any it
    /// has, last.
    pub interests: Vec<String>,
    /// The options it takes. A repository or an entity can set these and
    /// no others; the host refuses anything else by name.
    pub options: Vec<PluginOption>,
    /// Analyze declares generated source regions using committed metadata only.
    pub generates_sources: bool,
    /// The branching strategies it answers `branch` for, by name,
    /// lower-case `[a-z0-9-]`: "release-flow". A repository names one as
    /// `<plugin>:<name>`.
    pub branchings: Vec<String>,
    /// The versioning strategies it answers `version` for, by name:
    /// "conventional-commits".
    pub versionings: Vec<String>,
    /// The names of the services it says how to start for a test, as an
    /// entity's service names what it is (`spec.frontseat.service.as`),
    /// lower-case `[a-z0-9-]`: "postgres". The host asks
    /// [`Plugin::service`] for each.
    pub services: Vec<String>,
}

/// An option a plugin takes: its name, and the value it has where nothing
/// sets it. The value's kind is the option's kind.
pub struct PluginOption {
    /// What it is set by, lower-case `[a-z0-9-]`.
    pub name: &'static str,
    /// Its value where nothing sets it.
    pub default: Value,
    /// Whether an entity may set it, for its own directory and beneath;
    /// otherwise only a repository may.
    pub entity_settable: bool,
}

/// The value of an option.
#[derive(Clone, Debug, PartialEq)]
pub enum Value {
    /// Text.
    String(String),
    /// True or false.
    Bool(bool),
    /// A whole number.
    Int(i64),
    /// A list of text. A setting replaces the whole list.
    StringList(Vec<String>),
}

impl From<Value> for crate::OptionValue {
    fn from(value: Value) -> Self {
        use crate::option_value::{StringList, Value as WireValue};
        Self {
            value: Some(match value {
                Value::String(value) => WireValue::StringValue(value),
                Value::Bool(value) => WireValue::BoolValue(value),
                Value::Int(value) => WireValue::IntValue(value),
                Value::StringList(values) => WireValue::StringListValue(StringList { values }),
            }),
        }
    }
}

impl PluginOption {
    /// The declared kind, matching the default without a second source of truth.
    pub fn kind(&self) -> crate::wire::option::Kind {
        use crate::wire::option::Kind;
        match &self.default {
            Value::String(_) => Kind::String,
            Value::Bool(_) => Kind::Bool,
            Value::Int(_) => Kind::Int,
            Value::StringList(_) => Kind::StringList,
        }
    }
}

/// What a check printed, and how it ended: what a plugin reads its
/// findings from.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Printed {
    /// How the check ended.
    pub exit_code: i32,
    /// What it printed, cut to its first MiB.
    pub stdout: Vec<u8>,
    /// What it printed to standard error, cut to its first MiB.
    pub stderr: Vec<u8>,
}

/// What every plugin implements.
pub trait Plugin {
    /// Says what the plugin is. It reads nothing.
    fn describe(&self) -> Description;

    /// Returns what there is to build under a root: the projects found
    /// there, each with its actions. A plugin that reads no source of a
    /// repository, one that only starts services or only serves release
    /// strategies, leaves this, and finds nothing under any root.
    fn analyze(&self, _repo: &Repository, _root: &str) -> Result<Analysis, Error> {
        Ok(Analysis::default())
    }

    /// Returns the files a project's tool needs in order to find what the
    /// project was handed, each by its path relative to the repository:
    /// the files of the actions named, which are the ones that were
    /// handed it. A plugin whose tool finds what is placed for it writes
    /// none.
    fn arrange(
        &self,
        _repo: &Repository,
        _root: &str,
        actions: &[String],
        handed: &[Handed],
    ) -> Result<Vec<File>, Error> {
        if !actions.is_empty() || !handed.is_empty() {
            return Err(Error::new(
                crate::Code::InvalidArgument,
                "unsupported-arrangement",
                "this plugin declares no arrangement for handed actions",
            )
            .help(
                "Arrange only actions declared by a plugin that supports dependency placement.",
            ));
        }
        Ok(Vec::new())
    }

    /// What the files that describe entities should say of what the
    /// source says: edits, each a sequence of strings at a key path of
    /// one document of a file. A plugin that finds no entities answers
    /// that it reconciles nothing.
    fn reconcile(
        &self,
        _repo: &Repository,
        _root: &str,
        _entities: &[crate::Resolved],
    ) -> Result<Vec<crate::Edit>, Error> {
        Err(
            Error::not_served("reconcile", "this plugin reconciles no catalog").help(
                "Only a plugin that finds catalog entities says what their files should say.",
            ),
        )
    }

    /// Says what a branch releases of a unit, for a plugin that is a
    /// branching strategy: whether it releases at all, and the line of
    /// versions it is held to. It reads no file.
    fn branch(
        &self,
        _request: &crate::wire::BranchRequest,
    ) -> Result<crate::wire::BranchResponse, Error> {
        Err(no_release_strategy("branching"))
    }

    /// Says a unit's next version and why, for a plugin that is a
    /// versioning strategy. It reads no file.
    fn version(
        &self,
        _request: &crate::wire::VersionRequest,
    ) -> Result<crate::wire::VersionResponse, Error> {
        Err(no_release_strategy("versioning"))
    }

    /// Returns what a check found, read from what it printed: each
    /// finding with a rule the plugin qualifies, "go/vet", and a path
    /// relative to the repository. A plugin whose checks say what they
    /// found only by their exit code and what they printed leaves this,
    /// and the host records no findings for them.
    fn report(
        &self,
        _repo: &Repository,
        _root: &str,
        _action: &str,
        _printed: &Printed,
    ) -> Result<Vec<crate::Diagnostic>, Error> {
        Err(Error::not_served(
            "report",
            "this plugin reads no findings from what its checks print",
        )
        .help("A check of this plugin passes or fails by its exit code; read what it printed."))
    }

    /// Says how a service the plugin claims by name
    /// ([`Description::services`]) is started for a test: the whole of
    /// it, with every field of `written` as it was written, since those
    /// are what a person wrote beside the name. It reads nothing: what it
    /// is told is all it knows. A plugin that claims no service leaves
    /// this, and answers that it starts none.
    fn service(&self, name: &str, _written: &crate::Service) -> Result<crate::Service, Error> {
        Err(no_service(name))
    }
}

/// What a plugin that starts no service answers when it is asked for one.
pub(crate) fn no_service(name: &str) -> Error {
    Error::not_served("service", format!("this plugin starts no service {name}"))
        .help("Ask a plugin only for the services its description claims.")
}

pub(crate) fn no_release_strategy(kind: &str) -> Error {
    Error::not_served(
        &format!("{kind}-strategy"),
        format!("this plugin serves no {kind} strategy"),
    )
    .help("Name a strategy of a plugin that says it serves it.")
}
