//! The SDK holds a failure to what the host holds one to, so that an
//! author hears of a failure the host would refuse from the SDK, with
//! the message the plugin wrote.
use frontseat_plugin::prelude::*;
use frontseat_plugin::Code;
use frontseat_plugin_test::{TestHost, TestPlugin};

/// One row of `testdata/failures.json`: a failure as an author may write
/// one, and whether the host accepts it.
#[derive(serde::Deserialize)]
struct Case {
    case: String,
    code: i32,
    reason: String,
    message: String,
    path: String,
    line: i32,
    accepted: bool,
}

/// The table the host runs too (`TestTheSDKRefusesTheFailuresTheHostRefuses`,
/// `server/internal/pluginhost/error_internal_test.go`): a case the two
/// disagree on fails here or there.
#[test]
fn the_sdk_refuses_the_failures_the_host_refuses() {
    let table = std::fs::read(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/testdata/failures.json"
    ))
    .unwrap();
    let cases: Vec<Case> = serde_json::from_slice(&table).unwrap();
    assert!(cases.iter().any(|c| c.accepted) && cases.iter().any(|c| !c.accepted));
    for case in cases {
        let code = Code::try_from(case.code).unwrap();
        let written = Error::new(code, &case.reason, case.message.clone())
            .at_line(case.path.clone(), case.line);
        let sent = Error::from_status(written.into_status("go"));
        if case.accepted {
            assert_eq!(
                (sent.code, sent.reason.as_str(), sent.message.as_str()),
                (code, case.reason.as_str(), case.message.as_str()),
                "{}: the host accepts it, and the SDK did not send it",
                case.case
            );
            continue;
        }
        assert_eq!(
            (sent.code, sent.reason.as_str()),
            (Code::Internal, "invalid-failure"),
            "{}: the host refuses it, and the SDK sent it",
            case.case
        );
        // What is sent in its place is a failure the host accepts, and
        // holds what the plugin said.
        assert!(sent.message.contains("is not one the host accepts"));
        if !case.message.is_empty() {
            assert!(sent.message.ends_with(&case.message), "{}", case.case);
        }
    }
}

struct Careless;
impl Plugin for Careless {
    fn describe(&self) -> Description {
        Description {
            name: "careless".into(),
            ..Default::default()
        }
    }
    fn analyze(&self, _repo: &Repository, _root: &str) -> Result<Analysis, Error> {
        Err(Error::new(
            Code::FailedPrecondition,
            "Missing lock",
            "there is no Cargo.lock",
        ))
    }
}

/// A test written with the testkit hears of it as the host would say it,
/// with the plugin's own message, and not as the reason it asked for.
#[test]
fn a_test_hears_of_a_failure_the_host_would_refuse() {
    let plugin = TestPlugin::new(Careless);
    let context = plugin.context(Profile::Development);
    let refused = plugin
        .analyze(&TestHost::empty(), &context, &[""])
        .refused("invalid-failure");
    assert_eq!(refused.code, Code::Internal);
    assert!(
        refused
            .message
            .contains("its reason is not lower-case words")
            && refused.message.ends_with("It said: there is no Cargo.lock"),
        "{}",
        refused.message
    );
}
