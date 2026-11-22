//! Which of a module's directories are programs, and how each is run.
//!
//! A directory is a program where a Go file of it that the go command
//! builds on a worker, no test, says `package main`. Which files those
//! are is read here as the go command reads it (go/build's
//! parseFileHeader, shouldBuild and goodOSArchFile): not a file or a
//! directory whose name begins with `_` or `.`, nothing beneath a
//! `vendor` directory, not a file named for another system, not one that
//! imports "C", and not one whose build constraint, `//go:build` or the
//! older `// +build`, does not hold. A worker is Linux, builds without
//! cgo, and is arm64 or amd64: a file is counted where it is built on
//! either, so a program of one architecture alone is offered on both,
//! and running it where it was not built fails saying nothing left
//! it. The tags a repository builds with (the `tags` option) hold, and
//! a release tag (`go1.21`) up to the go it pins.
//!
//! This is the go command's rule written again, and so proven against
//! it: `differs_from_the_go_command_nowhere` below, run with every
//! other test over files written to be hard to read, and over real
//! repositories where it is told of some.

use crate::actions::{COMPILE, PROGRAMS};
use frontseat_plugin::prelude::*;

/// The operating systems and architectures the go command reads from a
/// file's name (go/build/syslist.go).
const SYSTEMS: &[&str] = &[
    "aix",
    "android",
    "darwin",
    "dragonfly",
    "freebsd",
    "hurd",
    "illumos",
    "ios",
    "js",
    "linux",
    "nacl",
    "netbsd",
    "openbsd",
    "plan9",
    "solaris",
    "wasip1",
    "windows",
    "zos",
];
const ARCHITECTURES: &[&str] = &[
    "386",
    "amd64",
    "amd64p32",
    "arm",
    "armbe",
    "arm64",
    "arm64be",
    "loong64",
    "mips",
    "mipsle",
    "mips64",
    "mips64le",
    "mips64p32",
    "mips64p32le",
    "ppc",
    "ppc64",
    "ppc64le",
    "riscv",
    "riscv64",
    "s390",
    "s390x",
    "sparc",
    "sparc64",
    "wasm",
];
/// The architectures a worker has.
const WORKERS: &[&str] = &["arm64", "amd64"];

/// What a file is built with, besides Linux without cgo: the tags the
/// repository builds with, and the minor version of its go, where the
/// repository pins one.
pub(super) struct Built<'a> {
    pub(super) tags: &'a [String],
    pub(super) go_minor: Option<u32>,
}

/// The go command a differential test holds the plugin to, as the
/// test is given it: it fetches nothing, takes no other toolchain and
/// no workspace, and keeps what it keeps where it is told.
#[cfg(test)]
pub(super) fn go_command(kept: &std::path::Path) -> std::process::Command {
    let mut go = std::process::Command::new("go");
    go.envs([
        ("GOWORK", "off"),
        ("GOPROXY", "off"),
        ("GOTOOLCHAIN", "local"),
    ])
    .env("GOCACHE", kept.join("cache"))
    .env("GOPATH", kept.join("path"));
    go
}

// What follows reads every Go file of every module, in every plugin
// module that is loaded: it is written over bytes with plain loops, and
// kept small, since each function of a module is paid for when the
// module is loaded (the budget of loading a plugin).

/// Whether a Go file, by its path within its module, is one of a
/// program: the go command builds it on a worker, and its package
/// clause names `main`.
pub(super) fn is_program(within: &str, source: &str, built: &Built) -> bool {
    let (directory, file) = within.rsplit_once('/').unwrap_or(("", within));
    if hidden(file) || file.ends_with("_test.go") || !visited(directory) {
        return false;
    }
    let Some(header) = read(source.as_bytes()) else {
        return false;
    };
    WORKERS.iter().any(|arch| {
        named_for(file, arch)
            && match header.expression {
                Some(text) => Expression::holds(text, arch, built),
                None => options_hold(&source.as_bytes()[..header.options], arch, built),
            }
    })
}

/// Whether a file, by its path within its module, is a test file by
/// its name and where it is: `_test.go`, in no directory the go command
/// passes by. Its directory is then one the go command may build a test
/// program for. Whether it does is the go command's to say, on the
/// worker, by the file's constraints and the worker's platform: nothing
/// here foretells it, and a test whose program was not built has
/// nothing to do (see `crate::actions`).
pub(super) fn is_test_file(within: &str) -> bool {
    let (directory, file) = within.rsplit_once('/').unwrap_or(("", within));
    file.ends_with("_test.go") && !hidden(file) && visited(directory)
}

/// Whether a name is one the go command passes by, of a file or of a
/// directory.
fn hidden(name: &str) -> bool {
    name.starts_with('_') || name.starts_with('.')
}

/// Whether the go command looks in a directory of a module for a
/// package: not in one whose name begins with `_` or `.`, nor in or
/// beneath `testdata`, nor beneath a directory named `vendor`. A
/// directory named `vendor` is itself looked in.
fn visited(directory: &str) -> bool {
    let mut parts = directory.split('/').peekable();
    while let Some(part) = parts.next() {
        if hidden(part) || part == "testdata" || (part == "vendor" && parts.peek().is_some()) {
            return false;
        }
    }
    true
}

