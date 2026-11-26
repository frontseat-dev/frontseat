//! What a Configuration's project does: its package, and a check of its
//! examples against its own definitions.
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{beneath, literal, NOT_FIXTURES};
use frontseat_plugin::{kinds, Repository};

use crate::manifest::{Configuration, FILE};

/// The tool every action runs: the Crossplane command line the
/// repository pins.
pub(crate) const TOOL: &str = "crossplane";
/// The check of the examples, by the name its findings are asked by.
pub(crate) const VALIDATE: &str = "validate";
/// Where `crossplane xpkg build` reads a package's examples from, its
/// own default, relative to the package's root.
pub(crate) const EXAMPLES: &str = "examples";
/// What a Backstage catalog file is named: a Component's, beside the
/// package, and no object of it, which `xpkg build` would refuse.
const CATALOG: &str = "catalog-info.yaml";
/// What a repository lists its plugins in, at its root.
const LISTED: &str = "frontseat.yaml";
/// What `xpkg build` and `resource validate` read as YAML.
const YAML: [&str; 2] = ["**/*.yaml", "**/*.yml"];

/// What no action of the package reads: a fixture, a catalog file, and
/// a package nested beneath this one, which is a project of its own.
pub(crate) fn excluded(repo: &Repository, root: &str) -> Result<Vec<String>, Error> {
    let mut excluded = vec![NOT_FIXTURES.to_string(), format!("!**/{CATALOG}")];
    // A Configuration at the repository's root is beside the file that
    // lists its plugins, which is no object of it.
    if root.is_empty() {
        excluded.push(format!("!{LISTED}"));
    }
    for directory in repo.nested(root, FILE, &[])? {
        excluded.push(format!("!{}/**", literal(&directory)));
    }
    Ok(excluded)
}

/// The actions of a Configuration at a root.
pub(crate) fn of(
    repo: &Repository,
    root: &str,
    configuration: &Configuration,
) -> Result<Vec<Action>, Error> {
    let excluded = excluded(repo, root)?;
    let mut actions = vec![package(configuration, &excluded)];
    if let Some(validate) = validate(repo, root, &excluded)? {
        actions.push(validate);
    }
    Ok(actions)
}

/// The package: `crossplane xpkg build` over every YAML file of the
/// Configuration, its examples among them, into a file named for it at
/// its root, where Crossplane writes one too (named with its digest
/// there, which is not known before it is built). It reads nothing but
/// what it is given and reaches nothing, and writes the same bytes for
/// the same objects, whenever and wherever it is built. What it leaves
/// is what the project ships.
fn package(configuration: &Configuration, excluded: &[String]) -> Action {
    let file = format!("{}.xpkg", configuration.name);
    let command = Command::new([
        TOOL,
        "xpkg",
        "build",
        "--package-root",
        ".",
        "--examples-root",
        &format!("./{EXAMPLES}"),
        "--ignore",
        CATALOG,
        "--package-file",
        &file,
    ]);
    Action::new(kinds::PACKAGE, command)
        .tool(TOOL)
        .inputs(sources(YAML.iter().copied(), excluded))
        .output(Output::required(file))
        .package()
}

