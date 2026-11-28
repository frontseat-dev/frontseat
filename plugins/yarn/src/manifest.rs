use frontseat_plugin::javascript::{Package, YARN_LOCK};
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{beneath, join};
use serde::de::IgnoredAny;
use serde::Deserialize;

/// The registries Yarn may fetch an npm package from: npm's own, and
/// Yarn's name for it, which the fetch is told to reach as npm's.
const REGISTRIES: [&str; 2] = ["https://registry.npmjs.org", "https://registry.yarnpkg.com"];

/// What a Yarn install is: its root's package, and the directories of
/// its workspaces beneath it, each relative to the root and sorted.
pub(super) struct Install {
    pub(super) package: Package,
    pub(super) workspaces: Vec<String>,
    /// The patterns of its workspaces' manifests, which the install reads.
    pub(super) manifests: Vec<String>,
    /// Whether it says how Yarn is configured, in its `.yarnrc.yml`.
    pub(super) configured: bool,
}

/// Whether a Yarn of a version retries a request safely (4.18.1 and
/// later): the got that Yarn bundled before may retry a request already
/// settled, and crash ("The `onCancel` handler was attached after the
/// promise settled", Yarn's #7249). A pre-release comes before its
/// release, and a version that is not one is taken for one that does not.
fn retries_safely(version: &str) -> bool {
    let (core, pre) = match version.split_once(['-', '+']) {
        Some((core, rest)) => (
            core,
            version[core.len()..].starts_with('-') && !rest.is_empty(),
        ),
        None => (version, false),
    };
    let parts: Vec<u64> = match core.split('.').map(str::parse).collect() {
        Ok(parts) => parts,
        Err(_) => return false,
    };
    let [major, minor, patch] = parts[..] else {
        return false;
    };
    let release = (major, minor, patch);
    release > (4, 18, 1) || (release == (4, 18, 1) && !pre)
}

/// The settings of `.yarnrc.yml` this plugin holds to what it builds;
/// Yarn reads the rest.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Settings {
    node_linker: Option<String>,
    plugins: Option<IgnoredAny>,
    npm_registry_server: Option<String>,
    npm_scopes: Option<IgnoredAny>,
    #[serde(flatten)]
    rest: std::collections::BTreeMap<String, IgnoredAny>,
}

/// The settings of `.yarnrc.yml` that change where Yarn fetches from, how
/// it is let through or who it says it is: not served, since a fetch
/// reaches npm's registry alone, as itself.
const FETCHING: [&str; 13] = [
    "httpProxy",
    "httpsProxy",
    "caFilePath",
    "httpsCaFilePath",
    "httpsCertFilePath",
    "httpsKeyFilePath",
    "networkSettings",
    "unsafeHttpWhitelist",
    "npmAuthToken",
    "npmAuthIdent",
    "npmAlwaysAuth",
    "npmPublishRegistry",
    "npmRegistries",
];

fn not_served(what: &str, path: &str, message: &str, help: &str) -> Error {
    Error::not_served(what, message).at(path).help(help)
}

