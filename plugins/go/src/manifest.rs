use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

/// What a go.mod says that this plugin acts on: the module's path and
/// what it requires. The rest it either knows to leave to the go
/// command, which reads the same file when it builds, or refuses.
#[derive(Debug)]
pub(super) struct Module {
    pub(super) path: String,
    /// The modules it requires, each with the version its line says and
    /// the line that says so. Under the go command's pruned module graph
    /// (go 1.17 and after) a go.mod requires each module it builds with
    /// at the version that is selected, `// indirect` ones too, and its
    /// go.sum locks it there.
    pub(super) requires: Vec<Required>,
}

/// A module a go.mod requires.
#[derive(Debug, PartialEq, Eq)]
pub(super) struct Required {
    pub(super) path: String,
    /// The version it is required at, as the go command writes one:
    /// v1.2.3.
    pub(super) version: String,
    pub(super) line: i32,
}

/// The directives the go command acts on by itself when it builds, and
/// that change nothing of what a module is handed or what is fetched
/// for it.
const LEFT_TO_GO: &[&str] = &[
    "go",
    "toolchain",
    "godebug",
    "exclude",
    "retract",
    "tool",
    "ignore",
];

impl Module {
    /// Reads a go.mod: directives one to a line, or several of one kind
    /// between brackets, with comments after two slashes.
    ///
    /// A replacement is refused. It says a required module comes from
    /// somewhere other than where it is required from, and what a module
    /// is handed of the workspace and what is fetched for it are worked
    /// out here without it: read past, it would build with something
    /// other than its author wrote. So is a directive this does not
    /// know, which may say as much.
    pub(super) fn parse(file: &str, raw: &str) -> Result<Module, Error> {
        let mut module = Module {
            path: String::new(),
            requires: Vec::new(),
        };
        let mut block: Option<String> = None;
        for (index, line) in raw.lines().enumerate() {
            let number = i32::try_from(index + 1).unwrap_or(i32::MAX);
            let text = line.split("//").next().unwrap_or("");
            let fields: Vec<&str> = text.split_whitespace().collect();
            let (directive, rest) = match (&block, fields.as_slice()) {
                (_, []) => continue,
                (Some(_), [")"]) => {
                    block = None;
                    continue;
                }
                (Some(directive), rest) => (directive.clone(), rest),
                (None, [directive, "("]) => {
                    block = Some(directive.to_string());
                    continue;
                }
                (None, [directive, rest @ ..]) => (directive.to_string(), rest),
            };
            match (directive.as_str(), rest) {
                ("module", [path]) => module.path = unquoted(path),
                ("require", [path, version, ..]) => module.requires.push(Required {
                    path: unquoted(path),
                    version: unquoted(version),
                    line: number,
                }),
                ("replace", _) => {
                    return Err(Error::not_served("directive", "the go.mod replaces a module it requires, and a replacement is not read: the module would be built with what the workspace hands it or what its lock fetches, and not with what this line names")
                        .at_line(file, number)
                        .help("Remove the replace directive. A module the workspace holds is handed to whatever requires it without one."));
                }
                (known, _) if LEFT_TO_GO.contains(&known) => {}
                _ => {
                    return Err(Error::not_served("directive", format!("the go.mod holds a line this plugin does not read: {}", text.trim()))
                        .at_line(file, number)
                        .help("A go.mod holds module, go, toolchain, godebug, require, exclude, retract, tool and ignore."));
                }
            }
        }
        if module.path.is_empty() {
            return Err(Error::invalid(
                "invalid-manifest",
                file,
                "the go.mod names no module",
                "Add a module directive, e.g. `module example.com/hello`.",
            ));
        }
        Ok(module)
    }
}

/// A path as a go.mod writes one: bare, or as a Go string where it holds
/// what a bare one may not.
fn unquoted(path: &str) -> String {
    path.trim_matches(|quote| quote == '"' || quote == '`')
        .to_string()
}

/// What a go.sum locks: each module at each version, with the lines of
/// the go.sum that say so, in the order the go.sum first names each.
/// Go needs older metadata too when it selects versions, even without
/// their archives: an entry may lock a module's go.mod alone.
#[derive(Default)]
pub(super) struct Locked {
    pub(super) entries: Vec<Entry>,
}

