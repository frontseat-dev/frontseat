//! Which files patterns select, as the host selects them: Go's
//! `pattern.Select` and `pattern.Within` (`server/internal/pattern`),
//! which match each pattern as doublestar does.
//!
//! Each pattern is first held to the contract's language (`spelled`),
//! and one that is not selects nothing. What is left is matched by
//! globset, given what doublestar means by it where the two read a
//! pattern apart:
//!
//! - braces are expanded first, as doublestar expands them, each
//!   alternative a pattern of its own: an empty alternative is one;
//! - "a/**" selects a itself, in each alternative: "{a,b}/**" selects
//!   b; but "x*/**" does not select x, as the path's end inside a star is
//!   no pattern of nothing to doublestar;
//! - a class is read as doublestar reads it (a `\` escapes in it, `-`
//!   after a range is itself, and an empty class is no pattern) and
//!   written as globset reads the same class;
//! - two stars are `**` only where they are a whole part of the path,
//!   and `*` anywhere else;
//! - `?` and a class stand for one character, not one byte: what globset
//!   compiles is matched in Unicode.
//!
//! What the language leaves out is where the two were found to differ,
//! by comparing them over 200,000 random patterns: empty braces, a run
//! of three stars, a `/` within braces, a pattern that ends in `/`. Over
//! the rest, no difference is known.
//!
//! `testdata/select.json` is the table of cases both matchers run
//! (`server/internal/pattern/select_test.go`), and each difference found
//! is a case in it.

use globset::GlobBuilder;
use regex_automata::meta::Regex;

/// Whether patterns select a path relative to where they are written. A
/// pattern that starts with "!" excludes what it matches, and the last
/// pattern to match decides. A pattern that is not one selects nothing.
pub(crate) fn select(patterns: &[&str], path: &str) -> bool {
    let mut keep = false;
    for pattern in patterns {
        let (negated, plain) = match pattern.strip_prefix('!') {
            Some(plain) => (true, plain),
            None => (false, *pattern),
        };
        if matches(plain, path) {
            keep = !negated;
        }
    }
    keep
}

/// Whether patterns select a file of the repository beneath a literal
/// directory, matched as it is named beneath it. The directory is a name
/// and never a pattern, cleaned as Go's `path.Clean` cleans it: empty or
/// "." is the root, and "app/", "./app" and "app/." are app.
pub(crate) fn within(directory: &str, file: &str, patterns: &[&str]) -> bool {
    let directory = clean(directory);
    let file = if directory == "." {
        file
    } else {
        match file
            .strip_prefix(directory.as_str())
            .and_then(|rest| rest.strip_prefix('/'))
        {
            Some(rest) => rest,
            None => return false,
        }
    };
    select(patterns, file)
}

/// Whether a pattern is written in the language the plugin contract
/// says (`host.proto`), as Go's `pattern.Spelled` holds it: no empty
/// part, no run of three stars, no `/` in a class, in braces or escaped,
/// no braces with nothing in them and no `}` that closes nothing. What
/// doublestar refuses besides is refused where it is read (`expand`,
/// `class_end`).
fn spelled(pattern: &str) -> bool {
    if pattern.is_empty()
        || pattern.starts_with('/')
        || pattern.ends_with('/')
        || pattern.contains("//")
        || pattern.contains("***")
    {
        return false;
    }
    let (mut class, mut braces) = (false, 0usize);
    let mut chars = pattern.chars().peekable();
    while let Some(c) = chars.next() {
        match c {
            '\\' => {
                if chars.next() == Some('/') {
                    return false;
                }
            }
            _ if class => {
                if c == '/' {
                    return false;
                }
                class = c != ']';
            }
            '[' => class = true,
            '{' if chars.peek() == Some(&'}') => return false,
            '{' => braces += 1,
            '}' if braces == 0 => return false,
            '}' => braces -= 1,
            '/' if braces > 0 => return false,
            _ => {}
        }
    }
    true
}

