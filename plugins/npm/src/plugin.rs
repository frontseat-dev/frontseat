use frontseat_plugin::prelude::*;

pub(crate) struct NpmPlugin;

impl Plugin for NpmPlugin {
    fn describe(&self) -> Description {
        frontseat_plugin::javascript::description("npm")
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let Some(package) = crate::manifest::read(repo, root)? else {
            return Ok(Analysis::default());
        };
        Ok(frontseat_plugin::javascript::installs(
            root,
            &package,
            "npm-package",
            (crate::actions::INSTALL, &[crate::actions::INSTALLED.into()]),
            crate::actions::of(),
        ))
    }
}
