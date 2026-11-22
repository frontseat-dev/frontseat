//! The SDK asked as the host asks it: what it checks of a call before a
//! plugin's own code runs, and how it encodes what it answers.
use frontseat_plugin::abi::dispatch;
use frontseat_plugin::option_value::{StringList, Value as Kind};
use frontseat_plugin::prelude::*;
use frontseat_plugin::{wire, OptionValue};
use frontseat_plugin_test::{Files, Read, TestHost};
use prost::Message;

fn example_context() -> wire::Context {
    wire::Context {
        profile: Profile::Development as i32,
        options: [
            ("text".into(), Value::String("value".into()).into()),
            ("switch".into(), Value::Bool(true).into()),
            ("number".into(), Value::Int(7).into()),
            ("list".into(), Value::StringList(vec!["a".into()]).into()),
        ]
        .into(),
        ..Default::default()
    }
}

#[test]
fn dispatch_refuses_unknown_missing_and_mistyped_options_before_plugin_code() {
    let host = source();
    let call = |context| {
        ask(
            &host,
            "Analyze",
            wire::AnalyzeRequest {
                context: Some(context),
                roots: vec!["valid".into()],
            },
        )
    };
    assert!(call(example_context()).is_ok());
    for name in ["text", "switch", "number", "list"] {
        let mut missing = example_context();
        missing.options.remove(name);
        assert!(call(missing).is_err(), "missing {name} was accepted");
        let mut wrong = example_context();
        wrong.options.insert(name.into(), OptionValue::default());
        assert!(call(wrong).is_err(), "unset {name} was accepted");
    }
    let mut extra = example_context();
    extra
        .options
        .insert("unknown".into(), Value::Bool(false).into());
    assert!(call(extra).is_err());
    let mut wrong = example_context();
    wrong
        .options
        .insert("number".into(), Value::String("7".into()).into());
    assert!(call(wrong).is_err());
    assert!(call(example_context()).is_ok());
}

#[test]
fn integer_options_round_trip_without_float_conversion() {
    struct Integers;
    impl Plugin for Integers {
        fn describe(&self) -> Description {
            Description {
                name: "integers".into(),
                options: vec![PluginOption::new(
                    "number",
                    Value::Int(9_007_199_254_740_993),
                )],
                ..Default::default()
            }
        }
    }
    let host = source();
    let response = dispatch(
        &Integers,
        &host,
        &wire::Call {
            method: "Describe".into(),
            request: wire::DescribeRequest {}.encode_to_vec(),
        },
    )
    .unwrap()
    .unwrap();
    let described = wire::DescribeResponse::decode(response.as_slice()).unwrap();
    let repo = Repository::new(
        &host,
        Some(wire::Context {
            options: described
                .options
                .into_iter()
                .map(|o| (o.name, o.default.unwrap()))
                .collect(),
            ..Default::default()
        }),
    );
    assert_eq!(repo.option_int("number").unwrap(), 9_007_199_254_740_993);
}

