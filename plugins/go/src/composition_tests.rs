use frontseat_plugin::prelude::*;
use frontseat_plugin_test::{Files, Read, TestHost, TestPlugin};

/// What testdata/ holds, spelled here: the repository hides its fixtures
/// from a run (.frontseatignore), so a test built on the grid cannot
/// read them. A malformed go.mod names no module; an unlocked module has
/// no go.sum.
fn files(malformed: bool, locked: bool) -> Files {
    let go_mod: &str = if malformed {
        "go 1.27\n"
    } else {
        "module example.com/hello\n\ngo 1.27\n\nrequire example.com/dep v1.0.0\n"
    };
    let files = Files::new()
        .with("app/go.mod", go_mod)
        .with("app/main.go", "package main\nfunc main() {}\n")
        .with("app/main_test.go", "package main\n")
        .with("app/internal/ledger/ledger.go", "package ledger\n")
        .with(
            "app/internal/ledger/ledger_test.go",
            "package ledger_test\n",
        )
        // A directory with nothing but a test of another system's: the
        // go command builds it no program on a worker.
        .with(
            "app/internal/other/other_windows_test.go",
            "package other\n",
        )
        // A module nested in it, which is a module of its own: nothing
        // of it is its tests'.
        .with("app/tools/go.mod", "module example.com/tools\n\ngo 1.27\n")
        .with("app/tools/tool.go", "package tools\n")
        // A fixture's go.mod, which is no module of the repository's.
        .with(
            "app/testdata/fixture/go.mod",
            "module example.com/fixture\n",
        );
    if locked {
        files.with(
            "app/go.sum",
            "example.com/dep v1.0.0 h1:archive\nexample.com/dep v1.0.0/go.mod h1:metadata\n",
        )
    } else {
        files
    }
}

fn plugin() -> TestPlugin<impl Plugin> {
    TestPlugin::new(crate::served())
}

