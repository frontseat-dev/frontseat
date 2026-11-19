use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{beneath, join, literal, NOT_FIXTURES};

/// What the go command reads of a module besides what its files embed:
/// its Go files, and with them the assembly beside them, the headers
/// that includes and the objects linked as they are. A directory named
/// testdata is no package's, and nothing in it is compiled.
///
/// A module is built without cgo, so C and what goes with it is left
/// out: the go command refuses it there.
const SOURCES: &[&str] = &[
    "go.mod",
    "go.sum",
    "**/*.go",
    "**/*.s",
    "**/*.h",
    "**/*.syso",
    NOT_FIXTURES,
];

/// What one reading of a module's Go files says of it.
pub(super) struct Read {
    /// Its inputs, by name.
    pub(super) sets: Vec<InputSet>,
    /// Its directories that are programs.
    pub(super) programs: Vec<String>,
    /// Its directories that hold a Go file the go command may find: its
    /// packages, by name.
    pub(super) packages: Vec<String>,
    /// Its directories that hold tests.
    pub(super) tested: Vec<Tested>,
    /// The directories of the modules nested in it, each a module of its
    /// own.
    pub(super) nested: Vec<String>,
}

/// A directory of a module that holds test files, by their names.
#[derive(Default)]
pub(super) struct Tested {
    /// The directory, within the module; empty for the module's own.
    pub(super) directory: String,
    /// The tests its files declare, by name, in order.
    pub(super) names: std::collections::BTreeSet<String>,
}

/// The inputs of a module, by name. A later pattern decides over an
/// earlier one, so what is embedded comes last: a file is embedded from
/// wherever its directive says, a directory of fixtures included.
///
/// The same reading says which of its directories are programs and which
/// hold tests, since every Go file is read once here: see
/// `crate::programs`.
pub(super) fn input_sets(repo: &Repository, root: &str) -> Result<Read, Error> {
    let tags = repo.string_list("tags")?;
    // The go the repository pins, by its minor version: "1.27.3" is 27.
    let go_minor = repo
        .tool_version("go")
        .and_then(|version| version.split('.').nth(1))
        .and_then(|minor| minor.parse().ok());
    let built = crate::programs::Built { tags, go_minor };
    let mut programs = std::collections::BTreeSet::new();
    // In the order of their names.
    let mut tested = Vec::<Tested>::new();
    let mut packages = std::collections::BTreeSet::new();
    let mut sources = SOURCES.iter().map(|v| v.to_string()).collect::<Vec<_>>();
    let mut module = SOURCES.iter().map(|v| v.to_string()).collect::<Vec<_>>();
    module.push("!**/*_test.go".into());
    // A module nested in this one is a module of its own: the go command
    // stops at its go.mod, and so does this module's reading.
    let nested = repo.nested(root, "go.mod", &[])?;
    for directory in &nested {
        // A directory's name is a name, and is written as one.
        sources.push(format!("!{}/**", literal(directory)));
        module.push(format!("!{}/**", literal(directory)));
    }
    for file in repo.glob(root, &["**/*.go", NOT_FIXTURES])? {
        let within = beneath(root, &file).unwrap_or(&file);
        if nested
            .iter()
            .any(|directory| within.starts_with(&format!("{directory}/")))
        {
            continue;
        }
        let Some(raw) = repo.text(&file)? else {
            continue;
        };
        let directory = within
            .rsplit_once('/')
            .map(|(directory, _)| directory)
            .unwrap_or("");
        if crate::programs::is_program(within, &raw, &built) {
            programs.insert(directory.to_string());
        }
        if crate::programs::is_package_file(within) {
            packages.insert(directory.to_string());
        }
        if crate::programs::is_test_file(within) {
            let at = match tested.binary_search_by(|held| held.directory.as_str().cmp(directory)) {
                Ok(at) => at,
                Err(at) => {
                    tested.insert(
                        at,
                        Tested {
                            directory: directory.to_string(),
                            ..Tested::default()
                        },
                    );
                    at
                }
            };
            crate::programs::test_names(&raw, &mut tested[at].names);
        }
        for pattern in embedded(&raw) {
            // A pattern names a file, or a directory and all beneath it.
            for input in [
                join(&literal(directory), &pattern),
                format!("{}/**", join(&literal(directory), &pattern)),
            ] {
                if !within.ends_with("_test.go") {
                    module.push(input.clone());
                }
                sources.push(input);
            }
        }
    }
    let sets = vec![
        // What a module is linted, compiled and tested from.
        InputSet {
            name: "sources".into(),
            inputs: sources.into_iter().map(Input::source).collect(),
            accepts_generated: true,
        },
        // What a module that requires this one is built from: its tests
        // are no part of it.
        InputSet {
            name: "module".into(),
            inputs: module.into_iter().map(Input::source).collect(),
            accepts_generated: true,
        },
    ];
    Ok(Read {
        sets,
        programs: programs.into_iter().collect(),
        packages: packages.into_iter().collect(),
        tested,
        nested,
    })
}

