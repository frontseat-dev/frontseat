//! A Yarn 2+ lockfile, read line by line in the one layout Yarn writes
//! it: an entry's key at the start of a line, its fields beneath it two
//! spaces in. A Backstage workspace's lock holds thousands of entries,
//! more than a YAML document the SDK reads may say; only what the plugin
//! holds the lock to is read here, and Yarn reads the rest.

use frontseat_plugin::prelude::*;

const HELP: &str = "Lock every dependency with `yarn install` and Yarn 2 or later: npm registry packages each with its checksum, the workspace's own packages, and patches of npm packages.";

/// Reads `text`, the lock at `path`, refusing a Yarn 1 lock and any
/// entry Yarn would install from elsewhere than the npm registry, or
/// with no checksum to check what it fetched against. What a refusal
/// says names its line and never repeats what is written there, which
/// may hold a credential.
pub(super) fn check(path: &str, text: &str) -> Result<(), Error> {
    let mut metadata = false;
    let mut entry: Option<Entry> = None;
    for (index, line) in text.lines().enumerate() {
        let number = i32::try_from(index + 1).unwrap_or(i32::MAX);
        if line.trim().is_empty() || line.starts_with('#') {
            continue;
        }
        if !line.starts_with(' ') {
            if let Some(done) = entry.take() {
                done.check(path)?;
            }
            if line == "__metadata:" {
                metadata = true;
            } else {
                entry = Some(Entry {
                    line: number,
                    ..Entry::default()
                });
            }
            continue;
        }
        let Some(current) = entry.as_mut() else {
            continue;
        };
        // A field of the entry, and not of one of its maps.
        let Some(field) = line
            .strip_prefix("  ")
            .filter(|rest| !rest.starts_with(' '))
        else {
            continue;
        };
        let (name, value) = field.split_once(':').unwrap_or((field, ""));
        let value = value.trim().trim_matches('"');
        match name {
            "resolution" => current.resolution = Some((value.into(), number)),
            "checksum" => current.checksum = !value.is_empty(),
            "conditions" => current.conditional = true,
            _ => {}
        }
    }
    if let Some(done) = entry {
        done.check(path)?;
    }
    if !metadata {
        return Err(Error::not_served(
            "yarn-classic",
            "a lock with no __metadata is Yarn 1's, which installs nothing offline as Yarn 2 and later do",
        )
        .at(path)
        .help("Pin Yarn 4 in mise.toml and run `yarn install` to write its lock."));
    }
    Ok(())
}

#[derive(Default)]
struct Entry {
    line: i32,
    resolution: Option<(String, i32)>,
    checksum: bool,
    conditional: bool,
}

impl Entry {
    fn check(self, path: &str) -> Result<(), Error> {
        let Some((resolution, line)) = self.resolution else {
            return Err(refused(path, self.line, "this entry says no resolution"));
        };
        // A locator is a name, scoped or not, `@` and its reference.
        let reference = resolution
            .get(1..)
            .and_then(|rest| rest.split_once('@'))
            .map(|(_, reference)| reference);
        let protocol = reference.and_then(|reference| reference.split_once(':'));
        match protocol {
            // A workspace's path, with no parameter a locator may carry
            // after `::`.
            Some(("workspace", at)) if !at.contains("::") => Ok(()),
            // A patch of an npm package by its bare version, the package
            // locked by its own entry, with a patch of Yarn's own or a file
            // of `.yarn/patches`, which the install reads.
            Some(("patch", patched)) if patches_an_npm_version(patched) => Ok(()),
            // A directory of the workspace, which Yarn links and fetches
            // nothing for.
            Some(("link", linked)) if links_within(linked) => Ok(()),
            // An npm package by its bare version, from the registry every
            // action names: no parameter (`::__archiveUrl=` would have Yarn
            // fetch it from anywhere). One Yarn fetches on some platforms
            // only is kept with no checksum, since Yarn never computes one
            // for it: it is the version npm's registry serves, which npm
            // never lets be published twice.
            Some(("npm", version)) if bare(version) && (self.checksum || self.conditional) => Ok(()),
            Some(("npm", version)) if bare(version) => {
                Err(refused(path, line, "this npm package is locked with no checksum"))
            }
            _ => Err(Error::not_served(
                "resolution",
                "this entry is resolved otherwise than from the npm registry, the workspace or a patch of an npm package",
            )
            .at_line(path, line)
            .help(HELP)),
        }
    }
}