/// The check of the examples: `crossplane resource validate` of every
/// example against the definitions of the Configuration, the files that
/// are no example and no metadata. It is given no schema of Crossplane's
/// own (`--crossplane-image=`), which it would download, and reads the
/// metadata of no package, whose dependencies it would download: it
/// reaches nothing. An example of a kind the Configuration does not
/// define is said to have no schema, and passes. There is none where the
/// Configuration has no example, or nothing to hold one to.
fn validate(repo: &Repository, root: &str, excluded: &[String]) -> Result<Option<Action>, Error> {
    // xpkg leaves out of the package every path that holds its examples'
    // root (`SkipContains`, crossplane-runtime v2.4.0 pkg/xpkg/build.go),
    // so "examples-old.yaml" and "examples2/" are no definitions either.
    let not_definitions = [
        format!("!{FILE}"),
        format!("!{EXAMPLES}*"),
        format!("!{EXAMPLES}*/**"),
    ];
    let patterns: Vec<String> = YAML
        .iter()
        .map(|pattern| pattern.to_string())
        .chain(not_definitions)
        .chain(excluded.iter().cloned())
        .collect();
    let mut definitions: Vec<String> = glob(repo, root, &patterns)?;
    definitions.sort();
    let examples: Vec<String> = YAML
        .iter()
        .map(|pattern| format!("{EXAMPLES}/{pattern}"))
        .chain(excluded.iter().cloned())
        .collect();
    if definitions.is_empty() || glob(repo, root, &examples)?.is_empty() {
        return Ok(None);
    }
    // The tool reads its definitions as one argument, the files apart by
    // commas, each written from the root so that none begins as a flag.
    if let Some(named) = definitions.iter().find(|file| file.contains(',')) {
        return Err(Error::invalid(
            "invalid-file-name",
            &frontseat_plugin::repository::join(root, named),
            "its name holds a comma, which `crossplane resource validate` reads as two files",
            "Rename the file without a comma.",
        ));
    }
    let listed = definitions
        .iter()
        .map(|file| format!("./{file}"))
        .collect::<Vec<_>>()
        .join(",");
    let command = Command::new([
        TOOL,
        "resource",
        "validate",
        "--crossplane-image=",
        "--output",
        "json",
        "--",
        &listed,
        &format!("./{EXAMPLES}"),
    ]);
    let inputs = definitions
        .iter()
        .map(|file| Input::source(literal(file)))
        .chain(sources(examples.iter().map(String::as_str), &[]))
        .collect::<Vec<_>>();
    Ok(Some(
        Action::new(VALIDATE, command)
            .tool(TOOL)
            .inputs(inputs)
            .check(),
    ))
}

/// The files beneath a root the patterns select, relative to it.
fn glob(repo: &Repository, root: &str, patterns: &[String]) -> Result<Vec<String>, Error> {
    let patterns: Vec<&str> = patterns.iter().map(String::as_str).collect();
    Ok(repo
        .glob(root, &patterns)?
        .iter()
        .filter_map(|file| beneath(root, file).map(String::from))
        .collect())
}

