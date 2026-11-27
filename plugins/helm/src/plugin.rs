use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

use crate::actions::{self, CHART, LINT, TEMPLATE};
use crate::chart::{self, coordinate_of};

pub(crate) struct HelmPlugin;

/// Whether a directory is a chart within another's `charts/`: a subchart,
/// which Helm reads as part of the chart that holds it, and no project of
/// its own.
fn subchart(repo: &Repository, root: &str) -> Result<bool, Error> {
    let parts: Vec<&str> = root.split('/').collect();
    // Helm loads no chart of charts/ whose name begins "_" or ".".
    for (at, window) in parts.windows(2).enumerate() {
        if window[0] == "charts" && !window[1].starts_with(['_', '.']) {
            let holder = parts[..at].join("/");
            if repo.read(&join(&holder, "Chart.yaml"))?.is_some() {
                return Ok(true);
            }
        }
    }
    Ok(false)
}

impl Plugin for HelmPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "helm".into(),
            interests: vec!["**/Chart.yaml".into()],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        if subchart(repo, root)? {
            return Ok(Analysis::default());
        }
        let Some(chart) = chart::read(repo, root)? else {
            return Ok(Analysis::default());
        };
        repo.pinned("helm", &chart.manifest)?;
        let requires = chart
            .local
            .iter()
            .map(|local| Requirement {
                coordinate: Some(coordinate_of(&local.directory)),
                origin: Some(local.origin.clone()),
                version: local.version.clone(),
            })
            .collect();
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                kind: "helm-chart".into(),
                name: chart.name.clone(),
                manifest: chart.manifest.clone(),
                provides: vec![Provision {
                    coordinate: Some(coordinate_of(&chart.directory)),
                    sources: CHART.into(),
                    ..Default::default()
                }],
                requires,
                actions: actions::of(&chart),
                input_sets: vec![actions::chart_files()],
                ..Default::default()
            }],
            ..Default::default()
        })
    }

    /// Helm finds a chart handed into `charts/` by itself: nothing is
    /// written for it.
    fn arrange(
        &self,
        _repo: &Repository,
        _root: &str,
        actions: &[String],
        _handed: &[Handoff],
    ) -> Result<Vec<File>, Error> {
        if let Some(other) = actions
            .iter()
            .find(|action| ![LINT, TEMPLATE].contains(&action.as_str()))
        {
            return Err(Error::new(
                Code::InvalidArgument,
                "unsupported-arrangement",
                format!("{other} is no action of a chart that is handed what it depends on"),
            )
            .help("Arrange only a chart's lint and template."));
        }
        Ok(Vec::new())
    }

    fn report(
        &self,
        repo: &Repository,
        root: &str,
        action: &str,
        printed: &Printed,
    ) -> Result<Vec<Diagnostic>, Error> {
        // Where a handed chart is, for a finding of its templates; a chart
        // that cannot be read now has its findings said of itself.
        let chart = chart::read(repo, root).ok().flatten();
        Ok(match action {
            LINT => actions::linted(root, chart.as_ref(), printed),
            TEMPLATE => actions::rendered(root, chart.as_ref(), printed),
            _ => Vec::new(),
        })
    }
}