/// One module at one version, as a go.sum locks it.
pub(super) struct Entry {
    /// The module and version, as the go command is asked for it:
    /// example.com/dep@v1.2.3.
    pub(super) module: String,
    /// The module's path alone: example.com/dep.
    pub(super) path: String,
    /// The version alone: v1.2.3.
    pub(super) version: String,
    /// Whether the go.sum locks the module's archive, and not its go.mod
    /// alone.
    pub(super) archive: bool,
    /// The go.sum's lines of it, in the order the go command writes them
    /// and each once, ending in a newline: all the go command is told of
    /// it when it fetches it. Two go.sums that lock it alike give it alike.
    pub(super) lines: String,
}

/// Reads the go.sum of the module at a root, or says it locks nothing
/// where there is none.
pub(super) fn locked(repo: &Repository, root: &str) -> Result<Locked, Error> {
    let file = join(root, "go.sum");
    match repo.text(&file)? {
        Some(raw) => Locked::parse(&file, &raw),
        None => Ok(Locked::default()),
    }
}

impl Locked {
    /// Parses the text of a go.sum, refusing a line the go command would
    /// not write with its file and line.
    fn parse(file: &str, raw: &str) -> Result<Locked, Error> {
        let refused = |number: i32, line: &str, what: &str| {
            Error::invalid(
                "invalid-manifest",
                file,
                format!("the go.sum holds a line that {what}: {}", line.trim()),
                "Write the go.sum as the go command does: `go mod tidy`.",
            )
            .at_line(file, number)
        };
        let mut locked = Locked::default();
        let mut at = std::collections::BTreeMap::new();
        for (number, line) in (1..).zip(raw.lines()) {
            let fields: Vec<_> = line.split_whitespace().collect();
            // A line is a module, a version and a hash, as the go command
            // writes it and refuses it otherwise; one that is not would be
            // fetched as a module that is no module.
            if fields.is_empty() {
                continue;
            }
            let [module, version, _checksum] = fields.as_slice() else {
                return Err(refused(
                    number,
                    line,
                    "is not a module, a version and a hash",
                ));
            };
            let (version, archive) = match version.strip_suffix("/go.mod") {
                Some(version) => (version, false),
                None => (*version, true),
            };
            // What is fetched is named on the go command's command line: a
            // path or a version it would not write in a go.sum is refused
            // here, where the line is, and never asked of the network.
            if !module_path(module) {
                return Err(refused(number, line, "names no module path"));
            }
            if !canonical(version) {
                return Err(refused(
                    number,
                    line,
                    "names no version, as the go command writes one",
                ));
            }
            if !of_major(version, path_major(module).unwrap_or("")) {
                return Err(refused(
                    number,
                    line,
                    "names a version its module path does not allow",
                ));
            }
            let both = format!("{module}@{version}");
            let i = *at.entry(both.clone()).or_insert_with(|| {
                locked.entries.push(Entry {
                    module: both,
                    path: module.to_string(),
                    version: version.to_string(),
                    archive: false,
                    lines: String::new(),
                });
                locked.entries.len() - 1
            });
            let entry = &mut locked.entries[i];
            entry.archive |= archive;
            entry.lines.push_str(&fields.join(" "));
            entry.lines.push('\n');
        }
        // In the order the go command writes them, each once.
        for entry in &mut locked.entries {
            let mut lines: Vec<&str> = entry.lines.lines().collect();
            lines.sort_unstable();
            lines.dedup();
            entry.lines = lines.iter().map(|line| format!("{line}\n")).collect();
        }
        Ok(locked)
    }
}