/// Source inputs of these patterns, less what is excluded.
fn sources<'a>(patterns: impl Iterator<Item = &'a str>, excluded: &[String]) -> Vec<Input> {
    patterns
        .map(Input::source)
        .chain(excluded.iter().map(Input::source))
        .collect()
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    fn fixture() -> Files {
        Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/valid")).unwrap()
    }

    fn actions(files: Files) -> Vec<Action> {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin
            .context(Profile::Development)
            .tool("crossplane", "2.5.0");
        plugin
            .analyze(&TestHost::new(files), &context, &["app"])
            .answered()
            .analyses
            .into_iter()
            .flat_map(|analysis| analysis.projects)
            .flat_map(|project| project.actions)
            .collect()
    }

    fn arguments(action: &Action) -> Vec<&str> {
        action
            .command
            .as_ref()
            .unwrap()
            .arguments
            .iter()
            .map(String::as_str)
            .collect()
    }

    fn sources(action: &Action) -> Vec<String> {
        action
            .inputs
            .iter()
            .map(|input| match &input.kind {
                Some(frontseat_plugin::wire::input::Kind::Source(pattern)) => pattern.clone(),
                other => panic!("an input that is no source: {other:?}"),
            })
            .collect()
    }

    /// A Configuration is packaged into a file named for it, which is
    /// what its project ships: a file it is sure to leave. It reads its
    /// YAML, less its catalog file and what is a fixture.
    #[test]
    fn a_configuration_is_packaged_into_a_file_named_for_it() {
        let found = actions(fixture().with("app/catalog-info.yaml", "kind: Component\n"));
        let package = &found[0];
        assert_eq!(package.name, "package");
        assert!(package.is_package());
        assert_eq!(package.tools, ["crossplane"]);
        assert_eq!(
            arguments(package),
            [
                "crossplane",
                "xpkg",
                "build",
                "--package-root",
                ".",
                "--examples-root",
                "./examples",
                "--ignore",
                "catalog-info.yaml",
                "--package-file",
                "network.xpkg"
            ]
        );
        assert_eq!(package.outputs, [Output::required("network.xpkg")]);
        assert_eq!(
            sources(package),
            [
                "**/*.yaml",
                "**/*.yml",
                "!**/testdata/**",
                "!**/catalog-info.yaml"
            ]
        );
    }

    /// The examples are validated against the definitions beside them,
    /// with no schema of Crossplane's own and no metadata, which the tool
    /// would download: a check, which reaches nothing.
    #[test]
    fn the_examples_are_validated_against_the_configurations_own_definitions() {
        let found = actions(fixture());
        let validate = &found[1];
        assert_eq!(validate.name, "validate");
        assert!(validate.as_check().is_some());
        assert_eq!(
            arguments(validate),
            [
                "crossplane",
                "resource",
                "validate",
                "--crossplane-image=",
                "--output",
                "json",
                "--",
                "./apis/composition.yaml,./apis/definition.yaml",
                "./examples"
            ]
        );
        assert_eq!(
            sources(validate),
            [
                "apis/composition.yaml",
                "apis/definition.yaml",
                "examples/**/*.yaml",
                "examples/**/*.yml",
                "!**/testdata/**",
                "!**/catalog-info.yaml"
            ]
        );
    }

    /// What xpkg leaves out of the package as its examples' is no
    /// definition, and a Configuration at the repository's root reads
    /// nothing of the file that lists its plugins; the control is a
    /// definition beside them, which is one.
    #[test]
    fn what_xpkg_takes_for_examples_and_the_plugins_list_are_no_definitions() {
        let found = actions(
            fixture()
                .with("app/examples-old.yaml", "garbage\n")
                .with("app/examples2/x.yaml", "garbage\n")
                .with("app/apis/other.yaml", "kind: X\n"),
        );
        assert_eq!(
            arguments(&found[1])[7],
            "./apis/composition.yaml,./apis/definition.yaml,./apis/other.yaml"
        );
        let plugin = TestPlugin::new(crate::served());
        let context = plugin
            .context(Profile::Development)
            .tool("crossplane", "2.5.0");
        let mut root = Files::new().with("frontseat.yaml", "plugins:\n  crossplane: {}\n");
        for (path, content) in [
            ("crossplane.yaml", "apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: network\n"),
            ("apis/definition.yaml", "kind: CompositeResourceDefinition\n"),
            ("examples/network.yaml", "kind: Network\n"),
        ] {
            root = root.with(path, content);
        }
        let at_root: Vec<Action> = plugin
            .analyze(&TestHost::new(root), &context, &[""])
            .answered()
            .analyses
            .into_iter()
            .flat_map(|analysis| analysis.projects)
            .flat_map(|project| project.actions)
            .collect();
        assert!(sources(&at_root[0]).contains(&"!frontseat.yaml".to_string()));
        assert_eq!(arguments(&at_root[1])[7], "./apis/definition.yaml");
        assert!(!sources(&found[0]).contains(&"!frontseat.yaml".to_string()));
    }

    /// A package beneath another is a project of its own, which the outer
    /// one neither packages nor validates; the control is the outer one
    /// alone.
    #[test]
    fn a_package_beneath_another_is_none_of_it() {
        let nested = fixture()
            .with("app/inner/crossplane.yaml", "apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: inner\n")
            .with("app/inner/apis/x.yaml", "kind: CompositeResourceDefinition\n");
        let found = actions(nested);
        assert!(sources(&found[0]).contains(&"!inner/**".to_string()));
        assert!(!arguments(&found[1])
            .iter()
            .any(|argument| argument.contains("inner")));
        assert!(!sources(&actions(fixture())[0]).contains(&"!inner/**".to_string()));
    }

    /// With no example, or nothing to hold one to, there is nothing to
    /// validate, and no check; the package is built all the same.
    #[test]
    fn with_no_example_or_no_definition_there_is_no_check() {
        let no_examples = fixture().without("app/examples/network.yaml");
        assert_eq!(
            actions(no_examples)
                .iter()
                .map(|a| a.name.as_str())
                .collect::<Vec<_>>(),
            ["package"]
        );
        let no_definitions = fixture()
            .without("app/apis/definition.yaml")
            .without("app/apis/composition.yaml");
        assert_eq!(
            actions(no_definitions)
                .iter()
                .map(|a| a.name.as_str())
                .collect::<Vec<_>>(),
            ["package"]
        );
    }

    /// A definition whose name holds a comma cannot be told to the tool,
    /// and is refused by name; the control is the same file renamed.
    #[test]
    fn a_definition_whose_name_holds_a_comma_is_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin
            .context(Profile::Development)
            .tool("crossplane", "2.5.0");
        let refused = plugin
            .analyze(
                &TestHost::new(fixture().with("app/apis/a,b.yaml", "kind: X\n")),
                &context,
                &["app"],
            )
            .refused("invalid-file-name");
        assert_eq!(refused.path, "app/apis/a,b.yaml");
        plugin
            .analyze(
                &TestHost::new(fixture().with("app/apis/a-b.yaml", "kind: X\n")),
                &context,
                &["app"],
            )
            .answered();
    }
}
