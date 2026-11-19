use crate::inputs::Tested;
use crate::manifest::{Entry, Locked};
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{join, literal};

/// What the two profiles mean for the go command. Development leaves the
/// compiler's optimisations and inlining off, so that a debugger sees the
/// program as it is written, given where its source is (a program names
/// its files by their packages). Production is compiled with them.
///
/// A module is installed, which compiles every package of it and leaves
/// each program where the go command keeps programs: go/bin in the
/// action's home. Either way no path of the machine that built it is
/// left in a program, so that one is the same wherever it is built, and
/// whatever is made from it is known by the same name.
fn compile(profile: Profile) -> &'static [&'static str] {
    match profile {
        Profile::Production => &["go", "install", "-trimpath"],
        _ => &["go", "install", "-trimpath", "-gcflags=all=-N -l"],
    }
}

/// Where the go command installs a module's programs, in the action's
/// home: its GOPATH there, which nothing sets to anything else. Each is
/// named after its directory, as the go command names it, and of two it
/// names alike it leaves one.
///
/// Which of a module's packages are programs is the go command's to
/// say, by its build constraints, its go.mod and the worker it runs on,
/// and a module may have none: so the directory is one a compile may
/// not leave.
const PROGRAMS: &str = "~/go/bin/";

/// The most bytes the argument that names a shard's tests may hold.
const SELECTION_AT_MOST: usize = 100_000;

/// Where a module's test programs are left, beneath the module: a
/// directory the go command reads no package from, its name beginning
/// with an underscore. Each program is in a directory of a number,
/// since the go command names a test program after its package's
/// directory and writes two of one name nowhere together.
const TESTS: &str = "_tests";

/// Where the modules a module requires of the workspace are placed, each
/// in a directory of its own path, relative to the module.
const REQUIRED: &str = ".frontseat/go";

/// A go command over the whole module, with the build tags the module is
/// built with.
fn over_module(command: &[&str], tags: &[String]) -> Vec<String> {
    let mut arguments = command.iter().map(|v| v.to_string()).collect::<Vec<_>>();
    if !tags.is_empty() {
        arguments.push("-tags".into());
        arguments.push(tags.join(","));
    }
    arguments.push("./...".into());
    arguments
}

/// What a fetch leaves of the go command's module cache: the files of its
/// own module at its own version, by the cache's own names, and nothing
/// the cache keeps of a module at every version (its list of versions,
/// its locks), which another fetch of another version of it also writes.
/// A fetch of an archive leaves the archive, its hash and the module
/// unpacked; a fetch of a go.mod alone leaves that.
fn kept(entry: &Entry) -> Vec<String> {
    let (module, version) = (escaped(&entry.path), escaped(&entry.version));
    let at = format!("~/go/pkg/mod/cache/download/{module}/@v/{version}");
    let mut kept = vec![format!("{at}.info"), format!("{at}.mod")];
    if entry.archive {
        kept.push(format!("{at}.zip"));
        kept.push(format!("{at}.ziphash"));
        kept.push(format!("~/go/pkg/mod/{module}@{version}/"));
    }
    kept
}

/// A module's path or version as the go command names it in its cache:
/// each capital letter as an exclamation mark and the letter in lower
/// case (golang.org/x/mod/module.EscapePath), so that a cache on a disk
/// that ignores case keeps two modules apart.
fn escaped(name: &str) -> String {
    let mut out = String::with_capacity(name.len());
    for c in name.chars() {
        if c.is_ascii_uppercase() {
            out.push('!');
            out.push(c.to_ascii_lowercase());
        } else {
            out.push(c);
        }
    }
    out
}

/// The module a fetch is in: nothing, for a go it is not worth changing.
const GO_MOD: &[u8] = b"module fetch.invalid\n\ngo 1.21\n";

/// What a fetch is told of the module it fetches: a module of its own
/// that is nothing, and the lines of the project's go.sum that lock the
/// module, so that the go command checks what it fetched against them,
/// and is told nothing else. The module says which go it is for, or the
/// go command writes that into it, and what an action is given cannot
/// be written: any version since modules' graphs were pruned (1.17) will
/// do, and it is one for every fetch, so that it is no part of a project.
fn told(entry: &Entry) -> Vec<File> {
    vec![
        File {
            path: "go.mod".into(),
            content: GO_MOD.to_vec(),
            ..Default::default()
        },
        File {
            path: "go.sum".into(),
            content: entry.lines.clone().into_bytes(),
            ..Default::default()
        },
    ]
}

