//! Cargo.lock, as cargo writes it: a `[[package]]` table for each
//! package, with its name, version, where it comes from (none for a
//! package of the workspace), the sha256 of its archive where it comes
//! from a registry, and the packages it depends on. It is read as TOML,
//! through the SDK, and the text of each package's table is kept by its
//! span, so that a lock narrowed to some packages is in cargo's own
//! words and `--locked` holds.

use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::line_of;
use serde::Deserialize;
use toml::Spanned;

/// A lock, read.
pub(super) struct Lock {
    text: String,
    pub(super) packages: Vec<Entry>,
}

/// One package of a lock.
pub(super) struct Entry {
    pub(super) name: String,
    pub(super) version: String,
    /// Where it comes from: a registry or a git repository. None for a
    /// package of the workspace.
    pub(super) source: Option<String>,
    /// The sha256 of its archive, where it comes from a registry.
    pub(super) checksum: Option<String>,
    /// What it depends on, each a name, or a name and a version where
    /// the lock holds the name more than once.
    pub(super) dependencies: Vec<String>,
    /// Where its table is in the text.
    span: std::ops::Range<usize>,
}

/// A lock as TOML says it. The span of a table is its header's, as the
/// reader gives it; a package's text runs from there to the next thing
/// at the top of the document, whatever it is and wherever it stands.
/// What else cargo writes at the top is named here, each with where it
/// is; a lock with something else there is not as cargo writes one.
#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields)]
struct File {
    #[serde(default)]
    package: Vec<Spanned<Package>>,
    version: Option<Spanned<toml::Value>>,
    metadata: Option<Spanned<toml::Value>>,
    patch: Option<Spanned<toml::Value>>,
}

#[derive(Deserialize)]
struct Package {
    name: Spanned<String>,
    version: Spanned<String>,
    source: Option<String>,
    checksum: Option<Spanned<String>>,
    #[serde(default)]
    dependencies: Vec<String>,
}

/// The registry crates.io is written as in a lock.
const CRATES_IO: &str = "registry+https://github.com/rust-lang/crates.io-index";

/// A crate of crates.io, as the lock holds it: what one fetch brings.
pub(super) struct Crate {
    pub(super) name: String,
    pub(super) version: String,
    pub(super) checksum: String,
}

/// How a package's crates are fetched: each on its own, where every
/// package it depends on from outside is a crate of crates.io with its
/// checksum; otherwise the whole lock at once by cargo, which alone can
/// fetch from a git repository or another registry.
pub(super) enum Fetching {
    Crates(Vec<Crate>),
    Whole,
}

impl Lock {
    /// Reads a lock, or says there is none. A package that is not as
    /// cargo writes one is refused where it is: what it names would be
    /// asked of a registry, by name and version, in an address.
    pub(super) fn read(repo: &Repository, path: &str) -> Result<Option<Lock>, Error> {
        let Some(typed) = repo.toml_document::<File>(path)? else {
            return Ok(None);
        };
        let text = typed.text;
        let refused = |at: usize, what: String| Error {
            reason: "invalid-lock".into(),
            message: format!("the Cargo.lock holds {what}"),
            path: path.to_string(),
            line: line_of(&text, Some(at)),
            help: "Write the Cargo.lock as cargo does: `cargo generate-lockfile`.".into(),
            ..Error::default()
        };
        // Where each package's text ends: at the line of the next thing
        // that begins after it, a package or anything else at the top,
        // or at the end. A span begins at a table's key, inside its
        // header, so the line it is on is where the header begins.
        let line_start = |at: usize| -> usize {
            let at = at.min(text.len());
            text[..at].rfind('\n').map_or(0, |newline| newline + 1)
        };
        let mut starts: Vec<usize> = typed
            .value
            .package
            .iter()
            .map(|package| package.span().start)
            .chain(
                [
                    &typed.value.version,
                    &typed.value.metadata,
                    &typed.value.patch,
                ]
                .into_iter()
                .flatten()
                .map(|other| other.span().start),
            )
            .map(line_start)
            .collect();
        starts.sort_unstable();
        starts.dedup();
        let mut packages = Vec::with_capacity(typed.value.package.len());
        for spanned in typed.value.package {
            let start = line_start(spanned.span().start);
            // Each package is a table of its own, headed as cargo heads
            // it: the lock is cut at headers, and nothing else is.
            if !text[start..].starts_with("[[package]]") {
                return Err(refused(
                    start,
                    "a package that is not written as a [[package]] table of its own".into(),
                ));
            }
            let end = starts
                .iter()
                .copied()
                .find(|&next| next > start)
                .unwrap_or(text.len());
            let span = start..end;
            let package = spanned.into_inner();
            let (name_at, name) = (package.name.span(), package.name.into_inner());
            if !crate_name(&name) {
                return Err(refused(
                    name_at.start,
                    format!("a package named {name:?}, which is no crate name"),
                ));
            }
            let (version_at, version) = (package.version.span(), package.version.into_inner());
            if !crate_version(&version) {
                return Err(refused(
                    version_at.start,
                    format!("the version {version:?}, which is no version"),
                ));
            }
            let checksum = match package.checksum {
                None => None,
                Some(checksum) => {
                    let (at, sum) = (checksum.span(), checksum.into_inner());
                    if sum.len() != 64
                        || !sum
                            .bytes()
                            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
                    {
                        return Err(refused(
                            at.start,
                            format!("the checksum {sum:?}, which is no hex sha256"),
                        ));
                    }
                    Some(sum)
                }
            };
            packages.push(Entry {
                name,
                version,
                source: package.source,
                checksum,
                dependencies: package.dependencies,
                span,
            });
        }
        Ok(Some(Lock { text, packages }))
    }

