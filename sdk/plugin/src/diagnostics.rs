//! Findings read from what a check printed, for what every plugin reads
//! alike: the files a formatter says it would change.
use crate::repository::join;
use crate::{Diagnostic, SourceLocation};
use std::collections::BTreeSet;

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
    /// The finding of one file, named relative to the project's root, or
    /// none where the name is no file beneath the root: empty, absolute,
    /// or with an empty or `..` part.
    pub fn file(&self, root: &str, file: &str) -> Option<Diagnostic> {
        if file.is_empty()
            || file.starts_with('/')
            || file.split('/').any(|part| part == ".." || part.is_empty())
        {
            return None;
        }
        Some(Diagnostic {
            rule_id: self.rule_id.into(),
            message: self.message.into(),
            source_location: Some(SourceLocation {
                repository: String::new(),
                path: join(root, file),
                line: 0,
            }),
            help: self.help.into(),
        })
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
    use super::Unformatted;

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