/// Fetches one module a go.sum locks into the go command's module cache,
/// which is in the action's home. The go command checks it against the
/// lines of the go.sum it is told, and that is the only check there is:
/// an action that builds takes what is in the cache as it is. So the
/// module is fetched alone, from its lock alone: nothing is asked of a
/// checksum database, and the cache is left writable, as a directory
/// that is handed on has to be. It prints each address it gets from, so
/// that the build's record shows where what it is made of came from.
///
/// A module the go.sum locks the go.mod of alone is not downloaded: the
/// go command is asked about it, which fetches its go.mod and checks it,
/// as it needs to choose versions.
///
/// The fetch is shared: it reads nothing of the project, and is the same
/// whichever module of the repository locks the module alike.
fn fetch(entry: &Entry) -> Action {
    let (name, arguments) = if entry.archive {
        ("fetch", vec!["go", "mod", "download", "-x"])
    } else {
        ("metadata", vec!["go", "list", "-x", "-m", "-json"])
    };
    let command = Command::new(arguments.into_iter().chain([entry.module.as_str()]))
        .env("GOFLAGS", "-modcacherw")
        .env("GOSUMDB", "off")
        .env("GOWORK", "off");
    Action {
        command: Some(command),
        files: told(entry),
        tools: vec!["go".into()],
        ..Action::fetch(format!("{name}/{}", entry.module), kept(entry))
    }
}

pub(super) fn actions(
    repo: &Repository,
    locked: &Locked,
    root: &str,
    module: &str,
    packages: &[String],
    // The modules nested in it, each a module of its own.
    nested: &[String],
    tested: &[Tested],
) -> Result<Vec<Action>, Error> {
    let tags = repo.string_list("tags")?;
    let timeout = repo.string("test-timeout")?;
    let fetches: Vec<Action> = locked.entries.iter().map(fetch).collect();
    // Each is named as every plugin names its like, reads the modules
    // this one requires of the workspace, and waits for the ones it
    // requires from outside. It reaches nothing, and is told so, so that
    // a module that was not fetched is said to be missing.
    let action = |name: &str, arguments: Vec<String>| {
        // Pure Go: with cgo, the go command reaches for whatever C compiler
        // the worker has, which is no input of the action.
        let command = Command::new(arguments)
            .env("CGO_ENABLED", "0")
            .env("GOPROXY", "off")
            .env("GOSUMDB", "off")
            // mise points the go command's GOBIN into the directory of
            // the tool itself unless it is told not to. Told so, the go
            // command installs a program where it keeps programs: go/bin
            // in the action's home.
            .env("MISE_GO_SET_GOBIN", "false");
        // Its sources, and all each fetch left: what it reads is what it
        // waits for.
        Action::new(name, command)
            .tool("go")
            .inputs([Input::set("sources")])
            .inputs(fetches.iter().map(|fetch| Input::output(&fetch.name)))
            .inputs([Input::dependencies(format!("{REQUIRED}/{{name}}"))])
    };
    let shards = repo.int("test-shards")?;
    if shards < 1 {
        // A plugin is told what an option is set to and not where: the
        // mistake is said of the module it was set for.
        return Err(Error::invalid(
            "invalid-test-shards",
            &join(root, "go.mod"),
            format!(
                "test-shards is {shards} for this module: a package's tests are cut into one action or more"
            ),
            "Set test-shards to 1 or more, in frontseat.yaml or the entity's catalog file.",
        ));
    }
    let fetched: Vec<String> = fetches.iter().map(|fetch| fetch.name.clone()).collect();
    let testing = Testing {
        root,
        module,
        packages,
        nested,
        tested,
        fetched: &fetched,
        // More than a worker's word holds is more than any package has
        // tests: as many as there are.
        shards: usize::try_from(shards).unwrap_or(usize::MAX),
        timeout,
        tags,
    };
    let tests = tests(&testing, &action)?;
    let compile = action("compile", over_module(compile(repo.profile()?), tags))
        .output(Output::optional(PROGRAMS));
    // vet passes or fails on what it finds, and what it found is read
    // from what it printed (report).
    let lint = action("lint", over_module(&["go", "vet"], tags)).check();
    // gofmt -l names what it would change and writes nothing: a check.
    // Its fix, gofmt -w, runs where the checkout is and nowhere else.
    let format = action("format", ["gofmt", "-l", "."].map(str::to_string).to_vec())
        .check()
        .fix(Command::new(["gofmt", "-w", "."]), ["**/*.go"]);
    let mut actions = fetches;
    actions.extend([lint, format, compile]);
    actions.extend(tests);
    Ok(actions)
}

/// What a module's tests are made of: the module, as it was read, and
/// how its tests are run.
#[derive(Clone, Copy)]
struct Testing<'a> {
    root: &'a str,
    module: &'a str,
    packages: &'a [String],
    // The modules nested in it, each a module of its own.
    nested: &'a [String],
    tested: &'a [Tested],
    // The modules its module's compile is given.
    fetched: &'a [String],
    shards: usize,
    timeout: &'a str,
    tags: &'a [String],
}