#[test]
fn served_plugin_composes_manifest_inputs_and_profile_actions() {
    let plugin = plugin();
    assert_eq!(plugin.describe().name, "go");
    for profile in [Profile::Development, Profile::Production] {
        let host = TestHost::new(files(false, true));
        let got = plugin
            .analyze(&host, &plugin.context(profile), &["app"])
            .answered();
        assert_eq!(got.analyses.len(), 1);
        let got = &got.analyses[0];
        assert_eq!(got.projects.len(), 1);
        let project = &got.projects[0];
        assert_eq!(
            (project.name.as_str(), project.manifest.as_str()),
            ("example.com/hello", "app/go.mod")
        );
        assert_eq!(project.requires[0].origin.as_ref().unwrap().line, 5);
        // It requires the module at the version its go.mod says, which
        // its go.sum locks.
        assert_eq!(project.requires[0].version, "v1.0.0");
        assert_eq!(project.provides[0].sources, "module");
        assert!(project.input_sets[1]
            .inputs
            .contains(&Input::source("!**/*_test.go")));
        let names: Vec<_> = project.actions.iter().map(|a| a.name.as_str()).collect();
        assert_eq!(
            names,
            [
                "fetch/example.com/dep@v1.0.0",
                "lint",
                "format",
                "compile",
                // The programs of its tests, then a test for each package
                // that holds a test file: the module's own and two
                // beneath it.
                "test-build",
                "test/.",
                "test/internal/ledger",
                "test/internal/other",
                // The module's own directory says package main: a program.
                "run/hello",
            ]
        );
        let fetch = &project.actions[0];
        assert!(fetch.shared && fetch.fetch && fetch.inputs.is_empty());
        assert_eq!(
            fetch.command.as_ref().unwrap().arguments,
            ["go", "mod", "download", "-x", "example.com/dep@v1.0.0"]
        );
        // It is told the module's own lines of the lock, and nothing of
        // the project.
        let told: Vec<_> = fetch
            .files
            .iter()
            .map(|file| {
                (
                    file.path.as_str(),
                    String::from_utf8(file.content.clone()).unwrap(),
                )
            })
            .collect();
        assert_eq!(
            told,
            [
                ("go.mod", "module fetch.invalid\n\ngo 1.21\n".to_string()),
                (
                    "go.sum",
                    "example.com/dep v1.0.0 h1:archive\nexample.com/dep v1.0.0/go.mod h1:metadata\n"
                        .to_string()
                ),
            ]
        );
        let kept: Vec<_> = fetch.outputs.iter().map(|o| o.path.as_str()).collect();
        assert_eq!(
            kept,
            [
                "~/go/pkg/mod/cache/download/example.com/dep/@v/v1.0.0.info",
                "~/go/pkg/mod/cache/download/example.com/dep/@v/v1.0.0.mod",
                "~/go/pkg/mod/cache/download/example.com/dep/@v/v1.0.0.zip",
                "~/go/pkg/mod/cache/download/example.com/dep/@v/v1.0.0.ziphash",
                "~/go/pkg/mod/example.com/dep@v1.0.0/",
            ]
        );
        // A program reads what the compile left, and nothing of a fetch.
        let program = project.actions.last().unwrap();
        assert_eq!(program.inputs, [Input::output("compile")]);
        assert!(program.program);
        for built in &project.actions[1..5] {
            assert!(
                built
                    .inputs
                    .contains(&Input::output("fetch/example.com/dep@v1.0.0")),
                "{} reads {:?}, and not all the fetch left",
                built.name,
                built.inputs
            );
            assert!(!built.shared, "{}", built.name);
        }
        assert_eq!(
            project.actions[3]
                .command
                .as_ref()
                .unwrap()
                .arguments
                .contains(&"-gcflags=all=-N -l".to_string()),
            profile == Profile::Development
        );
        let build = &project.actions[4];
        assert!(!build.test);
        assert_eq!(
            build.command.as_ref().unwrap().arguments,
            [
                "go",
                "test",
                "-c",
                "-trimpath",
                "-o",
                "_tests/0/",
                ".",
                "./internal/ledger/...",
                "./internal/other/..."
            ]
        );
        let left: Vec<_> = build
            .outputs
            .iter()
            .map(|o| (o.path.as_str(), o.optional))
            .collect();
        assert_eq!(
            left,
            [
                ("_tests/0/hello.test", true),
                ("_tests/0/ledger.test", true),
                ("_tests/0/other.test", true)
            ]
        );
        // The third has nothing to do on a worker, where the go command
        // builds it no program: its one file is named for another system.
        let other = &project.actions[7];
        assert!(other.test && other.idle_without_inputs);
        assert_eq!(other.inputs[0], Input::output("_tests/0/other.test"));
        // A test runs its own program in its package's directory, and
        // reads that program, what stands beside its package and its
        // fixtures: nothing of a fetch, and nothing of another package.
        let (own, ledger) = (&project.actions[5], &project.actions[6]);
        assert!(own.test && ledger.test);
        assert!(own.idle_without_inputs && ledger.idle_without_inputs);
        let command = own.command.as_ref().unwrap();
        assert_eq!(
            (command.arguments.as_slice(), command.directory.as_str()),
            (
                [
                    "./_tests/0/hello.test".to_string(),
                    "-test.paniconexit0".to_string(),
                    "-test.timeout=10m".to_string()
                ]
                .as_slice(),
                ""
            )
        );
        assert_eq!(
            own.inputs,
            [
                Input::output("_tests/0/hello.test"),
                // Its package's directory, but what others keep beneath
                // it, and the packages beneath it.
                Input::source("**"),
                Input::source("!**/vendor/**"),
                Input::source("!**/node_modules/**"),
                Input::source("!internal/ledger/**"),
                Input::source("!internal/other/**"),
                Input::source("!tools/**"),
                Input::source("testdata/**"),
                // What the compile is given of the module's lock, for a
                // test that runs the go command.
                Input::output("fetch/example.com/dep@v1.0.0")
            ]
        );
        let command = ledger.command.as_ref().unwrap();
        assert_eq!(
            (command.arguments.as_slice(), command.directory.as_str()),
            (
                [
                    "../../_tests/0/ledger.test".to_string(),
                    "-test.paniconexit0".to_string(),
                    "-test.timeout=10m".to_string()
                ]
                .as_slice(),
                "internal/ledger"
            )
        );
        assert_eq!(
            ledger.inputs,
            [
                Input::output("_tests/0/ledger.test"),
                Input::source("internal/ledger/**"),
                Input::source("!internal/ledger/**/vendor/**"),
                Input::source("!internal/ledger/**/node_modules/**"),
                Input::source("internal/ledger/testdata/**"),
                // What the compile is given of the module's lock, for a
                // test that runs the go command.
                Input::output("fetch/example.com/dep@v1.0.0")
            ]
        );
        assert!(
            !own.inputs.iter().any(|input| matches!(
                input.kind,
                Some(frontseat_plugin::wire::input::Kind::Dependency(_))
            )) && ledger.outputs.is_empty()
        );
        assert!(project.actions[1].check && project.actions[2].check);
        assert_eq!(project.actions[2].rewrites, ["**/*.go"]);
        assert_eq!(
            project.actions[2].command.as_ref().unwrap().arguments,
            ["gofmt", "-l", "."]
        );
        assert_eq!(
            project.actions[2].fix.as_ref().unwrap().arguments,
            ["gofmt", "-w", "."]
        );
        // What it read: the module's go.mod and go.sum and its Go files,
        // each listed beneath it and none beneath a fixture.
        let reads = host.reads();
        assert!(reads.contains(&Read::Glob {
            directory: "app".into(),
            patterns: vec!["**/*.go".into(), "!**/testdata/**".into()],
            found: true
        }));
        assert!(reads.contains(&Read::File {
            path: "app/go.sum".into(),
            found: true
        }));
        assert!(!reads.iter().any(|read| matches!(
            read,
            Read::File { path, .. } if path.contains("testdata")
        )));
    }
}
#[test]
fn served_plugin_reports_invalid_manifests_and_missing_projects() {
    let plugin = plugin();
    let host = TestHost::new(files(true, false));
    let context = plugin.context(Profile::Development);
    let err = plugin
        .analyze(&host, &context, &["app"])
        .refused("invalid-manifest");
    assert_eq!(err.path, "app/go.mod");
    assert!(plugin
        .analyze(&host, &context, &["absent"])
        .answered()
        .analyses[0]
        .projects
        .is_empty());
}
#[test]
fn served_plugin_arranges_local_dependencies_using_the_pinned_tool() {
    let plugin = plugin();
    let host = TestHost::new(files(false, false));
    let unpinned = plugin.context(Profile::Development);
    let pinned = unpinned.clone().tool("go", "1.27.3");
    let handed = Handed {
        coordinate: Some(Coordinate {
            ecosystem: "go".into(),
            name: "example.com/dep".into(),
        }),
        into: ".frontseat/go/example.com/dep".into(),
    };
    let got = plugin
        .arrange(&host, &pinned, "app", &["compile"], &[handed])
        .answered()
        .files;
    assert_eq!(got.len(), 1);
    assert_eq!(got[0].path, "app/go.work");
    assert_eq!(got[0].content,b"go 1.27.3\n\nuse .\n\nreplace (\n\texample.com/dep => ./.frontseat/go/example.com/dep\n)\n");
    plugin
        .arrange(&host, &unpinned, "app", &[], &[])
        .refused("unpinned-tool");
}

