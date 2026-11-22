use frontseat_plugin::prelude::*;
use frontseat_plugin::toml::Value;
use frontseat_plugin_test::{Files, TestContext, TestHost, TestPlugin};

/// The fixture workspace, which the conformance harness reads too
/// (testdata/valid): two packages, hello depending on greet by path,
/// greet on one locked crate through the workspace's dependencies.
fn valid() -> Files {
    Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/valid"))
        .expect("the fixture is beside the plugin")
}

/// The fixture's lock, as cargo wrote it.
const LOCK: &str = include_str!(concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/testdata/valid/Cargo.lock"
));

fn plugin() -> TestPlugin<impl Plugin> {
    TestPlugin::new(crate::served())
}

/// A context of a repository that pins these tools.
fn context(tools: &[(&str, &str)]) -> TestContext {
    tools.iter().fold(
        plugin().context(Profile::Development),
        |context, (name, version)| context.tool(name, version),
    )
}

/// What the plugin finds under a root, asked as the host asks it.
fn analyze(host: &TestHost, context: &TestContext, root: &str) -> Result<Analysis, Error> {
    plugin()
        .analyze(host, context, &[root])
        .result
        .map(|mut got| got.analyses.remove(0))
}

/// What the plugin finds of hello in a repository of these files, with
/// rust pinned.
fn analyze_hello(files: Files) -> Result<Analysis, Error> {
    analyze(
        &TestHost::new(files),
        &context(&[("rust", "1.99.1")]),
        "hello",
    )
}

