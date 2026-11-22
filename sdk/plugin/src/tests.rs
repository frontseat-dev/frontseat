use crate::wire;
use prost::Message;

#[test]
fn corrupt_host_replies_are_errors_and_empty_replies_remain_valid() {
    for bytes in [vec![0xff], vec![0x12, 0x05, b'a']] {
        let error = crate::abi::decode_host_reply::<wire::ReadResponse>(&bytes).unwrap_err();
        assert_eq!(error.reason, "invalid-host-response");
        assert_eq!(error.code, crate::Code::Internal);
        assert!(crate::abi::decode_host_reply::<wire::GlobResponse>(&bytes).is_err());
    }
    let absent = crate::abi::decode_host_reply::<wire::ReadResponse>(&[]).unwrap();
    assert!(!absent.found);
    let empty = wire::ReadResponse {
        found: true,
        content: vec![],
    };
    assert_eq!(
        crate::abi::decode_host_reply::<wire::ReadResponse>(&empty.encode_to_vec()).unwrap(),
        empty
    );
    assert!(crate::abi::decode_host_reply::<wire::GlobResponse>(&[])
        .unwrap()
        .paths
        .is_empty());
}

/// The README's plugin is the example, whole: one text, which the
/// example's build compiles and the crate's documentation tests run.
#[test]
fn the_readmes_plugin_is_the_example() {
    let example = include_str!("../examples/make.rs");
    let code = &example[example.find("use frontseat_plugin::prelude::*;").unwrap()..];
    assert!(
        include_str!("../README.md").contains(&format!("```rust\n{code}```\n")),
        "README.md's plugin is not examples/make.rs: copy the example into it"
    );
}

#[test]
fn a_failure_is_printed_as_a_person_reads_it_and_is_an_error() {
    let refused = crate::Error::new(crate::Code::FailedPrecondition, "missing-lock", "no lock.");
    assert_eq!(refused.to_string(), "no lock.");
    assert_eq!(
        refused.clone().at("app/Cargo.toml").to_string(),
        "app/Cargo.toml: no lock."
    );
    let whole = refused.at_line("app/Cargo.toml", 3).help("Commit it.");
    assert_eq!(
        whole.to_string(),
        "app/Cargo.toml: line 3: no lock. Commit it."
    );
    // What is printed is the status's message.
    assert_eq!(
        whole.clone().into_status("cargo").message,
        whole.to_string()
    );
    let boxed: Box<dyn std::error::Error + Send + Sync> = Box::new(whole);
    assert_eq!(
        boxed.to_string(),
        "app/Cargo.toml: line 3: no lock. Commit it."
    );
}

/// A plugin that counts how often it is asked what it is.
struct Counted(std::cell::Cell<u32>);

impl crate::Plugin for Counted {
    fn describe(&self) -> crate::Description {
        self.0.set(self.0.get() + 1);
        crate::Description {
            name: "counted".into(),
            options: vec![crate::PluginOption::new(
                "strict",
                "Whether a missing manifest is refused.",
                true,
            )],
            ..Default::default()
        }
    }
}

struct NoRepository;

impl crate::Host for NoRepository {
    fn read(&self, _path: &str) -> Result<Option<Vec<u8>>, crate::Error> {
        Ok(None)
    }
    fn glob(&self, _directory: &str, _patterns: &[&str]) -> Result<Vec<String>, crate::Error> {
        Ok(Vec::new())
    }
}

fn asked(plugin: &Counted, context: wire::Context) -> wire::reply::Result {
    let call = wire::Call {
        method: "Analyze".into(),
        request: wire::AnalyzeRequest {
            context: Some(context),
            roots: vec!["app".into()],
        }
        .encode_to_vec(),
    };
    let reply = crate::abi::answer(plugin, &NoRepository, &call.encode_to_vec());
    wire::Reply::decode(reply.as_slice())
        .unwrap()
        .result
        .unwrap()
}

fn context(options: &[(&str, crate::Value)]) -> wire::Context {
    wire::Context {
        profile: crate::Profile::Development as i32,
        options: options
            .iter()
            .map(|(name, value)| (name.to_string(), value.clone().into()))
            .collect(),
        ..Default::default()
    }
}

/// A call asks the plugin what it is once, whether it answers or fails.
#[test]
fn a_plugin_is_asked_what_it_is_once_in_a_call() {
    let plugin = Counted(0.into());
    assert!(matches!(
        asked(&plugin, context(&[("strict", true.into())])),
        wire::reply::Result::Response(_)
    ));
    assert_eq!(plugin.0.get(), 1);
    assert!(matches!(
        asked(&plugin, context(&[])),
        wire::reply::Result::Error(_)
    ));
    assert_eq!(plugin.0.get(), 2);
}

/// An option that is not as the plugin declares it is refused by what
/// is wrong with it, in the contract's names for the kinds.
#[test]
fn an_option_is_refused_by_what_is_wrong_with_it() {
    for (options, wrong) in [
        (vec![], "option strict was given no value"),
        (vec![("strict", 1.into())], "option strict is not a bool"),
        (
            vec![("strict", true.into()), ("loose", "yes".into())],
            "the plugin declares no option loose",
        ),
    ] {
        let wire::reply::Result::Error(status) = asked(&Counted(0.into()), context(&options))
        else {
            panic!("{wrong}: the call was answered");
        };
        let refused = crate::Error::from_status(status);
        assert_eq!(
            (refused.reason.as_str(), refused.message.as_str()),
            ("invalid-option", wrong)
        );
    }
}
