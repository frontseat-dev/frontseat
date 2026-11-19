use frontseat_plugin::prelude::*;

pub(super) fn of() -> Vec<Action> {
    // npm ci checks the native lock's SRI. `npm cache add URL` cannot be
    // told that SRI, so fetching one URL that way would lose the check.
    // Only the cache survives; no dependency code runs with a network.
    let fetch = Action {
        name: "fetch".into(),
        command: Some(Command {
            arguments: ["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"]
                .map(String::from)
                .to_vec(),
            ..Default::default()
        }),
        inputs: ["package.json", "package-lock.json"]
            .map(Input::source)
            .to_vec(),
        outputs: vec![Output {
            path: "~/.npm/_cacache/".into(),
            optional: true,
        }],
        tools: vec!["node".into()],
        fetch: true,
        ..Default::default()
    };
    // npm owns layout, optional/platform dependencies and dependency install
    // hooks. They run without a network and leave only node_modules.
    let install = Action {
        name: "install".into(),
        command: Some(Command {
            arguments: ["npm", "ci", "--offline", "--no-audit", "--no-fund"]
                .map(String::from)
                .to_vec(),
            ..Default::default()
        }),
        inputs: vec![
            Input::source("package.json"),
            Input::source("package-lock.json"),
            Input::output("fetch"),
        ],
        outputs: vec![Output {
            path: "node_modules/".into(),
            optional: true,
        }],
        tools: vec!["node".into()],
        ..Default::default()
    };
    // npm may omit every dependency for this platform, or have none to
    // install. It owns that decision; absent optional outputs stay absent.
    vec![fetch, install]
}
