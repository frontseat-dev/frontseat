//! What an image's configuration may say and still be built from its
//! lock alone. apko checks the configuration against the lock itself; here
//! only what would have it read something no action is given is refused,
//! by name, before anything runs.

use frontseat_plugin::prelude::*;
use serde::de::IgnoredAny;
use serde::Deserialize;

#[derive(Deserialize)]
struct Image {
    contents: Option<Contents>,
    include: Option<String>,
}

#[derive(Deserialize)]
struct Contents {
    repositories: Option<Vec<String>>,
    build_repositories: Option<Vec<String>>,
    runtime_repositories: Option<Vec<String>>,
    keyring: Option<Vec<String>>,
    baseimage: Option<IgnoredAny>,
}

/// What the configuration says that is valid and not built yet, each by
/// a reason of its own.
fn not_served(what: &str, path: &str, message: String) -> Error {
    Error::not_served(what, message).at(path).help(
        "Build this image from https package repositories and keys alone, in one configuration file.",
    )
}

/// Where a repository entry points: the entry itself, or what follows the
/// tag of one written "@tag address".
fn address(entry: &str) -> &str {
    match entry.split_once(char::is_whitespace) {
        Some((tag, rest)) if tag.starts_with('@') => rest.trim_start(),
        _ => entry,
    }
}

/// Holds a configuration to what is built: no other file included, no
/// base image, and no repository or key that is a file or a directory,
/// since each of those is read from where apko runs and nothing hands it
/// there.
pub(super) fn check(repo: &Repository, path: &str) -> Result<(), Error> {
    let Some(image) = repo
        .yaml::<Image>(path)
        .map(|read| read.map(|document| document.value))?
    else {
        return Err(Error::invalid(
            "invalid-lock",
            path,
            "the lock beside it was written of this configuration, which is not there",
            "Commit the configuration the lock was written of, or remove the lock.",
        ));
    };
    if image.include.is_some_and(|include| !include.is_empty()) {
        return Err(not_served(
            "include",
            path,
            "the configuration includes another, which is not served yet".into(),
        ));
    }
    let Some(contents) = image.contents else {
        return Ok(());
    };
    if contents.baseimage.is_some() {
        return Err(not_served(
            "base-image",
            path,
            "the configuration builds on a base image, which is not served yet".into(),
        ));
    }
    let repositories = [
        contents.repositories,
        contents.build_repositories,
        contents.runtime_repositories,
    ];
    for entry in repositories.iter().flatten().flatten() {
        if !address(entry).starts_with("https://") {
            return Err(not_served(
                "local-repository",
                path,
                format!("the repository {entry:?} is not an https address; a repository that is a directory is not served yet"),
            ));
        }
    }
    for entry in contents.keyring.iter().flatten() {
        if !entry.starts_with("https://") {
            return Err(not_served(
                "local-key",
                path,
                format!("the key {entry:?} is not an https address; a key that is a file is not served yet"),
            ));
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    /// What a configuration says that is valid and not built yet is
    /// refused as not served, each by its own reason; the control is the
    /// configuration of https repositories and keys alone, which is built.
    #[test]
    fn what_a_configuration_says_that_is_not_built_yet_is_refused_as_not_served() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let config = |contents: &str| {
            let files = Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/valid"))
                .unwrap()
                .with("app/apko.yaml", contents);
            TestHost::new(files)
        };
        let https = "contents:\n  repositories:\n    - https://packages.wolfi.dev/os\n  keyring:\n    - https://packages.wolfi.dev/os/wolfi-signing.rsa.pub\n";
        plugin
            .analyze(&config(https), &context, &["app"])
            .answered();
        for (contents, reason) in [
            ("include: base.yaml\n", "include-not-served"),
            ("contents:\n  baseimage: x\n", "base-image-not-served"),
            (
                "contents:\n  repositories:\n    - ./packages\n",
                "local-repository-not-served",
            ),
            (
                "contents:\n  keyring:\n    - key.pub\n",
                "local-key-not-served",
            ),
        ] {
            let refused = plugin
                .analyze(&config(contents), &context, &["app"])
                .refused(reason);
            assert_eq!(
                (refused.code, refused.path.as_str()),
                (Code::Unimplemented, "app/apko.yaml")
            );
        }
    }
}