/// One pattern against one path, as the host's `Select` matches one it
/// was asked with: false for a pattern the contract's language leaves
/// out, or doublestar refuses.
fn matches(pattern: &str, path: &str) -> bool {
    if !spelled(pattern) {
        return false;
    }
    let chars: Vec<char> = pattern.chars().collect();
    let mut at = 0;
    let Some(alternatives) = expand(&chars, &mut at, false) else {
        return false;
    };
    alternatives
        .iter()
        .any(|alternative| whole(alternative, path) || zero_length(alternative, path))
}

/// Whether one alternative, braces expanded, matches all of a path.
fn whole(alternative: &str, path: &str) -> bool {
    translate(alternative)
        .and_then(|glob| compile(&glob))
        .is_some_and(|glob| glob.is_match(path))
}

/// Whether the path ends where what is left of the alternative, "/**",
/// stands for nothing, as doublestar's `isZeroLengthPattern` says, so
/// that "a/**" selects a itself. Stars just before it match a character
/// or more: doublestar takes the path's end inside a star as what is
/// left, so "x*/**" does not select x, and "*/**" selects a.
fn zero_length(alternative: &str, path: &str) -> bool {
    alternative.strip_suffix("/**").is_some_and(|before| {
        let stars = before.chars().rev().take_while(|&c| c == '*').count();
        let rest = &before[..before.len() - stars];
        !before.is_empty()
            && !escaped_at_end(before)
            && if stars > 0 && !escaped_at_end(rest) {
                whole(&format!("{rest}?*"), path)
            } else {
                whole(before, path)
            }
    })
}

/// A globset pattern compiled, matched in Unicode: globset compiles a
/// pattern to a regular expression of bytes, in which `?` is one byte of
/// a name, and here it is one character.
fn compile(glob: &str) -> Option<Regex> {
    let compiled = GlobBuilder::new(glob)
        .literal_separator(true)
        .backslash_escape(true)
        .build()
        .ok()?;
    // globset promises nothing of what it writes here: its version is
    // pinned (Cargo.toml), and one that writes otherwise is heard of,
    // where every pattern would select nothing without a word.
    let bytes = compiled
        .regex()
        .strip_prefix("(?-u)")
        .expect("globset writes a pattern's regular expression after (?-u)");
    Regex::new(&unicode(bytes)?).ok()
}

/// A regular expression of bytes, as globset writes one, with each
/// character it wrote as its bytes, `\xc3\xa9`, written as itself.
fn unicode(regex: &str) -> Option<String> {
    let mut out = String::with_capacity(regex.len());
    let mut bytes = Vec::new();
    let mut rest = regex;
    while let Some(c) = rest.chars().next() {
        let byte = rest
            .strip_prefix("\\x")
            .and_then(|hex| hex.get(..2))
            .and_then(|hex| u8::from_str_radix(hex, 16).ok());
        if let Some(byte) = byte {
            bytes.push(byte);
            rest = &rest[4..];
            continue;
        }
        if !bytes.is_empty() {
            out.push_str(std::str::from_utf8(&bytes).ok()?);
            bytes.clear();
        }
        let width = if c == '\\' {
            1 + rest[1..].chars().next()?.len_utf8()
        } else {
            c.len_utf8()
        };
        out.push_str(&rest[..width]);
        rest = &rest[width..];
    }
    out.push_str(std::str::from_utf8(&bytes).ok()?);
    Some(out)
}

/// Whether a pattern ends in a backslash that escapes what follows it.
fn escaped_at_end(pattern: &str) -> bool {
    pattern.chars().rev().take_while(|&c| c == '\\').count() % 2 == 1
}

