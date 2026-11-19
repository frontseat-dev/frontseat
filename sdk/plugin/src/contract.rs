use crate::{Analysis, File, Handed, Repository};

/// A failure a plugin reports with what a reader needs to act on it.
#[derive(Clone, Debug, PartialEq)]
pub struct Error {
    /// Canonical RPC status; a source prerequisite failure by default.
    pub code: crate::Code,
    /// A plugin-qualified reason, written lower-case `[a-z0-9-]` by authors.
    pub reason: String,
    /// What went wrong.
    pub message: String,
    /// The file it is about, relative to the repository.
    pub path: String,
    /// The line in the file, from 1.
    pub line: i32,
    /// What to do about it.
    pub help: String,
}

impl Default for Error {
    fn default() -> Self {
        Self {
            code: crate::Code::FailedPrecondition,
            reason: String::new(),
            message: String::new(),
            path: String::new(),
            line: 0,
            help: String::new(),
        }
    }
}

impl Error {
    /// A file of the repository that is not as the plugin reads one: a
    /// source prerequisite failure (`FailedPrecondition`) with the
    /// plugin's reason, the file, what is wrong with it and what to do.
    ///
    /// ```ignore
    /// Error::invalid("invalid-manifest", "app/package.json", "the name is missing", "Name the package.")
    /// ```
    pub fn invalid(
        reason: &str,
        path: &str,
        message: impl Into<String>,
        help: impl Into<String>,
    ) -> Self {
        Self {
            reason: reason.into(),
            path: path.into(),
            message: message.into(),
            help: help.into(),
            ..Self::default()
        }
    }

    /// Encode one shared failure model, with a canonical reason and source detail.
    pub fn into_status(self, plugin: &str) -> crate::google::rpc::Status {
        use crate::frontseat::r#type::v1alpha1::{Diagnostic, SourceLocation};
        use crate::google::rpc::{ErrorInfo, Status};
        use prost::Message;
        let mut message = self.message.clone();
        if self.line > 0 {
            message = format!("line {}: {message}", self.line);
        }
        if !self.path.is_empty() {
            message = format!("{}: {message}", self.path);
        }
        if !self.help.is_empty() {
            message = format!("{message}. {}", self.help);
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
                    type_url: "type.googleapis.com/google.rpc.ErrorInfo".into(),
                    value: info.encode_to_vec(),
                },
                prost_types::Any {
                    type_url: "type.googleapis.com/frontseat.type.v1alpha1.Diagnostic".into(),
                    value: diagnostic.encode_to_vec(),
                },
            ],
        }
    }
}

/// What a plugin is.
#[derive(Default)]
pub struct Description {
    /// The plugin's name, lower-case `[a-z0-9-]`.
    pub name: String,
    /// Patterns of the files it reads, relative to a repository.
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
            return Err(Error {
                code: crate::Code::InvalidArgument,
                reason: "unsupported-arrangement".into(),
                message: "this plugin declares no arrangement for handed actions".into(),
                help:
                    "Arrange only actions declared by a plugin that supports dependency placement."
                        .into(),
                ..Default::default()
            });
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
        Err(Error {
            code: crate::Code::Unimplemented,
            reason: "unsupported-reconcile".into(),
            message: "this plugin reconciles no catalog".into(),
            help: "Only a plugin that finds catalog entities says what their files should say."
                .into(),
            ..Default::default()
        })
    }

    /// Says what a branch releases of a unit, for a plugin that is a
    /// branching strategy: whether it releases at all, as a prerelease or
    /// not, and the line of versions it is held to. It reads no file.
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
        Err(Error {
            code: crate::Code::Unimplemented,
            reason: "unsupported-report".into(),
            message: "this plugin reads no findings from what its checks print".into(),
            help: "A check of this plugin passes or fails by its exit code; read what it printed."
                .into(),
            ..Default::default()
        })
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
    Error {
        code: crate::Code::Unimplemented,
        reason: "unsupported-service".into(),
        message: format!("this plugin starts no service {name}"),
        help: "Ask a plugin only for the services its description claims.".into(),
        ..Default::default()
    }
}

fn no_release_strategy(kind: &str) -> Error {
    Error {
        code: crate::Code::Unimplemented,
        reason: "unsupported-release-strategy".into(),
        message: format!("this plugin serves no {kind} strategy"),
        help: "Name a strategy of a plugin that says it serves it.".into(),
        ..Default::default()
    }
}
