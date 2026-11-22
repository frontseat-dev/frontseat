use frontseat_plugin::prelude::*;

/// The action that fills npm's cache, which the install reads.
const FETCH: &str = "fetch";
/// The action that installs a package, and the directory it leaves:
/// what the package provides.
pub(super) const INSTALL: &str = "install";
pub(super) const INSTALLED: &str = "node_modules/";

pub(super) fn of() -> Vec<Action> {
    // npm ci checks the native lock's SRI. `npm cache add URL` cannot be
    // told that SRI, so fetching one URL that way would lose the check.
    // Only the cache survives; no dependency code runs with a network.
    let fetch = Action {
        fetch: true,
        ..Action::new(
            FETCH,
            Command::new(["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"]),
        )
        .tool("node")
        .inputs(["package.json", "package-lock.json"].map(Input::source))
        .output(Output::optional("~/.npm/_cacache/"))
    };
    // npm owns layout, optional/platform dependencies and dependency install
    // hooks. They run without a network and leave only node_modules.
    let install = Action::new(
        INSTALL,
        Command::new(["npm", "ci", "--offline", "--no-audit", "--no-fund"]),
    )
    .tool("node")
    .inputs([
        Input::source("package.json"),
        Input::source("package-lock.json"),
        Input::output(FETCH),
    ])
    .output(Output::optional(INSTALLED));
    // npm may omit every dependency for this platform, or have none to
    // install. It owns that decision; absent optional outputs stay absent.
    vec![fetch, install]
}