/// A module's tests: one action for each package that has tests, which
/// is what the go command tests by itself, named "test", a slash and
/// the package's directory in the module, "." for the module's own.
///
/// The go command tests a package by building a program of it and its
/// tests and running that in the package's directory. Here the two are
/// apart. The programs are built together, by `go test -c`, so what the
/// packages share is compiled once; and each test runs its own program,
/// in its package's directory, reading that program and the fixtures
/// under its package, and nothing of another package. A program is the same bytes for as long as its package and
/// what that imports are (-trimpath leaves no path of the worker in
/// it), so a test is run again where a change reached its program or
/// its fixtures, and answered from what the grid kept everywhere else.
///
/// Which packages have tests is the go command's to say, on the worker:
/// by each test file's build constraint and the worker's platform.
/// Nothing here foretells it. A directory that holds a file named as a
/// test file has a test; the go command builds a program for those
/// whose tests it builds there, and passes by the rest. Each program is
/// an output the build may not leave, and a test whose program was not
/// left has nothing to do and is not run (`idle_without_inputs`): one
/// whose tests are for another system, or behind a tag nobody gave.
///
/// The go command is handed each such directory: as a pattern
/// (`./dir/...`) where no package this reading sees is beneath it, since
/// a pattern that matches no package it can build is passed by, where a
/// package named outright (`./dir`) with no file to build is a mistake
/// to it; and outright where packages are beneath it, so that it never
/// reaches a package this reading does not see, as generated source,
/// which analysis never reads. The go command names a test program
/// after its package's directory and refuses to write two of one name
/// into one directory, whether or not both packages have tests, and two
/// packages of one name are ordinary: what would write two programs of
/// one name is handed apart, into an action and a directory each. A
/// package added may move another's program to another action, and its
/// test is then run again.
fn tests(
    testing: &Testing,
    action: &impl Fn(&str, Vec<String>) -> Action,
) -> Result<Vec<Action>, Error> {
    let Testing {
        root,
        module,
        packages,
        nested,
        tested,
        fetched,
        shards,
        timeout,
        tags,
    } = *testing;
    let beneath = |directory: &str, package: &str| {
        directory.is_empty()
            || package == directory
            || package
                .strip_prefix(directory)
                .is_some_and(|rest| rest.starts_with('/'))
    };
    // What the go command is handed, for each directory with tests: the
    // directory and all beneath it (`./dir/...`), a pattern it passes by
    // where it has nothing to build there, where no package this reading
    // sees is beneath it; else the directory alone (`./dir`). A pattern
    // over packages beneath would reach what this reading cannot see,
    // generated source above all (analysis reads none), and two of those
    // named alike are refused by the go command whether or not they have
    // tests.
    struct Given<'a> {
        argument: String,
        name: String,
        tested: &'a Tested,
    }
    let mut handed: Vec<Given> = Vec::new();
    for each in tested {
        let directory = &each.directory;
        // "@" parts a shard's name from its package's, and a path that
        // begins "~" is one of an action's home: the go command takes
        // neither for a package's directory, and no name is made of one.
        let plain = !directory.contains('@') && !directory.starts_with('~');
        let Some(name) = plain
            .then(|| crate::programs::test_named(module, directory))
            .flatten()
        else {
            return Err(mistake(
                root,
                directory,
                "unnamed-test-program",
                "has tests, and its test program cannot be named here: its directory's name holds more than letters, digits and . _ - ~ +".into(),
                "Rename the directory.",
            ));
        };
        let leaf = !packages
            .iter()
            .any(|package| package != directory && beneath(directory, package));
        let argument = match (directory.as_str(), leaf) {
            ("", true) => "./...".into(),
            ("", false) => ".".into(),
            (at, true) => format!("./{at}/..."),
            (at, false) => format!("./{at}"),
        };
        handed.push(Given {
            argument,
            name,
            tested: each,
        });
    }
    // Each group holds what the go command may be handed together: no
    // two programs it may write there are named alike.
    let mut groups: Vec<Vec<Given>> = Vec::new();
    for each in handed {
        match groups
            .iter_mut()
            .find(|group| group.iter().all(|other| other.name != each.name))
        {
            Some(group) => group.push(each),
            None => groups.push(vec![each]),
        }
    }
    let mut actions = Vec::new();
    for (number, group) in groups.iter().enumerate() {
        let built = if number == 0 {
            "test-build".to_string()
        } else {
            format!("test-build-{number}")
        };
        let mut arguments: Vec<String> = ["go", "test", "-c", "-trimpath"]
            .map(str::to_string)
            .to_vec();
        if !tags.is_empty() {
            arguments.push("-tags".into());
            arguments.push(tags.join(","));
        }
        arguments.push("-o".into());
        arguments.push(format!("{TESTS}/{number}/"));
        for each in group {
            arguments.push(each.argument.clone());
        }
        let mut build = action(&built, arguments);
        let mut pieces = Vec::new();
        for (each, name) in group.iter().map(|each| (each.tested, &each.name)) {
            let directory = &each.directory;
            let program = format!("{TESTS}/{number}/{name}.test");
            // Left where the go command builds one for the package on
            // this worker, which is its to say.
            build.outputs.push(Output::optional(program.clone()));
            // From the package's directory back to the module's, where
            // the program was left.
            let up = if directory.is_empty() {
                "./".to_string()
            } else {
                "../".repeat(directory.split('/').count())
            };
            let piece = format!(
                "test/{}",
                if directory.is_empty() { "." } else { directory }
            );
            // No more shards than it has tests to share out: a package
            // of one test, as one that hands its tests to a framework
            // of its own is, is one action whatever is asked.
            let names: Vec<&str> = each.names.iter().map(String::as_str).collect();
            let count = shards.min(names.len()).max(1);
            for shard in 0..count {
                let mut arguments = vec![
                    format!("{up}{program}"),
                    // As the go command runs it: a test that ends the
                    // program with 0 has not passed.
                    "-test.paniconexit0".into(),
                    format!("-test.timeout={timeout}"),
                ];
                let mut named = piece.clone();
                if count > 1 {
                    // "@" is in no directory the go command takes for a
                    // package, so a shard is named as no package's test is.
                    named = format!("{piece}@{}of{count}", shard + 1);
                    let selection = selecting(&names, shard, count);
                    // A worker takes no argument longer than this
                    // (Linux's MAX_ARG_STRLEN is 131072 bytes), and a
                    // shard that could not be started is no shard.
                    if selection.len() > SELECTION_AT_MOST {
                        return Err(mistake(
                            root,
                            directory,
                            "too-many-tests-to-shard",
                            format!(
                                "has {} tests, more than one argument can name a shard's share of",
                                names.len()
                            ),
                            "Set test-shards to 1 for the entity that owns the package, or part the package.",
                        ));
                    }
                    arguments.push(selection);
                }
                let mut test = action(&named, arguments).test();
                if let Some(command) = test.command.as_mut() {
                    command.directory = directory.to_string();
                }
                // Its program, and its package's own directory as the go
                // command runs a test there: everything beneath it, its
                // fixtures and what else it keeps, but a package beneath
                // it, which is that package's. What its files embed is in
                // the program. A directory's name is a name, and is
                // written as one. And the modules its module's compile is
                // given, where it is given them: a test that runs the go
                // command finds them as the compile does, and its program
                // changes with them all the same.
                // What a package keeps of other ecosystems' and of its
                // dependencies' (vendor, node_modules) is none of it, nor
                // a package or a module beneath it; its fixtures are all
                // its own, whatever they hold.
                let beneath_it = packages
                    .iter()
                    .chain(nested)
                    .filter(|other| *other != directory && beneath(directory, other))
                    .map(|other| Input::source(format!("!{}", join(&literal(other), "**"))));
                test.inputs = [
                    Input::output(&program),
                    Input::source(join(&literal(directory), "**")),
                    Input::source(format!("!{}", join(&literal(directory), "**/vendor/**"))),
                    Input::source(format!(
                        "!{}",
                        join(&literal(directory), "**/node_modules/**")
                    )),
                ]
                .into_iter()
                .chain(beneath_it)
                .chain([Input::source(join(&literal(directory), "testdata/**"))])
                .chain(fetched.iter().map(Input::output))
                .collect();
                test.idle_without_inputs = true;
                pieces.push(test);
            }
        }
        actions.push(build);
        actions.extend(pieces);
    }
    Ok(actions)
}