#[test]
fn a_package_is_a_project_requiring_what_it_depends_on_by_path() {
    let host = TestHost::new(valid());
    let repo = context(&[("rust", "1.99.1")]);
    assert_eq!(plugin().describe().name, "cargo");
    let got = analyze(&host, &repo, "hello").unwrap();
    assert_eq!(got.projects.len(), 1);
    let project = &got.projects[0];
    assert_eq!(
        (
            project.name.as_str(),
            project.kind.as_str(),
            project.manifest.as_str()
        ),
        ("hello", "cargo-package", "hello/Cargo.toml")
    );
    assert_eq!(
        project.provides[0].coordinate.as_ref().unwrap().name,
        "hello"
    );
    assert_eq!(project.requires.len(), 1);
    assert_eq!(
        project.requires[0].coordinate.as_ref().unwrap(),
        &Coordinate {
            ecosystem: "cargo".into(),
            name: "greet".into()
        }
    );
    assert_eq!(project.requires[0].origin.as_ref().unwrap().line, 7);
    // At the version its lock holds it at.
    assert_eq!(project.requires[0].version, "0.1.0");
    assert!(project.workspace_files.is_empty() && project.always_arranged);
    let names: Vec<&str> = project.actions.iter().map(|a| a.name.as_str()).collect();
    assert_eq!(
        names,
        [
            "fetch/cfg-if@1.0.0",
            "fetch/itoa@1.0.18",
            "compile",
            "test",
            "lint",
            "format"
        ]
    );
    // Each crate of the lock the package depends on is a shared fetch of
    // its own: it downloads the crate's archive by the lock's checksum,
    // unpacks it into cargo's home, and writes the checksum file cargo's
    // directory source reads beside it, with the configuration that
    // points cargo there; it runs no tool and reads no source.
    let itoa = &project.actions[1];
    assert!(itoa.as_shared_fetch().is_some() && itoa.command.is_none() && itoa.tools.is_empty());
    assert!(itoa.inputs.is_empty());
    assert_eq!(itoa.as_shared_fetch().unwrap().downloads.len(), 1);
    assert_eq!(
        (
            itoa.as_shared_fetch().unwrap().downloads[0].url.as_str(),
            itoa.as_shared_fetch().unwrap().downloads[0].sha256.as_str(),
            itoa.as_shared_fetch().unwrap().downloads[0].into.as_str()
        ),
        (
            "https://static.crates.io/crates/itoa/1.0.18/download",
            "8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682",
            "~/.cargo/vendor/"
        )
    );
    let files: Vec<(&str, &str)> = itoa
        .as_shared_fetch()
        .unwrap()
        .files
        .iter()
        .map(|f| (f.path.as_str(), std::str::from_utf8(&f.content).unwrap()))
        .collect();
    assert_eq!(
        files,
        [
            (
                "~/.cargo/vendor/itoa-1.0.18/.cargo-checksum.json",
                "{\"files\":{},\"package\":\"8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682\"}"
            ),
            (
                "~/.cargo/config.toml",
                "[source.crates-io]\nreplace-with = \"vendored\"\n\n[source.vendored]\ndirectory = \".cargo/vendor\"\n"
            ),
        ]
    );
    let outputs: Vec<&str> = itoa.outputs.iter().map(|o| o.path.as_str()).collect();
    assert_eq!(
        outputs,
        ["~/.cargo/vendor/itoa-1.0.18/", "~/.cargo/config.toml"]
    );
    for action in &project.actions[2..] {
        assert!(
            action.inputs.contains(&Input::dependencies("{root}")),
            "{}",
            action.name
        );
        assert_eq!(action.inputs[0], Input::set("package"));
        assert_eq!(action.tools, vec!["rust".to_string()]);
        // Nothing names the proxy's registry: the crates are in the home.
        assert!(
            !action
                .command
                .as_ref()
                .unwrap()
                .arguments
                .iter()
                .any(|a| a == "--config"),
            "{}",
            action.name
        );
    }
    let format = project.actions.iter().find(|a| a.name == "format").unwrap();
    assert!(
        format.as_check().is_some()
            && format.as_check().unwrap().fix.is_some()
            && format.as_check().unwrap().rewrites == ["**/*.rs"]
            && format.inputs == [Input::set("package"), Input::dependencies("{root}")]
    );
    let compile = project
        .actions
        .iter()
        .find(|a| a.name == "compile")
        .unwrap();
    assert!(
        compile
            .command
            .as_ref()
            .unwrap()
            .arguments
            .contains(&"--offline".to_string())
            && compile.inputs
                == [
                    Input::set("package"),
                    Input::output("fetch/cfg-if@1.0.0"),
                    Input::output("fetch/itoa@1.0.18"),
                    Input::dependencies("{root}")
                ]
    );
    assert_eq!(
        project.input_sets[0].inputs,
        [Input::source("**"), Input::source("!target/**")]
    );
    // greet inherits itoa from the workspace, a version: nothing of the repository.
    let greet = analyze(&host, &repo, "greet").unwrap();
    assert!(greet.projects[0].requires.is_empty());
    // greet needs its one crate, and not hello's.
    let greet_names: Vec<&str> = greet.projects[0]
        .actions
        .iter()
        .map(|a| a.name.as_str())
        .collect();
    assert_eq!(
        greet_names,
        ["fetch/itoa@1.0.18", "compile", "test", "lint", "format"]
    );
    // The root is a workspace alone: no project.
    assert!(analyze(&host, &repo, "").unwrap().projects.is_empty());
}

/// A crate from a git repository or another registry is one only cargo
/// can fetch: the package keeps one fetch of its whole lock, by cargo,
/// through the proxy's registry, which every action of it then names.
#[test]
fn a_lock_with_a_crate_cargo_alone_can_fetch_is_fetched_whole() {
    let files = valid().with(
        "Cargo.lock",
        LOCK.replace(
            "source = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"baf1de4339761588bc0619e3cbc0120ee582ebb74b53b4efbf79117bd2da40fd\"",
            "source = \"git+https://github.com/rust-lang/cfg-if?rev=abc#abc\"",
        ),
    );
    let host = TestHost::new(files);
    let repo = context(&[("rust", "1.99.1")]);
    let got = analyze(&host, &repo, "hello").unwrap();
    let names: Vec<&str> = got.projects[0]
        .actions
        .iter()
        .map(|a| a.name.as_str())
        .collect();
    assert_eq!(names, ["fetch", "compile", "test", "lint", "format"]);
    let fetch = &got.projects[0].actions[0];
    assert!(fetch.is_fetch());
    assert_eq!(fetch.tools, ["rust"]);
    assert_eq!(
        fetch.command.as_ref().unwrap().arguments,
        [
            "cargo",
            "fetch",
            "--locked",
            "--config",
            "source.crates-io.replace-with=\"frontseat\""
        ]
    );
    let compile = &got.projects[0].actions[1];
    assert_eq!(
        compile.inputs,
        [
            Input::set("package"),
            Input::output("fetch"),
            Input::dependencies("{root}")
        ]
    );
    assert!(compile
        .command
        .as_ref()
        .unwrap()
        .arguments
        .contains(&"--config".to_string()));
    // greet depends on itoa alone, from crates.io: its crates are fetched
    // one by one all the same.
    let greet = analyze(&host, &repo, "greet").unwrap();
    assert_eq!(greet.projects[0].actions[0].name, "fetch/itoa@1.0.18");
}

