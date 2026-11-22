use frontseat_plugin::prelude::*;

use crate::manifest::{Manifest, Place};

/// The checks of a workspace, by the names their findings are asked by.
pub(super) const LINT: &str = "lint";
pub(super) const FORMAT: &str = "format";

pub(super) fn of(manifest: &Manifest) -> (Vec<Action>, Vec<GeneratedSource>) {
    let inputs = ["buf.yaml", "buf.lock", "**/*.proto"]
        .map(Input::source)
        .to_vec();
    // buf lint passes or fails on what it finds, and what it found is
    // read from what it printed (linted).
    let lint = Action::new(LINT, Command::new(["buf", "lint"]))
        .tool("buf")
        .inputs(inputs.clone())
        .check();
    // buf format -d names what it would change, as a diff, and writes
    // nothing: a check. Its fix, buf format -w, runs where the checkout
    // is and nowhere else.
    let format = Action::new(FORMAT, Command::new(["buf", "format", "-d", "--exit-code"]))
        .tool("buf")
        .inputs(["buf.yaml", "**/*.proto"].map(Input::source))
        .check()
        .fix(Command::new(["buf", "format", "-w"]), ["**/*.proto"]);
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
            outputs.push(Output::required(path.clone()));
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
            inputs,
            outputs,
            tools: template.tools.clone(),
            ..Action::new(template.action.clone(), Command::new(arguments))
        });
    }
    (actions, generated)
}

/// What buf lint found: it prints each finding on a line of its own,
/// `path:line:column:message`, the path relative to the workspace. Any
/// other line is no finding, and neither is one of a file whose name
/// holds a colon. A file that does not compile is printed the same way,
/// and is a finding of the same rule.
pub(super) fn linted(root: &str, printed: &Printed) -> Vec<Diagnostic> {
    const LINTED: Finding = Finding {
        rule_id: "buf/lint",
        help: "Fix what buf lint reports; its rules are explained at https://buf.build/docs/lint/rules/.",
    };
    String::from_utf8_lossy(&printed.stdout)
        .lines()
        .filter_map(Located::read)
        .filter_map(|at| LINTED.at(root, at.path, at.line, at.message))
        .collect()
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
        help: &Unformatted::fixed_by("buf format -w"),
    }
    .files(root, files)
}

#[cfg(test)]
mod check_tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    /// buf lint is a check, as buf format is: what it finds is read from
    /// what it printed, each finding at its file and line. The control
    /// is a generate, which is none and finds nothing.
    #[test]
    fn what_lint_finds_is_reported_at_its_file_and_line() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let host = TestHost::new(Files::new().with("proto/buf.yaml", "version: v2\n").with(
            "proto/buf.gen.yaml",
            "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen\n",
        ));
        let analysis = plugin.analyze(&host, &context, &["proto"]).answered();
        let checks: Vec<(&str, bool)> = analysis.analyses[0].projects[0]
            .actions
            .iter()
            .map(|action| (action.name.as_str(), action.check))
            .collect();
        assert_eq!(
            checks,
            [("lint", true), ("format", true), ("generate", false)]
        );

        let printed = Printed {
            exit_code: 100,
            stdout: b"hello/v1/hello.proto:5:10:Field name \"Message\" should be lower_snake_case, such as \"message\".\n../up.proto:1:1:outside the workspace\nnot a finding\n".to_vec(),
            stderr: Vec::new(),
        };
        let found = plugin
            .report(&host, &context, "proto", "lint", &printed)
            .answered()
            .diagnostics;
        assert_eq!(found.len(), 1, "{found:?}");
        assert_eq!(found[0].rule_id, "buf/lint");
        assert_eq!(
            found[0].message,
            "Field name \"Message\" should be lower_snake_case, such as \"message\"."
        );
        let at = found[0].source_location.as_ref().unwrap();
        assert_eq!(
            (at.path.as_str(), at.line),
            ("proto/hello/v1/hello.proto", 5)
        );
        assert!(plugin
            .report(&host, &context, "proto", "generate", &printed)
            .answered()
            .diagnostics
            .is_empty());
    }
}