/// A mistake about a package of a module, said where it is: the
/// package's directory in the repository or, for the module's own, its
/// go.mod.
fn mistake(root: &str, directory: &str, reason: &str, what: String, help: &str) -> Error {
    let (at, said) = if directory.is_empty() {
        ("go.mod", "the module's directory")
    } else {
        (directory, directory)
    };
    Error::invalid(
        reason,
        &join(root, at),
        format!("the package in {said} {what}"),
        help,
    )
}

/// What one shard of a package's tests is told to run, of the tests
/// read from the package's files, in their order: every `count`th from
/// its own place. Each shard but the last runs its tests by name; the
/// last runs everything the others were not told to. So every test of
/// the program is run by one shard and no more, whatever was read: a
/// test that was not read, an example and a fuzz target's seeds are the
/// last shard's, and a name read that the program has none of is
/// nobody's.
///
/// The shares are even by count and blind to what a test costs, and a
/// test added moves the ones after it: every shard reads the one
/// program, so all of them are run again for any change to it anyway.
fn selecting(names: &[&str], shard: usize, count: usize) -> String {
    let last = shard + 1 == count;
    let chosen: Vec<&str> = names
        .iter()
        .enumerate()
        .filter(|(place, _)| (place % count == shard) != last)
        .map(|(_, name)| *name)
        .collect();
    format!(
        "-test.{}=^({})$",
        if last { "skip" } else { "run" },
        chosen.join("|")
    )
}

/// What a check found, read from what it printed, each finding named
/// with the module's root, as the repository names its files. The go
/// command's vet writes each as `path:line:col: message`, the path
/// relative to the module, under a `# package` line for each package.
/// gofmt -l names each file it would change, one a line.
pub(super) fn report(root: &str, action: &str, printed: &Printed) -> Vec<Diagnostic> {
    match action {
        "lint" => String::from_utf8_lossy(&printed.stderr)
            .lines()
            .filter_map(|line| vet(root, line))
            .collect(),
        "format" => unformatted(root, printed),
        _ => Vec::new(),
    }
}

/// The files gofmt -l names: each is not as gofmt would have it.
fn unformatted(root: &str, printed: &Printed) -> Vec<Diagnostic> {
    let text = String::from_utf8_lossy(&printed.stdout);
    let files = text
        .lines()
        .map(str::trim)
        .map(|line| line.strip_prefix("./").unwrap_or(line))
        .filter(|line| line.ends_with(".go"));
    let help = Unformatted::fixed_by("gofmt -w");
    Unformatted {
        rule_id: "go/format",
        message: "it is not formatted as gofmt formats it",
        help: &help,
    }
    .files(root, files)
}