#[test]
fn production_compiles_in_release() {
    let host = TestHost::new(valid());
    let production = plugin().context(Profile::Production).tool("rust", "1.99.1");
    let got = analyze(&host, &production, "hello").unwrap();
    let compile = got.projects[0]
        .actions
        .iter()
        .find(|a| a.name == "compile")
        .unwrap();
    assert!(compile
        .command
        .as_ref()
        .unwrap()
        .arguments
        .contains(&"--release".to_string()));
}

#[test]
fn the_workspace_manifest_is_narrowed_to_the_packages_present() {
    let host = TestHost::new(valid());
    let repo = context(&[("rust", "1.99.1")]);
    let handed = [Handoff {
        coordinate: Some(Coordinate {
            ecosystem: "cargo".into(),
            name: "greet".into(),
        }),
        into: "../greet".into(),
    }];
    let files = plugin()
        .arrange(&host, &repo, "hello", &["compile"], &handed)
        .answered()
        .files;
    assert_eq!(files.len(), 2);
    assert_eq!(
        (files[0].path.as_str(), files[1].path.as_str()),
        ("Cargo.toml", "Cargo.lock")
    );
    // Both packages present: the lock is the whole lock, as cargo wrote it.
    assert_eq!(String::from_utf8(files[1].content.clone()).unwrap(), LOCK);
    let content = String::from_utf8(files[0].content.clone()).unwrap();
    let table: frontseat_plugin::repository::TomlTable = content.parse().unwrap();
    let workspace = table["workspace"].as_table().unwrap();
    let members: Vec<&str> = workspace["members"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap())
        .collect();
    assert_eq!(members, ["greet", "hello"]);
    assert_eq!(
        workspace["package"]["edition"],
        Value::String("2021".into())
    );
    assert_eq!(workspace["dependencies"]["itoa"], Value::String("1".into()));
    assert!(workspace.get("exclude").is_none());
    // Alone, a package's manifest names only it, and its lock holds it
    // and what it depends on: hello is gone, itoa stays with greet.
    let alone = plugin()
        .arrange(&host, &repo, "greet", &[], &[])
        .answered()
        .files;
    let table: frontseat_plugin::repository::TomlTable =
        String::from_utf8(alone[0].content.clone())
            .unwrap()
            .parse()
            .unwrap();
    assert_eq!(table["workspace"]["members"].as_array().unwrap().len(), 1);
    let lock = String::from_utf8(alone[1].content.clone()).unwrap();
    assert!(
        lock.contains("name = \"greet\"")
            && lock.contains("name = \"itoa\"")
            && !lock.contains("name = \"hello\""),
        "{lock}"
    );
    assert!(
        lock.starts_with("# This file is automatically @generated by Cargo.\n")
            && lock
                .ends_with("8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682\"\n"),
        "{lock}"
    );
}

#[test]
fn a_repository_pinning_no_rust_is_refused_at_arrange() {
    let host = TestHost::new(valid());
    let err = plugin()
        .arrange(&host, &context(&[]), "hello", &[], &[])
        .refused("unpinned-tool");
    assert_eq!(err.path, "hello/Cargo.toml");
}

