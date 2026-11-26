use frontseat_plugin::prelude::*;

pub(crate) struct YarnPlugin;

impl Plugin for YarnPlugin {
    fn describe(&self) -> Description {
        frontseat_plugin::javascript::description("yarn")
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let Some(install) = crate::manifest::read(repo, root)? else {
            return Ok(Analysis::default());
        };
        Ok(frontseat_plugin::javascript::installs(
            root,
            &install.package,
            "yarn-install",
            (
                crate::actions::INSTALL,
                &crate::actions::installed(&install),
            ),
            crate::actions::of(&install),
        ))
    }
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    const ROOT: &str = r#"{"name":"portal","private":true,"workspaces":["packages/*"]}"#;
    const APP: &str = r#"{"name":"app","dependencies":{"@scope/is-number":"7.0.0"}}"#;
    const RC: &str = "nodeLinker: node-modules\n";

    fn workspace() -> Files {
        Files::new()
            .with("portal/package.json", ROOT)
            .with("portal/yarn.lock", crate::lock::tests::LOCK)
            .with("portal/.yarnrc.yml", RC)
            .with("portal/packages/app/package.json", APP)
            .with(
                "portal/packages/backend/package.json",
                r#"{"name":"backend"}"#,
            )
            .with(
                "portal/packages/app/testdata/fixture/package.json",
                r#"{"name":"fixture"}"#,
            )
    }

