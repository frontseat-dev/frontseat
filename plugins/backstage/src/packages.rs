//! The packages the Backstage CLI builds: a repository whose Yarn install's
//! root depends on `@backstage/cli`, and each package of it that names a
//! role (`backstage.role`). Each is built, linted and tested by the CLI's
//! own command for its role, as its `package.json` scripts run it, with
//! what the Yarn install left handed to it where it was installed.

use frontseat_plugin::javascript::{install_of, Manager, Package};
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{beneath, join};
use serde::Deserialize;

/// The CLI, as the install lays it out beneath the repository's root.
const CLI: &str = "node_modules/@backstage/cli/bin/backstage-cli";
/// The package that makes a repository one the CLI builds.
const CLI_PACKAGE: &str = "@backstage/cli";
/// Where the repository's one compilation writes its declarations, which
/// a package's build bundles its own from.
const TYPES: &str = "dist-types/";
/// Where a package's build writes what it built.
const BUILT: &str = "dist/";
/// The roles the CLI knows, from its source (`cli-node`'s `PackageRoles`).
const ROLES: [&str; 10] = [
    "frontend",
    "backend",
    "cli",
    "web-library",
    "node-library",
    "common-library",
    "frontend-plugin",
    "frontend-plugin-module",
    "backend-plugin",
    "backend-plugin-module",
];

const HELP: &str = "Build Backstage packages from a Yarn install whose root depends on @backstage/cli, as `backstage-cli new` lays one out, each package naming one of the CLI's roles.";

#[derive(Deserialize)]
struct Manifest {
    #[serde(flatten)]
    package: Package,
    backstage: Option<Backstage>,
}

#[derive(Deserialize)]
struct Backstage {
    role: Option<String>,
}

fn repository_of(name: &str) -> Coordinate {
    Coordinate {
        ecosystem: "backstage-repository".into(),
        name: name.into(),
    }
}

fn package_of(name: &str) -> Coordinate {
    Coordinate {
        ecosystem: "backstage-package".into(),
        name: name.into(),
    }
}

/// The line of a manifest's text that first names `name` as a key, from 1:
/// where a requirement is said.
fn line_of(text: &str, name: &str) -> i32 {
    let key = format!("\"{name}\"");
    text.lines()
        .position(|line| line.contains(&key))
        .and_then(|index| i32::try_from(index + 1).ok())
        .unwrap_or(1)
}

fn requirement(coordinate: Coordinate, manifest: &str, line: i32) -> Requirement {
    Requirement {
        coordinate: Some(coordinate),
        origin: Some(Origin {
            path: manifest.into(),
            line,
        }),
        version: String::new(),
    }
}

/// What the Backstage CLI builds at `root`: the repository's project at
/// its Yarn install's root, or a package's; none where `root` holds no
/// `package.json`, or one that is neither.
pub(super) fn projects(repo: &Repository, root: &str) -> Result<Vec<Project>, Error> {
    let manifest = join(root, "package.json");
    let Some(document) = repo.json::<Manifest>(&manifest)? else {
        return Ok(Vec::new());
    };
    let Manifest { package, backstage } = document.value;
    let role = backstage.and_then(|backstage| backstage.role);
    let install = install_of(repo, root)?;
    let yarn = install
        .as_ref()
        .filter(|install| install.manager == Manager::Yarn);
    if yarn.is_some_and(|install| install.root == root) && package.depends_on(CLI_PACKAGE) {
        return Ok(vec![repository(root, &package)]);
    }
    let Some(role) = role else {
        return Ok(Vec::new());
    };
    if !ROLES.contains(&role.as_str()) {
        return Err(Error::invalid(
            "invalid-role",
            &manifest,
            "backstage.role names no role the Backstage CLI knows",
            format!("Name one of {}.", ROLES.join(", ")),
        )
        .at_line(&manifest, line_of(&document.text, "role")));
    }
    let Some(install) = yarn else {
        return Err(Error::not_served(
            "backstage-install",
            "a Backstage package is built from a Yarn install, and no Yarn install serves this one",
        )
        .at(&manifest)
        .help(HELP));
    };
    let root_manifest = join(&install.root, "package.json");
    let workspace = repo
        .json::<Package>(&root_manifest)?
        .map(|document| document.value)
        .filter(|workspace| workspace.depends_on(CLI_PACKAGE))
        .ok_or_else(|| {
            Error::invalid(
                "missing-cli",
                &root_manifest,
                "the Yarn install that serves this Backstage package does not depend on @backstage/cli at its root",
                HELP,
            )
        })?;
    Ok(vec![package_project(
        root,
        &install.root,
        &workspace,
        &package,
        &role,
        &document.text,
    )])
}