    /// The version the lock holds a package of the workspace at: the
    /// one of that name that comes from nowhere, there being one of each
    /// name in a workspace however many a registry gives beside it.
    pub(super) fn workspace_version(&self, name: &str) -> Option<&str> {
        self.packages
            .iter()
            .find(|entry| entry.name == name && entry.source.is_none())
            .map(|entry| entry.version.as_str())
    }

    /// The packages named and everything they depend on, in the lock's
    /// order. A dependency is named by its name, or by its name and
    /// version where the lock holds the name more than once.
    pub(super) fn closure(&self, present: &[String]) -> Vec<&Entry> {
        let mut kept = vec![false; self.packages.len()];
        let mut queue: Vec<(String, Option<String>)> =
            present.iter().map(|name| (name.clone(), None)).collect();
        while let Some((name, version)) = queue.pop() {
            for (i, entry) in self.packages.iter().enumerate() {
                if entry.name != name
                    || version.as_ref().is_some_and(|v| *v != entry.version)
                    || kept[i]
                {
                    continue;
                }
                kept[i] = true;
                for dependency in &entry.dependencies {
                    let mut words = dependency.split(' ');
                    let name = words.next().unwrap_or("").to_string();
                    queue.push((name, words.next().map(str::to_string)));
                }
            }
        }
        self.packages
            .iter()
            .zip(kept)
            .filter_map(|(entry, kept)| kept.then_some(entry))
            .collect()
    }

    /// The lock narrowed to the packages named and everything they
    /// depend on, as cargo wrote it: the text with each package that is
    /// not of the closure left out, and what is not a package where it
    /// stands.
    pub(super) fn narrowed(&self, present: &[String]) -> String {
        let kept: Vec<&Entry> = self.closure(present);
        let mut out = String::with_capacity(self.text.len());
        let mut at = 0;
        for entry in &self.packages {
            out.push_str(&self.text[at..entry.span.start]);
            if kept.iter().any(|k| std::ptr::eq(*k, entry)) {
                out.push_str(&self.text[entry.span.clone()]);
            }
            at = entry.span.end;
        }
        out.push_str(&self.text[at..]);
        // Blank lines where packages were left out are folded to one.
        while out.contains("\n\n\n") {
            out = out.replace("\n\n\n", "\n\n");
        }
        out
    }

    /// How the packages named have their crates fetched (Fetching).
    pub(super) fn fetching(&self, present: &[String]) -> Fetching {
        let mut crates = Vec::new();
        for entry in self.closure(present) {
            let Some(source) = &entry.source else {
                continue;
            };
            match (source.as_str(), &entry.checksum) {
                (CRATES_IO, Some(checksum)) => crates.push(Crate {
                    name: entry.name.clone(),
                    version: entry.version.clone(),
                    checksum: checksum.clone(),
                }),
                // A crate with no checksum, or from a git repository or
                // another registry, whose archive the lock does not
                // name.
                _ => return Fetching::Whole,
            }
        }
        Fetching::Crates(crates)
    }
}

/// Whether a name is one cargo gives a crate: ASCII letters, digits,
/// dashes and underscores. What is fetched is named by it in an address
/// and a path.
fn crate_name(name: &str) -> bool {
    !name.is_empty()
        && name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
}

/// Whether a version is one cargo writes: ASCII letters, digits, dots,
/// plus and dashes, as semantic versioning spells one, build metadata
/// included.
fn crate_version(version: &str) -> bool {
    !version.is_empty()
        && version
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'.' || b == b'+' || b == b'-')
}

#[cfg(test)]
mod tests {
    use super::{Fetching, Lock};
    use frontseat_plugin::Repository;
    use frontseat_plugin_test::{Files, TestHost};