/// A module's programs are linked with the version where the `version`
/// option names the variables that say it: the compile alone is given
/// it, and where the option names none, nothing is.
#[test]
fn served_plugin_links_the_version_where_the_option_names_its_variables() {
    let plugin = plugin();
    let host = TestHost::new(files(false, true));
    let actions = |context: &frontseat_plugin_test::TestContext| {
        plugin.analyze(&host, context, &["app"]).answered().analyses[0].projects[0]
            .actions
            .clone()
    };
    let named = plugin.context(Profile::Production).option(
        "version",
        Value::StringList(vec![
            "example.com/hello/internal/build.Version=version".to_string(),
            "example.com/hello/internal/build.Commit=commit".to_string(),
        ]),
    );
    let versioned: Vec<_> = actions(&named)
        .into_iter()
        .filter(|action| action.versioned)
        .collect();
    assert_eq!(versioned.len(), 1, "only the compile is versioned");
    assert_eq!(versioned[0].name, "compile");
    assert_eq!(
        versioned[0].command.as_ref().unwrap().arguments,
        [
            "go",
            "install",
            "-trimpath",
            "-ldflags=-X example.com/hello/internal/build.Version={frontseat.version} -X example.com/hello/internal/build.Commit={frontseat.commit}",
            "./...",
        ]
    );
    // Control: with no variable named, the compile is as it was.
    let plain = actions(&plugin.context(Profile::Production));
    assert!(plain.iter().all(|action| !action.versioned));
    let compile = plain
        .iter()
        .find(|action| action.name == "compile")
        .unwrap();
    assert_eq!(
        compile.command.as_ref().unwrap().arguments,
        ["go", "install", "-trimpath", "./..."]
    );
    // A variable that is none, or set to nothing a release has, is the
    // module's author's mistake.
    for wrong in [
        "example.com/hello/internal/build.Version",
        "example.com/hello/internal/build.Version=date",
        "Version=version",
        "example.com/hello/internal/build.Version -X main.other=version",
        "main.version=version,main.version=tag",
    ] {
        let context = plugin.context(Profile::Production).option(
            "version",
            Value::StringList(wrong.split(',').map(str::to_string).collect()),
        );
        let err = plugin
            .analyze(&host, &context, &["app"])
            .refused("invalid-version-variable");
        assert_eq!(err.path, "app/go.mod", "{wrong}");
    }
}