/// Whether a module path is one the go command takes, as
/// golang.org/x/mod/module.CheckPath has it (v0.42.0): elements of ASCII
/// letters, digits and "-._~", none empty, all dots, or beginning or
/// ending with a dot, none a name Windows reserves or ending in a tilde
/// and digits; the first a host name, lower-case, with a dot, not
/// beginning with a dash; and a major-version suffix, if any, that is
/// one (path_major).
fn module_path(path: &str) -> bool {
    let first = path.split('/').next().unwrap_or("");
    if !first.contains('.') || first.starts_with('-') {
        return false;
    }
    for (i, element) in path.split('/').enumerate() {
        let bytes = element.as_bytes();
        if bytes.is_empty()
            || bytes.iter().all(|&b| b == b'.')
            || bytes[0] == b'.'
            || bytes[bytes.len() - 1] == b'.'
        {
            return false;
        }
        for &b in bytes {
            let ok = if i == 0 {
                b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-' || b == b'.'
            } else {
                b.is_ascii_alphanumeric() || b == b'-' || b == b'.' || b == b'_' || b == b'~'
            };
            if !ok {
                return false;
            }
        }
        let short = element.split('.').next().unwrap_or("");
        if RESERVED.iter().any(|name| name.eq_ignore_ascii_case(short)) {
            return false;
        }
        if let Some(tilde) = short.rfind('~') {
            let suffix = &short[tilde + 1..];
            if !suffix.is_empty() && suffix.bytes().all(|b| b.is_ascii_digit()) {
                return false;
            }
        }
    }
    path_major(path).is_some()
}

/// The names Windows reserves, which no element of a module path is.
const RESERVED: &[&str] = &[
    "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8",
    "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
];

/// The major-version suffix of a module path, as
/// golang.org/x/mod/module.SplitPathVersion has it: "/v2" and above, or
/// gopkg.in's ".v0" and up, which every gopkg.in path has; "" where there
/// is none; and None where the path ends in one that is no major version,
/// such as "/v1" or "/v02".
fn path_major(path: &str) -> Option<&str> {
    let bytes = path.as_bytes();
    if path.starts_with("gopkg.in/") {
        let end = path.strip_suffix("-unstable").map_or(path.len(), str::len);
        let mut i = end;
        while i > 0 && bytes[i - 1].is_ascii_digit() {
            i -= 1;
        }
        if i <= 1 || bytes[i - 1] != b'v' || bytes[i - 2] != b'.' {
            return None;
        }
        // As x/mod has it: the suffix with "-unstable", none but ".v0"
        // beginning with a 0.
        let major = &path[i - 2..];
        if major.len() <= 2 || (major.as_bytes()[2] == b'0' && major != ".v0") {
            return None;
        }
        return Some(major);
    }
    let mut i = bytes.len();
    let mut dot = false;
    while i > 0 && (bytes[i - 1].is_ascii_digit() || bytes[i - 1] == b'.') {
        dot |= bytes[i - 1] == b'.';
        i -= 1;
    }
    if i <= 1 || i == bytes.len() || bytes[i - 1] != b'v' || bytes[i - 2] != b'/' {
        return Some("");
    }
    let major = &path[i - 2..];
    if dot || major.len() <= 2 || major.as_bytes()[2] == b'0' || major == "/v1" {
        return None;
    }
    Some(major)
}

/// Whether a canonical version is one a module path allows, as
/// golang.org/x/mod/module.CheckPathMajor has it: v0 or v1, or
/// "+incompatible", where the path has no major-version suffix; that
/// major version where it has one; and, for gopkg.in's ".v1", the
/// v0.0.0 pseudo-versions an old bug wrote.
fn of_major(version: &str, major: &str) -> bool {
    let major = if major.starts_with(".v") {
        major.strip_suffix("-unstable").unwrap_or(major)
    } else {
        major
    };
    if version.starts_with("v0.0.0-") && major == ".v1" {
        return true;
    }
    let of = version.split(['.', '-', '+']).next().unwrap_or("");
    match major {
        "" => of == "v0" || of == "v1" || version.ends_with("+incompatible"),
        _ => of == &major[1..],
    }
}

/// Whether a version is canonical semantic versioning, as
/// golang.org/x/mod/semver.Canonical writes it: v, three numbers with no
/// leading zero, an optional pre-release of dot-separated identifiers,
/// and no build metadata but "+incompatible".
fn canonical(version: &str) -> bool {
    let Some(rest) = version.strip_prefix('v') else {
        return false;
    };
    let rest = rest.strip_suffix("+incompatible").unwrap_or(rest);
    let (core, pre) = rest.split_once('-').unwrap_or((rest, ""));
    if core.split('.').count() != 3 || !core.split('.').all(number) {
        return false;
    }
    if rest.contains('-') {
        for id in pre.split('.') {
            let digits = id.bytes().all(|b| b.is_ascii_digit());
            if id.is_empty()
                || !id.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-')
                || (digits && !number(id))
            {
                return false;
            }
        }
    }
    true
}