/// A repository of a manifest and a Go file in a directory whose name
/// holds what a pattern reads, which the host takes as a name.
fn source() -> TestHost {
    TestHost::new(
        Files::new()
            .with("pkg[1]/manifest", "content")
            .with("pkg[1]/main.go", "package main\n")
            .with("pkg1/main.go", "package main\n"),
    )
}
struct Example;
impl Plugin for Example {
    fn describe(&self) -> Description {
        Description {
            name: "example".into(),
            options: vec![
                PluginOption::new("text", Value::String("value".into())).entity_settable(),
                PluginOption::new("switch", Value::Bool(true)),
                PluginOption::new("number", Value::Int(7)),
                PluginOption::new("list", Value::StringList(vec!["a".into()])).entity_settable(),
            ],
            ..Default::default()
        }
    }
    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        if root == "bad" {
            return Err(Error::new(Code::FailedPrecondition, "invalid-manifest", "")
                .at_line("bad/manifest", 3)
                .help("Repair it."));
        }
        repo.read(&frontseat_plugin::repository::join(root, "manifest"))?;
        Ok(Analysis::default())
    }
    fn arrange(
        &self,
        _repo: &Repository,
        root: &str,
        actions: &[String],
        handed: &[Handoff],
    ) -> Result<Vec<File>, Error> {
        assert_eq!(actions, ["compile"]);
        assert_eq!(handed[0].into, "dependencies");
        Ok(vec![File {
            path: format!("{root}/workspace"),
            content: b"arranged".to_vec(),
            ..Default::default()
        }])
    }
}
fn ask<R: Message>(host: &TestHost, method: &str, request: R) -> Result<Vec<u8>, Error> {
    dispatch(
        &Example,
        host,
        &wire::Call {
            method: method.into(),
            request: request.encode_to_vec(),
        },
    )
    .unwrap()
}
#[test]
fn dispatch_encodes_all_setting_kinds() {
    let host = source();
    let response = ask(&host, "Describe", wire::DescribeRequest {}).unwrap();
    let got = wire::DescribeResponse::decode(response.as_slice()).unwrap();
    assert_eq!(got.name, "example");
    let kinds: Vec<_> = got.options.iter().map(|o| o.kind).collect();
    assert_eq!(
        kinds,
        [
            wire::option_declaration::Kind::String as i32,
            wire::option_declaration::Kind::Bool as i32,
            wire::option_declaration::Kind::Int as i32,
            wire::option_declaration::Kind::StringList as i32
        ]
    );
    assert_eq!(
        got.options[0].default.as_ref().unwrap().value,
        Some(Kind::StringValue("value".into()))
    );
    assert_eq!(
        got.options[1].default.as_ref().unwrap().value,
        Some(Kind::BoolValue(true))
    );
    assert_eq!(
        got.options[2].default.as_ref().unwrap().value,
        Some(Kind::IntValue(7))
    );
    assert_eq!(
        got.options[3].default.as_ref().unwrap().value,
        Some(Kind::StringListValue(StringList {
            values: vec!["a".into()]
        }))
    );
    assert!(got.options[0].entity_settable && !got.options[1].entity_settable);
    assert!(host.reads().is_empty());
}
#[test]
fn dispatch_preserves_roots_handover_and_diagnostics() {
    let host = source();
    for roots in [vec![], vec!["one".into(), "two".into()]] {
        let got = ask(
            &host,
            "Analyze",
            wire::AnalyzeRequest {
                context: Some(example_context()),
                roots: roots.clone(),
            },
        )
        .unwrap();
        let got = wire::AnalyzeResponse::decode(got.as_slice()).unwrap();
        let wanted = if roots.is_empty() {
            vec![String::new()]
        } else {
            roots
        };
        assert_eq!(
            got.analyses
                .iter()
                .map(|a| a.root.clone())
                .collect::<Vec<_>>(),
            wanted
        );
    }
    let err = ask(
        &host,
        "Analyze",
        wire::AnalyzeRequest {
            context: Some(example_context()),
            roots: vec!["bad".into()],
        },
    )
    .unwrap_err();
    assert_eq!(
        (
            err.reason.as_str(),
            err.path.as_str(),
            err.line,
            err.help.as_str()
        ),
        ("invalid-manifest", "bad/manifest", 3, "Repair it.")
    );
    let got = ask(
        &host,
        "Arrange",
        wire::ArrangeRequest {
            context: Some(example_context()),
            root: "app".into(),
            actions: vec!["compile".into()],
            handed: vec![Handoff {
                into: "dependencies".into(),
                ..Default::default()
            }],
        },
    )
    .unwrap();
    let got = wire::ArrangeResponse::decode(got.as_slice()).unwrap();
    assert_eq!(
        got.files,
        vec![File {
            path: "app/workspace".into(),
            content: b"arranged".to_vec(),
            ..Default::default()
        }]
    );
}
#[test]
fn malformed_requests_and_unknown_methods_cannot_succeed() {
    let host = source();
    for method in ["Describe", "Analyze", "Arrange"] {
        let err = dispatch(
            &Example,
            &host,
            &wire::Call {
                method: method.into(),
                request: vec![0xff],
            },
        )
        .unwrap()
        .unwrap_err();
        assert_eq!(err.reason, "invalid-request");
    }
    assert!(dispatch(
        &Example,
        &host,
        &wire::Call {
            method: "Unknown".into(),
            request: vec![]
        }
    )
    .is_none());
    assert!(host.reads().is_empty());
}
#[test]
fn repository_preserves_literal_paths_context_and_absence() {
    let host = source();
    let repo = Repository::new(
        &host,
        Some(wire::Context {
            profile: Profile::Production as i32,
            tools: [("go".into(), "1.27.3".into())].into(),
            ..example_context()
        }),
    );
    assert_eq!(repo.profile().unwrap(), Profile::Production);
    assert_eq!(repo.tool_version("go"), Some("1.27.3"));
    assert_eq!(repo.tool_version("unknown"), None);
    assert_eq!(repo.option_str("text").unwrap(), "value");
    assert!(repo.option_bool("switch").unwrap());
    assert_eq!(repo.option_int("number").unwrap(), 7);
    assert_eq!(repo.option_list("list").unwrap(), ["a"]);
    assert!(repo.option_str("switch").is_err());
    assert!(repo.option_bool("text").is_err());
    assert!(repo.option_int("absent").is_err());
    assert!(repo.option_list("absent").is_err());
    assert_eq!(
        repo.read("pkg[1]/manifest").unwrap(),
        Some(b"content".to_vec())
    );
    assert_eq!(repo.read("absent").unwrap(), None);
    assert_eq!(repo.glob("pkg[1]", &["*.go"]).unwrap(), ["pkg[1]/main.go"]);
    assert!(Repository::new(&host, None).profile().is_err());
    assert_eq!(
        host.reads(),
        [
            Read::File {
                path: "pkg[1]/manifest".into(),
                found: true
            },
            Read::File {
                path: "absent".into(),
                found: false
            },
            Read::Glob {
                directory: "pkg[1]".into(),
                patterns: vec!["*.go".into()],
                found: true
            },
        ]
    );
}

