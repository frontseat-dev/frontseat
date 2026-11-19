use frontseat_plugin::javascript::Package;
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;
use serde::Deserialize;
use std::collections::BTreeMap;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Lock {
    lockfile_version: u32,
    packages: BTreeMap<String, Locked>,
}

#[derive(Deserialize)]
struct Locked {
    resolved: Option<String>,
    integrity: Option<String>,
    #[serde(default)]
    link: bool,
}

pub(super) fn invalid(path: &str, message: impl Into<String>) -> Error {
    Error::invalid(
        "invalid-manifest",
        path,
        message,
        "Use a standalone npm package with a v3 package-lock.json and integrity-checked registry dependencies.",
    )
}

pub(super) fn read(repo: &Repository, root: &str) -> Result<Option<Package>, Error> {
    let path = join(root, "package.json");
    let Some(package) = repo.json_document::<Package>(&path)? else {
        return Ok(None);
    };
    if package.workspaces.is_some() || package.package_manager.is_some() {
        return Err(invalid(
            &path,
            "npm workspaces and a separate packageManager selection are not supported",
        ));
    }
    for hook in [
        "preinstall",
        "install",
        "postinstall",
        "prepublish",
        "preprepare",
        "prepare",
        "postprepare",
    ] {
        if package.scripts.contains_key(hook) {
            return Err(invalid(&path, format!("the root {hook} hook is not supported; its outputs and lifecycle must be declared before it can run")));
        }
    }
    // npm reads each of these differently. Do not silently replace what a
    // repository asks for with this adapter's first supported layout.
    for file in [
        "npm-shrinkwrap.json",
        "yarn.lock",
        "pnpm-lock.yaml",
        ".npmrc",
        "binding.gyp",
    ] {
        let file = join(root, file);
        if repo.read(&file)?.is_some() {
            return Err(invalid(&file, "this npm package uses a configuration or preparation path the adapter does not support"));
        }
    }
    let path = join(root, "package-lock.json");
    let lock = repo
        .json_document::<Lock>(&path)?
        .ok_or_else(|| invalid(&path, "a committed package-lock.json is required"))?;
    if lock.lockfile_version != 3 || !lock.packages.contains_key("") {
        return Err(invalid(&path, "the v3 lock must contain this package"));
    }
    for (location, locked) in lock.packages {
        if location.is_empty() {
            continue;
        }
        // npm silently accepts some malformed SRI as no integrity at all.
        // Require the native SHA512 shape through the shared strict decoder;
        // npm still verifies the downloaded bytes against this value.
        let url = locked
            .resolved
            .as_deref()
            .and_then(|value| url::Url::parse(value).ok());
        let registry = url.is_some_and(|url| {
            url.scheme() == "https"
                && url.host_str() == Some("registry.npmjs.org")
                && url.port().is_none()
                && url.username().is_empty()
                && url.password().is_none()
                && url.query().is_none()
                && url.fragment().is_none()
        });
        let checked = locked
            .integrity
            .as_deref()
            .and_then(|value| frontseat_plugin::checksum::decode(value, "sha512", 64))
            .is_some();
        if locked.link || !registry || !checked {
            return Err(invalid(&path, "every dependency must be an npm registry archive with one valid SHA512 integrity value; local, linked, git, bundled and other checksum forms are not supported"));
        }
    }
    Ok(Some(package))
}