/// The install whose root is `root`, where its directory holds a
/// `yarn.lock`; none elsewhere, a workspace's directory among them.
pub(super) fn read(repo: &Repository, root: &str) -> Result<Option<Install>, Error> {
    // A workspace of an install above is that install's, and no project
    // of its own. A package no Yarn install serves is pnpm's, npm's, or no
    // one's: a repository installs with one of npm, Yarn and pnpm, and
    // lists the one plugin.
    use frontseat_plugin::javascript::{install_of, installed_by_another, Manager};
    match install_of(repo, root)? {
        Some(install) if install.manager != Manager::Yarn => {
            return Err(installed_by_another(&install, "yarn"));
        }
        Some(install) if install.root == root => {}
        Some(_) => return Ok(None),
        None => {
            let npm = join(root, "package-lock.json");
            if repo.read(&npm)?.is_some() {
                return Err(not_served(
                    "npm",
                    &npm,
                    "this package is installed with npm",
                    "List the npm plugin in frontseat.yaml in place of yarn.",
                ));
            }
            return Err(Error::invalid(
                "invalid-lock",
                &join(root, "package.json"),
                "no yarn.lock serves this package: none is beside it, and the nearest above does not list it among its workspaces",
                "Run `yarn install` where the package is, or list it among the workspaces of the root above.",
            ));
        }
    }
    let lock = join(root, YARN_LOCK);
    let Some(text) = repo.read(&lock)? else {
        return Ok(None);
    };
    let path = join(root, "package.json");
    let Some(package) = repo.json::<Package>(&path)?.map(|document| document.value) else {
        return Ok(None);
    };
    let yarn = repo.pinned("yarn", &path)?;
    if yarn.starts_with("1.") {
        return Err(not_served(
            "yarn-classic",
            &path,
            "the repository pins Yarn 1, which installs nothing offline as Yarn 2 and later do",
            "Pin Yarn 4 in mise.toml and run `yarn install` to write its lock.",
        ));
    }
    if !retries_safely(yarn) {
        return Err(Error::invalid(
            "unsupported-yarn",
            &path,
            format!("the repository pins Yarn {yarn}, whose fetch can crash retrying a request that has already settled"),
            "Pin Yarn 4.18.1 or later in mise.toml.",
        ));
    }
    if let Some(manager) = &package.package_manager {
        // Yarn itself is pinned in mise.toml. A packageManager that names
        // another would be run by whatever starts Yarn otherwise.
        let named = manager
            .strip_prefix("yarn@")
            .map(|version| version.split('+').next().unwrap_or(version));
        if named != Some(yarn) {
            return Err(Error::invalid(
                "invalid-package-manager",
                &path,
                "packageManager names another package manager than the Yarn mise.toml pins",
                format!("Write \"packageManager\": \"yarn@{yarn}\", or leave it out."),
            ));
        }
    }
    let text = String::from_utf8(text).map_err(|_| {
        Error::invalid(
            "invalid-lock",
            &lock,
            "the lock is not UTF-8",
            "Write the lock with `yarn install`.",
        )
    })?;
    crate::lock::check(&lock, &text)?;
    let rc = join(root, ".yarnrc.yml");
    let settings = repo.yaml::<Settings>(&rc)?.map(|document| document.value);
    let configured = settings.is_some();
    let settings = settings.unwrap_or(Settings {
        node_linker: None,
        plugins: None,
        npm_registry_server: None,
        npm_scopes: None,
        rest: std::collections::BTreeMap::new(),
    });
    if let Some(setting) = FETCHING
        .iter()
        .find(|setting| settings.rest.contains_key(**setting))
    {
        return Err(not_served(
            "fetch-settings",
            &rc,
            &format!("{setting} changes where or how Yarn fetches, and a fetch reaches npm's registry alone, as itself"),
            "Leave proxies, certificates, credentials and registries out of .yarnrc.yml.",
        ));
    }
    if settings.node_linker.as_deref() != Some("node-modules") {
        return Err(not_served(
            "linker",
            &rc,
            "Yarn's linker is not node-modules, so the install would write no node_modules for a build to read",
            "Write `nodeLinker: node-modules` in .yarnrc.yml.",
        ));
    }
    if settings.plugins.is_some() {
        return Err(not_served(
            "yarn-plugins",
            &rc,
            "Yarn plugins the repository loads are not run yet",
            "Leave plugins out of .yarnrc.yml.",
        ));
    }
    let registry = settings
        .npm_registry_server
        .as_deref()
        .map(|server| server.trim_end_matches('/'));
    if settings.npm_scopes.is_some() || registry.is_some_and(|server| !REGISTRIES.contains(&server))
    {
        return Err(not_served(
            "registry",
            &rc,
            "a registry other than npm's is not reached yet",
            "Leave npmRegistryServer and npmScopes out of .yarnrc.yml.",
        ));
    }
    let manifests = package
        .workspaces
        .as_ref()
        .map(frontseat_plugin::javascript::Workspaces::manifests)
        .unwrap_or_default();
    let mut workspaces = Vec::new();
    if !manifests.is_empty() {
        let patterns: Vec<&str> = manifests
            .iter()
            .map(String::as_str)
            .chain(["!**/node_modules/**"])
            .collect();
        for manifest in repo.glob(root, &patterns)? {
            let Some(directory) =
                beneath(root, &manifest).and_then(|file| file.strip_suffix("/package.json"))
            else {
                continue;
            };
            if let Some(member) = repo.json::<Package>(&manifest)? {
                hooks(&member.value, &manifest)?;
                if member.value.workspaces.is_some() {
                    return Err(not_served(
                        "nested-workspaces",
                        &manifest,
                        "a workspace with workspaces of its own is not installed yet",
                        "List every workspace in the root's package.json.",
                    ));
                }
            }
            workspaces.push(directory.to_string());
        }
        workspaces.sort();
    }
    hooks(&package, &path)?;
    Ok(Some(Install {
        package,
        workspaces,
        manifests,
        configured,
    }))
}

/// Refuses a workspace whose scripts Yarn runs as it installs: what they
/// write, and need, would have to be declared first.
fn hooks(package: &Package, manifest: &str) -> Result<(), Error> {
    for hook in ["preinstall", "install", "postinstall"] {
        if package.scripts.contains_key(hook) {
            return Err(not_served(
                "install-hooks",
                manifest,
                &format!("the workspace's {hook} hook is not run yet: its outputs and lifecycle must be declared before it can run"),
                "Run it as a script of its own, or leave it out.",
            ));
        }
    }
    Ok(())
}