/// One finding of vet's, from a line it printed: `path:line:col:
/// message` or `path:line: message`, the path relative to the module,
/// with no directory above it. Any other line is no finding.
fn vet(root: &str, line: &str) -> Option<Diagnostic> {
    const VET: Finding = Finding {
        rule_id: "go/vet",
        help: "Fix what vet reports; see `go doc cmd/vet`.",
    };
    let at = Located::read(line)?;
    VET.at(root, at.path, at.line, at.message)
}

#[cfg(test)]
mod tests {
    use super::{actions, compile, escaped, Action, Locked, Tested};
    use frontseat_plugin::Input;

    #[test]
    fn a_name_is_escaped_as_the_go_command_escapes_it_in_its_cache() {
        assert_eq!(
            escaped("github.com/BurntSushi/toml"),
            "github.com/!burnt!sushi/toml"
        );
        assert_eq!(escaped("v1.0.0-RC1"), "v1.0.0-!r!c1");
        assert_eq!(escaped("gopkg.in/yaml.v3"), "gopkg.in/yaml.v3");
    }
    use frontseat_plugin::{Error, Profile, Repository, Value};
    use frontseat_plugin_test::{TestContext, TestHost, TestPlugin};

    /// What the host gives a call of the plugin's own options: each at
    /// its default.
    fn context() -> TestContext {
        TestPlugin::new(crate::served()).context(Profile::Development)
    }

    /// What the plugin answers for a module at "app" whose packages are
    /// the directories with tests and no others.
    fn answered(repo: &Repository, tested: &[Tested]) -> Result<Vec<Action>, Error> {
        let packages: Vec<String> = tested.iter().map(|each| each.directory.clone()).collect();
        among(
            repo,
            &packages.iter().map(String::as_str).collect::<Vec<_>>(),
            tested,
        )
    }

    /// The same, among the packages named.
    fn among(
        repo: &Repository,
        packages: &[&str],
        tested: &[Tested],
    ) -> Result<Vec<Action>, Error> {
        nested_among(repo, packages, &[], tested)
    }

    /// The same, among the packages and the nested modules named.
    fn nested_among(
        repo: &Repository,
        packages: &[&str],
        nested: &[&str],
        tested: &[Tested],
    ) -> Result<Vec<Action>, Error> {
        let owned =
            |names: &[&str]| -> Vec<String> { names.iter().map(|name| name.to_string()).collect() };
        actions(
            repo,
            &Locked::default(),
            "app",
            "example.com/m",
            &owned(packages),
            &owned(nested),
            tested,
        )
    }

    /// A directory that holds tests, with the tests read from it.
    fn tested(directory: &str, names: &[&str]) -> Tested {
        Tested {
            directory: directory.to_string(),
            names: names.iter().map(|name| name.to_string()).collect(),
        }
    }

    /// A repository that sets how many shards a package's tests are cut
    /// into at most.
    fn sharded(shards: i64) -> TestContext {
        context().option("test-shards", Value::Int(shards))
    }

    /// A test is given its package's directory, and nothing of a module
    /// nested beneath it, which is a module of its own, as nothing of a
    /// package beneath it; the control is a directory of fixtures, which
    /// is its package's.
    #[test]
    fn a_test_is_given_its_package_and_no_module_nested_in_it() {
        let empty = TestHost::empty();
        let repo = context().repository(&empty);
        let tested = [tested("", &["TestA"])];
        let actions = nested_among(&repo, &["", "pkg"], &["nested"], &tested).unwrap();
        // A directory with no package beneath it but a nested module is
        // handed as one with nothing beneath it is: all of it.
        let leaf = [super::tests::tested("a", &["TestA"])];
        let above = nested_among(&repo, &["a"], &["a/sub"], &leaf).unwrap();
        let handed = |actions: &[Action]| {
            actions
                .iter()
                .find(|action| action.name == "test-build")
                .and_then(|action| action.command.clone())
                .map(|command| command.arguments)
                .unwrap_or_default()
        };
        assert!(
            handed(&above).iter().any(|argument| argument == "./a/..."),
            "{:?}",
            handed(&above)
        );
        let test = actions.iter().find(|action| action.test).unwrap();
        assert!(test.inputs.contains(&Input::source("**")));
        assert!(test.inputs.contains(&Input::source("!nested/**")));
        assert!(test.inputs.contains(&Input::source("!pkg/**")));
        assert!(!test
            .inputs
            .iter()
            .any(|input| input == &Input::source("!fixtures/**")));
        // Its fixtures are its own whatever they hold, given last.
        assert_eq!(test.inputs.last(), Some(&Input::source("testdata/**")));
        // A nested module is no package of its module: the directory
        // above it is handed as it would be without it.
        let without = among(&repo, &["", "pkg"], &tested).unwrap();
        assert_eq!(handed(&actions), handed(&without));
    }

