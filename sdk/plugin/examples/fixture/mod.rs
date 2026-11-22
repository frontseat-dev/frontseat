//! What the contract fixtures that read one TOML manifest share: each is
//! named for its manifest, `<name>.toml`, and finds a project where one is.
use frontseat_plugin::repository::{join, TomlTable};
use frontseat_plugin::{Description, Error, Repository};

/// A fixture's description: its name, and the manifest it is interested in.
pub fn describe(name: &str) -> Description {
    Description {
        name: name.into(),
        interests: vec![format!("**/{name}.toml")],
        ..Default::default()
    }
}

/// The path of a fixture's manifest at a root, and what it holds: nothing
/// where there is none.
pub fn manifest(repo: &Repository, root: &str, name: &str) -> Result<(String, TomlTable), Error> {
    let manifest = join(root, &format!("{name}.toml"));
    let document = repo.toml_table(&manifest)?.unwrap_or_default();
    Ok((manifest, document))
}