#[cfg(test)]
mod analysis_tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestContext, TestHost, TestPlugin};

    const WEB: &str = "apiVersion: v2\nname: web\nversion: 0.1.0\n";

    fn pinned<P: Plugin>(plugin: &TestPlugin<P>) -> TestContext {
        plugin.context(Profile::Development).tool("helm", "4.3.1")
    }

    /// A directory that holds a Chart.yaml is a chart, named as its
    /// Chart.yaml names it, linted and rendered by checks over its own
    /// files. The control: a chart beneath another's charts/ is part of
    /// that one, and no project, while a directory named charts that no
    /// chart holds is any directory.
    #[test]
    fn a_chart_is_a_directory_that_holds_a_chart_yaml() {
        let plugin = TestPlugin::new(crate::served());
        let context = pinned(&plugin);
        let host = TestHost::new(Files::new().with("charts/web/Chart.yaml", WEB).with(
            "charts/web/charts/sub/Chart.yaml",
            "apiVersion: v2\nname: sub\nversion: 0.1.0\n",
        ));
        let analysis = plugin
            .analyze(&host, &context, &["charts/web", "charts/web/charts/sub"])
            .answered();
        let project = &analysis.analyses[0].projects[0];
        assert_eq!(
            (
                project.name.as_str(),
                project.kind.as_str(),
                project.manifest.as_str()
            ),
            ("web", "helm-chart", "charts/web/Chart.yaml")
        );
        let actions: Vec<(&str, &[String], bool)> = project
            .actions
            .iter()
            .map(|action| {
                (
                    action.name.as_str(),
                    action.command.as_ref().unwrap().arguments.as_slice(),
                    action.as_check().is_some(),
                )
            })
            .collect();
        assert_eq!(
            actions,
            [
                (
                    "lint",
                    &[
                        "helm".to_string(),
                        "lint".into(),
                        "--strict".into(),
                        ".".into()
                    ][..],
                    true
                ),
                (
                    "template",
                    &["helm".to_string(), "template".into(), ".".into()][..],
                    true
                ),
            ]
        );
        assert!(project.requires.is_empty());
        assert!(analysis.analyses[1].projects.is_empty());
    }

    /// A chart's actions run helm as the repository pins it: one that pins
    /// none is refused when it is analysed, beside a pinned control.
    #[test]
    fn a_repository_that_pins_no_helm_is_refused() {
        let plugin = TestPlugin::new(crate::served());
        let host = TestHost::new(Files::new().with("web/Chart.yaml", WEB));
        plugin.analyze(&host, &pinned(&plugin), &["web"]).answered();
        plugin
            .analyze(&host, &plugin.context(Profile::Development), &["web"])
            .refused("unpinned-tool");
    }

    /// A chart that depends on another of the repository by file:// is
    /// served: it requires that chart, at the line it names it, at the
    /// version its lock says, and is handed its files into charts/, where
    /// Helm looks, and not a copy the checkout holds there. One that
    /// depends on a chart fetched from outside is refused by name, as is a
    /// dependency's own dependency outside it; a dependency in the chart's
    /// own charts/ is in its files, and requires nothing.
    #[test]
    fn a_local_dependency_is_served_and_a_remote_one_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let context = pinned(&plugin);
        let common = "apiVersion: v2\nname: common\nversion: 0.2.0\n";
        let depending = |repository: &str| {
            format!("apiVersion: v2\nname: web\nversion: 0.1.0\ndependencies:\n  - name: vendored\n    version: 1.0.0\n  - name: common\n    version: 0.2.0\n    repository: {repository}\n")
        };
        let files = |web: String| {
            Files::new()
                .with("web/Chart.yaml", web)
                .with("web/Chart.lock", "dependencies:\n- name: common\n  repository: file://../library\n  version: 0.2.0\ndigest: sha256:0\ngenerated: \"2026-11-26T00:00:00Z\"\n")
                .with("library/Chart.yaml", common)
        };
        let analysis = plugin
            .analyze(
                &TestHost::new(files(depending("file://../library"))),
                &context,
                &["web"],
            )
            .answered();
        let project = &analysis.analyses[0].projects[0];
        assert_eq!(
            project.requires,
            [Requirement {
                coordinate: Some(Coordinate {
                    ecosystem: "helm".into(),
                    name: "library".into()
                }),
                origin: Some(Origin {
                    path: "web/Chart.yaml".into(),
                    line: 7
                }),
                version: "0.2.0".into(),
            }]
        );
        assert_eq!(
            project.actions[0].inputs,
            [
                Input::set("chart"),
                Input::source("!charts/common/**"),
                Input::source("!charts/common-[0-9]*.tgz"),
                Input::dependency(
                    Coordinate {
                        ecosystem: "helm".into(),
                        name: "library".into()
                    },
                    "charts/common"
                ),
            ]
        );
        let provided = plugin
            .analyze(
                &TestHost::new(files(depending("file://../library"))),
                &context,
                &["library"],
            )
            .answered();
        assert_eq!(
            provided.analyses[0].projects[0].provides[0].sources,
            "chart"
        );

        for remote in [
            "oci://registry.example.com/charts",
            "https://charts.example.com",
            "\"@stable\"",
        ] {
            let refused = plugin
                .analyze(&TestHost::new(files(depending(remote))), &context, &["web"])
                .refused("remote-dependencies-not-served");
            assert_eq!(refused.code, Code::Unimplemented, "{remote}");
        }
        // A remote dependency whose chart the checkout holds in charts/ is
        // read there, as Helm reads it, and fetches nothing.
        for held in [
            "web/charts/common-0.2.0.tgz",
            "web/charts/common/Chart.yaml",
        ] {
            let served = plugin
                .analyze(
                    &TestHost::new(
                        files(depending("oci://registry.example.com/charts")).with(held, "held"),
                    ),
                    &context,
                    &["web"],
                )
                .answered();
            assert!(served.analyses[0].projects[0].requires.is_empty(), "{held}");
        }
        // A directory within the chart that is not of its charts/ is not
        // where Helm looks.
        let within = files(depending("file://./sub")).with("web/sub/Chart.yaml", common);
        let refused = plugin
            .analyze(&TestHost::new(within), &context, &["web"])
            .refused("invalid-manifest");
        assert!(refused.message.contains("charts/"), "{}", refused.message);
        let deeper = files(depending("file://../library")).with(
            "library/Chart.yaml",
            "apiVersion: v2\nname: common\nversion: 0.2.0\ndependencies:\n  - name: base\n    repository: file://../base\n",
        );
        plugin
            .analyze(&TestHost::new(deeper), &context, &["web"])
            .refused("transitive-dependencies-not-served");
        for (wrong, why) in [
            ("file://../elsewhere", "holds no chart"),
            ("file://../../outside", "outside the repository"),
        ] {
            let refused = plugin
                .analyze(&TestHost::new(files(depending(wrong))), &context, &["web"])
                .refused("invalid-manifest");
            assert!(
                refused.message.contains(why),
                "{wrong}: {}",
                refused.message
            );
        }
    }

    /// Helm reads a chart's dependencies from requirements.yaml over
    /// Chart.yaml, and its lock from requirements.lock over Chart.lock,
    /// as its loader does; a lock that is not one is refused, as Helm
    /// refuses to load the chart.
    #[test]
    fn dependencies_and_their_lock_are_read_as_helm_reads_them() {
        let plugin = TestPlugin::new(crate::served());
        let context = pinned(&plugin);
        let host = |lock: &str| {
            TestHost::new(
                Files::new()
                    .with(
                        "web/Chart.yaml",
                        "apiVersion: v1\nname: web\nversion: 0.1.0\n",
                    )
                    .with(
                        "web/requirements.yaml",
                        "dependencies:\n  - name: common\n    repository: file://../library\n",
                    )
                    .with(
                        "web/Chart.lock",
                        "dependencies:\n- name: common\n  version: 0.1.0\n",
                    )
                    .with("web/requirements.lock", lock)
                    .with(
                        "library/Chart.yaml",
                        "apiVersion: v1\nname: common\nversion: 0.3.0\n",
                    ),
            )
        };
        let analysis = plugin
            .analyze(
                &host("dependencies:\n- name: common\n  version: 0.3.0\n"),
                &context,
                &["web"],
            )
            .answered();
        let required = &analysis.analyses[0].projects[0].requires[0];
        assert_eq!(
            (
                required.origin.as_ref().unwrap().path.as_str(),
                required.origin.as_ref().unwrap().line,
                required.version.as_str()
            ),
            ("web/requirements.yaml", 2, "0.3.0")
        );
        plugin
            .analyze(&host("dependencies: [\n"), &context, &["web"])
            .refused("invalid-yaml");
    }

    /// Two charts of one name, in two directories, are two charts: each is
    /// provided by its directory, and a file:// dependency requires the
    /// one it names.
    #[test]
    fn two_charts_of_one_name_are_provided_apart() {
        let plugin = TestPlugin::new(crate::served());
        let context = pinned(&plugin);
        let host = TestHost::new(
            Files::new()
                .with(
                    "a/Chart.yaml",
                    "apiVersion: v2\nname: app\nversion: 0.1.0\n",
                )
                .with(
                    "b/Chart.yaml",
                    "apiVersion: v2\nname: app\nversion: 0.1.0\n",
                )
                .with("Chart.yaml", "apiVersion: v2\nname: top\nversion: 0.1.0\n"),
        );
        let analysis = plugin.analyze(&host, &context, &["a", "b", ""]).answered();
        let provided: Vec<&str> = analysis
            .analyses
            .iter()
            .map(|a| {
                a.projects[0].provides[0]
                    .coordinate
                    .as_ref()
                    .unwrap()
                    .name
                    .as_str()
            })
            .collect();
        assert_eq!(provided, ["a", "b", "."]);
    }

    /// What Helm reads as nothing is nothing: a repository or a name that
    /// is null, a requirements.yaml or a lock that holds no document, a
    /// requirements.yaml whose dependencies are null, which clears
    /// Chart.yaml's. A chart beneath charts/_x is one Helm does not load,
    /// and no subchart.
    #[test]
    fn what_helm_reads_as_nothing_is_nothing() {
        let plugin = TestPlugin::new(crate::served());
        let context = pinned(&plugin);
        let analyze = |files: Files, root: &str| {
            plugin
                .analyze(&TestHost::new(files), &context, &[root])
                .answered()
                .analyses
                .remove(0)
                .projects
        };
        let web = |dependencies: &str| {
            Files::new().with(
                "web/Chart.yaml",
                format!("apiVersion: v2\nname: web\nversion: 0.1.0\ndependencies:\n{dependencies}"),
            )
        };
        let null = analyze(
            web("  - name: common\n    repository:\n  - name: other\n    repository: ~\n"),
            "web",
        );
        assert!(null[0].requires.is_empty());
        let empty = web("  - name: gone\n    repository: file://../gone\n")
            .with("web/requirements.yaml", "# nothing\n")
            .with("web/Chart.lock", "")
            .with("web/requirements.lock", "# nothing\n");
        plugin
            .analyze(&TestHost::new(empty.clone()), &context, &["web"])
            .refused("invalid-manifest");
        let cleared = empty.with("web/requirements.yaml", "dependencies: null\n");
        assert!(analyze(cleared, "web")[0].requires.is_empty());
        let ignored = Files::new()
            .with(
                "web/Chart.yaml",
                "apiVersion: v2\nname: web\nversion: 0.1.0\n",
            )
            .with(
                "web/charts/_x/Chart.yaml",
                "apiVersion: v2\nname: x\nversion: 0.1.0\n",
            );
        assert_eq!(analyze(ignored, "web/charts/_x").len(), 1);
    }
}