    #[test]
    fn a_packages_tests_are_cut_into_shards_that_run_each_test_once() {
        let empty = TestHost::empty();
        let repo = sharded(3).repository(&empty);
        let tested = [
            tested("many", &["TestA", "TestB", "TestC", "TestD", "TestE"]),
            // Fewer tests than shards asked for: a shard for each.
            tested("two", &["TestX", "TestY"]),
            // One test, or none read: one action, named as with no shards.
            tested("one", &["TestSuite"]),
            tested("none", &[]),
        ];
        let all = answered(&repo, &tested).unwrap();
        let said: Vec<(&str, &str)> = all
            .iter()
            .filter(|action| action.test)
            .map(|action| {
                let arguments = &action.command.as_ref().unwrap().arguments;
                (
                    action.name.as_str(),
                    arguments.get(3).map(String::as_str).unwrap_or(""),
                )
            })
            .collect();
        assert_eq!(
            said,
            [
                ("test/many@1of3", "-test.run=^(TestA|TestD)$"),
                ("test/many@2of3", "-test.run=^(TestB|TestE)$"),
                // The last runs what the others were not told to: TestC,
                // and whatever was not read.
                ("test/many@3of3", "-test.skip=^(TestA|TestB|TestD|TestE)$"),
                ("test/two@1of2", "-test.run=^(TestX)$"),
                ("test/two@2of2", "-test.skip=^(TestX)$"),
                ("test/one", ""),
                ("test/none", ""),
            ]
        );
        // Every shard of a package runs the one program, in its
        // directory, with its fixtures.
        for shard in all
            .iter()
            .filter(|action| action.name.starts_with("test/many@"))
        {
            let command = shard.command.as_ref().unwrap();
            assert_eq!(command.arguments[0], "../_tests/0/many.test");
            assert_eq!(command.directory, "many");
            assert_eq!(
                shard.inputs,
                [
                    Input::output("_tests/0/many.test"),
                    Input::source("many/**"),
                    Input::source("!many/**/vendor/**"),
                    Input::source("!many/**/node_modules/**"),
                    Input::source("many/testdata/**")
                ]
            );
        }
        // A count past what a worker's word holds is as many as there
        // are, and is not cut to its low bits.
        for shards in [1 << 32, (1 << 32) + 2, i64::MAX] {
            let repo = sharded(shards).repository(&empty);
            let all = answered(&repo, &tested).unwrap();
            let of_many = all
                .iter()
                .filter(|action| action.name.starts_with("test/many@"))
                .count();
            assert_eq!(of_many, 5, "test-shards {shards}");
        }
        // More tests than one argument can name are refused, by where
        // they are; and up to that many are not, which the longest
        // argument taken shows: one that is nearly the most.
        let (mut longest, mut refusals) = (0, 0);
        for count in (5000..6500).step_by(50) {
            let names: Vec<String> = (0..count)
                .map(|i| format!("TestGeneratedConformanceCase{i:05}"))
                .collect();
            let names: Vec<&str> = names.iter().map(String::as_str).collect();
            let repo = sharded(2).repository(&empty);
            match answered(&repo, &[super::tests::tested("huge", &names)]) {
                Ok(all) => {
                    assert_eq!(refusals, 0, "{count} tests were taken, and fewer refused");
                    for action in &all {
                        for argument in &action.command.as_ref().unwrap().arguments {
                            assert!(argument.len() <= super::SELECTION_AT_MOST, "{count}");
                            longest = longest.max(argument.len());
                        }
                    }
                }
                Err(refused) => {
                    refusals += 1;
                    assert_eq!(
                        (refused.reason.as_str(), refused.path.as_str()),
                        ("too-many-tests-to-shard", "app/huge")
                    );
                }
            }
        }
        assert!(
            longest > super::SELECTION_AT_MOST - 2000 && refusals > 0,
            "the longest argument taken is {longest} bytes, and {refusals} counts were refused"
        );
        // Fewer than one shard is refused, by the option's name.
        for shards in [0, -3] {
            let repo = sharded(shards).repository(&empty);
            let refused = answered(&repo, &tested).unwrap_err();
            assert!(
                refused.message.contains("test-shards"),
                "{}",
                refused.message
            );
        }
    }

    #[test]
    fn packages_named_alike_have_their_test_programs_built_apart() {
        let empty = TestHost::empty();
        let repo = context().repository(&empty);
        let tested = ["a/util", "b/util", "c"].map(|directory| tested(directory, &[]));
        let all = answered(&repo, &tested).unwrap();
        let names: Vec<_> = all.iter().map(|action| action.name.as_str()).collect();
        assert_eq!(
            names,
            [
                "lint",
                "format",
                "compile",
                "test-build",
                "test/a/util",
                "test/c",
                "test-build-1",
                "test/b/util"
            ]
        );
        let of = |name: &str| all.iter().find(|action| action.name == name).unwrap();
        let arguments = |name: &str| of(name).command.as_ref().unwrap().arguments.join(" ");
        assert_eq!(
            arguments("test-build"),
            "go test -c -trimpath -o _tests/0/ ./a/util/... ./c/..."
        );
        assert_eq!(
            arguments("test-build-1"),
            "go test -c -trimpath -o _tests/1/ ./b/util/..."
        );
        let left = |name: &str| -> Vec<&str> {
            of(name).outputs.iter().map(|o| o.path.as_str()).collect()
        };
        assert_eq!(
            left("test-build"),
            ["_tests/0/util.test", "_tests/0/c.test"]
        );
        assert_eq!(left("test-build-1"), ["_tests/1/util.test"]);
        // Each test reads the program of its own package, and no other's.
        assert_eq!(
            of("test/a/util").inputs[0],
            Input::output("_tests/0/util.test")
        );
        assert_eq!(
            of("test/b/util").inputs[0],
            Input::output("_tests/1/util.test")
        );
        assert_eq!(
            arguments("test/b/util"),
            "../../_tests/1/util.test -test.paniconexit0 -test.timeout=10m"
        );
    }