#[test]
fn failed_source_reads_never_become_absence_or_empty_documents() {
    fn failure() -> Error {
        Error::new(
            Code::Unavailable,
            "source-unavailable",
            "source is unavailable",
        )
    }
    let unavailable = source().fail_everything(failure());
    let repo = Repository::new(&unavailable, Some(example_context()));
    let failures = [
        repo.read("manifest").unwrap_err(),
        repo.text("manifest").unwrap_err(),
        repo.toml_table("Cargo.toml").unwrap_err(),
        repo.yaml_stream::<serde_json::Value>("catalog-info.yaml")
            .map(|read| read.map(|document| document.value))
            .unwrap_err(),
        repo.glob("", &["**/*"]).unwrap_err(),
    ];
    for error in failures {
        assert_eq!(error.reason, "source-unavailable");
        assert_eq!(error.code, Code::Unavailable);
    }
    let response = dispatch(
        &Example,
        &unavailable,
        &wire::Call {
            method: "Analyze".into(),
            request: wire::AnalyzeRequest {
                context: Some(example_context()),
                roots: vec!["valid".into()],
            }
            .encode_to_vec(),
        },
    )
    .unwrap()
    .unwrap_err();
    assert_eq!(response.reason, "source-unavailable");
    assert!(unavailable.reads().is_empty());
}

/// A plugin that implements nothing it may leave, and so answers every
/// call as the SDK does for a plugin that serves none of it.
struct Bare;
impl Plugin for Bare {
    fn describe(&self) -> Description {
        Description::default()
    }
}

#[test]
fn a_plugin_with_no_arrangement_cannot_accept_handed_actions() {
    let host = source();
    let repo = Repository::new(&host, Some(example_context()));
    assert!(Bare.arrange(&repo, "", &[], &[]).unwrap().is_empty());
    let error = Bare
        .arrange(&repo, "", &["unknown".into()], &[])
        .unwrap_err();
    assert_eq!(error.reason, "unsupported-arrangement");
}