/// Whether a Go file, by its path within its module, is one of a
/// package the go command may find: its directory is then one whose
/// test program, if it has tests, is named after it.
pub(super) fn is_package_file(within: &str) -> bool {
    let (directory, file) = within.rsplit_once('/').unwrap_or(("", within));
    !hidden(file) && visited(directory)
}

/// The tests a test file declares, by name: each function at the start
/// of a line named `Test` and then no lower-case letter, as the go
/// command names a test, `TestMain` left out. A name is read from where
/// a declaration would stand and not from the file's syntax: one read
/// that is no test names nothing a test program has, and a test that is
/// not read is run all the same (see `crate::actions`), so neither
/// leaves a test out.
pub(super) fn test_names(source: &str, into: &mut std::collections::BTreeSet<String>) {
    for line in source.lines() {
        let Some(rest) = line.strip_prefix("func Test") else {
            continue;
        };
        let more = rest
            .find(|c: char| !(c.is_alphanumeric() || c == '_'))
            .unwrap_or(rest.len());
        let name = &line["func ".len().."func Test".len() + more];
        if rest.chars().next().is_none_or(|c| !c.is_lowercase()) && name != "TestMain" {
            into.insert(name.to_string());
        }
    }
}

/// Whether a file's name lets it be built on Linux of an architecture,
/// as go/build's goodOSArchFile has it: the name is cut at its first
/// dot, what stands before its first underscore is its own, and
/// `_GOOS`, `_GOARCH` or `_GOOS_GOARCH` at its end say the one system
/// it is for.
fn named_for(file: &str, arch: &str) -> bool {
    let name = file.split('.').next().unwrap_or(file);
    let Some(at) = name.find('_') else {
        return true;
    };
    let mut parts = name[at..].rsplit('_');
    let mut last = parts.next().unwrap_or("");
    if last == "test" {
        last = parts.next().unwrap_or("");
    }
    let before = parts.next().unwrap_or("");
    if SYSTEMS.contains(&before) && ARCHITECTURES.contains(&last) {
        return before == "linux" && last == arch;
    }
    if SYSTEMS.contains(&last) {
        return last == "linux";
    }
    !ARCHITECTURES.contains(&last) || last == arch
}

/// Whether a tag holds on a worker of an architecture.
fn tagged(name: &str, arch: &str, built: &Built) -> bool {
    if let Some(minor) = name.strip_prefix("go1.") {
        // A release tag holds up to the go the repository pins; where
        // it pins none, for every release.
        let mut number: u32 = 0;
        for digit in minor.bytes() {
            if !digit.is_ascii_digit() || number > 100_000 {
                return false;
            }
            number = number * 10 + u32::from(digit - b'0');
        }
        return !minor.is_empty() && built.go_minor.is_none_or(|pinned| number <= pinned);
    }
    name == "linux"
        || name == "unix"
        || name == "gc"
        || name == arch
        // The level of an architecture a go builds for unless it is
        // told another, which nothing here tells it.
        || (arch == "amd64" && name == "amd64.v1")
        || (arch == "arm64" && name == "arm64.v8.0")
        || built.tags.iter().any(|given| given == name)
}

/// How many bytes at the start of a text are a tag's: letters, digits,
/// `_` and `.`, as go/build/constraint's isValidTag has it.
fn tag_length(text: &str) -> usize {
    for (at, c) in text.char_indices() {
        if !(c.is_alphanumeric() || c == '_' || c == '.') {
            return at;
        }
    }
    text.len()
}

/// Whether the `// +build` lines of a file's header hold: every line,
/// by any of its options, by every term of that option. A term that is
/// no tag, or is denied twice, holds for nothing (parsePlusBuildExpr).
fn options_hold(header: &[u8], arch: &str, built: &Built) -> bool {
    let mut rest = header;
    while !rest.is_empty() {
        let (line, after) = line_of(rest);
        rest = after;
        let Some(comment) = trimmed(line).strip_prefix(b"//") else {
            continue;
        };
        let Some(given) = trimmed(comment).strip_prefix(b"+build") else {
            continue;
        };
        if !(given.is_empty() || given[0] == b' ' || given[0] == b'\t') {
            continue;
        }
        let Ok(given) = core::str::from_utf8(given) else {
            return false;
        };
        let mut any = false;
        for option in given.split(' ') {
            for option in option.split('\t') {
                if option.is_empty() {
                    continue;
                }
                let mut all = true;
                for term in option.split(',') {
                    let (denied, name) = match term.strip_prefix('!') {
                        Some(name) => (true, name),
                        None => (false, term),
                    };
                    all &= !name.is_empty()
                        && !name.starts_with('!')
                        && tag_length(name) == name.len()
                        && (tagged(name, arch, built) != denied);
                }
                any |= all;
            }
        }
        if !any {
            return false;
        }
    }
    true
}

/// The most an expression may hold, as the go command bounds one: an
/// expression past it is refused, and none is read however deep.
const TOKENS_AT_MOST: usize = 1000;

/// A `//go:build` expression, read as the go command reads one
/// (go/build/constraint): names, `!`, `&&`, `||` and brackets, parted
/// by spaces and tabs and nothing else.
struct Expression<'a> {
    rest: &'a str,
    tokens: usize,
    arch: &'a str,
    built: &'a Built<'a>,
}

