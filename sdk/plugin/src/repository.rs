use crate::{wire, Error, Profile};

mod document;
pub use document::{line_of, TomlTable, Typed};

/// What the host serves a plugin while it answers a call.
pub trait Host {
    /// Returns a file of the repository, if it is there.
    fn read(&self, path: &str) -> Result<Option<Vec<u8>>, Error>;
    /// Returns repository-relative paths beneath a literal directory, sorted.
    /// Patterns are relative to that directory; empty directory means the root.
    fn glob(&self, directory: &str, patterns: &[&str]) -> Result<Vec<String>, Error>;
}

/// A plugin's view of the repository it is asked about.
pub struct Repository<'a> {
    host: &'a dyn Host,
    context: wire::Context,
}

impl<'a> Repository<'a> {
    /// Validate the entire option set before any plugin-specific code runs.
    pub(crate) fn validate_context(
        &self,
        declared: &[crate::PluginOption],
    ) -> Result<(), crate::Error> {
        use crate::option_value::Value;
        use crate::wire::option::Kind;
        self.profile()?;
        for name in self.context.options.keys() {
            if !declared.iter().any(|option| option.name == name) {
                return Err(Self::invalid_option(name, "a declared option name"));
            }
        }
        for option in declared {
            let value = self.option(option.name)?;
            if !matches!(
                (option.kind(), value),
                (Kind::String, Value::StringValue(_))
                    | (Kind::Bool, Value::BoolValue(_))
                    | (Kind::Int, Value::IntValue(_))
                    | (Kind::StringList, Value::StringListValue(_))
            ) {
                return Err(Self::invalid_option(
                    option.name,
                    option.kind().as_str_name(),
                ));
            }
        }
        Ok(())
    }

    /// The way the source is to be built. A plugin declares the commands
    /// of this profile: development is quick to build and easy to debug,
    /// production is compiled for speed and for safety. The host must supply
    /// one explicitly; unspecified and unknown values are refused.
    pub fn profile(&self) -> Result<Profile, Error> {
        match Profile::try_from(self.context.profile) {
            Ok(profile @ (Profile::Development | Profile::Production)) => Ok(profile),
            _ => Err(Error {
                code: crate::Code::InvalidArgument,
                reason: "invalid-profile".into(),
                message: format!(
                    "unknown or unspecified build profile {}",
                    self.context.profile
                ),
                help: "Supply the development or production profile.".into(),
                ..Default::default()
            }),
        }
    }

    /// Returns an option or an explicit error. Unknown and mistyped options
    /// never become a zero, empty string or empty list.
    fn option(&self, name: &str) -> Result<&crate::option_value::Value, crate::Error> {
        self.context
            .options
            .get(name)
            .and_then(|v| v.value.as_ref())
            .ok_or_else(|| Self::invalid_option(name, "a declared value"))
    }

    fn invalid_option(name: &str, expected: &str) -> crate::Error {
        crate::Error {
            code: crate::Code::InvalidArgument,
            reason: "invalid-option".into(),
            message: format!("option {name} requires {expected}"),
            help: "Supply every declared option with its declared kind.".into(),
            ..Default::default()
        }
    }

    /// Returns a string option, refusing a missing value or another kind.
    pub fn string(&self, name: &str) -> Result<&str, crate::Error> {
        match self.option(name)? {
            crate::option_value::Value::StringValue(value) => Ok(value),
            _ => Err(Self::invalid_option(name, "a string")),
        }
    }

    /// Returns a boolean option, preserving false as a present value.
    pub fn bool(&self, name: &str) -> Result<bool, crate::Error> {
        match self.option(name)? {
            crate::option_value::Value::BoolValue(value) => Ok(*value),
            _ => Err(Self::invalid_option(name, "a boolean")),
        }
    }

    /// Returns an exact signed 64-bit integer option.
    pub fn int(&self, name: &str) -> Result<i64, crate::Error> {
        match self.option(name)? {
            crate::option_value::Value::IntValue(value) => Ok(*value),
            _ => Err(Self::invalid_option(name, "an integer")),
        }
    }

    /// Returns a list option with its entries and order intact.
    pub fn string_list(&self, name: &str) -> Result<&[String], crate::Error> {
        match self.option(name)? {
            crate::option_value::Value::StringListValue(value) => Ok(&value.values),
            _ => Err(Self::invalid_option(name, "a string list")),
        }
    }

    /// The view a call's context and a host give.
    pub fn new(host: &'a dyn Host, context: Option<wire::Context>) -> Self {
        Repository {
            host,
            context: context.unwrap_or_default(),
        }
    }

    /// Returns a file, relative to the repository, if it is there.
    pub fn read(&self, path: &str) -> Result<Option<Vec<u8>>, Error> {
        self.host.read(path)
    }

    /// Returns repository-relative files selected beneath a literal directory.
    pub fn glob(&self, directory: &str, patterns: &[&str]) -> Result<Vec<String>, Error> {
        self.host.glob(directory, patterns)
    }

    /// Returns the version the repository pins for a tool.
    pub fn tool_version(&self, tool: &str) -> Option<&str> {
        self.context.tools.get(tool).map(String::as_str)
    }
}

/// Joins a repository-relative directory and a relative file name.
/// This preserves the names supplied by the caller; the host confines access.
pub fn join(dir: &str, file: &str) -> String {
    if dir.is_empty() {
        file.into()
    } else {
        format!("{dir}/{file}")
    }
}

/// Returns a name as a pattern that matches it and nothing else: what a
/// name read from the disk is written as where a pattern is called for.
/// A name is never a pattern; one that holds what a pattern reads, a
/// `[` or a `*`, is written so that it is read as the name.
pub fn literal(name: &str) -> String {
    let mut out = String::with_capacity(name.len());
    for (i, c) in name.chars().enumerate() {
        // A pattern that begins with "!" excludes what it matches: a
        // name that begins so is written to be read as the name.
        if matches!(c, '*' | '?' | '[' | ']' | '{' | '}' | '\\') || (i == 0 && c == '!') {
            out.push('\\');
        }
        out.push(c);
    }
    out
}

#[cfg(test)]
mod literal_tests {
    use super::literal;

    #[test]
    fn a_name_is_written_as_itself_in_a_pattern() {
        assert_eq!(literal("pkg[1]/a*b"), "pkg\\[1\\]/a\\*b");
        assert_eq!(literal("plain/name.go"), "plain/name.go");
        assert_eq!(literal("{x,y}?\\z"), "\\{x,y\\}\\?\\\\z");
        assert_eq!(literal("!x"), "\\!x");
        assert_eq!(literal("a!b"), "a!b");
    }
}