    #[test]
    fn a_directory_no_test_program_can_be_named_for_is_refused_by_where_it_is() {
        let empty = TestHost::empty();
        let repo = context().repository(&empty);
        let answered = |directory: &str| answered(&repo, &[tested(directory, &[])]);
        // A name the go command would not take either; "@", which parts
        // a shard's name from its package's; and a path of a home.
        for directory in ["pkg[1]", "x@1of2", "a@b/pkg", "~pkg"] {
            let refused = answered(directory).unwrap_err();
            assert_eq!(
                (refused.reason.as_str(), refused.path),
                ("unnamed-test-program", format!("app/{directory}")),
            );
        }
        // The control: names the go command takes.
        for directory in ["c++", "a~b", "yaml.v3", "Upper", ""] {
            let all = answered(directory).unwrap();
            assert!(all.iter().any(|action| action.test), "{directory}");
        }
    }

    #[test]
    fn a_test_has_nothing_to_do_where_its_program_was_not_built() {
        let empty = TestHost::empty();
        let repo = context().repository(&empty);
        let tested = [tested("elsewhere", &[]), tested("ledger", &[])];
        let all = answered(&repo, &tested).unwrap();
        let build = all
            .iter()
            .find(|action| action.name == "test-build")
            .unwrap();
        // The go command is handed each directory with tests as a
        // pattern, which it passes by where it has nothing to build, and
        // each program is one it may not leave.
        assert_eq!(
            build.command.as_ref().unwrap().arguments,
            [
                "go",
                "test",
                "-c",
                "-trimpath",
                "-o",
                "_tests/0/",
                "./elsewhere/...",
                "./ledger/..."
            ]
        );
        let left: Vec<_> = build
            .outputs
            .iter()
            .map(|output| (output.path.as_str(), output.optional))
            .collect();
        assert_eq!(
            left,
            [
                ("_tests/0/elsewhere.test", true),
                ("_tests/0/ledger.test", true)
            ]
        );
        assert!(!build.idle_without_inputs);
        let tests: Vec<_> = all.iter().filter(|action| action.test).collect();
        assert_eq!(tests.len(), 2);
        for test in tests {
            assert!(test.idle_without_inputs, "{}", test.name);
        }
    }

    #[test]
    fn what_would_write_two_programs_of_one_name_is_handed_apart() {
        let empty = TestHost::empty();
        let repo = context().repository(&empty);
        // What each action that builds tests is handed, and what it may
        // leave.
        let built = |packages: &[&str], with_tests: &[&str]| -> Vec<(String, Vec<String>)> {
            let tested: Vec<Tested> = with_tests
                .iter()
                .map(|directory| tested(directory, &[]))
                .collect();
            among(&repo, packages, &tested)
                .unwrap()
                .iter()
                .filter(|action| action.name.starts_with("test-build"))
                .map(|action| {
                    (
                        action.command.as_ref().unwrap().arguments[6..].join(" "),
                        action
                            .outputs
                            .iter()
                            .map(|output| output.path.clone())
                            .collect(),
                    )
                })
                .collect()
        };
        let one = |handed: &str, left: &[&str]| {
            (
                handed.to_string(),
                left.iter().map(|path| path.to_string()).collect::<Vec<_>>(),
            )
        };
        // A directory with packages beneath it is named outright, so the
        // go command reaches nothing this reading did not see beneath it,
        // as generated packages (five named v1alpha1 refused the Go SDK's
        // tests); a directory with none beneath it is a pattern.
        assert_eq!(
            built(&["a", "a/b", "a/c"], &["a", "a/b"]),
            [one(
                "./a ./a/b/...",
                &["_tests/0/a.test", "_tests/0/b.test"]
            )]
        );
        assert_eq!(built(&["", "api"], &[""]), [one(".", &["_tests/0/m.test"])]);
        // A package with no tests, named as one with tests is, stands in
        // nobody's way where no pattern holds it.
        assert_eq!(
            built(&["near/util", "far/util"], &["near/util"]),
            [one("./near/util/...", &["_tests/0/util.test"])]
        );
        // And where a pattern would hold both, the directory is named
        // outright, and what is beneath it handed by itself.
        assert_eq!(
            built(&["", "far/util", "near/util", "z"], &["", "near/util", "z"]),
            [one(
                ". ./near/util/... ./z/...",
                &["_tests/0/m.test", "_tests/0/util.test", "_tests/0/z.test"]
            )]
        );
        // Two with tests that would be named alike are built apart.
        assert_eq!(
            built(&["", "x/util", "y/util"], &["", "x/util", "y/util"]),
            [
                one(". ./x/util/...", &["_tests/0/m.test", "_tests/0/util.test"]),
                one("./y/util/...", &["_tests/1/util.test"])
            ]
        );
        // Two directories that each hold a package the other names alike
        // are named outright, and so built together: the go command
        // reaches neither package beneath them.
        assert_eq!(
            built(
                &["build", "build/testkit", "sandbox", "sandbox/testkit"],
                &["build", "sandbox"]
            ),
            [one(
                "./build ./sandbox",
                &["_tests/0/build.test", "_tests/0/sandbox.test"]
            )]
        );
        // The whole of a module is never one pattern where it has
        // packages beneath it.
        assert_eq!(
            built(&["", "a", "b"], &["", "b"]),
            [one(". ./b/...", &["_tests/0/m.test", "_tests/0/b.test"])]
        );
    }

