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

/// What a package says that npm reads and this plugin does not build yet.
fn not_served(what: &str, path: &str, message: &str) -> Error {
    Error::not_served(what, message).at(path).help(
        "Use a standalone npm package with a v3 package-lock.json and integrity-checked registry dependencies.",
    )
}

pub(super) fn read(repo: &Repository, root: &str) -> Result<Option<Package>, Error> {
    let path = join(root, "package.json");
    let Some(package) = repo.json_document::<Package>(&path)? else {
        return Ok(None);
    };
    if package.workspaces.is_some() {
        return Err(not_served(
            "workspaces",
            &path,
            "npm workspaces are not built yet",
        ));
    }
    if package.package_manager.is_some() {
        return Err(not_served(
            "package-manager",
            &path,
            "a packageManager the package names is not served yet",
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
            return Err(not_served(
                "install-hooks",
                &path,
                &format!("the root {hook} hook is not run yet: its outputs and lifecycle must be declared before it can run"),
            ));
        }
    }
    // npm reads each of these differently. What a repository asks for is
    // not replaced with the layout this plugin builds.
    for (file, what) in [
        ("npm-shrinkwrap.json", "shrinkwrap"),
        ("yarn.lock", "yarn"),
        ("pnpm-lock.yaml", "pnpm"),
        (".npmrc", "npmrc"),
        ("binding.gyp", "native-addons"),
    ] {
        let file = join(root, file);
        if repo.read(&file)?.is_some() {
            return Err(not_served(
                what,
                &file,
                "this package is configured or prepared in a way the plugin does not build yet",
            ));
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

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    /// A package npm installs: it names what it depends on, and runs
    /// nothing of its own.
    const PACKAGE: &str = r#"{"name":"web","devDependencies":{"jest":"30.5.2"}}"#;
    const LOCK: &str = r#"{"lockfileVersion":3,"packages":{"":{},"node_modules/jest":{"resolved":"https://registry.npmjs.org/jest/-/jest-30.5.2.tgz","integrity":"sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng=="}}}"#;

    /// What a package says that npm reads and this plugin does not build
    /// yet is refused as not served, each by its own reason; a lock that
    /// is wrong, and a name that is blank, are refused as invalid; the
    /// control is a standalone
    /// package with its v3 lock, which is built.
    #[test]
    fn what_npm_reads_and_is_not_built_yet_is_refused_as_not_served() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let package = Files::new()
            .with("app/package.json", PACKAGE)
            .with("app/package-lock.json", LOCK);
        let analyze = |files: Files| plugin.analyze(&TestHost::new(files), &context, &["app"]);
        analyze(package.clone()).answered();
        // The package, saying this beside its name.
        let saying = |what: &str| {
            let named = format!(r#""name":"web",{what}"#);
            package.clone().with(
                "app/package.json",
                PACKAGE.replace(r#""name":"web""#, &named),
            )
        };
        for (files, reason, at) in [
            (
                saying(r#""workspaces":["packages/*"]"#),
                "workspaces-not-served",
                "app/package.json",
            ),
            (
                saying(r#""packageManager":"pnpm@11""#),
                "package-manager-not-served",
                "app/package.json",
            ),
            (
                saying(r#""scripts":{"prepare":"node build.js"}"#),
                "install-hooks-not-served",
                "app/package.json",
            ),
            (
                package.clone().with("app/npm-shrinkwrap.json", LOCK),
                "shrinkwrap-not-served",
                "app/npm-shrinkwrap.json",
            ),
            (
                package.clone().with("app/yarn.lock", ""),
                "yarn-not-served",
                "app/yarn.lock",
            ),
            (
                package
                    .clone()
                    .with("app/pnpm-lock.yaml", "lockfileVersion: 9.0"),
                "pnpm-not-served",
                "app/pnpm-lock.yaml",
            ),
            (
                package.clone().with("app/.npmrc", "ignore-scripts=true"),
                "npmrc-not-served",
                "app/.npmrc",
            ),
            (
                package.clone().with("app/binding.gyp", "{}"),
                "native-addons-not-served",
                "app/binding.gyp",
            ),
        ] {
            let refused = analyze(files).refused(reason);
            assert_eq!(
                (refused.code, refused.path.as_str()),
                (Code::Unimplemented, at),
                "{reason}"
            );
        }
        analyze(package.clone().without("app/package-lock.json")).refused("invalid-manifest");
        let unnamed = PACKAGE.replace(r#""name":"web""#, r#""name":" ""#);
        analyze(package.with("app/package.json", unnamed)).refused("invalid-json");
    }
}