/// Whether a number is written as semantic versioning writes one: digits,
/// with no leading zero.
fn number(n: &str) -> bool {
    !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()) && (n == "0" || !n.starts_with('0'))
}

#[cfg(test)]
mod tests {
    use super::{Locked, Module, Required};
    use frontseat_plugin::Code;

    #[test]
    fn a_go_sum_locks_each_module_at_each_version_with_its_own_lines() {
        let locked = Locked::parse("m/go.sum", "a.com/x v1.0.0 h1:A=\nb.com/y/v2 v2.0.0/go.mod h1:B=\n\na.com/x v1.0.0/go.mod h1:C=\na.com/x v1.1.0/go.mod h1:D=\n").unwrap();
        let got: Vec<_> = locked
            .entries
            .iter()
            .map(|e| (e.module.as_str(), e.archive, e.lines.as_str()))
            .collect();
        assert_eq!(
            got,
            [
                (
                    "a.com/x@v1.0.0",
                    true,
                    "a.com/x v1.0.0 h1:A=\na.com/x v1.0.0/go.mod h1:C=\n"
                ),
                (
                    "b.com/y/v2@v2.0.0",
                    false,
                    "b.com/y/v2 v2.0.0/go.mod h1:B=\n"
                ),
                ("a.com/x@v1.1.0", false, "a.com/x v1.1.0/go.mod h1:D=\n"),
            ]
        );
    }
    #[test]
    fn a_go_sum_line_that_is_not_a_module_a_version_and_a_hash_is_refused_where_it_is() {
        for (raw, line) in [
            ("a.com/x v1.0.0 h1:A=\nnot a line here\n", 2),
            ("a.com/x v1.0.0\n", 1),
            ("-x v1.0.0 h1:A=\n", 1),
            ("a.com/x latest h1:A=\n", 1),
            ("a.com/x v1.0.0/go.mod/go.mod h1:A=\n", 1),
            ("a.com/x v1.0 h1:A=\n", 1),
            ("a.com/x v01.0.0 h1:A=\n", 1),
            ("a.com/x v1.0.0+meta h1:A=\n", 1),
            ("a.com/x v1.0.0-01 h1:A=\n", 1),
            ("A.com/x v1.0.0 h1:A=\n", 1),
            ("ab/x v1.0.0 h1:A=\n", 1),
            ("a.com//x v1.0.0 h1:A=\n", 1),
            ("a.com/x/ v1.0.0 h1:A=\n", 1),
            ("a.com/../x v1.0.0 h1:A=\n", 1),
            ("a.com/x@y v1.0.0 h1:A=\n", 1),
            ("a.com/x v1.0.0 h1:A=\na.com/y v1.0.0@latest h1:B=\n", 2),
            ("a.com/x/v1 v1.0.0 h1:A=\n", 1),
            ("a.com/x/v02 v2.0.0 h1:A=\n", 1),
            ("a.com/x/v2.1 v2.0.0 h1:A=\n", 1),
            ("a.com/con v1.0.0 h1:A=\n", 1),
            ("a.com/Lpt1.go v1.0.0 h1:A=\n", 1),
            ("a.com/FOO~1 v1.0.0 h1:A=\n", 1),
            ("a.com/x/... v1.0.0 h1:A=\n", 1),
            ("a.com/x/v2 v1.0.0 h1:A=\n", 1),
            ("a.com/x v2.0.0 h1:A=\n", 1),
            ("gopkg.in/yaml v1.0.0 h1:A=\n", 1),
            ("gopkg.in/yaml.v01 v1.0.0 h1:A=\n", 1),
            ("gopkg.in/yaml.v2 v3.0.0 h1:A=\n", 1),
            ("gopkg.in/x.v0-unstable v0.1.0 h1:A=\n", 1),
        ] {
            let refused = Locked::parse("m/go.sum", raw).err().expect(raw);
            assert_eq!(
                (refused.reason.as_str(), refused.path.as_str(), refused.line),
                ("invalid-manifest", "m/go.sum", line)
            );
        }
    }

