use conventional_commits::{Commit, Type};
use frontseat_plugin::prelude::*;
use semver::Version;

pub(super) const CONVENTIONAL_COMMITS: &str = "conventional-commits";

/// Conventional commits: a unit moves by the heaviest of its commits
/// since its last release. A breaking change is a major, a feature a
/// minor, a fix or a performance improvement a patch; anything else, a
/// message that is no conventional commit among it, moves nothing. Below
/// 1.0 a breaking change is a minor, so no commit takes a unit to 1.0. A
/// unit never released is released at its initial version.
pub(super) fn conventional_commits(request: &VersionRequest) -> Result<VersionResponse, Error> {
    let initial = version(&request.initial, "the initial version")?;
    let last = match request.last_version.as_str() {
        "" => None,
        last => Some(version(last, "the last version")?),
    };
    let stable = last.as_ref().is_some_and(|v| v.major > 0);
    let mut moves = Vec::new();
    for commit in &request.commits {
        let Ok(parsed) = Commit::parse(&commit.message) else {
            continue;
        };
        let bump = if parsed.breaking() {
            if stable {
                Bump::Major
            } else {
                Bump::Minor
            }
        } else if parsed.type_() == Type::FEAT {
            Bump::Minor
        } else if parsed.type_() == Type::FIX || parsed.type_() == Type::PERF {
            Bump::Patch
        } else {
            continue;
        };
        let summary = commit
            .message
            .lines()
            .next()
            .unwrap_or_default()
            .to_string();
        moves.push((
            bump,
            commit.id.clone(),
            summary,
            parsed.description().to_string(),
            parsed.breaking(),
        ));
    }
    // Heaviest first; among equals, newest first.
    moves.reverse();
    moves.sort_by_key(|(bump, ..)| std::cmp::Reverse(*bump as i32));
    let bump = moves.first().map_or(Bump::None, |(bump, ..)| *bump);
    let next = match (&last, bump) {
        (None, _) => Some(initial),
        (Some(_), Bump::None) => None,
        (Some(last), bump) => Some(moved(last, bump)),
    };
    // A breaking change is said as one whatever it moves the version by:
    // below 1.0 it is a minor, and still breaks.
    let mut notes = String::new();
    for (heading, of) in [
        ("Breaking changes", None),
        ("Features", Some(Bump::Minor)),
        ("Fixes", Some(Bump::Patch)),
    ] {
        let said: Vec<_> = moves
            .iter()
            .filter(|(bump, _, _, _, breaking)| match of {
                None => *breaking,
                Some(of) => !*breaking && *bump == of,
            })
            .collect();
        if said.is_empty() {
            continue;
        }
        notes.push_str(&format!("### {heading}\n\n"));
        for (_, _, _, description, _) in said {
            notes.push_str(&format!("- {description}\n"));
        }
        notes.push('\n');
    }
    Ok(VersionResponse {
        version: next.map(|v| v.to_string()).unwrap_or_default(),
        bump: bump as i32,
        reasons: moves
            .into_iter()
            .map(|(bump, commit, summary, _, _)| VersionReason {
                commit,
                bump: bump as i32,
                summary,
            })
            .collect(),
        notes: notes.trim_end().to_string(),
    })
}

fn moved(last: &Version, bump: Bump) -> Version {
    match bump {
        Bump::Major => Version::new(last.major + 1, 0, 0),
        Bump::Minor => Version::new(last.major, last.minor + 1, 0),
        _ => Version::new(last.major, last.minor, last.patch + 1),
    }
}

fn version(text: &str, what: &str) -> Result<Version, Error> {
    Version::parse(text).map_err(|_| Error {
        code: Code::InvalidArgument,
        reason: "invalid-version".into(),
        message: format!("{what} {text:?} is no semantic version"),
        help: "Write a version as major.minor.patch.".into(),
        ..Default::default()
    })
}