/// The patterns a Go file embeds: those of each //go:embed directive, as
/// the go command reads one. Patterns are separated by spaces, and one
/// that holds a space is written as a Go string, quoted or between
/// backquotes. "all:" before a pattern says to embed the files the go
/// command otherwise leaves out of a directory; they are given either
/// way, so it is dropped.
fn embedded(source: &str) -> Vec<String> {
    let mut patterns = Vec::new();
    for line in source.lines() {
        let Some(rest) = line.trim_start().strip_prefix("//go:embed") else {
            continue;
        };
        if !rest.starts_with([' ', '\t']) {
            continue;
        }
        let mut letters = rest.chars().peekable();
        while let Some(&first) = letters.peek() {
            if first.is_whitespace() {
                letters.next();
                continue;
            }
            let mut pattern = String::new();
            match first {
                '"' => {
                    letters.next();
                    let Some(decoded) = quoted(&mut letters) else {
                        break;
                    };
                    pattern = decoded;
                }
                '`' => {
                    letters.next();
                    pattern.extend(letters.by_ref().take_while(|&letter| letter != '`'));
                }
                _ => {
                    while let Some(letter) = letters.next_if(|letter| !letter.is_whitespace()) {
                        pattern.push(letter);
                    }
                }
            }
            let pattern = pattern.strip_prefix("all:").unwrap_or(&pattern);
            if !pattern.is_empty() {
                patterns.push(pattern.to_string());
            }
        }
    }
    patterns
}

// Go's byte and Unicode escape rules, following strconv.UnquoteChar in
// Go 1.27.3 (src/strconv/quote.go; copyright/license in ../LICENSE.go.txt).
// Byte escapes are assembled before UTF-8 decoding: "\xc3\xa9" is one rune.
// This is Go syntax, so it belongs to this plugin rather than the shared SDK.
fn quoted(letters: &mut impl Iterator<Item = char>) -> Option<String> {
    let mut bytes = Vec::new();
    while let Some(letter) = letters.next() {
        match letter {
            '"' => return String::from_utf8(bytes).ok(),
            '\n' => return None,
            '\\' => {
                let escape = letters.next()?;
                match escape {
                    'a' => bytes.push(7),
                    'b' => bytes.push(8),
                    'f' => bytes.push(12),
                    'n' => bytes.push(b'\n'),
                    'r' => bytes.push(b'\r'),
                    't' => bytes.push(b'\t'),
                    'v' => bytes.push(11),
                    '\\' | '"' => bytes.push(u8::try_from(escape).ok()?),
                    'x' | 'u' | 'U' => {
                        let digits = match escape {
                            'x' => 2,
                            'u' => 4,
                            _ => 8,
                        };
                        let mut value = 0u32;
                        for _ in 0..digits {
                            value = value * 16 + letters.next()?.to_digit(16)?;
                        }
                        if escape == 'x' {
                            bytes.push(u8::try_from(value).ok()?);
                        } else {
                            bytes.extend_from_slice(
                                char::from_u32(value)?.encode_utf8(&mut [0; 4]).as_bytes(),
                            );
                        }
                    }
                    '0'..='7' => {
                        let mut value = escape.to_digit(8)?;
                        for _ in 0..2 {
                            value = value * 8 + letters.next()?.to_digit(8)?;
                        }
                        bytes.push(u8::try_from(value).ok()?);
                    }
                    _ => return None,
                }
            }
            _ => bytes.extend_from_slice(letter.encode_utf8(&mut [0; 4]).as_bytes()),
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::embedded;
    #[test]
    fn a_directive_names_its_patterns_as_the_go_command_reads_them() {
        let source = "package p\n\nimport \"embed\"\n\n//go:embed asset.txt\nvar a string\n\n//go:embed static \"with space.txt\" `back quoted.txt`\t all:hidden\n//go:embed templates/*.tmpl\nvar files embed.FS\n";
        assert_eq!(
            embedded(source),
            [
                "asset.txt",
                "static",
                "with space.txt",
                "back quoted.txt",
                "hidden",
                "templates/*.tmpl"
            ]
        );
    }

    #[test]
    fn indentation_and_go_string_escapes_preserve_embed_names() {
        for directive in [
            "\t//go:embed asset.txt",
            r#"//go:embed "\x61sset.txt""#,
            r#"//go:embed "\141sset.txt""#,
            r#"//go:embed "\u0061sset.txt""#,
            r#"//go:embed "\U00000061sset.txt""#,
        ] {
            assert_eq!(embedded(directive), ["asset.txt"], "{directive}");
        }
        assert_eq!(embedded(r#"//go:embed "\xc3\xa9.txt""#), ["é.txt"]);
        assert_eq!(embedded(r#"//go:embed "a\tb.txt""#), ["a\tb.txt"]);
        for invalid in [
            r#"//go:embed "\x0""#,
            r#"//go:embed "\uD800""#,
            r#"//go:embed "\U00110000""#,
            r#"//go:embed "\400""#,
            r#"//go:embed "\q""#,
        ] {
            assert!(embedded(invalid).is_empty(), "{invalid}");
        }
    }

    #[test]
    fn what_only_looks_like_a_directive_names_nothing() {
        let source = "package p\n\n// go:embed spaced.txt\n//go:embedded joined.txt\nvar s = \"//go:embed in-a-string.txt\"\n//go:embed\n";
        assert_eq!(embedded(source), Vec::<String>::new());
    }
}
