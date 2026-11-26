use crate::manifest::Install;
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

/// The action that fills Yarn's cache, which the install reads.
const FETCH: &str = frontseat_plugin::kinds::FETCH;
/// The action that installs the workspace, and the directory it leaves
/// at its root: what the install provides.
pub(super) const INSTALL: &str = "install";
pub(super) const INSTALLED: &str = "node_modules/";
/// Yarn's cache, in the install's own directory rather than the user's.
const CACHE: &str = ".yarn/cache/";

/// Yarn, as every action runs it: the Yarn mise.toml pins, never a
/// release `.yarnrc.yml` vendors (`yarnPath`); its cache beside the
/// install; npm's registry, which a lock of Yarn's names no host of;
/// every archive checked against the lock's checksum and every
/// certificate verified, whatever `.yarnrc.yml` or `.env.yarn` say, a
/// variable of Yarn's own taking the place of both; and nothing sent
/// anywhere else.
fn yarn(arguments: &[&str]) -> Command {
    [
        ("YARN_IGNORE_PATH", "1"),
        ("YARN_ENABLE_GLOBAL_CACHE", "false"),
        ("YARN_CACHE_FOLDER", CACHE.trim_end_matches('/')),
        ("YARN_NPM_REGISTRY_SERVER", "https://registry.npmjs.org"),
        ("YARN_ENABLE_TELEMETRY", "false"),
        ("YARN_CHECKSUM_BEHAVIOR", "throw"),
        ("YARN_ENABLE_STRICT_SSL", "true"),
    ]
    .into_iter()
    .fold(
        Command::new(["yarn"].into_iter().chain(arguments.iter().copied())),
        |command, (name, value)| command.env(name, value),
    )
}

/// What the install leaves: the root's `node_modules` and each
/// workspace's, where Yarn keeps what it could not hoist, each optional.
pub(super) fn installed(install: &Install) -> Vec<String> {
    [INSTALLED.to_string()]
        .into_iter()
        .chain(
            install
                .workspaces
                .iter()
                .map(|workspace| join(workspace, INSTALLED)),
        )
        .collect()
}

/// How the install starts Yarn: by the Node mise.toml pins, which tells
/// node-gyp where that Node's own headers are (`npm_config_nodedir`), the
/// directory above the program that runs. With no network, node-gyp could
/// download none, nor `prebuild-install` a module built elsewhere, so a
/// native module is built from the source its checked archive holds
/// (`npm_config_build_from_source`), with the worker's C toolchain.
const LAUNCH: &str = "const {spawnSync} = require(\"node:child_process\"); \
const {dirname} = require(\"node:path\"); \
const run = spawnSync(\"yarn\", process.argv.slice(1), {stdio: \"inherit\", \
env: {...process.env, npm_config_nodedir: dirname(dirname(process.execPath))}}); \
if (run.error) { console.error(`yarn could not be started: ${run.error.message}`); process.exit(127); } \
if (run.signal) { process.kill(process.pid, run.signal); } \
process.exit(run.status);";

pub(super) fn of(install: &Install) -> Vec<Action> {
    // What Yarn reads to install: the manifests, the lock, its settings
    // and the patches a patch: resolution applies.
    let read: Vec<Input> = ["package.json", "yarn.lock", ".yarn/patches/**"]
        .into_iter()
        .map(String::from)
        .chain(install.configured.then(|| ".yarnrc.yml".to_string()))
        .chain(install.manifests.iter().cloned())
        .map(Input::source)
        .collect();
    // Yarn fetches every package the lock names into its cache, checking
    // each against the lock's checksum, and builds nothing: no
    // dependency's code runs with a network. Only the cache is kept.
    let fetch = Action::new(
        FETCH,
        yarn(&["install", "--immutable", "--mode=skip-build"]),
    )
    .tool("node")
    .tool("yarn")
    .inputs(read.clone())
    .output(Output::optional(CACHE))
    .fetch();
    // The install runs offline from that cache, refusing a lock that no
    // longer matches the manifests and a cache that would change. Yarn
    // owns the layout and what it builds; a workspace keeps the packages
    // Yarn could not hoist in a node_modules of its own.
    let mut offline = yarn(&["install", "--immutable", "--immutable-cache"])
        .env("YARN_ENABLE_NETWORK", "false")
        .env("npm_config_build_from_source", "true");
    offline.arguments = ["node", "-e", LAUNCH]
        .into_iter()
        .map(String::from)
        .chain(offline.arguments.into_iter().skip(1))
        .collect();
    let install = installed(install).into_iter().fold(
        Action::new(INSTALL, offline)
            .tool("node")
            .tool("yarn")
            .inputs(read.into_iter().chain([Input::output(FETCH)])),
        |action, output| action.output(Output::optional(output)),
    );
    vec![fetch, install]
}
