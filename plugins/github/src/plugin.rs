use frontseat_plugin::prelude::*;

pub(crate) struct GitHubPlugin;

/// What the repository lists its plugins in: listing this one is what
/// says the repository is GitHub's, and nothing else of the checkout
/// does that a plugin is shown.
const LISTED: &str = "frontseat.yaml";
/// The tool that publishes: GitHub's own command line.
const TOOL: &str = "gh";
/// What that tool reads its token from.
const TOKEN: &str = "GH_TOKEN";

/// The release is made with every file in one call: gh uploads them to
/// a draft and publishes it once all are there, so a release is never
/// seen half made. The tag must be there already, as the release that
/// runs this pushed it: gh makes none. A release there already for the
/// tag is an error.
const CREATE: [&str; 12] = [
    "gh",
    "release",
    "create",
    "{frontseat.tag}",
    "{frontseat.artifacts}",
    "--repo",
    "{frontseat.remote}",
    "--verify-tag",
    "--title",
    "{frontseat.tag}",
    "--notes-file",
    "{frontseat.notes}",
];

impl Plugin for GitHubPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "github".into(),
            interests: vec![LISTED.into()],
            ..Default::default()
        }
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        // One project for the repository, at its root, which no entity
        // owns where the root is no Component's: its publish is then
        // each unit's of the repository.
        // The file is found, and never read: what it says is the host's
        // to read, and listing this plugin is all it says to it.
        if !root.is_empty() || repo.glob("", &[LISTED])?.is_empty() {
            return Ok(Analysis::default());
        }
        let manifest = LISTED.to_string();
        // Its tool is pinned, or the repository is refused here, before
        // anything is built or tagged, as every plugin refuses one that
        // pins none of what its actions run.
        repo.pinned(TOOL, &manifest)?;
        let mut arguments: Vec<String> = CREATE.iter().map(|each| each.to_string()).collect();
        arguments.push("--prerelease={frontseat.prerelease}".into());
        Ok(Analysis {
            projects: vec![Project {
                root: root.into(),
                kind: "github-repository".into(),
                name: "github".into(),
                manifest,
                actions: vec![Action::new("publish/release", Command::new(arguments))
                    .tool(TOOL)
                    .publishes([TOKEN])],
                ..Default::default()
            }],
            ..Default::default()
        })
    }
}

#[cfg(test)]
mod tests {
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    use super::*;

    fn projects(files: Files, roots: &[&str]) -> Vec<Project> {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Production).tool("gh", "2.105.0");
        plugin
            .analyze(&TestHost::new(files), &context, roots)
            .answered()
            .analyses
            .into_iter()
            .flat_map(|analysis| analysis.projects)
            .collect()
    }

    /// A repository that lists it and pins no gh is refused when it is
    /// analysed, before anything is built or tagged, saying what to pin.
    #[test]
    fn a_repository_that_pins_no_gh_is_refused_when_it_is_analysed() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Production);
        let files = Files::new().with("frontseat.yaml", "plugins:\n  github: {}\n");
        let refused = plugin
            .analyze(&TestHost::new(files), &context, &[""])
            .refused("unpinned-tool");
        assert!(format!("{refused:?}").contains("pins no gh"), "{refused:?}");
    }

    /// A repository that lists the plugin has one project, at its root,
    /// whose one action publishes: GitHub's own command line makes the
    /// release for the tag, which must be there, with every file in the
    /// one call, and is given the token it reads.
    #[test]
    fn a_repository_that_lists_it_publishes_each_release_with_githubs_command_line() {
        let found = projects(
            Files::new().with("frontseat.yaml", "plugins:\n  github: {}\n"),
            &[""],
        );
        assert_eq!(found.len(), 1, "{found:?}");
        let project = &found[0];
        assert_eq!(
            (
                project.root.as_str(),
                project.kind.as_str(),
                project.manifest.as_str()
            ),
            ("", "github-repository", "frontseat.yaml")
        );
        assert_eq!(project.actions.len(), 1);
        let action = &project.actions[0];
        assert_eq!(action.name, "publish/release");
        assert_eq!(action.tools, ["gh"]);
        assert_eq!(
            action
                .publish
                .as_ref()
                .map(|publish| publish.environment.clone()),
            Some(vec!["GH_TOKEN".to_string()])
        );
        assert_eq!(
            action.command.as_ref().unwrap().arguments,
            [
                "gh",
                "release",
                "create",
                "{frontseat.tag}",
                "{frontseat.artifacts}",
                "--repo",
                "{frontseat.remote}",
                "--verify-tag",
                "--title",
                "{frontseat.tag}",
                "--notes-file",
                "{frontseat.notes}",
                "--prerelease={frontseat.prerelease}",
            ]
        );
        assert!(action.inputs.is_empty() && action.outputs.is_empty());
    }

    /// It has a project at the repository's root alone, and none where
    /// the file that lists it is not there.
    #[test]
    fn the_project_is_at_the_repositorys_root_and_nowhere_else() {
        let listed = || Files::new().with("frontseat.yaml", "plugins:\n  github: {}\n");
        assert!(projects(listed().with("nested/frontseat.yaml", ""), &["nested"]).is_empty());
        assert!(projects(Files::new().with("go.mod", "module x\n"), &[""]).is_empty());
    }

    #[test]
    fn it_takes_no_options_and_is_asked_at_the_repositorys_root() {
        let described = crate::served().describe();
        assert_eq!(described.name, "github");
        assert!(described.options.is_empty());
        assert_eq!(described.interests[0], "frontseat.yaml");
    }
}
