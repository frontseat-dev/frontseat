use frontseat_plugin::prelude::*;

fn on(branch: &str) -> BranchRequest {
    BranchRequest {
        strategy: "release-flow".into(),
        branch: branch.into(),
        default_branch: "main".into(),
        ..Default::default()
    }
}

fn after(last: &str, messages: &[&str]) -> VersionRequest {
    VersionRequest {
        strategy: "conventional-commits".into(),
        last_version: last.into(),
        initial: "0.1.0".into(),
        commits: messages
            .iter()
            .enumerate()
            .map(|(i, message)| ReleaseCommit {
                id: format!("c{i}"),
                message: (*message).into(),
                ..Default::default()
            })
            .collect(),
        ..Default::default()
    }
}

#[test]
fn it_says_the_strategies_it_serves() {
    let described = crate::served().describe();
    assert_eq!(described.name, "release");
    assert_eq!(described.branchings, ["release-flow"]);
    assert_eq!(described.versionings, ["conventional-commits"]);
}

#[test]
fn under_release_flow_a_release_branch_releases_its_line_and_nothing_else_releases() {
    let plugin = crate::served();
    for (branch, line) in [
        ("release/1.26", "1.26"),
        ("release/0.77", "0.77"),
        ("release/10.0", "10.0"),
    ] {
        let answered = plugin.branch(&on(branch)).unwrap();
        assert!(answered.releases, "{branch}");
        assert_eq!(answered.line, line, "{branch}");
        assert_eq!(answered.channel, line, "{branch}");
    }
    for branch in [
        "main",
        "feat/greet",
        "release/1",
        "release/1.26.0",
        "release/01.2",
        "release/1.02",
        "release/1.x",
        "release/-1.2",
        "release/1.2/x",
        "releases/1.2",
        "release/",
        "",
    ] {
        let answered = plugin.branch(&on(branch)).unwrap();
        assert!(!answered.releases, "{branch:?} releases");
        assert!(!answered.reason.is_empty(), "{branch:?} says no reason");
        assert!(answered.line.is_empty(), "{branch:?}");
    }
    let main = plugin.branch(&on("main")).unwrap();
    assert!(main.reason.contains("default branch"), "{}", main.reason);
}

#[test]
fn a_release_branch_that_is_the_default_branch_still_releases() {
    let mut request = on("release/1.2");
    request.default_branch = "release/1.2".into();
    assert!(crate::served().branch(&request).unwrap().releases);
}

#[test]
fn conventional_commits_move_a_version_by_the_heaviest_commit() {
    let plugin = crate::served();
    for (last, messages, next, bump) in [
        ("1.2.3", vec!["fix: trim"], "1.2.4", Bump::Patch),
        ("1.2.3", vec!["perf: faster"], "1.2.4", Bump::Patch),
        (
            "1.2.3",
            vec!["fix: trim", "feat: greet by name"],
            "1.3.0",
            Bump::Minor,
        ),
        (
            "1.2.3",
            vec!["feat(greet): by name", "fix: trim"],
            "1.3.0",
            Bump::Minor,
        ),
        (
            "1.2.3",
            vec!["feat!: drop the old greeting"],
            "2.0.0",
            Bump::Major,
        ),
        (
            "1.2.3",
            vec!["fix: trim\n\nBREAKING CHANGE: the greeting is shorter"],
            "2.0.0",
            Bump::Major,
        ),
        // Below 1.0 a breaking change is a minor: 0.x never becomes 1.0 by a commit.
        (
            "0.76.1",
            vec!["feat!: drop the old greeting"],
            "0.77.0",
            Bump::Minor,
        ),
        ("0.76.1", vec!["feat: greet by name"], "0.77.0", Bump::Minor),
        ("0.76.1", vec!["fix: trim"], "0.76.2", Bump::Patch),
    ] {
        let answered = plugin.version(&after(last, &messages)).unwrap();
        assert_eq!(answered.version, next, "{last} {messages:?}");
        assert_eq!(answered.bump, bump as i32, "{last} {messages:?}");
        assert!(!answered.reasons.is_empty(), "{last} {messages:?}");
        assert_eq!(
            answered.reasons[0].bump, bump as i32,
            "heaviest first: {messages:?}"
        );
    }
}

