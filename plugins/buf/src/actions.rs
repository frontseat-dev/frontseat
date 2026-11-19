use frontseat_plugin::prelude::*;

use crate::manifest::{Manifest, Place};

pub(super) fn of(manifest: &Manifest) -> (Vec<Action>, Vec<GeneratedSource>) {
    let inputs = ["buf.yaml", "buf.lock", "**/*.proto"]
        .map(Input::source)
        .to_vec();
    let lint = Action {
        name: "lint".into(),
        command: Some(Command {
            arguments: vec!["buf".into(), "lint".into()],
            ..Default::default()
        }),
        inputs: inputs.clone(),
        tools: vec!["buf".into()],
        ..Default::default()
    };
    // buf format -d names what it would change, as a diff, and writes
    // nothing: a check. Its fix, buf format -w, runs where the checkout
    // is and nowhere else.
    let format = Action {
        name: "format".into(),
        command: Some(Command {
            arguments: vec![
                "buf".into(),
                "format".into(),
                "-d".into(),
                "--exit-code".into(),
            ],
            ..Default::default()
        }),
        inputs: ["buf.yaml", "**/*.proto"].map(Input::source).to_vec(),
        tools: vec!["buf".into()],
        check: true,
        fix: Some(Command {
            arguments: vec!["buf".into(), "format".into(), "-w".into()],
            ..Default::default()
        }),
        rewrites: vec!["**/*.proto".into()],
        ..Default::default()
    };
    let mut actions = vec![lint, format];
    let mut generated = vec![];
    // One action for each template, generating what it says.
    for template in &manifest.templates {
        if template.outputs.is_empty() {
            continue;
        }
        let mut inputs = inputs.clone();
        inputs.push(Input::source(&template.file));
        let mut arguments = vec!["buf".to_string(), "generate".into()];
        if template.file != "buf.gen.yaml" {
            arguments.extend(["--template".into(), template.file.clone()]);
        }
        if let Some(base) = &template.base {
            arguments.extend(["-o".into(), base.clone()]);
        }
        let mut outputs = vec![];
        for output in &template.outputs {
            let path = format!("{}/", output.path.trim_end_matches('/'));
            outputs.push(Output {
                path: path.clone(),
                ..Default::default()
            });
            let (directory, destination) = match &output.place {
                Place::Directory(directory) => (directory.clone(), String::new()),
                Place::Destination(destination) => (String::new(), destination.clone()),
            };
            generated.push(GeneratedSource {
                action: template.action.clone(),
                output: path,
                directory,
                destination,
                patterns: output.patterns.clone(),
            });
        }
        actions.push(Action {
            name: template.action.clone(),
            command: Some(Command {
                arguments,
                ..Default::default()
            }),
            inputs,
            outputs,
            tools: template.tools.clone(),
            ..Default::default()
        });
    }
    (actions, generated)
}

/// The files buf format -d would change: each diff names its file on a
/// "+++" line, relative to the workspace, with the time after a tab.
/// The ".orig" side is what the file says now.
pub(super) fn unformatted(root: &str, printed: &Printed) -> Vec<Diagnostic> {
    let text = String::from_utf8_lossy(&printed.stdout);
    let files = text.lines().filter_map(|line| {
        let rest = line.strip_prefix("+++ ")?;
        let file = rest.split('\t').next()?.trim();
        (!file.ends_with(".orig")).then_some(file)
    });
    Unformatted {
        rule_id: "buf/format",
        message: "it is not formatted as buf formats it",
        help: "Run `frontseat build --fix`, which runs buf format -w where the checkout is.",
    }
    .files(root, files)
}
