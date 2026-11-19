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

    fn branch(&self, request: &BranchRequest) -> Result<BranchResponse, Error> {
        match request.strategy.as_str() {
            branching::RELEASE_FLOW => Ok(branching::release_flow(request)),
            other => Err(unknown("branching", other)),
        }
    }

    fn version(&self, request: &VersionRequest) -> Result<VersionResponse, Error> {
        match request.strategy.as_str() {
            versioning::CONVENTIONAL_COMMITS => versioning::conventional_commits(request),
            other => Err(unknown("versioning", other)),
        }
    }
}

fn unknown(kind: &str, strategy: &str) -> Error {
    Error {
        code: Code::InvalidArgument,
        reason: "unknown-strategy".into(),
        message: format!("release serves no {kind} strategy {strategy:?}"),
        help: "Name a strategy the plugin says it serves.".into(),
        ..Default::default()
    }
}