#[test]
fn commits_that_release_nothing_hold_a_version() {
    let plugin = crate::served();
    for messages in [
        vec![],
        vec!["docs: say more"],
        vec!["chore: tidy", "test: more", "ci: faster"],
        vec!["not a conventional commit"],
        vec!["Merge branch 'main' into release/1.2"],
        vec![""],
    ] {
        let answered = plugin.version(&after("1.2.3", &messages)).unwrap();
        assert_eq!(answered.version, "", "{messages:?}");
        assert_eq!(answered.bump, Bump::None as i32, "{messages:?}");
        assert!(answered.reasons.is_empty(), "{messages:?}");
    }
}

#[test]
fn a_first_release_is_the_initial_version_whatever_its_commits_say() {
    let plugin = crate::served();
    for messages in [vec![], vec!["docs: say more"], vec!["feat!: everything"]] {
        let answered = plugin.version(&after("", &messages)).unwrap();
        assert_eq!(answered.version, "0.1.0", "{messages:?}");
    }
}

#[test]
fn reasons_name_their_commits_and_notes_say_what_changed() {
    let answered = crate::served()
        .version(&after(
            "1.2.3",
            &[
                "docs: say more",
                "fix: trim the greeting\n\nbody",
                "feat: greet by name",
            ],
        ))
        .unwrap();
    let said: Vec<_> = answered
        .reasons
        .iter()
        .map(|r| (r.commit.as_str(), r.summary.as_str()))
        .collect();
    assert_eq!(
        said,
        [
            ("c2", "feat: greet by name"),
            ("c1", "fix: trim the greeting")
        ]
    );
    assert!(
        answered.notes.contains("greet by name"),
        "{}",
        answered.notes
    );
    assert!(
        answered.notes.contains("trim the greeting"),
        "{}",
        answered.notes
    );
    assert!(!answered.notes.contains("say more"), "{}", answered.notes);
}

#[test]
fn what_it_is_asked_wrongly_is_refused() {
    // A strategy it does not serve is refused as the host asks it,
    // through the SDK's dispatch.
    let asked = frontseat_plugin_test::TestPlugin::new(crate::served());
    let mut branch = on("release/1.2");
    branch.strategy = "github-flow".into();
    let refused = asked.branch(branch).refused("unclaimed-strategy");
    assert_eq!(refused.code, Code::InvalidArgument);
    let mut version = after("1.2.3", &["fix: trim"]);
    version.strategy = "manual".into();
    let refused = asked.version(version).refused("unclaimed-strategy");
    assert_eq!(refused.code, Code::InvalidArgument);
    let plugin = crate::served();
    for (last, initial) in [("1.2", "0.1.0"), ("v1.2.3", "0.1.0"), ("", "one"), ("", "")] {
        let mut request = after(last, &["fix: trim"]);
        request.initial = initial.into();
        let error = plugin.version(&request).unwrap_err();
        assert_eq!(error.code, Code::InvalidArgument, "{last:?} {initial:?}");
    }
    assert!(
        plugin.version(&after("1.2.3", &["fix: trim"])).is_ok(),
        "the control was refused"
    );
}

#[test]
fn a_breaking_change_is_said_as_one_whatever_it_moves_the_version_by() {
    for last in ["0.76.1", "1.2.3"] {
        let answered = crate::served()
            .version(&after(
                last,
                &["feat!: drop the old greeting", "feat: greet by name"],
            ))
            .unwrap();
        assert!(
            answered
                .notes
                .starts_with("### Breaking changes\n\n- drop the old greeting"),
            "{last}: {}",
            answered.notes
        );
        assert!(
            answered.notes.contains("### Features\n\n- greet by name"),
            "{last}: {}",
            answered.notes
        );
    }
}
