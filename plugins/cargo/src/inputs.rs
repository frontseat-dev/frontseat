use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{join, literal};

/// What a package is: its directory, as cargo packages it, except what
/// cargo writes there and any package nested in it, which is a package
/// of its own.
pub(super) fn input_sets(repo: &Repository, root: &str) -> Result<Vec<InputSet>, Error> {
    let mut package = vec!["**".to_string(), "!target/**".to_string()];
    for directory in nested_packages(repo, root)? {
        package.push(format!("!{}/**", literal(&directory)));
    }
    Ok(vec![InputSet {
        name: "package".into(),
        inputs: package.into_iter().map(Input::source).collect(),
        accepts_generated: true,
    }])
}

/// The directories of the packages nested in this one, relative to it.
pub(super) fn nested_packages(repo: &Repository, root: &str) -> Result<Vec<String>, Error> {
    let own = join(root, "Cargo.toml");
    let mut nested = Vec::new();
    for manifest in repo.glob(root, &["**/Cargo.toml", "!target/**", "!**/testdata/**"])? {
        if manifest == own {
            continue;
        }
        let within = manifest
            .strip_prefix(root)
            .unwrap_or(&manifest)
            .trim_start_matches('/');
        if let Some(directory) = within.strip_suffix("/Cargo.toml") {
            nested.push(directory.to_string());
        }
    }
    nested.sort();
    Ok(nested)
}