impl<'a> Expression<'a> {
    /// Whether an expression holds; one that cannot be read holds for
    /// nothing, as the go command refuses its file.
    fn holds(text: &'a [u8], arch: &'a str, built: &'a Built<'a>) -> bool {
        let Ok(rest) = core::str::from_utf8(text) else {
            return false;
        };
        let mut expression = Expression {
            rest,
            tokens: 0,
            arch,
            built,
        };
        if expression.either() != Some(true) {
            return false;
        }
        expression.skip();
        expression.rest.is_empty() && expression.tokens <= TOKENS_AT_MOST
    }

    /// Passes the spaces and tabs that part tokens.
    fn skip(&mut self) {
        while self.rest.starts_with(' ') || self.rest.starts_with('\t') {
            self.rest = &self.rest[1..];
        }
    }

    /// Takes a token if it comes next, past the spaces before it. Each
    /// token taken is counted.
    fn next(&mut self, token: &[u8]) -> bool {
        self.skip();
        if !self.rest.as_bytes().starts_with(token) {
            return false;
        }
        self.rest = &self.rest[token.len()..];
        self.tokens += 1;
        true
    }

    fn either(&mut self) -> Option<bool> {
        let mut value = self.both()?;
        while self.next(b"||") {
            value |= self.both()?;
        }
        Some(value)
    }

    fn both(&mut self) -> Option<bool> {
        let mut value = self.not()?;
        while self.next(b"&&") {
            value &= self.not()?;
        }
        Some(value)
    }

    fn not(&mut self) -> Option<bool> {
        // Each bracket and each denial is a token: what is read is no
        // deeper than the tokens it may hold.
        if self.tokens >= TOKENS_AT_MOST {
            return None;
        }
        if self.next(b"!") {
            // The go command refuses a doubled negation.
            if self.next(b"!") {
                return None;
            }
            return self.not().map(|value| !value);
        }
        if self.next(b"(") {
            let value = self.either()?;
            return self.next(b")").then_some(value);
        }
        self.skip();
        let length = tag_length(self.rest);
        if length == 0 {
            return None;
        }
        let name = &self.rest[..length];
        self.rest = &self.rest[length..];
        self.tokens += 1;
        Some(tagged(name, self.arch, self.built))
    }
}

/// What a Go file of a program says before its code.
struct Header<'a> {
    /// What follows `//go:build`, where the file has such a line.
    expression: Option<&'a [u8]>,
    /// How much of the file `// +build` lines count in, where it has
    /// none.
    options: usize,
}

/// Whether a byte is blank, as a line is trimmed of them.
fn blank(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\r' | b'\n' | 0x0b | 0x0c)
}

fn trimmed(mut text: &[u8]) -> &[u8] {
    while let [first, rest @ ..] = text {
        if !blank(*first) {
            break;
        }
        text = rest;
    }
    while let [rest @ .., last] = text {
        if !blank(*last) {
            break;
        }
        text = rest;
    }
    text
}

/// The first line of a text, without its newline, and what follows it.
fn line_of(text: &[u8]) -> (&[u8], &[u8]) {
    match text.iter().position(|byte| *byte == b'\n') {
        Some(newline) => (&text[..newline], &text[newline + 1..]),
        None => (text, &[]),
    }
}

/// Where a block comment closes in a text, past its `*/`.
fn closes(text: &[u8]) -> Option<usize> {
    text.windows(2)
        .position(|two| two == b"*/")
        .map(|at| at + 2)
}

/// Reads a Go file's header as go/build's parseFileHeader and
/// shouldBuild read it, line by line, and returns it where the file is
/// one of a program the go command would read: its package clause names
/// `main`, and it does not import "C", which a build without cgo leaves
/// out. A `//go:build` line begins its line, above the first text that
/// is no comment, and is the file's one constraint: a second refuses
/// the file. `// +build` lines are read only where there is none, and
/// only above the last blank line that stands before the first line
/// that is no `//` comment.
fn read(source: &[u8]) -> Option<Header<'_>> {
    let source = source.strip_prefix(b"\xef\xbb\xbf").unwrap_or(source);
    let mut header = Header {
        expression: None,
        options: 0,
    };
    let (mut ended, mut in_block) = (false, false);
    let mut rest = source;
    let code = 'lines: loop {
        if rest.is_empty() {
            return None;
        }
        let (whole, after) = line_of(rest);
        rest = after;
        let mut line = trimmed(whole);
        if line.is_empty() && !ended {
            header.options = source.len() - rest.len();
            continue;
        }
        if !line.starts_with(b"//") {
            ended = true;
        }
        if !in_block {
            if let Some(text) = line.strip_prefix(b"//go:build") {
                if text.is_empty() || text[0] == b' ' || text[0] == b'\t' {
                    if header.expression.is_some() {
                        return None;
                    }
                    header.expression = Some(text);
                }
            }
        }
        while !line.is_empty() {
            if in_block {
                match closes(line) {
                    Some(closed) => {
                        in_block = false;
                        line = trimmed(&line[closed..]);
                        continue;
                    }
                    None => continue 'lines,
                }
            }
            if line.starts_with(b"//") {
                continue 'lines;
            }
            if let Some(opened) = line.strip_prefix(b"/*") {
                in_block = true;
                line = trimmed(opened);
                continue;
            }
            // Text that is no comment: the code begins here, in this
            // line, and goes on to the end of the file.
            break 'lines &source[line.as_ptr() as usize - source.as_ptr() as usize..];
        }
    };
    // The clause: `package`, then its name, on this line or a later
    // one, after any comments.
    let mut clause = trimmed(code).strip_prefix(b"package")?;
    if !clause
        .first()
        .is_some_and(|byte| blank(*byte) || *byte == b'/')
    {
        return None;
    }
    loop {
        clause = match trimmed(clause) {
            [b'/', b'*', comment @ ..] => &comment[closes(comment)?..],
            [b'/', b'/', comment @ ..] => line_of(comment).1,
            name => {
                clause = name;
                break;
            }
        };
    }
    let after = clause.strip_prefix(b"main")?;
    let after = core::str::from_utf8(after).unwrap_or("");
    if after
        .chars()
        .next()
        .is_some_and(|c| c.is_alphanumeric() || c == '_')
    {
        return None;
    }
    (!imports_c(after.as_bytes())).then_some(header)
}