    #[test]
    fn what_the_go_command_writes_in_a_go_sum_is_taken() {
        let locked = Locked::parse(
            "m/go.sum",
            concat!(
                "github.com/BurntSushi/toml v1.2.1 h1:A=\n",
                "gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405 h1:B=\n",
                "github.com/a/b v2.0.0+incompatible h1:C=\n",
                "go.opentelemetry.io/x_y~z v1.0.0-rc.1.0 h1:D=\n",
                "a.com/x/v2 v2.0.0 h1:E=\n",
                "a.com/x/v10 v10.1.0/go.mod h1:F=\n",
                "gopkg.in/yaml.v3 v3.0.1 h1:G=\n",
                "gopkg.in/yaml.v0 v0.1.0 h1:H=\n",
                "gopkg.in/mgo.v2-unstable v2.0.0 h1:I=\n",
                "a.com/console~x v1.0.0 h1:J=\n",
                "a.com/x/v1x v1.0.0 h1:K=\n",
            ),
        )
        .unwrap();
        assert_eq!(locked.entries.len(), 11);
    }

    #[test]
    fn two_go_sums_that_lock_a_module_alike_tell_its_fetch_alike() {
        let tidy = "a.com/x v1.0.0 h1:A=\na.com/x v1.0.0/go.mod h1:C=\n";
        let edited =
            "a.com/x v1.0.0/go.mod h1:C=\na.com/x v1.0.0 h1:A=\na.com/x  v1.0.0/go.mod h1:C=\n";
        let lines = |raw: &str| {
            Locked::parse("m/go.sum", raw).unwrap().entries[0]
                .lines
                .clone()
        };
        assert_eq!(
            lines(tidy),
            "a.com/x v1.0.0 h1:A=\na.com/x v1.0.0/go.mod h1:C=\n"
        );
        assert_eq!(lines(edited), lines(tidy));
        // The control: a different hash is not alike.
        let other = "a.com/x v1.0.0 h1:Z=\na.com/x v1.0.0/go.mod h1:C=\n";
        assert_ne!(lines(other), lines(tidy));
    }

    #[test]
    fn a_go_mod_says_what_the_module_is_and_what_it_requires() {
        let raw = "module \"example.com/hello\" // the module\n\ngo 1.27\ntoolchain go1.27.3\n\nrequire example.com/greet v0.0.0\n\nrequire (\n\tgithub.com/spf13/cobra v1.8.1\n\t\"github.com/spf13/pflag\" v1.0.5 // indirect\n)\n\nexclude (\n\texample.com/old v1.0.0\n)\n\nretract v0.1.0\n";
        let module = Module::parse("hello/go.mod", raw).unwrap();
        assert_eq!(module.path, "example.com/hello");
        let required = |path: &str, version: &str, line| Required {
            path: path.into(),
            version: version.into(),
            line,
        };
        // An indirect requirement is one as any other: the version the
        // module is built with.
        assert_eq!(
            module.requires,
            [
                required("example.com/greet", "v0.0.0", 6),
                required("github.com/spf13/cobra", "v1.8.1", 9),
                required("github.com/spf13/pflag", "v1.0.5", 10),
            ]
        );
    }

    #[test]
    fn a_replacement_is_refused_where_it_is_written() {
        for (raw, line) in [
            ("module example.com/hello\n\nrequire example.com/greet v0.0.0\n\nreplace example.com/greet => ../greet\n", 5),
            ("module example.com/hello\n\nreplace (\n\texample.com/greet v0.0.0 => example.com/fork v1.0.0\n)\n", 4),
        ] {
            let refused = Module::parse("hello/go.mod", raw).unwrap_err();
            assert_eq!((refused.code, refused.reason.as_str(), refused.path.as_str(), refused.line), (Code::Unimplemented, "directive-not-served", "hello/go.mod", line));
        }
    }

    #[test]
    fn a_line_nobody_knows_is_refused_and_not_read_past() {
        let refused = Module::parse(
            "go.mod",
            "module example.com/hello\n\nvendor example.com/greet\n",
        )
        .unwrap_err();
        assert_eq!(
            (refused.code, refused.reason.as_str(), refused.line),
            (Code::Unimplemented, "directive-not-served", 3)
        );
        assert!(refused.message.contains("vendor example.com/greet"));
    }
}
