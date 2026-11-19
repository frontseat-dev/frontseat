use frontseat_plugin::prelude::*;

pub(super) const RELEASE_FLOW: &str = "release-flow";

/// Release Flow: a release branch, `release/<major>.<minor>`, releases,
/// held to the line its name gives; no other branch releases anything.
/// The default branch is where work lands, and a release is cut from it
/// by making the branch.
pub(super) fn release_flow(request: &BranchRequest) -> BranchResponse {
    if let Some(line) = line_of(&request.branch) {
        return BranchResponse {
            releases: true,
            line: line.into(),
            channel: line.into(),
            ..Default::default()
        };
    }
    let reason = if request.branch == request.default_branch {
        format!(
            "{} is the default branch, which releases nothing under Release Flow; a release is made from a branch release/<major>.<minor>",
            request.branch
        )
    } else {
        format!(
            "{:?} is no release branch; under Release Flow a release is made from a branch release/<major>.<minor>",
            request.branch
        )
    };
    BranchResponse {
        reason,
        ..Default::default()
    }
}

/// The line a release branch's name gives: "1.26" of "release/1.26". Each
/// number is written as a version writes it, with no sign and no leading
/// zero, so that one line has one branch.
fn line_of(branch: &str) -> Option<&str> {
    let line = branch.strip_prefix("release/")?;
    let (major, minor) = line.split_once('.')?;
    (number(major) && number(minor)).then_some(line)
}

fn number(text: &str) -> bool {
    !text.is_empty()
        && text.bytes().all(|b| b.is_ascii_digit())
        && (text == "0" || !text.starts_with('0'))
        && text.parse::<u64>().is_ok()
}