#[test]
fn findings_are_read_from_what_clippy_and_rustfmt_print() {
    let host = TestHost::new(valid());
    let repo = context(&[("rust", "1.99.1")]);
    let plugin = plugin();
    let lint = plugin
        .report(&host, &repo, "hello", "lint", &Printed::new(101, Vec::new(), b"    Checking hello v0.1.0\nhello/src/main.rs:2:9: warning: unused variable: `x`\nwarning: `hello` (bin \"hello\") generated 1 warning\nerror: could not compile `hello` (bin \"hello\") due to 1 previous error; 1 warning emitted\n".to_vec()))
        .answered()
        .diagnostics;
    assert_eq!(lint.len(), 1);
    assert_eq!(
        (lint[0].rule_id.as_str(), lint[0].message.as_str()),
        ("cargo/clippy", "unused variable: `x`")
    );
    let at = lint[0].source_location.as_ref().unwrap();
    assert_eq!((at.path.as_str(), at.line), ("hello/src/main.rs", 2));
    let format = plugin
        .report(&host, &repo, "hello", "format", &Printed::new(1, b"Diff in /work/repo/hello/src/main.rs:1:\n-fn main(){}\n+fn main() {}\nDiff in /work/repo/hello/src/main.rs:4:\n-x\n+y\n".to_vec(), Vec::new()))
        .answered()
        .diagnostics;
    assert_eq!(format.len(), 1);
    assert_eq!(
        (
            format[0].rule_id.as_str(),
            format[0].source_location.as_ref().unwrap().path.as_str()
        ),
        ("cargo/format", "hello/src/main.rs")
    );
}

#[test]
fn a_malformed_manifest_is_refused_with_its_line() {
    let files = valid().with("hello/Cargo.toml", "[package\nname = \"hello\"\n");
    let err = analyze_hello(files).unwrap_err();
    assert_eq!(
        (err.reason.as_str(), err.path.as_str()),
        ("invalid-toml", "hello/Cargo.toml")
    );
    assert!(err.line >= 1, "{err:?}");
}

#[test]
fn a_package_without_a_lock_or_outside_its_workspace_is_refused() {
    let files = valid().without("Cargo.lock");
    assert_eq!(analyze_hello(files).unwrap_err().reason, "missing-lock");
    let files = valid().with("Cargo.toml", "[workspace]\nmembers = [\"greet\"]\n");
    assert_eq!(analyze_hello(files).unwrap_err().reason, "not-a-member");
}

/// A package the workspace excludes is no member of it, and is the root
/// of its own, as cargo has it: it is built alone, from a lock of its
/// own. Following the refusal of a package that is no member, by
/// excluding it, is accepted.
#[test]
fn a_package_the_workspace_excludes_is_built_as_a_package_of_its_own() {
    let refused = analyze_hello(valid().with("Cargo.toml", "[workspace]\nmembers = [\"greet\"]\n"))
        .unwrap_err();
    assert!(refused.help.contains("exclude"), "{refused:?}");
    for exclude in ["hello", "hello/"] {
        let files = valid()
            .with(
                "Cargo.toml",
                format!("[workspace]\nmembers = [\"greet\"]\nexclude = [\"{exclude}\"]\n"),
            )
            .with(
                "hello/Cargo.toml",
                "[package]\nname = \"hello\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
            )
            .with(
                "hello/Cargo.lock",
                "version = 4\n\n[[package]]\nname = \"hello\"\nversion = \"0.1.0\"\n",
            );
        let got = analyze_hello(files).unwrap();
        let project = &got.projects[0];
        assert_eq!(project.name, "hello", "{exclude}");
        // Its own root: no workspace manifest is written for it.
        assert!(!project.always_arranged, "{exclude}");
        assert!(project.workspace_files.is_empty(), "{exclude}");
    }
}

/// The valid workspace with a member `module`, a package named
/// `my-module` whose manifest ends with what is given.
fn with_module(rest: &str) -> TestHost {
    TestHost::new(
        valid()
            .with("Cargo.toml", "[workspace]\nmembers = [\"greet\", \"hello\", \"module\"]\n[workspace.dependencies]\nitoa = \"1\"\n")
            .with("module/Cargo.toml", format!("[package]\nname = \"my-module\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n{rest}"))
            .with("module/src/lib.rs", "pub fn f() {}\n"),
    )
}

/// A context that pins rust and the standard library for wasm, with
/// the option `wasm` set as told.
fn wasm_context(wasm: bool) -> TestContext {
    context(&[("rust", "1.99.1"), ("rust-std-wasm32-wasip1", "1.99.1")])
        .option("wasm", frontseat_plugin::Value::Bool(wasm))
}

