use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{beneath, join, literal, plain, NOT_FIXTURES};

use crate::chart::{coordinate_of, Chart, Local};

/// The checks of a chart, by the names their findings are asked by.
pub(crate) const LINT: &str = frontseat_plugin::kinds::LINT;
pub(crate) const TEMPLATE: &str = "template";

/// The input set of a chart's own files: what Helm reads of its
/// directory, and what a chart that depends on it is handed.
pub(crate) const CHART: &str = "chart";

pub(crate) fn chart_files() -> InputSet {
    InputSet {
        name: CHART.into(),
        inputs: vec![Input::source("**"), Input::source(NOT_FIXTURES)],
        ..Default::default()
    }
}

pub(crate) fn of(chart: &Chart) -> Vec<Action> {
    let mut inputs = vec![Input::set(CHART)];
    // A chart depended on by file:// is handed into charts/, where Helm
    // looks for it: what the checkout holds there of it, a copy or the
    // archive `helm dependency build` leaves, is not read beside it.
    // Each is placed by its own name, which is not its coordinate's.
    for local in &chart.local {
        let name = literal(&local.name);
        inputs.push(Input::source(format!("!charts/{name}/**")));
        inputs.push(Input::source(format!("!charts/{name}-[0-9]*.tgz")));
    }
    for local in &chart.local {
        inputs.push(Input::dependency(
            coordinate_of(&local.directory),
            format!("charts/{}", local.name),
        ));
    }
    // helm lint passes or fails on what it finds, warnings too (--strict),
    // and what it found is read from what it printed (linted).
    let lint = Action::new(LINT, Command::new(["helm", "lint", "--strict", "."]))
        .tool("helm")
        .inputs(inputs.clone())
        .check();
    // helm template renders the chart as an install would, with no
    // cluster, and fails where it cannot: a check that writes nothing.
    let template = Action::new(TEMPLATE, Command::new(["helm", "template", "."]))
        .tool("helm")
        .inputs(inputs)
        .check();
    vec![lint, template]
}

const LINTED: Finding = Finding {
    rule_id: "helm/lint",
    help: "Fix what helm lint reports; run helm lint --strict in the chart's directory to see it.",
};

const RENDERED: Finding = Finding {
    rule_id: "helm/template",
    help: "Fix what helm template reports; run helm template in the chart's directory to see it.",
};

/// What helm lint found. It prints each finding as `[SEVERITY] path:
/// message`, the path relative to the chart or the chart's own directory,
/// and a message that says where in a template it is as `<chart>/<file>:
/// <line>`, the file named beneath the chart's name; what follows it on
/// lines of their own, up to the next finding or a blank line, is part of
/// it. A finding is an error or a warning, which --strict fails on;
/// information is none.
pub(crate) fn linted(root: &str, chart: Option<&Chart>, printed: &Printed) -> Vec<Diagnostic> {
    let text = String::from_utf8_lossy(&printed.stdout);
    let mut found: Vec<(String, String)> = Vec::new();
    let mut open = false;
    for line in text.lines() {
        if let Some(rest) = ["[ERROR] ", "[WARNING] "]
            .iter()
            .find_map(|severity| line.strip_prefix(severity))
        {
            let (path, message) = rest.split_once(": ").unwrap_or(("", rest));
            found.push((path.into(), message.trim().into()));
            open = true;
        } else if line.trim().is_empty() || line.starts_with('[') || line.starts_with("==> ") {
            open = false;
        } else if let (true, Some((_, message))) = (open, found.last_mut()) {
            message.push(' ');
            message.push_str(line.trim());
        }
    }
    found
        .iter()
        .filter_map(|(path, message)| at(&LINTED, root, chart, path, message))
        .collect()
}

/// What helm template could not render, printed on a line of its own
/// beginning `Error: `, where a template is named as helm lint names one.
pub(crate) fn rendered(root: &str, chart: Option<&Chart>, printed: &Printed) -> Vec<Diagnostic> {
    if printed.exit_code == 0 {
        return Vec::new();
    }
    String::from_utf8_lossy(&printed.stderr)
        .lines()
        .filter_map(|line| line.strip_prefix("Error: "))
        .filter_map(|message| at(&RENDERED, root, chart, "", message.trim()))
        .collect()
}

/// A finding of a file of the repository: the template the message names,
/// else the file Helm named, else the chart's Chart.yaml. A template of a
/// chart handed into `charts/` is named in that chart's own directory.
fn at(
    rule: &Finding,
    root: &str,
    chart: Option<&Chart>,
    path: &str,
    message: &str,
) -> Option<Diagnostic> {
    if let Some((file, line)) = template_of(message) {
        let handed = chart.and_then(|chart| handed(&chart.local, file));
        return match handed {
            Some(file) => rule.at("", &file, line, message),
            None => rule.at(root, file, line, message),
        };
    }
    let file = if plain(path) { path } else { "Chart.yaml" };
    rule.at(root, file, 0, message)
}

/// Where a file beneath a chart's `charts/` is in the repository, where
/// it is of a chart handed there: Helm names it by the dependency's alias
/// where it has one, and by its name otherwise.
fn handed(local: &[Local], file: &str) -> Option<String> {
    let rest = file.strip_prefix("charts/")?;
    local.iter().find_map(|dependency| {
        let named = dependency.alias.as_deref().unwrap_or(&dependency.name);
        let within = beneath(named, rest)?;
        Some(join(&dependency.directory, within))
    })
}

