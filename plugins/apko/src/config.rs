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

fn unsupported(path: &str, message: String) -> Error {
    Error {
        reason: "unsupported-image".into(),
        path: path.into(),
        message,
        help: "Build this image from https package repositories and keys alone, in one configuration file.".into(),
        ..Default::default()
    }
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
    let Some(image) = repo.yaml_document::<Image>(path)? else {
        return Err(Error {
            reason: "invalid-lock".into(),
            path: path.into(),
            message: "the lock beside it was written of this configuration, which is not there"
                .into(),
            help: "Commit the configuration the lock was written of, or remove the lock.".into(),
            ..Default::default()
        });
    };
    if image.include.is_some_and(|include| !include.is_empty()) {
        return Err(unsupported(
            path,
            "the configuration includes another, which is not served yet".into(),
        ));
    }
    let Some(contents) = image.contents else {
        return Ok(());
    };
    if contents.baseimage.is_some() {
        return Err(unsupported(
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
            return Err(unsupported(
                path,
                format!("the repository {entry:?} is not an https address; a repository that is a directory is not served yet"),
            ));
        }
    }
    for entry in contents.keyring.iter().flatten() {
        if !entry.starts_with("https://") {
            return Err(unsupported(
                path,
                format!("the key {entry:?} is not an https address; a key that is a file is not served yet"),
            ));
        }
    }
    Ok(())
}