/// Whether a reference after `npm:` is a version and nothing else, or a
/// package of another name at a version (`string-width@4.2.3`, as Yarn
/// writes an alias): what a version and a name are written with, and
/// none of what a locator's parameters (`::`, `=`, `&`) or an address
/// (`:`, `%`, `#`) are.
fn bare(reference: &str) -> bool {
    match reference.get(1..).and_then(|rest| rest.rsplit_once('@')) {
        Some((name, version)) => named(&reference[..=name.len()]) && version_of(version),
        None => version_of(reference),
    }
}

/// Whether text is a version as npm writes one.
fn version_of(version: &str) -> bool {
    !version.is_empty()
        && version
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b".+-".contains(&byte))
}

/// Whether text is a package's name as npm takes one: lower case, and
/// scoped (`@scope/name`) or not, with nothing else.
fn named(name: &str) -> bool {
    let unscoped = match name.strip_prefix('@') {
        Some(scoped) => match scoped.split_once('/') {
            Some((scope, unscoped)) if !scope.is_empty() => unscoped,
            _ => return false,
        },
        None => name,
    };
    !unscoped.is_empty()
        && !unscoped.contains('/')
        && name.bytes().all(|byte| {
            byte.is_ascii_lowercase() || byte.is_ascii_digit() || b"-._~@/".contains(&byte)
        })
        && name.matches('@').count() == usize::from(name.starts_with('@'))
}

/// Whether what follows `patch:` patches an npm package by its bare
/// version (`name@npm%3A1.0.0`), with a patch of Yarn's own
/// (`optional!builtin<…>`, `builtin<…>`) or a file beneath
/// `.yarn/patches`, then the parameters Yarn adds after `::`.
fn patches_an_npm_version(patched: &str) -> bool {
    let Some((source, patch)) = patched.split_once('#') else {
        return false;
    };
    let source = source
        .get(1..)
        .and_then(|rest| rest.split_once("@npm%3A"))
        .map(|(name, version)| (&source[..=name.len()], version));
    let patch = patch.split_once("::").map_or(patch, |(patch, _)| patch);
    let builtin = ["optional!builtin<", "builtin<"]
        .iter()
        .any(|prefix| patch.starts_with(prefix));
    let file = ["~/.yarn/patches/", "./.yarn/patches/"]
        .iter()
        .find_map(|prefix| patch.strip_prefix(prefix))
        .is_some_and(|file| {
            !file.is_empty() && !file.split('/').any(|part| part == ".." || part.is_empty())
        });
    source.is_some_and(|(name, version)| named(name) && version_of(version)) && (builtin || file)
}

/// Whether what follows `link:` is a directory within the install's root:
/// a relative path, taken from the workspace the locator Yarn adds after
/// `::` names (`locator=backend%40workspace%3Apackages%2Fbackend`), or the
/// root's where it names none, that reaches neither above the root nor
/// anywhere by an absolute path; and no other parameter.
fn links_within(linked: &str) -> bool {
    let (path, parameters) = linked.split_once("::").unwrap_or((linked, ""));
    let mut from = Vec::new();
    for parameter in parameters
        .split('&')
        .filter(|parameter| !parameter.is_empty())
    {
        let Some(locator) = parameter.strip_prefix("locator=") else {
            return false;
        };
        let locator = locator
            .replace("%40", "@")
            .replace("%3A", ":")
            .replace("%2F", "/");
        if locator.contains('%') {
            return false;
        }
        let Some(directory) = locator
            .get(1..)
            .and_then(|rest| rest.split_once("@workspace:"))
            .map(|(_, directory)| directory)
        else {
            return false;
        };
        if directory != "." {
            from.extend(directory.split('/').map(String::from));
        }
    }
    if path.is_empty() || path.starts_with('/') || path.contains('\\') {
        return false;
    }
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                if from.pop().is_none() {
                    return false;
                }
            }
            part => from.push(part.into()),
        }
    }
    !from.is_empty()
}