/// The first template a message names, `<chart>/<file>:<line>` or
/// `<chart>/<file>:<line>:<column>`, as the file beneath the chart and the
/// line, from 1.
fn template_of(message: &str) -> Option<(&str, i32)> {
    message
        .split(|c: char| c.is_whitespace() || c == '(' || c == ')')
        .find_map(|word| {
            let (path, rest) = word.split_once(':')?;
            let number = rest.split(':').next()?;
            let line: i32 = number.parse().ok().filter(|line| *line >= 1)?;
            let (_, file) = path.split_once('/')?;
            plain(file).then_some((file, line))
        })
}

#[cfg(test)]
mod report_tests {
    use super::*;
    use crate::chart::Local;

    fn chart() -> Chart {
        Chart {
            name: "web".into(),
            directory: "deploy/web".into(),
            manifest: "deploy/web/Chart.yaml".into(),
            local: vec![Local {
                name: "common".into(),
                alias: Some("shared".into()),
                directory: "deploy/common".into(),
                origin: Origin::default(),
                version: String::new(),
            }],
        }
    }

    fn located(found: &[Diagnostic]) -> Vec<(&str, &str, i32)> {
        found
            .iter()
            .map(|d| {
                let at = d.source_location.as_ref().unwrap();
                (d.rule_id.as_str(), at.path.as_str(), at.line)
            })
            .collect()
    }

    /// Each error and warning helm lint prints is a finding at its file
    /// and line, re-rooted from the chart's name to the repository; what
    /// it says of a chart handed into charts/ is said where that chart is,
    /// and what it says of the chart's directory, at its Chart.yaml.
    /// Information is no finding, and neither is the summary.
    #[test]
    fn what_lint_prints_is_a_finding_at_the_file_and_line_it_names() {
        let printed = Printed::new(
            1,
            "==> Linting .\n\
             [INFO] Chart.yaml: icon is recommended\n\
             [ERROR] templates/: parse error at (web/templates/a.yaml:2): unclosed action started at web/templates/a.yaml:1\n\
             [ERROR] templates/: web/templates/b.yaml:6:9\n  executing \"web/templates/b.yaml\" at <.Values.a.b>:\n    nil pointer evaluating interface {}.b\n\
             [ERROR] templates/: parse error at (web/charts/shared/templates/_x.tpl:3): function \"nofunc\" not defined\n\
             [ERROR] templates/c.yaml: unable to parse YAML: error converting YAML to JSON: yaml: line 4: did not find expected key\n\
             [WARNING] /worker/build/root/deploy/web: chart directory is missing these dependencies: common\n\
             \n\
             Error: 1 chart(s) linted, 1 chart(s) failed\n",
            Vec::new(),
        );
        let found = linted("deploy/web", Some(&chart()), &printed);
        assert_eq!(
            located(&found),
            [
                ("helm/lint", "deploy/web/templates/a.yaml", 2),
                ("helm/lint", "deploy/web/templates/b.yaml", 6),
                ("helm/lint", "deploy/common/templates/_x.tpl", 3),
                ("helm/lint", "deploy/web/templates/c.yaml", 0),
                ("helm/lint", "deploy/web/Chart.yaml", 0),
            ]
        );
        assert_eq!(
            found[0].message,
            "parse error at (web/templates/a.yaml:2): unclosed action started at web/templates/a.yaml:1"
        );
        assert_eq!(
            found[1].message,
            "web/templates/b.yaml:6:9 executing \"web/templates/b.yaml\" at <.Values.a.b>: nil pointer evaluating interface {}.b"
        );
        // The control: a chart that lints clean prints information alone.
        let clean = Printed::new(
            0,
            "==> Linting .\n[INFO] Chart.yaml: icon is recommended\n\n1 chart(s) linted, 0 chart(s) failed\n",
            Vec::new(),
        );
        assert!(linted("deploy/web", Some(&chart()), &clean).is_empty());
    }

    /// What helm template could not render is a finding at the template it
    /// names; a render that passed found nothing, whatever it printed.
    #[test]
    fn what_template_could_not_render_is_a_finding_at_its_template() {
        let failed = Printed::new(
            1,
            Vec::new(),
            "Error: parse error at (web/templates/a.yaml:2): unclosed action\n\nUse --debug flag to render out invalid YAML\n",
        );
        assert_eq!(
            located(&rendered("deploy/web", None, &failed)),
            [("helm/template", "deploy/web/templates/a.yaml", 2)]
        );
        let missing = Printed::new(1, Vec::new(), "Error: an error occurred while checking for chart dependencies: found in Chart.yaml, but missing in charts/ directory: common\n");
        assert_eq!(
            located(&rendered("deploy/web", None, &missing)),
            [("helm/template", "deploy/web/Chart.yaml", 0)]
        );
        let passed = Printed::new(
            0,
            "---\n# Source: web/templates/a.yaml\n",
            "Error: not one\n",
        );
        assert!(rendered("deploy/web", None, &passed).is_empty());
    }

    #[test]
    fn a_template_is_named_beneath_the_chart_at_a_line_of_one_or_more() {
        assert_eq!(
            template_of("parse error at (web/templates/a.yaml:2): x"),
            Some(("templates/a.yaml", 2))
        );
        assert_eq!(
            template_of("web/templates/b.yaml:6:9"),
            Some(("templates/b.yaml", 6))
        );
        for none in [
            "yaml: line 4: did not find expected key",
            "a.yaml:3: no chart before it",
            "web/templates/a.yaml:0: no line",
            "web/../a.yaml:3: leaves the chart",
            "web/templates/a.yaml:x",
        ] {
            assert_eq!(template_of(none), None, "{none}");
        }
    }
}