/// Whether what follows a package clause imports "C", read from its
/// import declarations as they are commonly written: `import "C"` on a
/// line, or `"C"` on a line of a bracketed import. The reading stops at
/// the first line that declares something else.
fn imports_c(mut rest: &[u8]) -> bool {
    while !rest.is_empty() {
        let (line, after) = line_of(rest);
        rest = after;
        let mut line = trimmed(line);
        if let Some(imported) = line.strip_prefix(b"import") {
            line = trimmed(imported);
            if let Some(bracketed) = line.strip_prefix(b"(") {
                line = trimmed(bracketed);
            }
        }
        if let Some(quoted) = line.strip_prefix(b"\"C\"") {
            if quoted
                .first()
                .is_none_or(|byte| blank(*byte) || *byte == b'/' || *byte == b')' || *byte == b';')
            {
                return true;
            }
        }
        for declares in [&b"func"[..], b"var", b"const", b"type"] {
            if line.strip_prefix(declares).is_some_and(|more| {
                more.first()
                    .is_some_and(|byte| blank(*byte) || *byte == b'(')
            }) {
                return false;
            }
        }
    }
    false
}

/// What the go command names the program of a directory of a module:
/// the last element of the package's import path, or the one before it
/// where the last is a major version such as v2 (cmd/go's
/// DefaultExecName, internal/load/pkg.go).
fn named(module: &str, directory: &str) -> Option<String> {
    // A program is named in an action's name and in a path: one whose
    // name is more than letters, digits, a dot, a dash and an underscore
    // is not offered.
    named_of(module, directory, false)
}

/// What the go command names the test program of a directory of a
/// module, before `.test`: as it names the directory's program. It is
/// written in a path and no action's name, so it may hold what the go
/// command lets a directory of a package hold beside letters and
/// digits (`c++`, `a~b`); "@", which it does not, parts a shard's name
/// from its package's.
pub(super) fn test_named(module: &str, directory: &str) -> Option<String> {
    named_of(module, directory, true)
}

/// The name, of letters, digits, `.`, `_` and `-`, and, in a path
/// alone, `~` and `+`.
fn named_of(module: &str, directory: &str, in_a_path: bool) -> Option<String> {
    let path = if directory.is_empty() {
        module.to_string()
    } else {
        format!("{module}/{directory}")
    };
    let mut elements = path.rsplit('/');
    let last = elements.next()?;
    let name = if version_element(last) {
        elements.next()?
    } else {
        last
    };
    let plain = !name.is_empty()
        && name != "."
        && name != ".."
        && name.chars().all(|c| {
            c.is_ascii_alphanumeric()
                || matches!(c, '.' | '_' | '-')
                || (in_a_path && matches!(c, '~' | '+'))
        });
    plain.then(|| name.to_string())
}

/// Whether a path element is a major version as the go command reads
/// one: v2 and up, with no leading zero.
fn version_element(element: &str) -> bool {
    let Some(digits) = element.strip_prefix('v') else {
        return false;
    };
    !digits.is_empty()
        && digits.chars().all(|c| c.is_ascii_digit())
        && !digits.starts_with('0')
        && digits != "1"
}

/// The programs of a module that are offered, each by its name and the
/// directory of its package, in the order of their names: those the go
/// command names, one to a name.
pub(super) fn offered(module: &str, directories: &[String]) -> Vec<(String, String)> {
    let mut named: Vec<(String, String)> = directories
        .iter()
        .filter_map(|directory| Some((named(module, directory)?, directory.clone())))
        .collect();
    named.sort();
    let all = named.clone();
    named.retain(|(name, _)| all.iter().filter(|(other, _)| other == name).count() == 1);
    named
}