    const LOCK: &str = "# This file is automatically @generated by Cargo.\n# It is not intended for manual editing.\nversion = 4\n\n[[package]]\nname = \"greet\"\nversion = \"0.1.0\"\ndependencies = [\n \"itoa\",\n]\n\n[[package]]\nname = \"hello\"\nversion = \"0.1.0\"\ndependencies = [\n \"cfg-if\",\n \"greet\",\n]\n\n[[package]]\nname = \"cfg-if\"\nversion = \"1.0.0\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd\"\n\n[[package]]\nname = \"itoa\"\nversion = \"1.0.18\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682\"\n";

    fn read(text: &str) -> Result<Lock, frontseat_plugin::Error> {
        let host = TestHost::new(Files::new().with("ws/Cargo.lock", text));
        let repo = Repository::new(&host, None);
        Lock::read(&repo, "ws/Cargo.lock").map(|lock| lock.expect("a lock"))
    }

    fn names(lock: &Lock, present: &[&str]) -> Vec<String> {
        let present: Vec<String> = present.iter().map(|s| s.to_string()).collect();
        lock.closure(&present)
            .iter()
            .map(|e| e.name.clone())
            .collect()
    }

    #[test]
    fn a_package_of_the_workspace_is_at_the_version_the_lock_holds_it_at() {
        let lock = read(LOCK).unwrap();
        assert_eq!(lock.workspace_version("greet"), Some("0.1.0"));
        // A crate of a registry is no package of the workspace.
        assert_eq!(lock.workspace_version("itoa"), None);
        assert_eq!(lock.workspace_version("absent"), None);
        // Two of one name: the workspace's is the one from nowhere,
        // wherever it stands.
        let registry = "[[package]]\nname = \"greet\"\nversion = \"9.9.9\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd\"\n";
        let own = "[[package]]\nname = \"greet\"\nversion = \"0.1.0\"\n";
        for packages in [[registry, own], [own, registry]] {
            let lock = read(&format!("version = 4\n\n{}\n{}", packages[0], packages[1])).unwrap();
            assert_eq!(lock.packages.len(), 2);
            assert_eq!(lock.workspace_version("greet"), Some("0.1.0"));
        }
    }

    #[test]
    fn a_package_closure_names_the_crates_it_depends_on_and_no_other() {
        let lock = read(LOCK).unwrap();
        assert_eq!(names(&lock, &["greet"]), ["greet", "itoa"]);
        assert_eq!(
            names(&lock, &["hello"]),
            ["greet", "hello", "cfg-if", "itoa"]
        );
        let Fetching::Crates(crates) = lock.fetching(&["greet".to_string()]) else {
            panic!("greet's crates are not fetched one by one");
        };
        assert_eq!(crates.len(), 1);
        assert_eq!(
            (crates[0].name.as_str(), crates[0].version.as_str()),
            ("itoa", "1.0.18")
        );
        assert_eq!(lock.narrowed(&["greet".to_string()]), LOCK.replace("[[package]]\nname = \"hello\"\nversion = \"0.1.0\"\ndependencies = [\n \"cfg-if\",\n \"greet\",\n]\n\n", "").replace("[[package]]\nname = \"cfg-if\"\nversion = \"1.0.0\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd\"\n\n", ""));
        // Everything present: the lock as it is.
        assert_eq!(
            lock.narrowed(&["hello".to_string(), "greet".to_string()]),
            LOCK
        );
    }

    #[test]
    fn a_crate_from_elsewhere_than_crates_io_has_the_whole_lock_fetched() {
        let git = LOCK.replace(
            "source = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682\"",
            "source = \"git+https://github.com/dtolnay/itoa?rev=abc#abc\"",
        );
        let lock = read(&git).unwrap();
        assert!(
            matches!(lock.fetching(&["hello".to_string()]), Fetching::Whole),
            "a git crate was fetched on its own"
        );
        // So is a crate of crates.io the lock holds no checksum of.
        let unchecked = LOCK.replace(
            "checksum = \"8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682\"\n",
            "",
        );
        assert!(matches!(
            read(&unchecked).unwrap().fetching(&["greet".to_string()]),
            Fetching::Whole
        ));
        // A package of the workspace alone needs nothing from outside.
        let Fetching::Crates(none) = lock.fetching(&["nothing".to_string()]) else {
            panic!()
        };
        assert!(none.is_empty());
    }

