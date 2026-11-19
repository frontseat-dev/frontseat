//! Findings read from what a check printed: a finding of a rule at a
//! file of the project ([`Finding`]), what most tools print one as
//! ([`Located`]), and the files a formatter says it would change
//! ([`Unformatted`]).
//!
//! ```
//! use frontseat_plugin::diagnostics::{Finding, Located};
//!
//! const VET: Finding = Finding { rule_id: "go/vet", help: "See `go doc cmd/vet`." };
//! let printed = "# example.com/app\n./main.go:7:2: unreachable code\n";
//! let found: Vec<_> = printed
//!     .lines()
//!     .filter_map(Located::read)
//!     .filter_map(|at| VET.at("app", at.path, at.line, at.message))
//!     .collect();
//! assert_eq!(found[0].source_location.as_ref().unwrap().path, "app/main.go");
//! ```
use crate::repository::{join, plain};
use crate::{Diagnostic, SourceLocation};
use std::collections::BTreeSet;

/// The findings of one rule, each of a file beneath a project's root:
/// the rule, qualified by the plugin, and what to do about any of them.
pub struct Finding<'a> {
    /// The rule, qualified by the plugin: "go/vet".
    pub rule_id: &'a str,
    /// What to do about it.
    pub help: &'a str,
}

impl Finding<'_> {
    /// The finding at a file, named relative to the project's root, and
    /// a line of it from 1, or 0 for none; or none where the name is no
    /// file beneath the root ([`plain`]).
    pub fn at(
        &self,
        root: &str,
        file: &str,
        line: i32,
        message: impl Into<String>,
    ) -> Option<Diagnostic> {
        plain(file).then(|| Diagnostic {
            rule_id: self.rule_id.into(),
            message: message.into(),
            source_location: Some(SourceLocation {
                repository: String::new(),
                path: join(root, file),
                line,
            }),
            help: self.help.into(),
        })
    }
}

/// A finding as most tools print one, on a line of its own:
/// `path:line: message` or `path:line:column: message`, as go vet,
/// clippy's short messages and most linters print them.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Located<'a> {
    /// The file, as the tool named it, without a leading "./".
    pub path: &'a str,
    /// The line, from 1.
    pub line: i32,
    /// The column, from 1, where the tool said one.
    pub column: Option<u32>,
    /// What it says there, trimmed.
    pub message: &'a str,
}

impl<'a> Located<'a> {
    /// Reads a line a tool printed, or none where it is no finding: a
    /// line with no line number of 1 or more after its path, or with
    /// nothing to say.
    pub fn read(printed: &'a str) -> Option<Self> {
        let (path, rest) = printed.trim_end().split_once(':')?;
        let (number, rest) = rest.split_once(':')?;
        let line: i32 = number.trim().parse().ok().filter(|line| *line >= 1)?;
        let (column, message) = match rest.split_once(':') {
            Some((column, message)) => match column.trim().parse::<u32>() {
                Ok(column) => (Some(column), message),
                Err(_) => (None, rest),
            },
            None => (None, rest),
        };
        let message = message.trim();
        (!message.is_empty()).then_some(Located {
            path: path.strip_prefix("./").unwrap_or(path),
            line,
            column,
            message,
        })
    }
}

/// The finding a formatter's check makes of each file it would change:
/// one rule, message and help for every file, each file named beneath a
/// project's root. The plugin reads its tool's own lines into the files
/// they name; this says what is a file of the project and makes each a
/// finding.
pub struct Unformatted<'a> {
    /// The rule, qualified by the plugin: "go/format".
    pub rule_id: &'a str,
    /// What is wrong with each file: "it is not formatted as gofmt formats it".
    pub message: &'a str,
    /// What to do about it.
    pub help: &'a str,
}

impl Unformatted<'_> {
    /// What `frontseat build --fix` is said to do, by every formatter's
    /// finding alike: run the formatter, named, where the checkout is.
    pub fn fixed_by(formatter: &str) -> String {
        format!("Run `frontseat build --fix`, which runs {formatter} where the checkout is.")
    }

    /// The finding of one file, named relative to the project's root, or
    /// none where the name is no file beneath the root: empty, absolute,
    /// or with an empty or `..` part.
    pub fn file(&self, root: &str, file: &str) -> Option<Diagnostic> {
        Finding {
            rule_id: self.rule_id,
            help: self.help,
        }
        .at(root, file, 0, self.message)
    }

    /// The findings of the files named, in the order they are named, each
    /// once: those that are no file beneath the root are left out, as
    /// [`Unformatted::file`] says.
    pub fn files<'f>(
        &self,
        root: &str,
        files: impl IntoIterator<Item = &'f str>,
    ) -> Vec<Diagnostic> {
        let mut seen = BTreeSet::new();
        files
            .into_iter()
            .filter_map(|file| {
                let found = self.file(root, file)?;
                seen.insert(file).then_some(found)
            })
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::{Located, Unformatted};

    #[test]
    fn a_line_a_tool_prints_is_a_finding_where_it_names_a_line_of_a_file() {
        assert_eq!(
            Located::read("./main.go:7:2: unreachable code\n"),
            Some(Located {
                path: "main.go",
                line: 7,
                column: Some(2),
                message: "unreachable code"
            })
        );
        assert_eq!(
            Located::read("src/lib.rs:3:5: warning: unused: x"),
            Some(Located {
                path: "src/lib.rs",
                line: 3,
                column: Some(5),
                message: "warning: unused: x"
            })
        );
        assert_eq!(
            Located::read("a.go:4: it is wrong: here"),
            Some(Located {
                path: "a.go",
                line: 4,
                column: None,
                message: "it is wrong: here"
            })
        );
        for not in [
            "# example.com/app",
            "vet: a.go:1:1: x",
            "a.go:0: x",
            "a.go:x: y",
            "a.go:3:",
            "a.go:3:4:  ",
            "",
        ] {
            assert_eq!(Located::read(not), None, "{not}");
        }
    }

    const FORMAT: Unformatted = Unformatted {
        rule_id: "tool/format",
        message: "it is not formatted",
        help: "Format it.",
    };

    #[test]
    fn each_file_beneath_the_root_is_a_finding_once_and_nothing_else_is() {
        let found = FORMAT.files(
            "app",
            [
                "a.go",
                "",
                "/abs.go",
                "../up.go",
                "x/../y.go",
                "x//y.go",
                "b/c.go",
                "a.go",
            ],
        );
        let paths: Vec<_> = found
            .iter()
            .map(|d| d.source_location.as_ref().unwrap().path.as_str())
            .collect();
        assert_eq!(paths, ["app/a.go", "app/b/c.go"]);
        assert_eq!(
            (
                found[0].rule_id.as_str(),
                found[0].message.as_str(),
                found[0].help.as_str(),
                found[0].source_location.as_ref().unwrap().line
            ),
            ("tool/format", "it is not formatted", "Format it.", 0)
        );
        assert_eq!(
            FORMAT
                .file("", "a.go")
                .unwrap()
                .source_location
                .unwrap()
                .path,
            "a.go"
        );
    }
}