/// A plugin that reads no source of a repository, one that only
/// starts services or only serves release strategies, implements no analysis:
/// asked of a root, it reads nothing there and finds nothing.
#[test]
fn a_plugin_that_implements_no_analysis_reads_nothing_and_finds_nothing() {
    let host = source();
    let analyzed = dispatch(
        &Bare,
        &host,
        &wire::Call {
            method: "Analyze".into(),
            request: wire::AnalyzeRequest {
                context: Some(wire::Context {
                    profile: Profile::Development as i32,
                    ..Default::default()
                }),
                roots: vec!["pkg[1]".into()],
            }
            .encode_to_vec(),
        },
    )
    .unwrap()
    .unwrap();
    let analyzed = wire::AnalyzeResponse::decode(analyzed.as_slice()).unwrap();
    assert_eq!(
        analyzed.analyses,
        [Analysis {
            root: "pkg[1]".into(),
            ..Default::default()
        }]
    );
    assert!(host.reads().is_empty());
}

/// A strategy plugin says which strategies it serves in Describe and is
/// asked Branch and Version; a plugin that serves none says none, and
/// answers that it serves none.
#[test]
fn a_plugin_declares_and_answers_the_release_strategies_it_serves() {
    struct Strategies;
    impl Plugin for Strategies {
        fn describe(&self) -> Description {
            Description {
                name: "release".into(),
                branchings: vec!["release-flow".into()],
                versionings: vec!["conventional-commits".into()],
                ..Default::default()
            }
        }
        fn branch(&self, request: &wire::BranchRequest) -> Result<wire::BranchResponse, Error> {
            Ok(wire::BranchResponse {
                releases: request.branch == "release/1.2",
                ..Default::default()
            })
        }
        fn version(&self, request: &wire::VersionRequest) -> Result<wire::VersionResponse, Error> {
            Ok(wire::VersionResponse {
                version: request.initial.clone(),
                ..Default::default()
            })
        }
    }
    let host = source();
    let call = |plugin: &dyn Plugin, method: &str, request: Vec<u8>| {
        dispatch(
            plugin,
            &host,
            &wire::Call {
                method: method.into(),
                request,
            },
        )
        .expect("the SDK serves the method")
    };
    let described = |plugin: &dyn Plugin| {
        wire::DescribeResponse::decode(call(plugin, "Describe", Vec::new()).unwrap().as_slice())
            .unwrap()
    };
    let roles = described(&Strategies).release.expect("it said its roles");
    assert_eq!(roles.branchings, ["release-flow"]);
    assert_eq!(roles.versionings, ["conventional-commits"]);
    assert!(described(&Bare).release.is_none());

    let branch = wire::BranchRequest {
        strategy: "release-flow".into(),
        branch: "release/1.2".into(),
        ..Default::default()
    };
    let answered = call(&Strategies, "Branch", branch.encode_to_vec()).unwrap();
    assert!(
        wire::BranchResponse::decode(answered.as_slice())
            .unwrap()
            .releases
    );
    let version = wire::VersionRequest {
        strategy: "conventional-commits".into(),
        initial: "0.1.0".into(),
        ..Default::default()
    };
    let answered = call(&Strategies, "Version", version.encode_to_vec()).unwrap();
    assert_eq!(
        wire::VersionResponse::decode(answered.as_slice())
            .unwrap()
            .version,
        "0.1.0"
    );
    for method in ["Branch", "Version"] {
        let error = call(&Bare, method, Vec::new()).unwrap_err();
        assert_eq!(error.code, Code::Unimplemented, "{method}");
        assert_eq!(
            error.reason,
            format!(
                "{}-strategy-not-served",
                if method == "Branch" {
                    "branching"
                } else {
                    "versioning"
                }
            ),
            "{method}"
        );
    }
    // A strategy it does not claim is not asked of it, as a service it
    // does not claim is not: the SDK refuses it first.
    let unclaimed = wire::BranchRequest {
        strategy: "github-flow".into(),
        ..branch
    };
    let error = call(&Strategies, "Branch", unclaimed.encode_to_vec()).unwrap_err();
    assert_eq!(
        (error.code, error.reason.as_str()),
        (Code::InvalidArgument, "unclaimed-strategy")
    );
    let unclaimed = wire::VersionRequest {
        strategy: "release-flow".into(),
        ..version
    };
    let error = call(&Strategies, "Version", unclaimed.encode_to_vec()).unwrap_err();
    assert_eq!(
        (error.code, error.reason.as_str()),
        (Code::InvalidArgument, "unclaimed-strategy")
    );
}