/// The patterns a pattern's braces stand for, read from `at` to its end,
/// or, `inside` braces, to the comma or brace that ends an alternative.
/// Escapes and classes are copied as they are; a `}` that closes nothing
/// is itself. None for braces not closed, a class not closed or a
/// backslash at the end.
fn expand(chars: &[char], at: &mut usize, inside: bool) -> Option<Vec<String>> {
    let mut patterns = vec![String::new()];
    let push = |patterns: &mut Vec<String>, text: &str| {
        for pattern in patterns.iter_mut() {
            pattern.push_str(text);
        }
    };
    while let Some(&c) = chars.get(*at) {
        match c {
            '\\' => {
                let escaped: String = ['\\', *chars.get(*at + 1)?].iter().collect();
                push(&mut patterns, &escaped);
                *at += 2;
            }
            '[' => {
                let end = class_end(chars, *at)?;
                let class: String = chars[*at..=end].iter().collect();
                push(&mut patterns, &class);
                *at = end + 1;
            }
            '{' => {
                *at += 1;
                let mut alternatives = Vec::new();
                loop {
                    alternatives.extend(expand(chars, at, true)?);
                    let ended = *chars.get(*at)?;
                    *at += 1;
                    if ended == '}' {
                        break;
                    }
                }
                patterns = patterns
                    .iter()
                    .flat_map(|before| {
                        alternatives
                            .iter()
                            .map(move |after| format!("{before}{after}"))
                    })
                    .collect();
            }
            ',' | '}' if inside => return Some(patterns),
            '}' => {
                push(&mut patterns, "\\}");
                *at += 1;
            }
            c => {
                push(&mut patterns, c.encode_utf8(&mut [0; 4]));
                *at += 1;
            }
        }
    }
    Some(patterns)
}

/// Where the class that begins at `start` ends, as doublestar reads one:
/// at the first `]` not escaped, after a `!` or `^` that negates it. None
/// for one not closed, or empty.
fn class_end(chars: &[char], start: usize) -> Option<usize> {
    let mut at = start + 1;
    if matches!(chars.get(at), Some('!' | '^')) {
        at += 1;
    }
    if chars.get(at) == Some(&']') {
        return None;
    }
    while *chars.get(at)? != ']' {
        at += if chars[at] == '\\' { 2 } else { 1 };
    }
    Some(at)
}

/// A pattern with no braces, written as globset reads what doublestar
/// means by it. None for a class doublestar refuses.
fn translate(pattern: &str) -> Option<String> {
    let chars: Vec<char> = pattern.chars().collect();
    let mut out = String::with_capacity(pattern.len());
    let mut at = 0;
    while let Some(&c) = chars.get(at) {
        match c {
            '\\' => {
                out.push('\\');
                out.push(*chars.get(at + 1)?);
                at += 2;
            }
            '[' => {
                let end = class_end(&chars, at)?;
                out.push_str(&class(&chars[at + 1..end])?);
                at = end + 1;
            }
            '*' => {
                let run = chars[at..].iter().take_while(|&&c| c == '*').count();
                let whole = run == 2
                    && (at == 0 || chars[at - 1] == '/')
                    && matches!(chars.get(at + run), None | Some('/'));
                out.push_str(if whole { "**" } else { "*" });
                at += run;
            }
            c => {
                out.push(c);
                at += 1;
            }
        }
    }
    Some(out)
}

/// A class's contents, between its brackets, read as doublestar reads
/// them and written as globset reads the same class: `]` first, `-` last,
/// and no `!` or `^` first unless it negates.
fn class(contents: &[char]) -> Option<String> {
    let (negated, mut rest) = match contents.first() {
        Some('!' | '^') => (true, &contents[1..]),
        _ => (false, contents),
    };
    let mut singles: Vec<char> = Vec::new();
    let mut ranges: Vec<(char, char)> = Vec::new();
    let mut last: Option<char> = None;
    while let Some(&c) = rest.first() {
        rest = &rest[1..];
        if c == '-' && !rest.is_empty() {
            if let Some(low) = last {
                let mut high = rest[0];
                rest = &rest[1..];
                if high == '\\' {
                    high = *rest.first()?;
                    rest = &rest[1..];
                }
                // The low end was taken as itself; it is the range's.
                singles.pop();
                ranges.push((low, high));
                last = None;
                continue;
            }
        }
        let single = if c == '\\' {
            let escaped = *rest.first()?;
            rest = &rest[1..];
            escaped
        } else {
            c
        };
        singles.push(single);
        last = Some(single);
    }
    // Where they stand is what globset reads them by: a `]` first is
    // itself, a `-` last is itself, and a `!` or `^` first negates. A
    // class of those alone that is not negated is written as the
    // characters it stands for, one of them.
    let marks = |c: &char| matches!(c, ']' | '-' | '!' | '^');
    let plain: String = singles.iter().filter(|c| !marks(c)).collect();
    if !negated && plain.is_empty() && ranges.is_empty() && !singles.contains(&']') {
        let each: Vec<String> = singles.iter().map(|c| format!("\\{c}")).collect();
        return Some(format!("{{{}}}", each.join(",")));
    }
    let mut out = String::from(if negated { "[!" } else { "[" });
    if singles.contains(&']') {
        out.push(']');
    }
    out.push_str(&plain);
    for (low, high) in &ranges {
        out.push(*low);
        out.push('-');
        out.push(*high);
    }
    out.extend(singles.iter().filter(|c| matches!(c, '!' | '^')));
    if singles.contains(&'-') {
        out.push('-');
    }
    out.push(']');
    Some(out)
}