/// The repository's project: the one compilation over every package,
/// as Backstage's `tsc` script runs it, writing the declarations each
/// package's build bundles its own from; and what every package of the
/// repository reads at its root.
fn repository(root: &str, package: &Package) -> Project {
    let manifest = join(root, "package.json");
    let types = Action::new(
        format!("{}/types", frontseat_plugin::kinds::COMPILE),
        // Once, from nothing, as Backstage's `tsc:full` runs it for CI:
        // an incremental compilation would leave what the next one reads.
        Command::new([
            "node",
            "node_modules/typescript/bin/tsc",
            "--incremental",
            "false",
        ]),
    )
    .tool("node")
    .inputs([Input::set("repository"), Input::dependencies("{root}")])
    .output(Output::required(TYPES));
    Project {
        root: root.into(),
        kind: "backstage-repository".into(),
        name: package.name.clone(),
        manifest: manifest.clone(),
        input_sets: vec![
            InputSet {
                name: "repository".into(),
                inputs: [
                    "**",
                    "!**/node_modules/**",
                    "!**/dist/**",
                    "!dist-types/**",
                    "!.yarn/**",
                ]
                .map(Input::source)
                .to_vec(),
                ..Default::default()
            },
            InputSet {
                name: "configuration".into(),
                inputs: [
                    "package.json",
                    "tsconfig.json",
                    "yarn.lock",
                    "backstage.json",
                    "app-config*.yaml",
                    ".eslintrc*",
                    ".eslintignore",
                ]
                .map(Input::source)
                .to_vec(),
                ..Default::default()
            },
        ],
        requires: vec![requirement(package.installation(), &manifest, 1)],
        provides: vec![Provision {
            coordinate: Some(repository_of(&package.name)),
            sources: "configuration".into(),
            artifacts: vec![OutputRef {
                action: types.name.clone(),
                output: TYPES.into(),
            }],
        }],
        actions: vec![types],
        ..Default::default()
    }
}

