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
            _ => Err(Error::new(
                crate::Code::InvalidArgument,
                "invalid-profile",
                format!(
                    "unknown or unspecified build profile {}",
                    self.context.profile
                ),
            )
            .help("Supply the development or production profile.")),
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
        crate::Error::new(
            crate::Code::InvalidArgument,
            "invalid-option",
            format!("option {name} requires {expected}"),
        )
        .help("Supply every declared option with its declared kind.")
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

    /// Returns the version the repository pins for a tool its actions
    /// run, or refuses, about the file named, as every plugin refuses a
    /// repository that pins none (`unpinned-tool`).
    pub fn pinned(&self, tool: &str, path: &str) -> Result<&str, Error> {
        self.tool_version(tool).ok_or_else(|| {
            Error::invalid(
                "unpinned-tool",
                path,
                format!("the repository pins no {tool}, which this project's actions run"),
                format!("Pin {tool} in mise.toml."),
            )
        })
    }

    /// Returns the directories beneath a project's root that hold a
    /// project of their own, each by its manifest's name ("go.mod",
    /// "Cargo.toml"), relative to the root and sorted; the root's own is
    /// not one. What the patterns `also_excluded` exclude, and every
    /// fixture ([`NOT_FIXTURES`]), is not looked at.
    pub fn nested(
        &self,
        root: &str,
        manifest: &str,
        also_excluded: &[&str],
    ) -> Result<Vec<String>, Error> {
        let anywhere = format!("**/{}", literal(manifest));
        let patterns: Vec<&str> = [anywhere.as_str()]
            .into_iter()
            .chain(also_excluded.iter().copied())
            .chain([NOT_FIXTURES])
            .collect();
        let mut nested: Vec<String> = self
            .glob(root, &patterns)?
            .iter()
            .filter_map(|file| beneath(root, file)?.strip_suffix(&format!("/{manifest}")))
            .map(String::from)
            .collect();
        nested.sort();
        Ok(nested)
    }
}

/// The pattern that leaves every fixture out: a `testdata` directory
/// holds fixtures, to every plugin, and what is in one is no project.
/// The SDK adds it to every plugin's interests; a plugin writes it where
/// it selects files of its own, in an input set or a glob.
pub const NOT_FIXTURES: &str = "!**/testdata/**";

/// Joins a repository-relative directory and a relative file name.
/// This preserves the names supplied by the caller; the host confines access.
pub fn join(dir: &str, file: &str) -> String {
    if dir.is_empty() {
        file.into()
    } else {
        format!("{dir}/{file}")
    }
}

/// Returns a path relative to a directory it is beneath, or none where
/// it is not beneath it, or is it: the inverse of [`join`]. Beneath the
/// root, the empty directory, is every path but the empty one.
///
/// ```
/// use frontseat_plugin::repository::beneath;
/// assert_eq!(beneath("app", "app/src/main.rs"), Some("src/main.rs"));
/// assert_eq!(beneath("", "go.mod"), Some("go.mod"));
/// assert_eq!(beneath("app", "application/go.mod"), None);
/// ```
pub fn beneath<'a>(directory: &str, path: &'a str) -> Option<&'a str> {
    if directory.is_empty() {
        return (!path.is_empty()).then_some(path);
    }
    path.strip_prefix(directory)?
        .strip_prefix('/')
        .filter(|rest| !rest.is_empty())
}

/// Returns a path written relative to a directory of the repository as
/// the repository's own, its "." and ".." parts resolved, or none where
/// it is absolute or leaves the repository.
///
/// ```
/// use frontseat_plugin::repository::resolve;
/// assert_eq!(resolve("api", "../lib/./gen").as_deref(), Some("lib/gen"));
/// assert_eq!(resolve("api", "../../gen"), None);
/// ```
pub fn resolve(directory: &str, relative: &str) -> Option<String> {
    if relative.starts_with('/') {
        return None;
    }
    let mut parts: Vec<&str> = Vec::new();
    for part in directory.split('/').chain(relative.split('/')) {
        match part {
            "" | "." => {}
            ".." => {
                parts.pop()?;
            }
            name => parts.push(name),
        }
    }
    Some(parts.join("/"))
}

/// Whether a path names a file beneath where it is relative to, written
/// plainly: not empty, not absolute, and with no empty or ".." part.
/// What a tool prints is named so before it is a finding of a file.
pub fn plain(path: &str) -> bool {
    !path.is_empty()
        && !path.starts_with('/')
        && path.split('/').all(|part| part != ".." && !part.is_empty())
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
    use super::{beneath, literal, plain, resolve};

    #[test]
    fn a_path_is_read_relative_to_a_directory_as_the_repository_names_it() {
        assert_eq!(beneath("proto", "proto/gen/go"), Some("gen/go"));
        assert_eq!(beneath("", "gen/go"), Some("gen/go"));
        for apart in ["proto", "protos/gen", "lib/proto/gen", ""] {
            assert_eq!(beneath("proto", apart), None, "{apart}");
        }
        assert_eq!(resolve("", "a/./b/../c").as_deref(), Some("a/c"));
        assert_eq!(
            resolve("proto/api", "../../lib/gen").as_deref(),
            Some("lib/gen")
        );
        assert_eq!(resolve("api", "..").as_deref(), Some(""));
        for outside in ["../../gen", "/abs"] {
            assert_eq!(resolve("api", outside), None, "{outside}");
        }
        assert!(plain("a/b.go") && plain("a.go"));
        for not in ["", "/a.go", "../a.go", "x/../y.go", "x//y.go", "x/"] {
            assert!(!plain(not), "{not}");
        }
    }

    #[test]
    fn a_name_is_written_as_itself_in_a_pattern() {
        assert_eq!(literal("pkg[1]/a*b"), "pkg\\[1\\]/a\\*b");
        assert_eq!(literal("plain/name.go"), "plain/name.go");
        assert_eq!(literal("{x,y}?\\z"), "\\{x,y\\}\\?\\\\z");
        assert_eq!(literal("!x"), "\\!x");
        assert_eq!(literal("a!b"), "a!b");
    }
}
