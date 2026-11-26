//! A package's metadata, `crossplane.yaml`, as the plugin reads it: what
//! the package is and what it is named.
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;
use serde::Deserialize;

/// The file a package's metadata is in, at the package's root, as
/// `crossplane xpkg build` reads it.
pub(crate) const FILE: &str = "crossplane.yaml";

/// The group every package's metadata is of, at any of its versions.
const GROUP: &str = "meta.pkg.crossplane.io";

/// A Configuration: the one kind of package built. Its name is what
/// its package file is named for.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct Configuration {
    pub(crate) name: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Metadata {
    #[serde(default)]
    api_version: String,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    metadata: Named,
}

#[derive(Default, Deserialize)]
struct Named {
    #[serde(default)]
    name: String,
}

const HELP: &str = "Write the package's metadata as `crossplane xpkg init` does: apiVersion meta.pkg.crossplane.io/v1, kind Configuration, and metadata.name.";

/// The Configuration whose metadata is at the root, or none where there
/// is no metadata there. Metadata that is not a package's, or that names
/// no package, is refused; a Function's and a Provider's are valid and
/// not built yet.
pub(crate) fn read(repo: &Repository, root: &str) -> Result<Option<Configuration>, Error> {
    let path = join(root, FILE);
    let Some(document) = repo.yaml::<Metadata>(&path)? else {
        return Ok(None);
    };
    let metadata = document.value;
    let invalid = |message: String| Error::invalid("invalid-manifest", &path, message, HELP);
    // A file of this name that is no package's metadata is some other
    // tool's, and no project: Crossplane takes metadata by its group.
    if metadata.api_version.split_once('/').map(|(group, _)| group) != Some(GROUP) {
        return Ok(None);
    }
    match metadata.kind.as_str() {
        "Configuration" => {}
        // A Function and a Provider each embed the image they run, which
        // their metadata does not name and no action of the build makes.
        "Function" => return Err(Error::not_served(
            "functions",
            "a Function's package embeds the image it runs, which nothing of the build makes yet",
        )
        .at(&path)),
        "Provider" => return Err(Error::not_served(
            "providers",
            "a Provider's package embeds the image it runs, which nothing of the build makes yet",
        )
        .at(&path)),
        _ => {
            return Err(invalid(
                "its kind is none of a package's: a package is a Configuration, a Function or a Provider".into(),
            ))
        }
    }
    let name = metadata.metadata.name;
    if name.is_empty() {
        return Err(invalid(
            "the Configuration names no package: its metadata.name is missing".into(),
        ));
    }
    if !is_name(&name) {
        return Err(invalid(
            "the Configuration's metadata.name is not a name its package file can be named for: lower-case letters, digits, '-' and '.', beginning and ending with a letter or a digit, each part between dots of at most 63, and of at most 250 in all".into(),
        ));
    }
    Ok(Some(Configuration { name }))
}

/// Whether a name is one Kubernetes takes for an object, a DNS subdomain
/// (RFC 1123), as Crossplane names a package, and short enough that its
/// package file, `<name>.xpkg`, is a name every file system takes.
fn is_name(name: &str) -> bool {
    let alphanumeric = |c: char| c.is_ascii_lowercase() || c.is_ascii_digit();
    name.len() <= 250
        && name.split('.').all(|label| {
            !label.is_empty()
                && label.len() <= 63
                && label.starts_with(alphanumeric)
                && label.ends_with(alphanumeric)
                && label.chars().all(|c| alphanumeric(c) || c == '-')
        })
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    fn analyzed(metadata: &str) -> TestHost {
        TestHost::new(
            Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/valid"))
                .unwrap()
                .with("app/crossplane.yaml", metadata),
        )
    }

    /// Metadata that is no package's, or names no package, is refused as
    /// a manifest that is wrong, saying where; a Function's and a
    /// Provider's are valid, and refused as not built yet, each by its
    /// own reason. The control is the fixture's Configuration, which is
    /// a project.
    #[test]
    fn metadata_that_is_no_configurations_is_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin
            .context(Profile::Development)
            .tool("crossplane", "2.5.0");
        let control = "apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: network\n";
        let analysis = plugin
            .analyze(&analyzed(control), &context, &["app"])
            .answered();
        assert_eq!(analysis.analyses[0].projects[0].name, "network");
        for (metadata, reason) in [
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nspec: {}\n", "invalid-manifest"),
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: Network\n", "invalid-manifest"),
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: a/b\n", "invalid-manifest"),
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "invalid-manifest"),
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: a..b\n", "invalid-manifest"),
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Package\nmetadata:\n  name: network\n", "invalid-manifest"),
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Configuration\nmetadata: {name: [network\n", "invalid-yaml"),
            ("apiVersion: meta.pkg.crossplane.io/v1beta1\nkind: Function\nmetadata:\n  name: function-x\n", "functions-not-served"),
            ("apiVersion: meta.pkg.crossplane.io/v1\nkind: Provider\nmetadata:\n  name: provider-x\n", "providers-not-served"),
        ] {
            let refused = plugin
                .analyze(&analyzed(metadata), &context, &["app"])
                .refused(reason);
            assert_eq!(refused.path, "app/crossplane.yaml", "{metadata}");
            assert!(!refused.help.is_empty(), "{metadata}");
            // What it refused is not said again: a value may hold a token.
            assert!(!refused.message.contains("Network") && !refused.message.contains("a/b"), "{refused:?}");
        }
    }

    /// A file of the name that is no package's metadata, another tool's,
    /// is no project, and refuses nothing; the control is the
    /// Configuration, which is one.
    #[test]
    fn a_file_of_the_name_that_is_no_packages_metadata_is_no_project() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin
            .context(Profile::Development)
            .tool("crossplane", "2.5.0");
        for metadata in [
            "apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata:\n  name: crossplane\n",
            "apiVersion: pkg.crossplane.io/v1\nkind: Configuration\nmetadata:\n  name: network\n",
            "replicas: 2\n",
        ] {
            let analysis = plugin
                .analyze(&analyzed(metadata), &context, &["app"])
                .answered();
            assert!(
                analysis.analyses.iter().all(|a| a.projects.is_empty()),
                "{metadata}"
            );
        }
    }
}
