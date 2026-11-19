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
    // the plugin is asked; what is left of each match is never reached.
    fn branch(&self, request: &BranchRequest) -> Result<BranchResponse, Error> {
        match request.strategy.as_str() {
            branching::RELEASE_FLOW => Ok(branching::release_flow(request)),
            other => Err(Error::not_served("branching-strategy", other)),
        }
    }

    fn version(&self, request: &VersionRequest) -> Result<VersionResponse, Error> {
        match request.strategy.as_str() {
            versioning::CONVENTIONAL_COMMITS => versioning::conventional_commits(request),
            other => Err(Error::not_served("versioning-strategy", other)),
        }
    }
}