/// A program action for each program of a module, `run/<name>`: the
/// program the compile left where the go command keeps programs, run as
/// it is. Two directories
/// the go command names alike leave one program between them, whichever
/// it built last: neither is offered, since which it is nobody can say.
pub(super) fn programs(module: &str, directories: &[String]) -> Vec<Action> {
    offered(module, directories)
        .into_iter()
        .map(|(name, _)| Action {
            name: format!("{}/{name}", frontseat_plugin::kinds::RUN),
            command: Some(Command {
                arguments: vec![format!("{PROGRAMS}{name}")],
                ..Default::default()
            }),
            // All the compile left: the programs, and nothing else.
            inputs: vec![Input::output(COMPILE)],
            kind: Some(ActionKind::Program(Default::default())),
            ..Default::default()
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::{is_program, named, programs, Built};
    use frontseat_plugin::Input;

    #[test]
    fn a_test_file_is_one_by_its_name_and_where_it_is() {
        for file in [
            "ledger/ledger_test.go",
            "main_test.go",
            "x_windows_test.go",
            "c++/x_test.go",
        ] {
            assert!(super::is_test_file(file), "{file}");
        }
        for file in [
            "ledger/ledger.go",
            "_x_test.go",
            ".x_test.go",
            "testdata/x_test.go",
            "a/testdata/b/x_test.go",
            "_skipped/x_test.go",
            ".git/x_test.go",
            "vendor/x/x_test.go",
            "a/vendor/w/x_test.go",
        ] {
            assert!(!super::is_test_file(file), "{file}");
        }
        // A directory named vendor is looked in, and nothing beneath it.
        for file in ["internal/vendor/vendor_test.go", "vendor/vendor_test.go"] {
            assert!(super::is_test_file(file), "{file}");
        }
        for (file, found) in [
            ("x.go", true),
            ("a/x_windows.go", true),
            ("_x.go", false),
            ("testdata/x.go", false),
            ("a/vendor/w/x.go", false),
        ] {
            assert_eq!(super::is_package_file(file), found, "{file}");
        }
    }

    #[test]
    fn the_tests_a_file_declares_are_read_by_name() {
        let source = "package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc Test(t *testing.T) {}\nfunc Test_b(t *testing.T) {}\nfunc TestÄ(t *testing.T) {}\nfunc Test9(t *testing.T) {}\n\tfunc TestIndented(t *testing.T) {}\nfunc Testable() {}\nfunc TestMain(m *testing.M) {}\nfunc (s *S) TestMethod() {}\nfunc BenchmarkA(b *testing.B) {}\nfunc ExampleA() {}\n// func TestRemarked(t *testing.T) {}\n";
        let mut read = std::collections::BTreeSet::new();
        super::test_names(source, &mut read);
        let names: Vec<&str> = read.iter().map(String::as_str).collect();
        // Testable begins as a test and is none, by the letter after
        // Test; what is indented, a method, a benchmark, an example and
        // a remark are not read, and are run by the last shard if they
        // are tests.
        assert_eq!(names, ["Test", "Test9", "TestA", "Test_b", "TestÄ"]);
    }

    fn program(within: &str, source: &str) -> bool {
        is_program(
            within,
            source,
            &Built {
                tags: &[],
                go_minor: Some(27),
            },
        )
    }

    #[test]
    fn a_file_is_of_a_program_where_its_package_clause_says_main() {
        for source in [
            "package main\n",
            "// A comment.\n\npackage main // why\n",
            "/* a\n block */ package\tmain\nfunc main() {}\n",
            "\u{feff}package main",
            "package\nmain\n",
            "package /* x */ main\n",
            "package main\r\n",
        ] {
            assert!(program("cmd/x/main.go", source), "{source:?}");
        }
        for source in [
            "package mainly\n",
            "package main_test\n",
            "package greet\n",
            "// package main\npackage greet\n",
            "/* package main */ package greet\n",
            "/* never closed\npackage main\n",
            "packagemain\n",
            "",
        ] {
            assert!(!program("cmd/x/main.go", source), "{source:?}");
        }
    }

    #[test]
    fn a_file_the_go_command_leaves_out_on_a_worker_makes_no_program() {
        let main = "package main\n";
        for built in [
            "main.go",
            "cmd/serve/main.go",
            "cmd/serve/serve_linux.go",
            "cmd/serve/serve_arm64.go",
            "cmd/serve/serve_linux_amd64.go",
            "cmd/serve/linux.go",
            "cmd/serve/windows.go",
            "cmd/serve/a_b.go",
            "cmd/serve/x_unknown.go",
            // A directory named vendor is looked in, and nothing
            // beneath it.
            "cmd/vendor/main.go",
            "vendor/main.go",
        ] {
            assert!(program(built, main), "{built}");
        }
        for left_out in [
            "_examples/serve/main.go",
            ".hidden/serve/main.go",
            "vendor/example.com/x/main.go",
            "cmd/vendor/x/main.go",
            "cmd/testdata/x/main.go",
            "cmd/serve/_main.go",
            "cmd/serve/.main.go",
            "cmd/serve/main_test.go",
            "cmd/serve/serve_windows.go",
            "cmd/serve/serve_darwin_arm64.go",
            "cmd/serve/serve_linux_386.go",
            "cmd/serve/serve_wasm.go",
            "cmd/serve/x_windows_amd64.go",
        ] {
            assert!(!program(left_out, main), "{left_out}");
        }
    }

    #[test]
    fn a_build_constraint_is_read_as_the_go_command_reads_it() {
        let tagged = |constraint: &str, tags: &[&str]| {
            let tags: Vec<String> = tags.iter().map(|tag| tag.to_string()).collect();
            is_program(
                "main.go",
                &format!("{constraint}\n\npackage main\n"),
                &Built {
                    tags: &tags,
                    go_minor: Some(27),
                },
            )
        };
        for holds in [
            "//go:build linux",
            "//go:build unix && !windows",
            "//go:build amd64",
            "//go:build arm64 || 386",
            "//go:build !ignore",
            "//go:build go1.21",
            "//go:build (linux && gc) || windows",
            "// +build linux",
            "// +build windows linux",
            "// +build linux,!cgo",
            "// +build linux\n// +build arm64 amd64",
            "//go:build linux\n// +build windows",
            "// a comment\n\n// +build linux",
        ] {
            assert!(tagged(holds, &[]), "{holds:?}");
        }
        for fails in [
            "//go:build ignore",
            "//go:build   ignore  ",
            "//go:build never",
            "//go:build windows",
            "//go:build cgo",
            "//go:build linux && 386",
            "//go:build !linux",
            "//go:build ignore // a script",
            "//go:build linux &&",
            "//go:build (linux",
            "//go:build !!linux",
            "//go:build integration",
            "// +build ignore",
            "// +build windows",
            "// +build linux,cgo",
            "// +build linux\n// +build windows",
            "//+build ignore",
        ] {
            assert!(!tagged(fails, &[]), "{fails:?}");
        }
        // A tag the repository builds with holds.
        assert!(tagged("//go:build integration", &["integration"]));
        assert!(tagged("// +build integration,linux", &["integration"]));
        // Option lines not parted from the clause by a blank line are a
        // comment, and no constraint.
        assert!(program("main.go", "// +build ignore\npackage main\n"));
        // A constraint after the clause is none.
        assert!(program("main.go", "package main\n\n//go:build ignore\n"));
    }

    #[test]
    fn a_program_is_named_as_the_go_command_names_it() {
        assert_eq!(named("example.com/app", "").as_deref(), Some("app"));
        assert_eq!(
            named("example.com/app", "cmd/serve").as_deref(),
            Some("serve")
        );
        assert_eq!(named("example.com/app/v2", "").as_deref(), Some("app"));
        assert_eq!(
            named("example.com/app", "cmd/tool/v3").as_deref(),
            Some("tool")
        );
        // v1 and v0 are names like any other, as the go command has it.
        assert_eq!(named("example.com/app", "cmd/v1").as_deref(), Some("v1"));
        assert_eq!(named("example.com/app", "cmd/a b"), None);
    }

    #[test]
    fn each_program_is_run_as_the_compile_left_it_and_two_named_alike_are_not_offered() {
        let offered = programs(
            "example.com/app",
            &["cmd/serve".into(), "tools/serve".into(), "cmd/greet".into()],
        );
        assert_eq!(offered.len(), 1);
        let greet = &offered[0];
        assert_eq!(greet.name, "run/greet");
        assert_eq!(
            greet.command.as_ref().unwrap().arguments,
            ["~/go/bin/greet"]
        );
        assert_eq!(greet.inputs, [Input::output("compile")]);
        assert!(greet.is_program());
        assert!(greet.outputs.is_empty() && greet.tools.is_empty());
    }

    #[test]
    fn an_expression_of_any_depth_or_length_is_read_or_refused_and_never_traps() {
        for depth in [10, 400, 600, 100_000, 400_000] {
            let deep = format!(
                "//go:build {}linux{}\n\npackage main\n",
                "(".repeat(depth),
                ")".repeat(depth)
            );
            // Within the thousand tokens an expression may hold, it
            // holds; past them, the file is refused.
            assert_eq!(program("main.go", &deep), depth < 500, "{depth} brackets");
            let denied = format!("//go:build {}linux\n\npackage main\n", "!(".repeat(depth));
            assert!(!program("main.go", &denied), "{depth} denials left open");
        }
        let long = format!(
            "//go:build linux{}\n\npackage main\n",
            " || linux".repeat(100_000)
        );
        assert!(!program("main.go", &long));
    }

    /// The differential run that proves the rules above against the go
    /// command itself, which the test is given (`spec.frontseat.test.tools`
    /// of the plugin's catalog file, and mise): a module of files written
    /// to be hard to read (`TRICKY`), which needs nothing fetched. Over
    /// modules of real repositories too, where it is told of some:
    ///
    ///   GO_PROGRAMS_ROOTS=/a/module:/b/module \
    ///     cargo test -p frontseat-plugin-go differs_from -- --nocapture
    ///
    /// For each module it asks the go command which directories are
    /// programs on Linux, without cgo, on arm64 and on amd64, and holds
    /// the two together to what is found here.
    #[test]
    fn differs_from_the_go_command_nowhere() {
        use std::collections::BTreeSet;
        use std::path::Path;
        let go = "go";
        let roots = std::env::var("GO_PROGRAMS_ROOTS").unwrap_or_default();
        // The minor version of that go command: 27 for go1.27.3.
        let version = std::process::Command::new(go)
            .args(["env", "GOVERSION"])
            .env("GOTOOLCHAIN", "local")
            .output()
            .expect("the go command this test holds the plugin to is not found");
        let minor = String::from_utf8_lossy(&version.stdout)
            .trim()
            .strip_prefix("go1.")
            .and_then(|rest| rest.split('.').next()?.parse().ok());
        assert!(minor.is_some(), "the go command says no version");
        fn walk(root: &Path, at: &Path, minor: Option<u32>, found: &mut BTreeSet<String>) {
            for entry in std::fs::read_dir(at).unwrap().flatten() {
                let path = entry.path();
                let kind = entry.file_type().unwrap();
                if kind.is_dir() {
                    // A nested module is its own, and testdata nobody's.
                    if path.join("go.mod").exists() || entry.file_name() == "testdata" {
                        continue;
                    }
                    walk(root, &path, minor, found);
                } else if kind.is_file() && path.extension().is_some_and(|e| e == "go") {
                    let within = path.strip_prefix(root).unwrap().to_str().unwrap();
                    let Ok(source) = std::fs::read_to_string(&path) else {
                        continue;
                    };
                    if is_program(
                        within,
                        &source,
                        &Built {
                            tags: &[],
                            go_minor: minor,
                        },
                    ) {
                        let directory = within.rsplit_once('/').map(|(d, _)| d).unwrap_or("");
                        found.insert(directory.to_string());
                    }
                }
            }
        }
        // A module of files written to be hard to read, one to a
        // directory, beside the real ones.
        let tricky = std::env::temp_dir().join(format!("go-programs-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&tricky);
        std::fs::create_dir_all(&tricky).unwrap();
        std::fs::write(
            tricky.join("go.mod"),
            "module example.com/tricky\n\ngo 1.21\n",
        )
        .unwrap();
        for (i, (file, source)) in TRICKY.iter().enumerate() {
            let file = tricky.join(format!("d{i:03}")).join(file);
            std::fs::create_dir_all(file.parent().unwrap()).unwrap();
            std::fs::write(file, source).unwrap();
        }
        // What the go command keeps is kept beside the module, and goes
        // with it.
        let kept = tricky.join("_kept");
        let roots = format!("{roots}:{}", tricky.display());
        let mut modules = 0;
        let mut differences = Vec::new();
        for root in roots.split(':').filter(|root| !root.is_empty()) {
            let root = std::fs::canonicalize(root).unwrap();
            let mut here = BTreeSet::new();
            walk(&root, &root, minor, &mut here);
            let mut there = BTreeSet::new();
            for arch in ["arm64", "amd64"] {
                let listed = super::go_command(&kept)
                    .args([
                        "list",
                        "-e",
                        "-find",
                        "-f",
                        "{{if eq .Name \"main\"}}{{.Dir}}{{end}}",
                        "./...",
                    ])
                    .current_dir(&root)
                    .envs([
                        ("GOOS", "linux"),
                        ("GOARCH", arch),
                        ("CGO_ENABLED", "0"),
                        ("GOFLAGS", "-mod=mod"),
                    ])
                    .output()
                    .unwrap();
                if !listed.status.success() {
                    let _ = std::fs::remove_dir_all(&tricky);
                }
                assert!(
                    listed.status.success(),
                    "the go command lists nothing: {}",
                    String::from_utf8_lossy(&listed.stderr)
                );
                for line in String::from_utf8_lossy(&listed.stdout)
                    .lines()
                    .filter(|l| !l.is_empty())
                {
                    let directory = Path::new(line)
                        .strip_prefix(&root)
                        .unwrap()
                        .to_str()
                        .unwrap();
                    there.insert(directory.to_string());
                }
            }
            modules += 1;
            println!(
                "{}: {} programs here, {} by the go command",
                root.display(),
                here.len(),
                there.len()
            );
            let said = |directory: &str| -> String {
                directory
                    .split('/')
                    .next()
                    .and_then(|first| first.strip_prefix('d'))
                    .and_then(|index| index.parse::<usize>().ok())
                    .filter(|_| root.ends_with(tricky.file_name().unwrap()))
                    .and_then(|index| TRICKY.get(index))
                    .map(|(file, source)| format!(" ({file}: {source:?})"))
                    .unwrap_or_default()
            };
            for only in here.difference(&there) {
                differences.push(format!(
                    "{}: {only:?} is a program here and not to the go command{}",
                    root.display(),
                    said(only)
                ));
            }
            for only in there.difference(&here) {
                differences.push(format!(
                    "{}: {only:?} is a program to the go command and not here{}",
                    root.display(),
                    said(only)
                ));
            }
        }
        let _ = std::fs::remove_dir_all(&tricky);
        assert!(modules > 0, "no module was read");
        assert!(differences.is_empty(), "{}", differences.join("\n"));
    }

    /// Files written to be hard to read, each a file's path in a
    /// directory of its own and what it holds: the differential run
    /// holds each to the go command.
    const TRICKY: &[(&str, &str)] = &[
        ("main.go", "package main\n"),
        ("main.go", "package lib\n"),
        ("main.go", "//go:build ignore\n\npackage main\n"),
        ("main.go", "//go:build linux\n\npackage main\n"),
        ("main.go", "//go:build windows\n\npackage main\n"),
        ("main.go", "//go:build arm64 || amd64\n\npackage main\n"),
        ("main.go", "//go:build 386\n\npackage main\n"),
        ("main.go", "//go:build cgo\n\npackage main\n"),
        ("main.go", "//go:build !cgo && unix && gc\n\npackage main\n"),
        ("main.go", "//go:build go1.21\n\npackage main\n"),
        ("main.go", "//go:build go1.999\n\npackage main\n"),
        (
            "main.go",
            "//go:build amd64.v1 || arm64.v8.0\n\npackage main\n",
        ),
        ("main.go", "//go:build amd64.v3\n\npackage main\n"),
        ("main.go", "//go:build\n\npackage main\n"),
        (
            "main.go",
            "//go:build linux\n//go:build linux\n\npackage main\n",
        ),
        ("main.go", "//go:build !!linux\n\npackage main\n"),
        ("main.go", "//go:build linux &&\n\npackage main\n"),
        ("main.go", "//go:build (linux\n\npackage main\n"),
        (
            "main.go",
            "//go:build linux\x0c&&\x0clinux\n\npackage main\n",
        ),
        ("main.go", "//go:build ignore // a script\n\npackage main\n"),
        (
            "main.go",
            "//go:build linux\n// +build windows\n\npackage main\n",
        ),
        ("main.go", "// +build ignore\n\npackage main\n"),
        ("main.go", "//+build ignore\n\npackage main\n"),
        ("main.go", "// +build ignore\npackage main\n"),
        ("main.go", "// +build linux\n\npackage main\n"),
        ("main.go", "// +build windows linux\n\npackage main\n"),
        ("main.go", "// +build linux,cgo\n\npackage main\n"),
        (
            "main.go",
            "// +build linux\n// +build windows\n\npackage main\n",
        ),
        ("main.go", "// +build !!linux\n\npackage main\n"),
        ("main.go", "// +build !\n\npackage main\n"),
        (
            "main.go",
            "// +build linux,amd64.v1 linux,arm64.v8.0\n\npackage main\n",
        ),
        ("main.go", "// +build li-nux\n\npackage main\n"),
        ("main.go", "// +buildx ignore\n\npackage main\n"),
        (
            "main.go",
            "// Copyright.\n\n// +build ignore\n\npackage main\n",
        ),
        (
            "main.go",
            "// Copyright.\n// +build ignore\n\npackage main\n",
        ),
        ("main.go", "/* c */\n\n// +build ignore\n\npackage main\n"),
        ("main.go", "/* c */\n// +build ignore\n\npackage main\n"),
        ("main.go", "/* c */ //go:build ignore\n\npackage main\n"),
        ("main.go", "/* c */ // +build ignore\n\npackage main\n"),
        ("main.go", "/*\n*/ //go:build ignore\n\npackage main\n"),
        ("main.go", "/* a\n*/ // +build ignore\n\npackage main\n"),
        ("main.go", "/*\n//go:build ignore\n*/\n\npackage main\n"),
        ("main.go", "/* c */\n\n//go:build ignore\n\npackage main\n"),
        ("main.go", "// +build ignore\n\n/* c */\n\npackage main\n"),
        (
            "main.go",
            "\n\n// +build ignore\n\n\n// more\npackage main\n",
        ),
        ("main.go", "// +build ignore\r\n\r\npackage main\r\n"),
        ("main.go", "//go:build ignore\r\n\r\npackage main\r\n"),
        ("main.go", "  //go:build ignore\n\npackage main\n"),
        ("main.go", "package main\n\n//go:build ignore\n"),
        ("main.go", "package main\n\nimport \"C\"\n"),
        (
            "main.go",
            "package main\n\n// #include <stdio.h>\nimport \"C\"\n\nfunc main() {}\n",
        ),
        (
            "main.go",
            "package main\n\nimport (\n\t\"fmt\"\n\t\"C\"\n)\n\nfunc main() { fmt.Println() }\n",
        ),
        (
            "main.go",
            "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"import \\\"C\\\"\") }\n",
        ),
        ("main.go", "package\nmain\n"),
        ("main.go", "package /* x */ main\n"),
        ("main.go", "/* x */ package main\n"),
        ("main.go", "package main_test\n"),
        ("x_linux.go", "package main\n"),
        ("x_windows.go", "package main\n"),
        ("x_linux_arm64.go", "package main\n"),
        ("x_linux_386.go", "package main\n"),
        ("x_amd64.go", "package main\n"),
        ("x_wasm.go", "package main\n"),
        ("linux.go", "package main\n"),
        ("windows.go", "package main\n"),
        ("_linux.go", "package main\n"),
        ("x__windows.go", "package main\n"),
        ("x_windows.gen.go", "package main\n"),
        ("x_linux_386.gen.go", "package main\n"),
        ("a.b_windows.go", "package main\n"),
        ("x_windows_test.go", "package main\n"),
        ("x_unknown.go", "package main\n"),
        ("_x.go", "package main\n"),
        (".x.go", "package main\n"),
        ("vendor/main.go", "package main\n"),
        ("vendor/x/main.go", "package main\n"),
        ("cmd/vendor/main.go", "package main\n"),
        ("cmd/vendor/x/y/main.go", "package main\n"),
        ("_x/main.go", "package main\n"),
        (".x/main.go", "package main\n"),
        ("x/_y/main.go", "package main\n"),
    ];
}
