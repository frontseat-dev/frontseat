//! The packages the Backstage CLI builds: a repository whose Yarn install's
//! root depends on `@backstage/cli`, and each package of it that names a
//! role (`backstage.role`). Each is built, linted and tested by the CLI's
//! own command for its role, as its `package.json` scripts run it, handed
//! only what that command reads, each where it was installed.
//!
//! What a project requires is handed with everything its provider
//! requires in turn, so the repository's project provides every
//! workspace's sources, and requires only the install: a package that
//! takes another's sources takes nothing else with them, and two
//! packages that link each other link sources alone, which no action
//! makes. Only what a package's build leaves (`dist/`) is its own
//! project's to provide, and only a backend's build reads it.

use std::collections::{BTreeMap, BTreeSet};

use frontseat_plugin::diagnostics::{Finding, Located};
use frontseat_plugin::javascript::{install_of, Manager, Workspaces};
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{beneath, join};
use serde::de::IgnoredAny;
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
/// What a backend's build writes, which it ships: the workspace it packed
/// and its manifests alone (`backstage-cli package build`).
const BUNDLES: [&str; 2] = ["dist/bundle.tar.gz", "dist/skeleton.tar.gz"];
/// The action that compiles the repository.
const COMPILE_TYPES: &str = "compile/types";
/// The actions this plugin declares, each of which is handed what it
/// reads where its tool reads it, and so arranged with nothing written.
pub(super) const ARRANGED: [&str; 4] = [
    frontseat_plugin::kinds::COMPILE,
    frontseat_plugin::kinds::LINT,
    frontseat_plugin::kinds::TEST,
    COMPILE_TYPES,
];
/// The roles the pinned CLI knows (`@backstage/cli-node` 0.3.4,
/// `dist/roles/PackageRoles.cjs.js`).
const ROLES: [&str; 12] = [
    "frontend",
    "backend",
    "cli",
    "cli-module",
    "web-library",
    "node-library",
    "common-library",
    "frontend-plugin",
    "frontend-plugin-module",
    "frontend-dynamic-container",
    "backend-plugin",
    "backend-plugin-module",
];
/// The roles whose build bundles its declarations from `dist-types/`: the
/// libraries and plugins, which the CLI builds for publishing.
const TYPED: [&str; 7] = [
    "web-library",
    "node-library",
    "common-library",
    "frontend-plugin",
    "frontend-plugin-module",
    "backend-plugin",
    "backend-plugin-module",
];
/// The roles whose build bundles the sources of every workspace package
/// it links: an app.
const BUNDLED: [&str; 2] = ["frontend", "frontend-dynamic-container"];

const HELP: &str = "Build Backstage packages from a Yarn install whose root depends on @backstage/cli, as `backstage-cli new` lays one out, each package naming one of the CLI's roles.";

/// A `package.json` as this plugin reads one: what says whether it is a
/// Backstage repository or package, and nothing it requires. A manifest
/// that is neither is no concern of this plugin's, whatever else it says.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Manifest {
    name: Option<String>,
    backstage: Option<Backstage>,
    #[serde(default)]
    dependencies: BTreeMap<String, IgnoredAny>,
    #[serde(default)]
    dev_dependencies: BTreeMap<String, IgnoredAny>,
    workspaces: Option<Workspaces>,
}

#[derive(Deserialize)]
struct Backstage {
    role: Option<String>,
}

impl Manifest {
    fn depends_on(&self, name: &str) -> bool {
        self.dependencies.contains_key(name) || self.dev_dependencies.contains_key(name)
    }

    fn links(&self) -> impl Iterator<Item = &String> {
        self.dependencies.keys().chain(self.dev_dependencies.keys())
    }
}

/// A workspace of the repository: where it is, and what it links of the
/// others, by name.
struct Member {
    directory: String,
    links: BTreeSet<String>,
}

/// The repository a package is of: its root, its name, and its
/// workspaces by name.
struct Repo {
    root: String,
    name: String,
    members: BTreeMap<String, Member>,
}

fn coordinate(ecosystem: &str, name: &str) -> Coordinate {
    Coordinate {
        ecosystem: ecosystem.into(),
        name: name.into(),
    }
}