    #[test]
    fn a_compile_may_leave_programs_and_nothing_else_does() {
        let empty = TestHost::empty();
        let repo = context().repository(&empty);
        for action in answered(&repo, &[]).unwrap() {
            let outputs: Vec<(&str, bool)> = action
                .outputs
                .iter()
                .map(|output| (output.path.as_str(), output.optional))
                .collect();
            if action.name == "compile" {
                assert_eq!(outputs, [("~/go/bin/", true)]);
            } else {
                assert_eq!(outputs, [], "{}", action.name);
            }
            // mise is told to leave where programs are installed alone.
            assert_eq!(
                action
                    .command
                    .as_ref()
                    .and_then(|command| command.environment.get("MISE_GO_SET_GOBIN"))
                    .map(String::as_str),
                Some("false"),
                "{}",
                action.name
            );
        }
    }

    #[test]
    fn either_profile_installs_every_package_and_leaves_no_path_of_the_worker_in_a_program() {
        let empty = TestHost::empty();
        for profile in [Profile::Development, Profile::Production] {
            assert_eq!(
                &compile(profile)[..3],
                ["go", "install", "-trimpath"],
                "{profile:?}"
            );
        }
        assert_eq!(
            compile(Profile::Development),
            ["go", "install", "-trimpath", "-gcflags=all=-N -l"]
        );
        let repo = context().repository(&empty);
        let all = answered(&repo, &[]).unwrap();
        let arguments = &all
            .iter()
            .find(|action| action.name == "compile")
            .unwrap()
            .command
            .as_ref()
            .unwrap()
            .arguments;
        assert_eq!(arguments.last().map(String::as_str), Some("./..."));
    }
}

#[cfg(test)]
mod report_tests {
    use super::{report, vet};
    use frontseat_plugin::Printed;

    #[test]
    fn vet_findings_are_read_with_their_file_and_line() {
        let printed = Printed {
            exit_code: 1,
            stdout: Vec::new(),
            stderr: b"# example.com/app\n./main.go:6:14: fmt.Printf format %d has arg \"x\" of wrong type string\nsub/other.go:3: unreachable code\n".to_vec(),
        };
        let found = report("app", "lint", &printed);
        assert_eq!(found.len(), 2);
        assert_eq!(found[0].rule_id, "go/vet");
        assert_eq!(
            found[0].source_location.as_ref().unwrap().path,
            "app/main.go"
        );
        assert_eq!(found[0].source_location.as_ref().unwrap().line, 6);
        assert_eq!(
            found[0].message,
            "fmt.Printf format %d has arg \"x\" of wrong type string"
        );
        assert_eq!(
            found[1].source_location.as_ref().unwrap().path,
            "app/sub/other.go"
        );
        assert_eq!(found[1].source_location.as_ref().unwrap().line, 3);
        // The root module's files are named from the repository's root.
        assert_eq!(
            report("", "lint", &printed)[0]
                .source_location
                .as_ref()
                .unwrap()
                .path,
            "main.go"
        );
    }

    #[test]
    fn what_is_not_a_finding_is_not_one() {
        for line in [
            "# example.com/app",
            "vet: cannot find package",
            "",
            "../up.go:1:1: x",
            "/abs.go:1:1: x",
            "main.go:zero: x",
            "main.go:0:1: x",
            "main.go:3:4:   ",
        ] {
            assert!(vet("app", line).is_none(), "{line:?}");
        }
        let formatted = report(
            "app",
            "format",
            &Printed {
                exit_code: 0,
                stdout: b"main.go\n./sub/x.go\n../up.go\nnot a file\n".to_vec(),
                stderr: Vec::new(),
            },
        );
        assert_eq!(formatted.len(), 2);
        assert_eq!(formatted[0].rule_id, "go/format");
        assert_eq!(
            formatted[0].source_location.as_ref().unwrap().path,
            "app/main.go"
        );
        assert_eq!(
            formatted[1].source_location.as_ref().unwrap().path,
            "app/sub/x.go"
        );
        assert!(report("app", "format", &Printed::default()).is_empty());
    }
}