/// A plugin that starts a service says so in Describe and is asked
/// Service for the names it claims, and for no other; one that claims
/// none, or claims a name and says nothing of how it starts it, answers
/// that it starts none.
#[test]
fn a_plugin_declares_and_answers_the_services_it_starts() {
    use frontseat_plugin_test::TestPlugin;
    struct Database;
    impl Plugin for Database {
        fn describe(&self) -> Description {
            Description {
                name: "database".into(),
                services: vec!["database".into()],
                ..Default::default()
            }
        }
        fn service(&self, _name: &str, written: &Service) -> Result<Service, Error> {
            let mut service = written.clone();
            service.image = "registry.example/db@sha256:0".into();
            Ok(service)
        }
    }
    let database = TestPlugin::new(Database);
    assert_eq!(database.describe().services, ["database"]);
    let written = Service {
        ports: [("sql".to_string(), 5432)].into(),
        ..Default::default()
    };
    let answered = database
        .service("database", written.clone())
        .answered()
        .service
        .expect("the whole service");
    assert_eq!(
        (answered.image.as_str(), &answered.ports),
        ("registry.example/db@sha256:0", &written.ports)
    );
    let refused = database
        .service("other", written.clone())
        .refused("unclaimed-service");
    assert_eq!(refused.code, Code::InvalidArgument);

    // A plugin that claims none starts none, as every plugin answers
    // what it does not serve.
    let bare = TestPlugin::new(Bare);
    assert!(bare.describe().services.is_empty());
    bare.service("database", written.clone()).unsupported();
    struct Claims;
    impl Plugin for Claims {
        fn describe(&self) -> Description {
            Description {
                name: "claims".into(),
                services: vec!["database".into()],
                ..Default::default()
            }
        }
    }
    let claims = TestPlugin::new(Claims);
    claims.service("database", written).unsupported();
}

/// A testdata directory holds fixtures, to every plugin: the SDK leaves
/// every fixture out of a plugin's interests, last, and adds nothing to
/// a plugin that has none.
#[test]
fn every_plugins_interests_leave_out_its_fixtures() {
    use frontseat_plugin::repository::NOT_FIXTURES;
    use frontseat_plugin_test::TestPlugin;
    struct Reads(Vec<String>);
    impl Plugin for Reads {
        fn describe(&self) -> Description {
            Description {
                name: "reads".into(),
                interests: self.0.clone(),
                ..Default::default()
            }
        }
    }
    let described = TestPlugin::new(Reads(vec!["**/x.toml".into()])).describe();
    assert_eq!(described.interests, ["**/x.toml", NOT_FIXTURES]);
    let already = vec![NOT_FIXTURES.into(), "**/x.toml".into()];
    let described = TestPlugin::new(Reads(already.clone())).describe();
    assert_eq!(described.interests, already);
    assert!(TestPlugin::new(Reads(Vec::new()))
        .describe()
        .interests
        .is_empty());
}

/// A refusal is read back as it was written: the SDK's decoder is the
/// inverse of its encoder, whatever of a refusal is said.
#[test]
fn a_refusal_is_read_back_as_it_was_written() {
    for error in [
        Error::invalid(
            "invalid-manifest",
            "app/go.mod",
            "it is wrong.",
            "Repair it.",
        ),
        Error::new(
            Code::Unavailable,
            "source-unavailable",
            "the source is unavailable",
        ),
        Error::not_served("workspaces", "workspaces are not built").at_line("a/package.json", 2),
    ] {
        let status = error.clone().into_status("tool");
        assert_eq!(Error::from_status(status.clone()), error);
        // One stop between what went wrong and what to do.
        assert!(!status.message.contains(".."), "{}", status.message);
    }
    // A status with no detail of a plugin's is read as far as it goes.
    let bare = frontseat_plugin::google::rpc::Status {
        code: Code::Internal as i32,
        message: "trapped".into(),
        details: Vec::new(),
    };
    assert_eq!(
        Error::from_status(bare),
        Error::new(Code::Internal, "", "trapped")
    );
}