fn installation(name: &str) -> Coordinate {
    coordinate("javascript-install", name)
}
fn configuration(name: &str) -> Coordinate {
    coordinate("backstage-configuration", name)
}
fn types(name: &str) -> Coordinate {
    coordinate("backstage-types", name)
}
fn sources(name: &str) -> Coordinate {
    coordinate("backstage-sources", name)
}
fn built(name: &str) -> Coordinate {
    coordinate("backstage-built", name)
}
/// The line of a manifest's text that names `name` as a key of its
/// dependencies, from 1: where a requirement is said; the first line
/// where none does.
fn line_of(text: &str, name: &str) -> i32 {
    let key = format!("\"{name}\"");
    let mut within = false;
    for (index, line) in text.lines().enumerate() {
        let trimmed = line.trim_start();
        if trimmed.starts_with("\"dependencies\"") || trimmed.starts_with("\"devDependencies\"") {
            within = true;
        } else if within && trimmed.starts_with('}') {
            within = false;
        } else if within && trimmed.starts_with(&key) {
            return i32::try_from(index + 1).unwrap_or(1);
        }
    }
    1
}

/// The line of a manifest's text that says its role, from 1.
fn role_line(text: &str) -> i32 {
    text.lines()
        .position(|line| line.contains("\"role\""))
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

fn read(repo: &Repository, path: &str) -> Result<Option<(Manifest, String)>, Error> {
    Ok(repo
        .json::<Manifest>(path)?
        .map(|document| (document.value, document.text)))
}

/// What the Backstage CLI builds at `root`: the repository's project at
/// its Yarn install's root, or a package's; none where `root` holds no
/// `package.json`, or one that is neither.
pub(super) fn projects(repo: &Repository, root: &str) -> Result<Vec<Project>, Error> {
    let manifest = join(root, "package.json");
    // Most roots are a catalog file's: where no manifest matched, none is
    // read, and the host is not asked again on every warm request for a
    // file that is not there.
    if !repo.matched(&manifest) {
        return Ok(Vec::new());
    }
    let Some((package, text)) = read(repo, &manifest)? else {
        return Ok(Vec::new());
    };
    let role = package
        .backstage
        .as_ref()
        .and_then(|backstage| backstage.role.clone());
    let at_root = package.depends_on(CLI_PACKAGE);
    if role.is_none() && !at_root {
        return Ok(Vec::new());
    }
    let install = install_of(repo, root)?.filter(|install| install.manager == Manager::Yarn);
    if at_root && install.as_ref().is_some_and(|install| install.root == root) {
        let repository = repository_of(repo, root, package)?;
        return Ok(vec![repository_project(&repository)]);
    }
    let Some(role) = role else {
        return Ok(Vec::new());
    };
    if !ROLES.contains(&role.as_str()) {
        return Err(Error::invalid(
            "invalid-role",
            &manifest,
            "backstage.role names no role the pinned Backstage CLI knows",
            format!("Name one of {}.", ROLES.join(", ")),
        )
        .at_line(&manifest, role_line(&text)));
    }
    let Some(install) = install else {
        return Err(Error::not_served(
            "backstage-install",
            "a Backstage package is built from a Yarn install, and no Yarn install serves this one",
        )
        .at(&manifest)
        .help(HELP));
    };
    let root_manifest = join(&install.root, "package.json");
    let Some((workspace, _)) =
        read(repo, &root_manifest)?.filter(|(workspace, _)| workspace.depends_on(CLI_PACKAGE))
    else {
        return Err(Error::invalid(
            "missing-cli",
            &root_manifest,
            "the Yarn install that serves this Backstage package does not depend on @backstage/cli at its root",
            HELP,
        ));
    };
    let repository = repository_of(repo, &install.root, workspace)?;
    Ok(vec![package_project(
        root,
        &repository,
        &package,
        &role,
        &text,
    )])
}

/// The repository whose root is `root`, read with each of its workspaces.
fn repository_of(repo: &Repository, root: &str, package: Manifest) -> Result<Repo, Error> {
    let manifest = join(root, "package.json");
    let Some(name) = package.name.filter(|name| !name.trim().is_empty()) else {
        return Err(Error::invalid(
            "invalid-manifest",
            &manifest,
            "the root of a Backstage repository names it: its projects are named for it",
            "Name the root's package.json, as `backstage-cli new` does.",
        ));
    };
    let mut members = BTreeMap::new();
    let patterns = package
        .workspaces
        .as_ref()
        .map(Workspaces::manifests)
        .unwrap_or_default();
    if !patterns.is_empty() {
        let patterns: Vec<&str> = patterns
            .iter()
            .map(String::as_str)
            .chain(["!**/node_modules/**"])
            .collect();
        for path in repo.glob(root, &patterns)? {
            let Some(directory) =
                beneath(root, &path).and_then(|file| file.strip_suffix("/package.json"))
            else {
                continue;
            };
            let Some((member, _)) = read(repo, &path)? else {
                continue;
            };
            let Some(name) = member.name.clone() else {
                continue;
            };
            let links = member.links().cloned().collect();
            members.insert(
                name,
                Member {
                    directory: directory.into(),
                    links,
                },
            );
        }
    }
    Ok(Repo {
        root: root.into(),
        name,
        members,
    })
}

impl Repo {
    /// The workspaces a package links, and those they link in turn, each
    /// with the one it links directly through it: Yarn links a workspace
    /// by any range its version meets, so a link is by name.
    fn linked(&self, package: &Manifest) -> BTreeMap<String, String> {
        let own = package.name.as_deref().unwrap_or_default();
        let mut reached: BTreeMap<String, String> = BTreeMap::new();
        let mut next: Vec<(String, String)> = package
            .links()
            .filter(|name| self.members.contains_key(*name) && name.as_str() != own)
            .map(|name| (name.clone(), name.clone()))
            .collect();
        while let Some((name, through)) = next.pop() {
            if reached.contains_key(&name) || name == own {
                continue;
            }
            if let Some(member) = self.members.get(&name) {
                next.extend(
                    member
                        .links
                        .iter()
                        .filter(|linked| self.members.contains_key(*linked))
                        .map(|linked| (linked.clone(), through.clone())),
                );
            }
            reached.insert(name, through);
        }
        reached
    }
}

/// The repository's project: the one compilation over every package,
/// as Backstage's `tsc` script runs it, writing the declarations each
/// package's build bundles its own from; what every package reads at its
/// root; and each workspace's sources.
fn repository_project(repository: &Repo) -> Project {
    let manifest = join(&repository.root, "package.json");
    let compile = Action::new(
        COMPILE_TYPES,
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
    .inputs([
        Input::set("repository"),
        Input::dependency(installation(&repository.name), "{root}"),
    ])
    .output(Output::required(TYPES));
    let mut input_sets = vec![
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
    ];
    let mut provides = vec![
        Provision {
            coordinate: Some(configuration(&repository.name)),
            sources: "configuration".into(),
            ..Default::default()
        },
        Provision {
            coordinate: Some(types(&repository.name)),
            artifacts: vec![OutputRef {
                action: COMPILE_TYPES.into(),
                output: TYPES.into(),
            }],
            ..Default::default()
        },
    ];
    for (index, (name, member)) in repository.members.iter().enumerate() {
        let set = format!("workspace-{index}");
        input_sets.push(InputSet {
            name: set.clone(),
            inputs: [
                format!("{}/**", member.directory),
                format!("!{}/node_modules/**", member.directory),
                format!("!{}/dist/**", member.directory),
            ]
            .map(Input::source)
            .to_vec(),
            ..Default::default()
        });
        provides.push(Provision {
            coordinate: Some(sources(name)),
            sources: set,
            ..Default::default()
        });
    }
    Project {
        root: repository.root.clone(),
        kind: "backstage-repository".into(),
        name: repository.name.clone(),
        manifest: manifest.clone(),
        input_sets,
        requires: vec![requirement(installation(&repository.name), &manifest, 1)],
        provides,
        actions: vec![compile],
        ..Default::default()
    }
}

/// A package's project: its build, lint and test, each the CLI's command
/// for its role, run from the install, and each handed only what it reads:
///
/// - every one: the install and the repository's configuration;
/// - a library's or a plugin's build: the repository's declarations, its
///   imports left to whoever depends on it;
/// - an app's build: the sources of what it links, which it bundles;
/// - a backend's build: the sources of what it links and what their
///   builds left, which it packs without building them again;
/// - a test: the sources of what it links, which its imports reach.
fn package_project(
    root: &str,
    repository: &Repo,
    package: &Manifest,
    role: &str,
    text: &str,
) -> Project {
    let manifest = join(root, "package.json");
    let name = package.name.clone().unwrap_or_default();
    // The way up from the package to the install's root, where the CLI is.
    let up = beneath(&repository.root, root).map_or(0, |below| below.split('/').count());
    let cli = std::iter::repeat_n("..", up)
        .chain([CLI])
        .collect::<Vec<_>>()
        .join("/");
    // SWC (1.16 and later) places its native addon in a cache, in the
    // action's home or else its TMPDIR, and refuses one with an ancestor
    // it cannot tell is root's or the user's own: a worker's /tmp is the
    // host's root's, which a rootless sandbox shows as a mapped nobody, so
    // no cache passes, and SWC does not load. SWC's own opt-out skips that
    // ancestor check alone; its paths, its cache entries' own ownership and
    // an executable mount are still checked. Both places are the action's
    // own, made anew for it, in a sandbox that runs one action at a time.
    let command = |arguments: &[&str]| {
        Command::new(
            ["node", cli.as_str(), "package"]
                .into_iter()
                .chain(arguments.iter().copied()),
        )
        .env("SWC_NATIVE_BINDING_CACHE_SKIP_SECURITY_CHECK", "1")
    };
    let linked = repository.linked(package);
    let placed = |coordinate: Coordinate| Input::dependency(coordinate, "{root}");
    let every = || {
        [
            Input::set("package"),
            placed(installation(&repository.name)),
            placed(configuration(&repository.name)),
        ]
    };
    let linked_sources = || linked.keys().map(|name| placed(sources(name)));
    let backend = role == "backend";
    let typed = TYPED.contains(&role);
    let bundled = BUNDLED.contains(&role) || backend;
    let mut compile_inputs: Vec<Input> = every().into_iter().collect();
    if typed {
        compile_inputs.push(placed(types(&repository.name)));
    }
    if bundled {
        compile_inputs.extend(linked_sources());
    }
    if backend {
        compile_inputs.extend(linked.keys().map(|name| placed(built(name))));
    }
    // A backend packs each workspace package it links as that package's
    // own build left it, handed it here; it builds none of them again.
    let build = if backend {
        command(&["build", "--skip-build-dependencies"])
    } else {
        command(&["build"])
    };
    // What a backend's build leaves is what it ships: the workspace it
    // packed, and the manifests alone, which its image installs from
    // (Backstage's own recipe), on a machine of the image's platform, so
    // that the native modules it builds are that platform's.
    let built_files: Vec<&str> = if backend {
        BUNDLES.to_vec()
    } else {
        vec![BUILT]
    };
    let mut compile = built_files.iter().fold(
        Action::new(frontseat_plugin::kinds::COMPILE, build)
            .tool("node")
            .inputs(compile_inputs),
        |action, output| action.output(Output::required(*output)),
    );
    if backend {
        compile = compile.package();
    }
    // Each finding on a line of its own, which `report` reads.
    let lint = Action::new(
        frontseat_plugin::kinds::LINT,
        command(&["lint", "--format", "unix"]),
    )
    .tool("node")
    .inputs(every())
    .check();
    // Once, not watching, as continuous integration runs Jest.
    let test = Action::new(
        frontseat_plugin::kinds::TEST,
        command(&["test", "--no-watch", "--ci"]).env("CI", "true"),
    )
    .tool("node")
    .inputs(every().into_iter().chain(linked_sources()))
    .test();
    let mut requires = vec![
        requirement(installation(&repository.name), &manifest, 1),
        requirement(configuration(&repository.name), &manifest, 1),
    ];
    if typed {
        requires.push(requirement(types(&repository.name), &manifest, 1));
    }
    for (linked, through) in &linked {
        let line = line_of(text, through);
        requires.push(requirement(sources(linked), &manifest, line));
        if backend {
            requires.push(requirement(built(linked), &manifest, line));
        }
    }
    Project {
        root: root.into(),
        kind: "backstage-package".into(),
        name: name.clone(),
        manifest,
        input_sets: vec![InputSet {
            name: "package".into(),
            inputs: ["**", "!node_modules/**", "!dist/**"]
                .map(Input::source)
                .to_vec(),
            ..Default::default()
        }],
        requires,
        provides: vec![Provision {
            coordinate: Some(built(&name)),
            artifacts: built_files
                .iter()
                .map(|output| OutputRef {
                    action: frontseat_plugin::kinds::COMPILE.into(),
                    output: (*output).into(),
                })
                .collect(),
            ..Default::default()
        }],
        actions: vec![compile, lint, test],
        ..Default::default()
    }
}

/// What a package's lint found, each finding as ESLint's `unix` format
/// prints it, `file:line:column: message [Severity/rule]`, its file named
/// whole; a file not beneath the package's directory is no finding of it.
pub(super) fn linted(root: &str, printed: &Printed) -> Vec<Diagnostic> {
    const LINTED: Finding = Finding {
        rule_id: "backstage/lint",
        help: "Fix what ESLint reports, by the rule it names, as `backstage-cli package lint` runs it.",
    };
    let within = format!("/{root}/");
    String::from_utf8_lossy(&printed.stdout)
        .lines()
        .filter_map(Located::read)
        .filter_map(|at| {
            let file = at.path.rsplit_once(within.as_str()).map(|(_, file)| file)?;
            LINTED.at(root, file, at.line, at.message)
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin::wire::input::Kind;
    use frontseat_plugin_test::{Files, Read, TestHost, TestPlugin};

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
            .projects()
            .into_iter()
            .collect()
    }

    fn project(files: Files, root: &str) -> Project {
        projects(files, &[root])
            .pop()
            .unwrap_or_else(|| panic!("no project at {root}"))
    }

    fn action<'a>(project: &'a Project, name: &str) -> &'a Action {
        project
            .actions
            .iter()
            .find(|action| action.name == name)
            .unwrap_or_else(|| panic!("{} has no {name}", project.name))
    }

    fn arguments(project: &Project, name: &str) -> Vec<String> {
        action(project, name).command.clone().unwrap().arguments
    }

    /// What an action is handed of other projects, each as
    /// `ecosystem:name`, and the input sets it reads.
    fn handed(project: &Project, name: &str) -> Vec<String> {
        action(project, name)
            .inputs
            .iter()
            .map(|input| match input.kind.as_ref() {
                Some(Kind::Dependency(dependency)) => match dependency.selection.as_ref() {
                    Some(frontseat_plugin::wire::dependency_input::Selection::Coordinate(
                        coordinate,
                    )) => {
                        assert_eq!(dependency.into, "{root}");
                        format!("{}:{}", coordinate.ecosystem, coordinate.name)
                    }
                    other => panic!("{other:?}"),
                },
                Some(Kind::InputSet(set)) => format!("set {set}"),
                other => panic!("{other:?}"),
            })
            .collect()
    }

    fn required(project: &Project) -> Vec<(String, i32)> {
        project
            .requires
            .iter()
            .map(|requirement| {
                let coordinate = requirement.coordinate.as_ref().unwrap();
                (
                    format!("{}:{}", coordinate.ecosystem, coordinate.name),
                    requirement.origin.as_ref().unwrap().line,
                )
            })
            .collect()
    }

    /// A root its catalog file made one, with no manifest beside it, is
    /// analyzed with no manifest read: the host said what matched there,
    /// and a file not read is not asked of the host again on every warm
    /// request. The control: a root where the manifest matched is read,
    /// and is a package's project.
    #[test]
    fn a_manifest_that_did_not_match_is_not_read() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let files = repository().with(
            "catalog/catalog-info.yaml",
            "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: catalog}\nspec: {type: service, lifecycle: production, owner: team}\n",
        );
        let host = TestHost::new(files);
        let found = plugin.analyze(&host, &context, &["catalog"]).projects();
        assert!(found.is_empty(), "{found:?}");
        assert!(
            !read_of(&host, "catalog/package.json"),
            "{:?}",
            host.reads()
        );
        let host = TestHost::new(repository());
        let found = plugin
            .analyze(&host, &context, &["plugins/hello"])
            .projects();
        assert_eq!(found.len(), 1, "{found:?}");
        assert!(read_of(&host, "plugins/hello/package.json"));
    }

    /// A root a package's manifest made, with no catalog file beside it,
    /// is analysed with no catalog file read; the control: a root whose
    /// catalog file matched is read, and its entity found.
    #[test]
    fn a_catalog_file_that_did_not_match_is_not_read() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let host = TestHost::new(repository());
        let analysis = plugin
            .analyze(&host, &context, &["plugins/hello"])
            .answered();
        assert!(analysis.analyses.iter().all(|a| a.entities.is_empty()));
        assert!(
            !read_of(&host, "plugins/hello/catalog-info.yaml"),
            "{:?}",
            host.reads()
        );
        let files = repository().with(
            "catalog/catalog-info.yaml",
            "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: catalog}\nspec: {type: service, lifecycle: production, owner: team}\n",
        );
        let host = TestHost::new(files);
        let analysis = plugin.analyze(&host, &context, &["catalog"]).answered();
        assert_eq!(analysis.analyses[0].entities.len(), 1);
        assert!(read_of(&host, "catalog/catalog-info.yaml"));
    }

    fn read_of(host: &TestHost, path: &str) -> bool {
        host.reads()
            .iter()
            .any(|read| matches!(read, Read::File { path: p, .. } if p == path))
    }

    /// What a package is handed is placed where the CLI reads it, at the
    /// install's root ({root}), so nothing is written for it: arranging
    /// what was handed answers no file, for a package's actions and the
    /// repository's compilation alike.
    #[test]
    fn what_is_handed_is_read_where_it_is_placed_and_nothing_is_written() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let host = TestHost::new(repository());
        let handed = |name: &str, into: &str| Handoff {
            coordinate: Some(Coordinate {
                ecosystem: "javascript-install".into(),
                name: name.into(),
            }),
            into: into.into(),
        };
        for (root, actions, into) in [
            (
                "plugins/hello",
                ["compile", "lint", "test"].as_slice(),
                "../..",
            ),
            ("", ["compile/types"].as_slice(), "."),
        ] {
            let files = plugin
                .arrange(
                    &host,
                    &context,
                    root,
                    actions,
                    &[handed("backstage-hello", into)],
                )
                .answered()
                .files;
            assert!(files.is_empty(), "{root}: wrote {files:?}");
        }
        // The control: an action it does not declare is refused.
        plugin
            .arrange(&host, &context, "plugins/hello", &["invented"], &[])
            .refused("unsupported-arrangement");
    }

    /// The repository is one project at its root, whose one compilation
    /// writes the declarations, and which provides its configuration, its
    /// declarations and each workspace's sources; each package that names
    /// a role is a project the CLI builds, lints and tests as its role
    /// says, run from the install.
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
        let repository = &found[0];
        assert_eq!(
            arguments(repository, "compile/types"),
            [
                "node",
                "node_modules/typescript/bin/tsc",
                "--incremental",
                "false"
            ]
        );
        assert_eq!(
            handed(repository, "compile/types"),
            ["set repository", "javascript-install:root"]
        );
        let provided: Vec<String> = repository
            .provides
            .iter()
            .map(|provision| {
                let coordinate = provision.coordinate.as_ref().unwrap();
                format!("{}:{}", coordinate.ecosystem, coordinate.name)
            })
            .collect();
        assert_eq!(
            provided,
            [
                "backstage-configuration:root",
                "backstage-types:root",
                "backstage-sources:@hello/backend",
                "backstage-sources:@hello/common",
                "backstage-sources:@hello/react",
                "backstage-sources:backend",
            ]
        );
        let configuration = repository
            .input_sets
            .iter()
            .find(|set| set.name == "configuration")
            .unwrap();
        let patterns: Vec<&str> = configuration
            .inputs
            .iter()
            .filter_map(|input| match input.kind.as_ref() {
                Some(Kind::Source(pattern)) => Some(pattern.as_str()),
                _ => None,
            })
            .collect();
        assert_eq!(
            patterns,
            [
                "package.json",
                "tsconfig.json",
                "yarn.lock",
                "backstage.json",
                "app-config*.yaml",
                ".eslintrc*",
                ".eslintignore"
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
            ["node", cli, "package", "lint", "--format", "unix"]
        );
        assert_eq!(
            arguments(&found[1], "test"),
            ["node", cli, "package", "test", "--no-watch", "--ci"]
        );
        let test = action(&found[1], "test");
        assert_eq!(
            test.command
                .as_ref()
                .unwrap()
                .environment
                .get("CI")
                .map(String::as_str),
            Some("true")
        );
        // Each of the CLI's commands loads SWC where its addon may be
        // placed in a sandbox's own directories.
        for name in ["compile", "lint", "test"] {
            assert_eq!(
                action(&found[1], name)
                    .command
                    .as_ref()
                    .unwrap()
                    .environment
                    .get("SWC_NATIVE_BINDING_CACHE_SKIP_SECURITY_CHECK")
                    .map(String::as_str),
                Some("1"),
                "{name}"
            );
        }
        assert!(test.as_test().is_some() && action(&found[1], "lint").as_check().is_some());
        let provided = &found[1].provides[0];
        assert_eq!(
            (
                provided.coordinate.as_ref().unwrap().name.as_str(),
                provided.artifacts[0].output.as_str()
            ),
            ("@hello/common", "dist/")
        );
    }

    /// Each action is handed only what its command reads: a library's
    /// build the declarations and none of what it links; a backend's build
    /// the sources and builds of everything it links, at any depth, and
    /// no declarations; lint nothing linked; a test the sources of what it
    /// links, and no declarations, so that a change elsewhere runs it
    /// again only where it reaches what it links.
    #[test]
    fn each_action_is_handed_only_what_its_command_reads() {
        let every = [
            "set package",
            "javascript-install:root",
            "backstage-configuration:root",
        ];
        let with = |more: &[&str]| -> Vec<String> {
            every
                .iter()
                .chain(more)
                .map(|input| input.to_string())
                .collect()
        };
        let library = project(repository(), "plugins/hello");
        assert_eq!(handed(&library, "compile"), with(&["backstage-types:root"]));
        assert_eq!(handed(&library, "lint"), with(&[]));
        assert_eq!(handed(&library, "test"), with(&[]));
        let plugin = project(repository(), "plugins/hello-backend");
        assert_eq!(handed(&plugin, "compile"), with(&["backstage-types:root"]));
        assert_eq!(
            handed(&plugin, "test"),
            with(&["backstage-sources:@hello/common"])
        );
        let backend = project(repository(), "packages/backend");
        assert_eq!(
            handed(&backend, "compile"),
            with(&[
                "backstage-sources:@hello/backend",
                "backstage-sources:@hello/common",
                "backstage-built:@hello/backend",
                "backstage-built:@hello/common",
            ])
        );
        // What it reaches through the plugin is said at the line that
        // links the plugin.
        assert_eq!(
            required(&backend),
            [
                ("javascript-install:root".to_string(), 1),
                ("backstage-configuration:root".to_string(), 1),
                ("backstage-sources:@hello/backend".to_string(), 12),
                ("backstage-built:@hello/backend".to_string(), 12),
                ("backstage-sources:@hello/common".to_string(), 12),
                ("backstage-built:@hello/common".to_string(), 12),
            ]
        );
    }

    /// Yarn links a workspace by any range its version meets, so a
    /// workspace named by a plain range is linked; and two packages that
    /// link each other, a dependency cycle Yarn allows, link sources
    /// alone, and no build of one is handed to the other.
    #[test]
    fn a_workspace_is_linked_by_its_name_and_a_cycle_links_sources_alone() {
        let plain = repository().with(
            "plugins/hello-backend/package.json",
            "{\n  \"name\": \"@hello/backend\",\n  \"backstage\": {\"role\": \"backend-plugin\", \"pluginPackages\": [\"@hello/common\"]},\n  \"dependencies\": {\n    \"@hello/common\": \"^0.1.0\"\n  }\n}\n",
        );
        let plugin = project(plain, "plugins/hello-backend");
        assert!(
            required(&plugin).contains(&("backstage-sources:@hello/common".to_string(), 5)),
            "{:?}",
            required(&plugin)
        );
        let cycle = repository().with(
            "plugins/hello/package.json",
            r#"{"name":"@hello/common","backstage":{"role":"common-library"},"devDependencies":{"@hello/backend":"workspace:^"}}"#,
        );
        for root in ["plugins/hello", "plugins/hello-backend"] {
            let package = project(cycle.clone(), root);
            assert!(
                required(&package)
                    .iter()
                    .all(|(coordinate, _)| !coordinate.starts_with("backstage-built:")),
                "{root}: {:?}",
                required(&package)
            );
        }
    }

    /// A `package.json` that says nothing of Backstage is no concern of
    /// this plugin's, however little it says, and the catalog file beside
    /// it is read; the controls are the roles the pinned CLI knows, which
    /// are built, and one it does not, which is refused at its line.
    #[test]
    fn a_package_json_of_no_backstage_is_none_of_its_concern() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let catalog = "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: lib}\nspec: {type: library, lifecycle: production, owner: team}\n";
        for manifest in [
            r#"{"type":"module"}"#,
            r#"{"private":true,"dependencies":{"x":{"y":1}}}"#,
            r#"{"name":"lib"}"#,
        ] {
            let files = Files::new()
                .with("lib/package.json", manifest)
                .with("lib/catalog-info.yaml", catalog);
            let answered = plugin
                .analyze(&TestHost::new(files), &context, &["lib"])
                .answered();
            assert_eq!(answered.analyses[0].entities.len(), 1, "{manifest}");
            assert!(answered.analyses[0].projects.is_empty(), "{manifest}");
        }
        for role in ["cli-module", "frontend-dynamic-container", "frontend"] {
            let files = repository().with(
                "plugins/hello/package.json",
                format!(r#"{{"name":"@hello/common","backstage":{{"role":"{role}"}}}}"#),
            );
            project(files, "plugins/hello");
        }
        let unknown = repository().with(
            "plugins/hello/package.json",
            "{\n  \"name\": \"@hello/common\",\n  \"backstage\": {\n    \"role\": \"widget\"\n  }\n}\n",
        );
        let refused = plugin
            .analyze(&TestHost::new(unknown), &context, &["plugins/hello"])
            .refused("invalid-role");
        assert_eq!(refused.line, 4);
    }

    /// What the CLI cannot build is refused by name: a package no Yarn
    /// install serves; a Yarn install whose root does not depend on the
    /// CLI; a root of no name. The controls are a package that names no
    /// role, and a Yarn install of no Backstage, which are no projects.
    #[test]
    fn what_the_cli_cannot_build_is_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let refused = |files: Files, root: &str, reason: &str| {
            plugin
                .analyze(&TestHost::new(files), &context, &[root])
                .refused(reason)
        };
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
        let nameless = repository().with(
            "package.json",
            r#"{"private":true,"workspaces":["packages/*","plugins/*"],"devDependencies":{"@backstage/cli":"^0.36.5"}}"#,
        );
        refused(nameless, "", "invalid-manifest");
        let roleless =
            repository().with("plugins/hello/package.json", r#"{"name":"@hello/common"}"#);
        assert!(projects(roleless, &["plugins/hello"]).is_empty());
    }

    /// What ESLint found is read from its `unix` lines as findings of the
    /// package's files, at their lines, its own summary and a file beyond
    /// the package left out.
    #[test]
    fn what_lint_finds_is_reported_at_its_file_and_line() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let printed = Printed::new(
            1,
            "/b/root/x/plugins/hello-react/src/Greeting.tsx:1:8: React default imports are deprecated. [Warning/no-restricted-syntax]\n/b/root/x/plugins/other/src/a.ts:3:1: elsewhere [Error/x]\n\n2 problems\n",
            "",
        );
        let found = plugin
            .report(
                &TestHost::new(repository()),
                &context,
                "plugins/hello-react",
                "lint",
                &printed,
            )
            .answered()
            .diagnostics;
        let at: Vec<(String, i32, &str)> = found
            .iter()
            .map(|finding| {
                let location = finding.source_location.as_ref().unwrap();
                (
                    location.path.clone(),
                    location.line,
                    finding.rule_id.as_str(),
                )
            })
            .collect();
        assert_eq!(
            at,
            [(
                "plugins/hello-react/src/Greeting.tsx".to_string(),
                1,
                "backstage/lint"
            )]
        );
    }

    /// A backend's build is what it ships, platform-neutral: the workspace
    /// it packed and its manifests, each a file a release lists, which its
    /// image installs from on a machine of the image's platform; a
    /// library's build is no package, and leaves its `dist/`.
    #[test]
    fn a_backends_build_is_its_package_of_what_it_packed() {
        let backend = project(repository(), "packages/backend");
        let compile = action(&backend, "compile");
        assert!(compile.is_package());
        assert_eq!(
            compile.outputs,
            [
                Output::required("dist/bundle.tar.gz"),
                Output::required("dist/skeleton.tar.gz")
            ]
        );
        assert!(
            backend
                .actions
                .iter()
                .filter(|action| action.is_package())
                .count()
                == 1
        );
        let library = project(repository(), "plugins/hello");
        let compile = action(&library, "compile");
        assert!(!compile.is_package());
        assert_eq!(compile.outputs[0].path, "dist/");
    }
}
