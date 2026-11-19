use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::literal;

/// What a package is: its directory, as cargo packages it, except what
/// cargo writes there and any package nested in it, which is a package
/// of its own.
pub(super) fn input_sets(repo: &Repository, root: &str) -> Result<Vec<InputSet>, Error> {
    let mut package = vec!["**".to_string(), "!target/**".to_string()];
    for directory in repo.nested(root, "Cargo.toml", &["!target/**"])? {
        package.push(format!("!{}/**", literal(&directory)));
    }
    Ok(vec![InputSet {
        name: "package".into(),
        inputs: package.into_iter().map(Input::source).collect(),
        accepts_generated: true,
    }])
}
