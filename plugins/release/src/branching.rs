use frontseat_plugin::prelude::*;

pub(super) const RELEASE_FLOW: &str = "release-flow";

/// Release Flow: a release branch, `release/<major>.<minor>`, releases,
/// held to the line its name gives, and a release is cut from the
/// default branch by making one. Every other branch releases a
/// prerelease of the version it is heading for, named by the branch: the
/// default branch, where work lands, is heading for a line not cut yet,
/// and so for the next minor at least above everything released; any
/// other branch for whatever its commits call for. A branch beneath
/// `release/` that names no line releases nothing: it is a release
/// branch written wrongly, and says so. So does any branch where the
/// default branch is not known: where it is heading is then not either.
pub(super) fn release_flow(request: &BranchRequest) -> BranchResponse {
    if let Some(line) = line_of(&request.branch) {
        return BranchResponse {
            releases: true,
            line: line.into(),
            channel: line.into(),
            ..Default::default()
        };
    }
    let channel = channel_of(&request.branch);
    if request.branch.starts_with("release/") || channel.is_empty() {
        return BranchResponse {
            reason: format!(
                "{:?} is no release branch; under Release Flow a release is made from a branch release/<major>.<minor>",
                request.branch
            ),
            ..Default::default()
        };
    }
    // Which branch is the default is a fact of where the repository was
    // brought from, and a clone made otherwise does not know it: where a
    // branch is heading is then not known either, and it is said so, not
    // guessed, so that one commit is not planned two ways.
    if request.default_branch.is_empty() {
        return BranchResponse {
            reason: format!(
                "the default branch of this repository is not known here, so what {:?} is heading for is not; under Release Flow a release is made from a branch release/<major>.<minor>",
                request.branch
            ),
            ..Default::default()
        };
    }
    let at_least = if request.branch == request.default_branch {
        Bump::Minor
    } else {
        Bump::Unspecified
    };
    BranchResponse {
        releases: true,
        prerelease: true,
        channel,
        at_least: at_least.into(),
        ..Default::default()
    }
}

/// The channel a branch's prereleases are of: its name, as a channel is
/// named, every character but a letter, a digit and a dash made a dash,
/// from its first letter or digit, as far as a channel's name goes. It
/// is empty for a branch whose name holds neither.
fn channel_of(branch: &str) -> String {
    branch
        .chars()
        .skip_while(|c| !c.is_ascii_alphanumeric())
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '-' })
        .take(128)
        .collect()
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