fn refused(path: &str, line: i32, message: &str) -> Error {
    Error::invalid("invalid-lock", path, message, HELP).at_line(path, line)
}

#[cfg(test)]
pub(crate) mod tests {
    use super::check;
    use frontseat_plugin::Code;

    pub(crate) const LOCK: &str = r#"# This file is generated by running "yarn install" inside your project.
# Manual changes might be lost - proceed with caution!

__metadata:
  version: 8
  cacheKey: 10c0

"fsevents@npm:^2.3.3":
  version: 2.3.3
  resolution: "fsevents@npm:2.3.3"
  conditions: os=darwin
  languageName: node
  linkType: hard

"fsevents@patch:fsevents@npm%3A^2.3.3#optional!builtin<compat/fsevents>":
  version: 2.3.3
  resolution: "fsevents@patch:fsevents@npm%3A2.3.3#optional!builtin<compat/fsevents>::version=2.3.3&hash=df0bf1"
  dependencies:
    node-gyp: "npm:latest"
  conditions: os=darwin
  languageName: node
  linkType: hard

"@scope/is-number@npm:7.0.0":
  version: 7.0.0
  resolution: "@scope/is-number@npm:7.0.0"
  checksum: 10c0/b4686d0d3053146095ccd45346461bc8e53b80aeb7671cc52a4de02dbbf7dc0d1d2a986e2fe4ae206984b4d34ef37e8b795ebc4f4295c978373e6575e295d811
  languageName: node
  linkType: hard

"web@workspace:.":
  version: 0.0.0-use.local
  resolution: "web@workspace:."
  dependencies:
    "@scope/is-number": "npm:7.0.0"
  languageName: unknown
  linkType: soft
"#;

    /// A lock of npm packages, each checked or conditional, a patch and
    /// the workspace is read; each other form is refused at its line,
    /// saying nothing of what is written there.
    #[test]
    fn a_lock_is_held_to_what_installs_offline_and_is_checked() {
        check("yarn.lock", LOCK).expect("the control is read");
        let secret = "https://token:s3cret@git.example.com/repo.git";
        for (lock, reason, code, line) in [
            (
                LOCK.replace("  checksum: 10c0/b4686d0d3053146095ccd45346461bc8e53b80aeb7671cc52a4de02dbbf7dc0d1d2a986e2fe4ae206984b4d34ef37e8b795ebc4f4295c978373e6575e295d811\n", ""),
                "invalid-lock",
                Code::FailedPrecondition,
                26,
            ),
            (
                LOCK.replace("resolution: \"@scope/is-number@npm:7.0.0\"", &format!("resolution: \"@scope/is-number@{secret}\"")),
                "resolution-not-served",
                Code::Unimplemented,
                26,
            ),
            (
                LOCK.replace("  resolution: \"web@workspace:.\"\n", ""),
                "invalid-lock",
                Code::FailedPrecondition,
                31,
            ),
            (
                LOCK.replace("__metadata:\n  version: 8\n  cacheKey: 10c0\n", ""),
                "yarn-classic-not-served",
                Code::Unimplemented,
                0,
            ),
        ] {
            let refused = check("yarn.lock", &lock).expect_err(reason);
            assert_eq!(
                (refused.reason.as_str(), refused.code, refused.line),
                (reason, code, line),
                "{}",
                refused.message
            );
            assert!(!format!("{refused:?}").contains("s3cret"), "{refused:?}");
        }
    }