/// A directory as Go's `path.Clean` writes it.
fn clean(directory: &str) -> String {
    let rooted = directory.starts_with('/');
    let mut parts: Vec<&str> = Vec::new();
    for part in directory.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                if parts.last().is_some_and(|last| *last != "..") {
                    parts.pop();
                } else if !rooted {
                    parts.push("..");
                }
            }
            other => parts.push(other),
        }
    }
    let joined = parts.join("/");
    match (rooted, joined.is_empty()) {
        (true, _) => format!("/{joined}"),
        (false, true) => ".".into(),
        (false, false) => joined,
    }
}

#[cfg(test)]
mod tests {
    use super::{clean, select, within};

    #[derive(serde::Deserialize)]
    struct Case {
        patterns: Vec<String>,
        path: String,
        directory: Option<String>,
        selected: bool,
    }

    /// The table the host's matcher runs too
    /// (`server/internal/pattern/select_test.go`).
    #[test]
    fn selects_as_the_host_does_every_case_of_the_shared_table() {
        let raw =
            std::fs::read(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/select.json")).unwrap();
        let cases: Vec<Case> = serde_json::from_slice(&raw).unwrap();
        assert!(!cases.is_empty());
        let mut wrong = Vec::new();
        for case in &cases {
            let patterns: Vec<&str> = case.patterns.iter().map(String::as_str).collect();
            let got = match &case.directory {
                Some(directory) => within(directory, &case.path, &patterns),
                None => select(&patterns, &case.path),
            };
            if got != case.selected {
                wrong.push(format!(
                    "{:?} beneath {:?} selects {:?}: {got}, the table says {}",
                    case.patterns, case.directory, case.path, case.selected
                ));
            }
        }
        assert!(wrong.is_empty(), "{}", wrong.join("\n"));
    }

    /// A name written as a pattern (`repository::literal`) selects the
    /// name and nothing near it, by this matcher and, each a case of the
    /// shared table, by the host's.
    #[test]
    fn a_name_written_as_a_pattern_selects_itself_alone() {
        let raw =
            std::fs::read(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/select.json")).unwrap();
        let cases: Vec<Case> = serde_json::from_slice(&raw).unwrap();
        for (name, near) in [
            ("a[1]", "a1"),
            ("{x,y}", "x"),
            ("!x", "x"),
            ("a\\b", "ab"),
            ("é?", "éx"),
            ("a*b", "axb"),
            ("pkg[1]/a*b", "pkg1/axb"),
        ] {
            let pattern = frontseat_plugin::repository::literal(name);
            assert!(
                select(&[&pattern], name),
                "{pattern} does not select {name}"
            );
            assert!(!select(&[&pattern], near), "{pattern} selects {near}");
            for (path, selected) in [(name, true), (near, false)] {
                assert!(
                    cases.iter().any(|case| case.patterns == [pattern.as_str()]
                        && case.path == path
                        && case.selected == selected
                        && case.directory.is_none()),
                    "the shared table holds no case of {pattern} and {path}, for the host to run"
                );
            }
        }
    }

    #[test]
    fn a_directory_is_cleaned_as_go_cleans_a_path() {
        for (directory, cleaned) in [
            ("", "."),
            (".", "."),
            ("app/", "app"),
            ("./app", "app"),
            ("app/.", "app"),
            ("a/../b", "b"),
            ("../x", "../x"),
            ("a//b", "a/b"),
            ("/a/..", "/"),
        ] {
            assert_eq!(clean(directory), cleaned, "{directory}");
        }
    }
}