    /// An install is a project at its root that fetches and then installs
    /// offline the Yarn mise.toml pins, reading every workspace's manifest
    /// and leaving each workspace's node_modules; a workspace is no project
    /// of its own, and a fixture beneath one is no workspace.
    #[test]
    fn a_workspace_is_installed_at_its_root_and_its_members_are_no_projects() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development).tool("yarn", "4.13.0");
        let host = TestHost::new(workspace());
        let projects: Vec<Project> = plugin
            .analyze(
                &host,
                &context,
                &["portal", "portal/packages/app", "portal/packages/backend"],
            )
            .answered()
            .analyses
            .into_iter()
            .flat_map(|analysis| analysis.projects)
            .collect();
        assert_eq!(projects.len(), 1, "{projects:?}");
        let project = &projects[0];
        assert_eq!(
            (
                project.root.as_str(),
                project.name.as_str(),
                project.kind.as_str()
            ),
            ("portal", "portal", "yarn-install")
        );
        let install = project
            .actions
            .iter()
            .find(|action| action.name == "install")
            .expect("an install");
        let outputs: Vec<&str> = install
            .outputs
            .iter()
            .map(|output| output.path.as_str())
            .collect();
        assert_eq!(
            outputs,
            [
                "node_modules/",
                "packages/app/node_modules/",
                "packages/backend/node_modules/"
            ]
        );
        let command = install.command.as_ref().expect("a command");
        // Yarn, started by the pinned Node, which tells node-gyp where its
        // headers are, so that a native module is built offline.
        assert_eq!(command.arguments[..2], ["node", "-e"]);
        assert!(command.arguments[2].contains("npm_config_nodedir"));
        assert_eq!(
            command.arguments[3..],
            ["install", "--immutable", "--immutable-cache"]
        );
        for (name, value) in [
            ("YARN_ENABLE_NETWORK", "false"),
            ("npm_config_build_from_source", "true"),
        ] {
            assert_eq!(
                command.environment.get(name).map(String::as_str),
                Some(value),
                "{name}"
            );
        }
        let provided: Vec<&str> = project.provides[0]
            .artifacts
            .iter()
            .map(|artifact| artifact.output.as_str())
            .collect();
        assert_eq!(provided, outputs);
        assert_eq!(install.tools, ["node", "yarn"]);
        let fetch = project
            .actions
            .iter()
            .find(|action| action.name == "fetch")
            .expect("a fetch");
        assert!(fetch.is_fetch());
        let read: Vec<&str> = fetch
            .inputs
            .iter()
            .filter_map(|input| match input.kind.as_ref() {
                Some(frontseat_plugin::wire::input::Kind::Source(pattern)) => {
                    Some(pattern.as_str())
                }
                _ => None,
            })
            .collect();
        assert_eq!(
            read,
            [
                "package.json",
                "yarn.lock",
                ".yarn/patches/**",
                ".yarnrc.yml",
                "packages/*/package.json"
            ]
        );
    }

    /// What Yarn reads and the plugin does not build yet is refused as not
    /// served, each by its own reason; what is wrong, as invalid; the
    /// control is the workspace, which is built, and its refusals' help is
    /// followed and built.
    #[test]
    fn what_yarn_reads_and_is_not_built_yet_is_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development).tool("yarn", "4.13.0");
        let analyze = |files: Files, context: &frontseat_plugin_test::TestContext| {
            plugin.analyze(&TestHost::new(files), context, &["portal"])
        };
        analyze(workspace(), &context).answered();
        let named = |what: &str| ROOT.replace(r#""private":true"#, what);
        for (files, reason, at) in [
            (
                workspace().without("portal/.yarnrc.yml"),
                "linker-not-served",
                "portal/.yarnrc.yml",
            ),
            (
                workspace().with("portal/.yarnrc.yml", "nodeLinker: pnp\n"),
                "linker-not-served",
                "portal/.yarnrc.yml",
            ),
            (
                workspace().with(
                    "portal/.yarnrc.yml",
                    format!("{RC}plugins:\n  - path: .yarn/plugins/x.cjs\n"),
                ),
                "yarn-plugins-not-served",
                "portal/.yarnrc.yml",
            ),
            (
                workspace().with(
                    "portal/.yarnrc.yml",
                    format!("{RC}npmRegistryServer: https://npm.example.com\n"),
                ),
                "registry-not-served",
                "portal/.yarnrc.yml",
            ),
            (
                workspace().with(
                    "portal/package.json",
                    named(r#""scripts":{"postinstall":"node x.js"}"#),
                ),
                "install-hooks-not-served",
                "portal/package.json",
            ),
            (
                workspace().with(
                    "portal/packages/app/package.json",
                    r#"{"name":"app","scripts":{"preinstall":"x"}}"#,
                ),
                "install-hooks-not-served",
                "portal/packages/app/package.json",
            ),
            (
                workspace().with(
                    "portal/package.json",
                    named(r#""packageManager":"yarn@4.12.0""#),
                ),
                "invalid-package-manager",
                "portal/package.json",
            ),
            (
                workspace().with(
                    "portal/package.json",
                    named(r#""packageManager":"pnpm@11.0.0""#),
                ),
                "invalid-package-manager",
                "portal/package.json",
            ),
        ] {
            let refused = analyze(files, &context).refused(reason);
            assert_eq!(refused.path, at, "{reason}");
        }
        for setting in [
            "httpsProxy: http://proxy.example.com",
            "caFilePath: ca.pem",
            "unsafeHttpWhitelist: [example.com]",
            "npmAuthToken: secret",
            "networkSettings: {}",
        ] {
            let refused = analyze(
                workspace().with("portal/.yarnrc.yml", format!("{RC}{setting}\n")),
                &context,
            )
            .refused("fetch-settings-not-served");
            assert!(
                !format!("{refused:?}").contains("example.com")
                    && !format!("{refused:?}").contains("secret"),
                "{refused:?}"
            );
        }
        analyze(workspace(), &plugin.context(Profile::Development)).refused("unpinned-tool");
        analyze(
            workspace(),
            &plugin.context(Profile::Development).tool("yarn", "1.22.22"),
        )
        .refused("yarn-classic-not-served");
        // The help followed: the pinned Yarn named, with its hash, and npm's
        // registry named as Yarn names it.
        analyze(
            workspace().with(
                "portal/package.json",
                named(r#""packageManager":"yarn@4.13.0+sha512.abc""#),
            ),
            &context,
        )
        .answered();
        analyze(
            workspace().with(
                "portal/.yarnrc.yml",
                format!("{RC}npmRegistryServer: \"https://registry.yarnpkg.com\"\n"),
            ),
            &context,
        )
        .answered();
    }

    /// A package no Yarn install serves is refused by name: npm's, saying
    /// to list the npm plugin, or one no lock serves; the control is a
    /// workspace of an install above, which is no project of its own.
    #[test]
    fn a_package_no_yarn_install_serves_is_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development).tool("yarn", "4.13.0");
        let web = Files::new().with("web/package.json", r#"{"name":"web"}"#);
        let analyze =
            |files: Files, root: &str| plugin.analyze(&TestHost::new(files), &context, &[root]);
        let refused = analyze(web.clone().with("web/package-lock.json", "{}"), "web")
            .refused("npm-not-served");
        assert_eq!(refused.path, "web/package-lock.json");
        assert!(refused.help.contains("npm plugin"), "{refused:?}");
        assert_eq!(
            analyze(web, "web").refused("invalid-lock").path,
            "web/package.json"
        );
        let answered = analyze(workspace(), "portal/packages/app").answered();
        assert!(answered
            .analyses
            .iter()
            .all(|analysis| analysis.projects.is_empty()));
    }
}