#[test]
fn a_cdylib_is_built_for_wasm_where_the_option_says_so() {
    let host = with_module("[lib]\ncrate-type = [\"cdylib\"]\n");
    let got = analyze(&host, &wasm_context(true), "module").unwrap();
    let wasm = got.projects[0]
        .actions
        .iter()
        .find(|a| a.name == "package/wasm32-wasip1")
        .expect("a wasm action");
    assert_eq!(wasm.tools, ["rust", "rust-std-wasm32-wasip1"]);
    assert_eq!(
        wasm.outputs[0].path,
        "target/wasm32-wasip1/release/my_module.wasm"
    );
    // The control: the option unset, nothing is built for wasm, whatever
    // the repository pins.
    let got = analyze(&host, &wasm_context(false), "module").unwrap();
    assert!(
        got.projects[0]
            .actions
            .iter()
            .all(|a| a.name != "package/wasm32-wasip1"),
        "built for wasm with the option unset"
    );
    // A library that is no cdylib is built for no wasm.
    assert!(analyze(&host, &wasm_context(true), "hello")
        .unwrap()
        .projects[0]
        .actions
        .iter()
        .all(|a| a.name != "package/wasm32-wasip1"));
    // Set, with no standard library for wasm pinned, it is refused, and
    // not built for nothing.
    let unpinned =
        context(&[("rust", "1.99.1")]).option("wasm", frontseat_plugin::Value::Bool(true));
    let refused = analyze(&host, &unpinned, "module").unwrap_err();
    assert_eq!(
        (refused.reason.as_str(), refused.path.as_str()),
        ("unpinned-tool", "module/Cargo.toml")
    );
}

#[test]
fn a_packages_cdylib_examples_are_built_for_wasm_together() {
    let host = with_module("[[example]]\nname = \"first\"\ncrate-type = [\"cdylib\"]\n\n[[example]]\nname = \"tool\"\n\n[[example]]\nname = \"second\"\ncrate-type = [\"cdylib\"]\n");
    let got = analyze(&host, &wasm_context(true), "module").unwrap();
    let examples = got.projects[0]
        .actions
        .iter()
        .find(|a| a.name == "compile/wasm32-wasip1-examples")
        .expect("an action that builds the examples for wasm");
    assert!(examples
        .command
        .as_ref()
        .unwrap()
        .arguments
        .contains(&"--examples".to_string()));
    // An example that is a program is built, and is no module.
    let left: Vec<&str> = examples.outputs.iter().map(|o| o.path.as_str()).collect();
    assert_eq!(
        left,
        [
            "target/wasm32-wasip1/release/examples/first.wasm",
            "target/wasm32-wasip1/release/examples/second.wasm"
        ]
    );
    // The control: with the option unset, none.
    assert!(analyze(&host, &wasm_context(false), "module")
        .unwrap()
        .projects[0]
        .actions
        .iter()
        .all(|a| a.name != "compile/wasm32-wasip1-examples"));
}

/// A dependency a package has only on some targets is one all the same:
/// cargo builds it where the target is, and the graph holds it wherever
/// a table of `[target.'cfg(…)']` names it by path, of every kind.
#[test]
fn a_dependency_by_path_for_some_targets_is_required() {
    for table in ["dependencies", "dev-dependencies", "build-dependencies"] {
        let files = valid().with(
            "hello/Cargo.toml",
            format!("[package]\nname = \"hello\"\nedition.workspace = true\nversion.workspace = true\n\n[dependencies]\ncfg-if = \"1\"\n\n[target.'cfg(unix)'.{table}]\ngreet = {{ path = \"../greet\" }}\n"),
        );
        let got = analyze_hello(files).unwrap();
        let requires: Vec<(&str, i32)> = got.projects[0]
            .requires
            .iter()
            .map(|r| {
                (
                    r.coordinate.as_ref().unwrap().name.as_str(),
                    r.origin.as_ref().unwrap().line,
                )
            })
            .collect();
        assert_eq!(
            requires,
            [("greet", 10)],
            "under [target.'cfg(unix)'.{table}]"
        );
    }
}