/// A package's project: its build, lint and test, each the CLI's command
/// for its role, run from the install, and handed the install, the
/// repository's declarations and configuration, and each workspace
/// package it links, its sources and what its build left, where each is.
fn package_project(
    root: &str,
    install: &str,
    workspace: &Package,
    package: &Package,
    role: &str,
    text: &str,
) -> Project {
    let manifest = join(root, "package.json");
    // The way up from the package to the install's root, where the CLI is.
    let up = beneath(install, root).map_or(0, |below| below.split('/').count());
    let cli = std::iter::repeat_n("..", up)
        .chain([CLI])
        .collect::<Vec<_>>()
        .join("/");
    let command = |arguments: &[&str]| {
        Command::new(
            ["node", cli.as_str(), "package"]
                .into_iter()
                .chain(arguments.iter().copied()),
        )
    };
    let inputs = || [Input::set("package"), Input::dependencies("{root}")];
    // A backend packs each workspace package it links as that package's
    // own build left it, handed it here; it builds none of them again.
    let build = if role == "backend" {
        command(&["build", "--skip-build-dependencies"])
    } else {
        command(&["build"])
    };
    let compile = Action::new(frontseat_plugin::kinds::COMPILE, build)
        .tool("node")
        .inputs(inputs())
        .output(Output::required(BUILT));
    let lint = Action::new(frontseat_plugin::kinds::LINT, command(&["lint"]))
        .tool("node")
        .inputs(inputs())
        .check();
    // Once, not watching, as continuous integration runs Jest.
    let test = Action::new(
        frontseat_plugin::kinds::TEST,
        command(&["test", "--no-watch", "--ci"]).env("CI", "true"),
    )
    .tool("node")
    .inputs(inputs())
    .test();
    let linked = package
        .dependencies
        .iter()
        .chain(&package.dev_dependencies)
        .filter(|(_, range)| range.starts_with("workspace:"))
        .map(|(name, _)| requirement(package_of(name), &manifest, line_of(text, name)));
    Project {
        root: root.into(),
        kind: "backstage-package".into(),
        name: package.name.clone(),
        manifest: manifest.clone(),
        input_sets: vec![InputSet {
            name: "package".into(),
            inputs: ["**", "!node_modules/**", "!dist/**"]
                .map(Input::source)
                .to_vec(),
            ..Default::default()
        }],
        requires: [
            requirement(workspace.installation(), &manifest, 1),
            requirement(repository_of(&workspace.name), &manifest, 1),
        ]
        .into_iter()
        .chain(linked)
        .collect(),
        provides: vec![Provision {
            coordinate: Some(package_of(&package.name)),
            sources: "package".into(),
            artifacts: vec![OutputRef {
                action: compile.name.clone(),
                output: BUILT.into(),
            }],
        }],
        actions: vec![compile, lint, test],
        ..Default::default()
    }
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    /// A repository as `backstage-cli new` lays one out: the manifests of
    /// the archive's `backstage-hello`, whose lock Yarn 4 wrote.
    fn repository() -> Files {
        Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/packages"))
            .expect("the fixture is read")
    }

    fn projects(files: Files, roots: &[&str]) -> Vec<Project> {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        plugin
            .analyze(&TestHost::new(files), &context, roots)
            .answered()
            .analyses
            .into_iter()
            .flat_map(|analysis| analysis.projects)
            .collect()
    }

    fn arguments(project: &Project, action: &str) -> Vec<String> {
        project
            .actions
            .iter()
            .find(|candidate| candidate.name == action)
            .and_then(|action| action.command.as_ref())
            .map(|command| command.arguments.clone())
            .unwrap_or_else(|| panic!("{} has no {action}", project.name))
    }

    /// The repository is one project at its root, whose one compilation
    /// writes the declarations; each package that names a role is a
    /// project the CLI builds, lints and tests as its role says, run from
    /// the install, requiring the install, the repository and each
    /// workspace package it links, at the line that links it.
    #[test]
    fn a_repository_and_each_of_its_packages_are_built_by_the_cli() {
        let found = projects(
            repository(),
            &[
                "",
                "plugins/hello",
                "plugins/hello-react",
                "plugins/hello-backend",
                "packages/backend",
            ],
        );
        let kinds: Vec<(&str, &str)> = found
            .iter()
            .map(|project| (project.root.as_str(), project.kind.as_str()))
            .collect();
        assert_eq!(
            kinds,
            [
                ("", "backstage-repository"),
                ("plugins/hello", "backstage-package"),
                ("plugins/hello-react", "backstage-package"),
                ("plugins/hello-backend", "backstage-package"),
                ("packages/backend", "backstage-package"),
            ]
        );
        assert_eq!(
            arguments(&found[0], "compile/types"),
            [
                "node",
                "node_modules/typescript/bin/tsc",
                "--incremental",
                "false"
            ]
        );
        let cli = "../../node_modules/@backstage/cli/bin/backstage-cli";
        assert_eq!(
            arguments(&found[1], "compile"),
            ["node", cli, "package", "build"]
        );
        assert_eq!(
            arguments(&found[4], "compile"),
            ["node", cli, "package", "build", "--skip-build-dependencies"]
        );
        assert_eq!(
            arguments(&found[1], "lint"),
            ["node", cli, "package", "lint"]
        );
        assert_eq!(
            arguments(&found[1], "test"),
            ["node", cli, "package", "test", "--no-watch", "--ci"]
        );
        let library = &found[1];
        let kinds: Vec<bool> = ["compile", "lint", "test"]
            .iter()
            .map(|name| {
                let action = library
                    .actions
                    .iter()
                    .find(|action| action.name == *name)
                    .unwrap();
                match *name {
                    "lint" => action.as_check().is_some(),
                    "test" => action.as_test().is_some(),
                    _ => action.as_check().is_none() && action.as_test().is_none(),
                }
            })
            .collect();
        assert_eq!(kinds, [true, true, true]);
        let required: Vec<(String, String, i32)> = found[3]
            .requires
            .iter()
            .map(|requirement| {
                let coordinate = requirement.coordinate.as_ref().unwrap();
                let origin = requirement.origin.as_ref().unwrap();
                (
                    coordinate.ecosystem.clone(),
                    coordinate.name.clone(),
                    origin.line,
                )
            })
            .collect();
        assert_eq!(
            required,
            [
                ("javascript-install".to_string(), "root".to_string(), 1),
                ("backstage-repository".to_string(), "root".to_string(), 1),
                (
                    "backstage-package".to_string(),
                    "@hello/common".to_string(),
                    17
                ),
            ]
        );
        let provided = &found[3].provides[0];
        assert_eq!(
            (
                provided.sources.as_str(),
                provided.artifacts[0].output.as_str()
            ),
            ("package", "dist/")
        );
    }

    /// What the CLI cannot build is refused by name: a role it does not
    /// know, at its line; a package no Yarn install serves; and a Yarn
    /// install whose root does not depend on the CLI. The controls are a
    /// package that names no role, and a Yarn install of no Backstage,
    /// which are no projects of this plugin's.
    #[test]
    fn what_the_cli_cannot_build_is_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let refused = |files: Files, root: &str, reason: &str| {
            plugin
                .analyze(&TestHost::new(files), &context, &[root])
                .refused(reason)
        };
        let unknown = repository().with(
            "plugins/hello/package.json",
            "{\n  \"name\": \"@hello/common\",\n  \"backstage\": {\n    \"role\": \"widget\"\n  }\n}\n",
        );
        assert_eq!(refused(unknown, "plugins/hello", "invalid-role").line, 4);
        let npm = Files::new()
            .with(
                "web/package.json",
                r#"{"name":"web","backstage":{"role":"web-library"}}"#,
            )
            .with("web/package-lock.json", "{}");
        refused(npm, "web", "backstage-install-not-served");
        let without = repository().with(
            "package.json",
            r#"{"name":"root","private":true,"workspaces":["packages/*","plugins/*"]}"#,
        );
        assert_eq!(
            refused(without.clone(), "plugins/hello", "missing-cli").path,
            "package.json"
        );
        assert!(projects(without, &[""]).is_empty());
        let roleless =
            repository().with("plugins/hello/package.json", r#"{"name":"@hello/common"}"#);
        assert!(projects(roleless, &["plugins/hello"]).is_empty());
    }
}