/// What a module ships is each of its programs, made for each platform
/// its entity ships for: a package for a program and a platform, built
/// for that system and named for both, given the version where the
/// module's variables are named, and declared for production alone.
/// The control is a development build, and a module of no platform.
#[test]
fn served_plugin_packages_each_program_for_each_platform_it_ships_for() {
    let plugin = plugin();
    let host = TestHost::new(files(false, true));
    let packages = |context: &frontseat_plugin_test::TestContext| -> Vec<Action> {
        plugin.analyze(&host, context, &["app"]).answered().analyses[0].projects[0]
            .actions
            .iter()
            .filter(|action| action.package)
            .cloned()
            .collect()
    };
    let shipped = |profile| {
        plugin
            .context(profile)
            .platform("linux/amd64")
            .platform("darwin/arm64")
            .option(
                "version",
                Value::StringList(vec!["main.version=version".to_string()]),
            )
    };
    let made = packages(&shipped(Profile::Production));
    assert_eq!(
        made.iter().map(|a| a.name.as_str()).collect::<Vec<_>>(),
        ["package/hello-linux-amd64", "package/hello-darwin-arm64"]
    );
    let linux = &made[0];
    let command = linux.command.as_ref().unwrap();
    assert_eq!(
        command.arguments,
        [
            "go",
            "build",
            "-trimpath",
            "-ldflags=-X main.version={frontseat.version}",
            "-o",
            "_package/hello-linux-amd64",
            ".",
        ]
    );
    assert_eq!(command.environment["GOOS"], "linux");
    assert_eq!(command.environment["GOARCH"], "amd64");
    assert_eq!(command.environment["CGO_ENABLED"], "0");
    assert!(linux.versioned && !linux.test && !linux.check);
    assert_eq!(
        linux
            .outputs
            .iter()
            .map(|o| (o.path.as_str(), o.optional))
            .collect::<Vec<_>>(),
        [("_package/hello-linux-amd64", false)]
    );
    assert_eq!(
        made[1].command.as_ref().unwrap().environment["GOOS"],
        "darwin"
    );
    // Controls: nothing is packaged for development, nor for a module
    // whose entity ships for no platform.
    assert!(packages(&shipped(Profile::Development)).is_empty());
    assert!(packages(&plugin.context(Profile::Production)).is_empty());
}