    /// The lock Yarn 4.13.0 itself wrote for a workspace of two packages,
    /// one depending on the other and each on another version of one
    /// npm package, is read.
    /// A link to a directory within the install's root, as Backstage's
    /// template links its app to its backend, is read; one that reaches
    /// above the root, by an absolute path, from a locator that names no
    /// workspace, or with another parameter, is refused at its line.
    #[test]
    fn a_link_within_the_workspace_is_read_and_one_beyond_it_refused() {
        let with = |resolution: &str| {
            LOCK.replace(
                "resolution: \"@scope/is-number@npm:7.0.0\"",
                &format!("resolution: \"{resolution}\""),
            )
        };
        for resolution in [
            "app@link:../app::locator=backend%40workspace%3Apackages%2Fbackend",
            "@scope/is-number@link:shared",
        ] {
            check("yarn.lock", &with(resolution))
                .unwrap_or_else(|refused| panic!("{resolution}: {refused:?}"));
        }
        for resolution in [
            "app@link:../../../outside::locator=backend%40workspace%3Apackages%2Fbackend",
            "app@link:/etc::locator=backend%40workspace%3Apackages%2Fbackend",
            "app@link:..",
            "app@link:../app::locator=backend%40npm%3A1.0.0",
            "app@link:../app::locator=backend%40workspace%3Apackages%2Fbackend&__archiveUrl=https%3A%2F%2Fexample.com",
            "app@link:",
        ] {
            let refused = check("yarn.lock", &with(resolution)).expect_err(resolution);
            assert_eq!(refused.line, 26, "{resolution}");
        }
    }

    #[test]
    fn the_lock_yarn_writes_is_read() {
        check("yarn.lock", include_str!("../testdata/workspace/yarn.lock"))
            .expect("Yarn's own lock is read");
    }

    /// What Yarn would fetch from elsewhere than npm's registry, though it
    /// writes it in a lock as an npm package or a patch of one, is refused
    /// at its line; the controls are the forms Yarn writes for a package
    /// of npm's: by version, by another name at a version, patched by
    /// Yarn or by a file of `.yarn/patches`.
    #[test]
    fn what_yarn_would_fetch_from_elsewhere_is_refused_however_it_is_written() {
        let with = |resolution: &str| {
            LOCK.replace(
                "resolution: \"@scope/is-number@npm:7.0.0\"",
                &format!("resolution: \"{resolution}\""),
            )
        };
        for resolution in [
            "@scope/is-number@npm:7.0.0",
            "string-width-cjs@npm:string-width@4.2.3",
            "@scope/is-number@npm:@other/is-number@7.0.0-rc.1+build.5",
            "@scope/is-number@patch:@scope/is-number@npm%3A7.0.0#~/.yarn/patches/is-number-npm-7.0.0-abc.patch::version=7.0.0&hash=1",
            "@scope/is-number@patch:@scope/is-number@npm%3A7.0.0#optional!builtin<compat/fsevents>::version=7.0.0&hash=1",
        ] {
            check("yarn.lock", &with(resolution)).unwrap_or_else(|refused| panic!("{resolution}: {refused:?}"));
        }
        for resolution in [
            "@scope/is-number@npm:7.0.0::__archiveUrl=https%3A%2F%2Fcodeload.github.com%2Fa%2Fb",
            "@scope/is-number@npm:7.0.0#https://example.com/x.tgz",
            "@scope/is-number@npm:https://example.com/x.tgz",
            "@scope/is-number@npm:",
            "@scope/is-number@npm:Evil/Name@7.0.0",
            "@scope/is-number@patch:@scope/is-number@npm%3A7.0.0%3A%3A__archiveUrl%3Dhttps#optional!builtin<compat/fsevents>",
            "@scope/is-number@patch:@scope/is-number@npm%3A7.0.0#./patches/x.patch",
            "@scope/is-number@patch:@scope/is-number@npm%3A7.0.0#~/.yarn/patches/../../x.patch",
            "@scope/is-number@patch:@scope/is-number@https%3A//example.com/@npm%3A7.0.0#optional!builtin<compat/fsevents>",
            "@scope/is-number@workspace:.::__archiveUrl=https%3A%2F%2Fexample.com",
        ] {
            let refused = check("yarn.lock", &with(resolution)).expect_err(resolution);
            assert_eq!(refused.line, 26, "{resolution}");
            assert!(!format!("{refused:?}").contains("example.com"), "{refused:?}");
        }
    }
}