    /// What TOML allows and cargo writes in other ways is read all the
    /// same: no blank line between tables, dependencies on one line,
    /// spaces around a key, a `[metadata]` table after the packages, two
    /// versions of one crate, and the build metadata some versions carry.
    #[test]
    fn a_lock_is_read_as_toml_however_it_is_spelled() {
        let appended = LOCK.trim_end_matches('\n').to_string()
            + "\n[[package]]\nname  = \"my-module\"\nversion = \"0.1.0\"\ndependencies = [\"greet\", \"toml 1.2.1+spec-1.1.0\"]\n\n[[package]]\nname = \"toml\"\nversion = \"1.2.1+spec-1.1.0\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd\"\n\n[[package]]\nname = \"toml\"\nversion = \"0.9.0\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682\"\n\n[metadata]\nx = \"y\"\n";
        let lock = read(&appended).unwrap();
        assert_eq!(
            names(&lock, &["my-module"]),
            ["greet", "itoa", "my-module", "toml"]
        );
        let Fetching::Crates(crates) = lock.fetching(&["my-module".to_string()]) else {
            panic!()
        };
        let versions: Vec<&str> = crates.iter().map(|c| c.version.as_str()).collect();
        assert_eq!(versions, ["1.0.18", "1.2.1+spec-1.1.0"]);
        let narrowed = lock.narrowed(&["my-module".to_string()]);
        assert!(
            narrowed.ends_with("\n\n[metadata]\nx = \"y\"\n"),
            "{narrowed}"
        );
        assert!(!narrowed.contains("version = \"0.9.0\""));
        assert!(narrowed.contains("[[package]]\nname  = \"my-module\""));
        // A table before the packages, which TOML allows, stays where it
        // is, and the package before it ends where it begins.
        let metadata_first =
            LOCK.replacen("version = 4\n", "version = 4\n\n[metadata]\nx = \"y\"\n", 1);
        let lock = read(&metadata_first).unwrap();
        let narrowed = lock.narrowed(&["greet".to_string()]);
        assert!(narrowed.starts_with("# This file is automatically @generated by Cargo.\n# It is not intended for manual editing.\nversion = 4\n\n[metadata]\nx = \"y\"\n\n[[package]]\nname = \"greet\""), "{narrowed}");
        assert!(!narrowed.contains("hello"));
        // A table between two packages ends the one before it.
        let between = LOCK.replacen(
            "\n[[package]]\nname = \"hello\"",
            "\n[metadata]\nx = \"y\"\n\n[[package]]\nname = \"hello\"",
            1,
        );
        let lock = read(&between).unwrap();
        let greet_only = lock.narrowed(&["greet".to_string()]);
        assert!(
            greet_only.contains("[metadata]\nx = \"y\"") && !greet_only.contains("hello"),
            "{greet_only}"
        );
        // What cargo writes after the packages, an array table whose
        // header the reader spans from its key, keeps its header whole
        // when the package before it is left out; and every narrowing
        // is TOML again.
        let patched = LOCK.to_string() + "\n[[patch.unused]]\nname = \"zz\"\nversion = \"9.0.0\"\n";
        let lock = read(&patched).unwrap();
        for present in [
            vec!["cfg-if"],
            vec!["greet"],
            vec!["hello"],
            vec!["nothing"],
        ] {
            let present: Vec<String> = present.iter().map(|s| s.to_string()).collect();
            let narrowed = lock.narrowed(&present);
            assert!(
                narrowed.contains("\n[[patch.unused]]\nname = \"zz\""),
                "{present:?}: {narrowed}"
            );
            toml::from_str::<toml::Table>(&narrowed)
                .unwrap_or_else(|e| panic!("{present:?} narrowed to no TOML: {e}\n{narrowed}"));
        }
        // Packages written as an inline array are no lock cargo writes,
        // and could not be cut at headers: refused.
        let inline = "version = 4\npackage = [{ name = \"a\", version = \"0.1.0\" }, { name = \"b\", version = \"0.1.0\" }]\n";
        let refused = match read(inline) {
            Err(refused) => refused,
            Ok(_) => panic!("an inline package array was accepted"),
        };
        assert_eq!(refused.reason, "invalid-lock");
        assert!(
            refused.message.contains("[[package]]"),
            "{}",
            refused.message
        );
    }

    #[test]
    fn a_lock_that_is_not_as_cargo_writes_it_is_refused_where_it_is() {
        for (edit, line, what) in [
            (("name = \"itoa\"", "name = \"it oa\""), 27, "no crate name"),
            (("version = \"1.0.18\"", "version = \"1.0?x=1\""), 28, "no version"),
            (("version = \"1.0.18\"", "version = \"1.0.18/x\""), 28, "no version"),
            (("checksum = \"8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682\"", "checksum = \"8F42\""), 30, "no hex sha256"),
        ] {
            let text = LOCK.replace(edit.0, edit.1);
            let refused = match read(&text) {
                Err(refused) => refused,
                Ok(_) => panic!("{what}: accepted"),
            };
            assert_eq!(
                (refused.reason.as_str(), refused.path.as_str(), refused.line),
                ("invalid-lock", "ws/Cargo.lock", line),
                "{what}: {}",
                refused.message
            );
            assert!(refused.message.contains(what), "{}", refused.message);
        }
        // A package with no name is no TOML of a lock: the reader says so.
        assert!(read(&LOCK.replace("name = \"itoa\"\n", "")).is_err());
    }
}
