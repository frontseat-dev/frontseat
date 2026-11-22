use frontseat_plugin::prelude::*;

use crate::{branching, versioning};

pub(super) struct ReleasePlugin;

impl Plugin for ReleasePlugin {
    fn describe(&self) -> Description {
        Description {
            name: "release".into(),
            branchings: vec![branching::RELEASE_FLOW.into()],
            versionings: vec![versioning::CONVENTIONAL_COMMITS.into()],
            ..Default::default()
        }
    }

    // The SDK asks Branch and Version only of a strategy the plugin
    // says it serves, and refuses any other (`unclaimed-strategy`) before
    // the plugin is asked: it serves one of each, so the one is what it
    // is asked.
    fn branch(&self, request: &BranchRequest) -> Result<BranchResponse, Error> {
        Ok(branching::release_flow(request))
    }

    fn version(&self, request: &VersionRequest) -> Result<VersionResponse, Error> {
        versioning::conventional_commits(request)
    }
}
