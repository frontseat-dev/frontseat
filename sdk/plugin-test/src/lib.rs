//! What a frontseat plugin's unit tests are written with.
//!
//! A test holds a repository in memory ([`Files`]), serves it through a
//! host that reads and globs as the real host does, records what was
//! read and fails when it is told to ([`TestHost`]), and asks the plugin
//! as the host asks it ([`TestPlugin`]): through the SDK's own dispatch,
//! each call encoded and its answer decoded, so the SDK checks options
//! and profiles and encodes a refusal as it does in a module.
//!
//! ```
//! use frontseat_plugin::prelude::*;
//! use frontseat_plugin_test::{Files, TestHost, TestPlugin};
//!
//! struct Modules;
//! impl Plugin for Modules {
//!     fn describe(&self) -> Description {
//!         Description { name: "modules".into(), interests: vec!["**/go.mod".into()], ..Default::default() }
//!     }
//!     fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
//!         if repo.read(&format!("{root}/go.mod"))?.is_none() {
//!             return Err(Error::invalid("missing-manifest", root, "there is no go.mod", "Add one."));
//!         }
//!         Ok(Analysis::default())
//!     }
//! }
//!
//! let plugin = TestPlugin::new(Modules);
//! let context = plugin.context(Profile::Development);
//! let host = TestHost::new(Files::new().with("app/go.mod", "module example.com/app\n"));
//! plugin.analyze(&host, &context, &["app"]).answered();
//! let refused = plugin.analyze(&TestHost::empty(), &context, &["app"]).refused("missing-manifest");
//! assert_eq!(refused.path, "app");
//! ```
//!
//! # What it cannot check
//!
//! It runs the plugin natively, on the machine that builds it, and so
//! checks the plugin's own logic and the SDK's half of the contract. It
//! cannot check what only the real host shows of the built module:
//!
//! - the module's 32-bit word: a number cut by `as usize` is whole here
//!   and cut there;
//! - the memory a module is given (256 MiB), and what it does when that
//!   runs out;
//! - the shapes of the module's exports;
//! - the host's own checks of an answer, such as what an analysis and a
//!   service must be;
//! - cancellation, and calls made at once.
//!
//! Those are checked by running the built `.wasm` in the host.

mod glob;

use std::cell::RefCell;
use std::collections::BTreeMap;
use std::path::Path;

use frontseat_plugin::{
    wire, AnalyzeResponse, ArrangeResponse, BranchRequest, BranchResponse, Code, DescribeResponse,
    Error, Handed, Host, Plugin, Printed, Profile, ReconcileResponse, ReportResponse, Repository,
    Resolved, Service, ServiceResponse, Value, VersionRequest, VersionResponse,
};
use prost::Message;

/// A repository held in memory: each file's bytes by its path relative
/// to the root, with slashes.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Files(BTreeMap<String, Vec<u8>>);

impl Files {
    /// A repository with no file in it.
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds a file, or replaces it; the path is relative to the
    /// repository, with slashes.
    #[must_use]
    pub fn with(mut self, path: &str, contents: impl Into<Vec<u8>>) -> Self {
        self.0.insert(path.into(), contents.into());
        self
    }

    /// Takes a file away, if it is there.
    #[must_use]
    pub fn without(mut self, path: &str) -> Self {
        self.0.remove(path);
        self
    }

    /// Reads a directory on disk, such as `testdata/valid`, into memory:
    /// each file beneath it, by its path relative to it. A link is read
    /// as the host reads one: as the file it leads to, where that is a
    /// file within the directory read, and as nothing otherwise; one to a
    /// directory is not entered. A name that is not UTF-8 is an error,
    /// since a repository's paths are text.
    pub fn from_dir(dir: impl AsRef<Path>) -> std::io::Result<Self> {
        fn walk(root: &Path, dir: &Path, beneath: &str, files: &mut Files) -> std::io::Result<()> {
            for entry in std::fs::read_dir(dir)? {
                let entry = entry?;
                let name = entry.file_name().into_string().map_err(|_| {
                    let place = if beneath.is_empty() {
                        "at the top of the directory read".to_string()
                    } else {
                        format!("in {beneath}")
                    };
                    std::io::Error::new(
                        std::io::ErrorKind::InvalidData,
                        format!("a file's name {place} is not UTF-8"),
                    )
                })?;
                let relative = frontseat_plugin::repository::join(beneath, &name);
                // The entry's own kind, which follows no link.
                let kind = entry.file_type()?;
                if kind.is_dir() {
                    walk(root, &entry.path(), &relative, files)?;
                } else if kind.is_file() {
                    files.0.insert(relative, std::fs::read(entry.path())?);
                } else if kind.is_symlink() {
                    if let Ok(target) = std::fs::canonicalize(entry.path()) {
                        if target.starts_with(root) && target.is_file() {
                            files.0.insert(relative, std::fs::read(&target)?);
                        }
                    }
                }
            }
            Ok(())
        }
        let root = std::fs::canonicalize(dir.as_ref())?;
        let mut files = Files::new();
        walk(&root, &root, "", &mut files)?;
        Ok(files)
    }
}

/// One thing the plugin read through the host, as the host records it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Read {
    /// A file was asked for; `found` says whether it was there.
    File { path: String, found: bool },
    /// Files beneath a directory were listed by patterns; `found` says
    /// whether any matched.
    Glob {
        directory: String,
        patterns: Vec<String>,
        found: bool,
    },
}

/// A host that serves [`Files`] as the real host does, records what is
/// read, and fails when it is told to. A read or a listing that fails is
/// not recorded, as the host records none.
///
/// The real host fails the call a read it could not serve was made in,
/// whatever the plugin does after, and tells the module nothing of it.
/// A call asked through [`TestPlugin`] fails with it too; the plugin is
/// told of it here, as an error, so a plugin that reads past a failure,
/// taking it for a file that is not there, is answered with the failure
/// all the same, and code of the plugin's may run that would not run in
/// the host.
#[derive(Default)]
pub struct TestHost {
    files: Files,
    everything: Option<Error>,
    reads_failing: BTreeMap<String, Error>,
    globs_failing: BTreeMap<String, Error>,
    reads: RefCell<Vec<Read>>,
    failed: RefCell<Option<Error>>,
}

impl TestHost {
    /// A host serving these files.
    pub fn new(files: Files) -> Self {
        Self {
            files,
            ..Self::default()
        }
    }

    /// A host of an empty repository.
    pub fn empty() -> Self {
        Self::default()
    }

    /// Reading this path fails with this error instead of returning the
    /// file.
    #[must_use]
    pub fn fail_read(mut self, path: &str, error: Error) -> Self {
        self.reads_failing.insert(path.into(), error);
        self
    }

    /// Listing beneath this directory fails with this error, whatever the
    /// patterns.
    #[must_use]
    pub fn fail_glob(mut self, directory: &str, error: Error) -> Self {
        self.globs_failing.insert(directory.into(), error);
        self
    }

    /// Every read and every listing fails with this error, as when the
    /// source is unavailable.
    #[must_use]
    pub fn fail_everything(mut self, error: Error) -> Self {
        self.everything = Some(error);
        self
    }

    /// What the plugin read, in the order it read it.
    pub fn reads(&self) -> Vec<Read> {
        self.reads.borrow().clone()
    }

    fn failing(&self, of: &BTreeMap<String, Error>, key: &str) -> Result<(), Error> {
        match self.everything.as_ref().or_else(|| of.get(key)) {
            Some(error) => Err(self.fail(error.clone())),
            None => Ok(()),
        }
    }

    /// Notes the first failure of a call, which the call ends with, and
    /// returns it, which the plugin is told here and not in the host.
    fn fail(&self, error: Error) -> Error {
        self.failed
            .borrow_mut()
            .get_or_insert_with(|| error.clone());
        error
    }
}

impl Host for TestHost {
    fn read(&self, path: &str) -> Result<Option<Vec<u8>>, Error> {
        self.failing(&self.reads_failing, path)?;
        // The host reads a file of the repository, and fails the call for
        // a path that leaves it and for a directory: neither is a file
        // that is not there.
        let Some(within) = frontseat_plugin::repository::resolve("", path) else {
            return Err(self.fail(
                Error::new(
                    Code::InvalidArgument,
                    "read-outside-repository",
                    "a read's path leaves the repository",
                )
                .help("Read a file by its path relative to the repository's root."),
            ));
        };
        let directory = format!("{within}/");
        if within.is_empty() || self.files.0.keys().any(|file| file.starts_with(&directory)) {
            return Err(self.fail(
                Error::new(
                    Code::InvalidArgument,
                    "read-of-directory",
                    "a read's path is a directory, not a file",
                )
                .help("Read a file; list a directory's files with a glob."),
            ));
        }
        let found = self.files.0.get(path).cloned();
        self.reads.borrow_mut().push(Read::File {
            path: path.into(),
            found: found.is_some(),
        });
        // The host answers a file larger than a plugin takes as too
        // large, and the SDK says so to the plugin, which may go on.
        let most =
            usize::try_from(frontseat_plugin::repository::READ_AT_MOST).unwrap_or(usize::MAX);
        if found.as_ref().is_some_and(|content| content.len() > most) {
            return Err(frontseat_plugin::repository::too_large(path));
        }
        Ok(found)
    }

    fn glob(&self, directory: &str, patterns: &[&str]) -> Result<Vec<String>, Error> {
        self.failing(&self.globs_failing, directory)?;
        // The host globs beneath a directory of the repository, and
        // fails the call for one that is not.
        if !directory.is_empty() && frontseat_plugin::repository::resolve("", directory).is_none() {
            return Err(self.fail(
                Error::new(
                    Code::InvalidArgument,
                    "glob-outside-repository",
                    "a glob's directory leaves the repository",
                )
                .help("Glob beneath a directory relative to the repository's root."),
            ));
        }
        // The files are held sorted, so what is selected is too.
        let paths: Vec<String> = self
            .files
            .0
            .keys()
            .filter(|file| glob::within(directory, file, patterns))
            .cloned()
            .collect();
        self.reads.borrow_mut().push(Read::Glob {
            directory: directory.into(),
            patterns: patterns.iter().map(|p| p.to_string()).collect(),
            found: !paths.is_empty(),
        });
        Ok(paths)
    }
}

/// A call's context: its profile, the options it is given and the tools
/// the repository pins.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct TestContext(wire::Context);

impl TestContext {
    /// Sets an option. The SDK refuses a call whose option is not of the
    /// kind the plugin declares, or is one it does not declare.
    #[must_use]
    pub fn option(mut self, name: &str, value: impl Into<Value>) -> Self {
        self.0.options.insert(name.into(), value.into().into());
        self
    }

    /// Adds a platform what the plugin is asked about is shipped for, as
    /// its entity's catalog file would say it: `linux/amd64`.
    #[must_use]
    pub fn platform(mut self, platform: &str) -> Self {
        self.0.platforms.push(platform.into());
        self
    }

    /// Pins a tool at a version, as the repository's mise configuration
    /// would.
    #[must_use]
    pub fn tool(mut self, name: &str, version: &str) -> Self {
        self.0.tools.insert(name.into(), version.into());
        self
    }

    /// The repository a plugin's own functions are given, read through
    /// this host with this context, for a test of what the plugin does
    /// below its calls.
    pub fn repository<'a>(&self, host: &'a TestHost) -> Repository<'a> {
        Repository::new(host, Some(self.0.clone()))
    }
}

/// What the plugin answered, decoded as the host decodes it: the
/// response, or the refusal read back by the SDK's own decoder
/// ([`Error::from_status`]), its reason as its author wrote it.
///
/// An answer not looked at is a warning, which the gate fails on:
///
/// ```compile_fail
/// #![deny(unused_must_use)]
/// # use frontseat_plugin::prelude::*;
/// # use frontseat_plugin_test::{TestHost, TestPlugin};
/// # struct Empty;
/// # impl Plugin for Empty {
/// #     fn describe(&self) -> Description {
/// #         Description { name: "empty".into(), ..Default::default() }
/// #     }
/// # }
/// let plugin = TestPlugin::new(Empty);
/// let context = plugin.context(Profile::Development);
/// plugin.analyze(&TestHost::empty(), &context, &[""]);
/// ```
///
/// and one looked at is none:
///
/// ```
/// #![deny(unused_must_use)]
/// # use frontseat_plugin::prelude::*;
/// # use frontseat_plugin_test::{TestHost, TestPlugin};
/// # struct Empty;
/// # impl Plugin for Empty {
/// #     fn describe(&self) -> Description {
/// #         Description { name: "empty".into(), ..Default::default() }
/// #     }
/// # }
/// let plugin = TestPlugin::new(Empty);
/// let context = plugin.context(Profile::Development);
/// plugin.analyze(&TestHost::empty(), &context, &[""]).answered();
/// ```
#[derive(Debug)]
#[must_use = "an answer is checked with answered(), refused() or unsupported()"]
pub struct Answer<T> {
    /// The response, or the refusal.
    pub result: Result<T, Error>,
    /// The method it answers.
    method: String,
    /// Whether the call failed for what the host could not serve, and
    /// not as the plugin answered.
    host_failed: bool,
}

impl<T> Answer<T> {
    /// The response; panics with the refusal's reason, message and help
    /// if it was refused.
    #[track_caller]
    pub fn answered(self) -> T {
        match self.result {
            Ok(response) => response,
            Err(refusal) if self.host_failed => panic!(
                "the host could not serve what the plugin read, {:?} for the reason {}: {refusal}",
                refusal.code, refusal.reason
            ),
            Err(refusal) => panic!(
                "the plugin refused, {:?} for the reason {}: {refusal}",
                refusal.code, refusal.reason
            ),
        }
    }

    /// The refusal; panics if the plugin answered, or refused for another
    /// reason. The reason is as the plugin wrote it, `invalid-manifest`.
    #[track_caller]
    pub fn refused(self, reason: &str) -> Error {
        match self.result {
            Ok(_) => panic!("the plugin answered, and was to refuse with {reason}"),
            Err(refusal) => {
                assert_eq!(
                    refusal.reason, reason,
                    "refused for another reason: {refusal:?}"
                );
                refusal
            }
        }
    }

    /// Panics unless the call was answered as the host reads a method
    /// unserved: `UNIMPLEMENTED`, for the method's own reason or the
    /// SDK's `unknown-method`. `UNIMPLEMENTED` for another reason is a
    /// failure of the call, as the host reads it.
    #[track_caller]
    pub fn unsupported(self) {
        // The reason the host reads as the method unserved, of each
        // method a plugin may leave (server/internal/pluginhost/error.go).
        let reason = match self.method.as_str() {
            "Report" => "report-not-served",
            "Reconcile" => "reconcile-not-served",
            "Service" => "service-not-served",
            "Branch" => "branching-strategy-not-served",
            "Version" => "versioning-strategy-not-served",
            _ => "unknown-method",
        };
        match self.result {
            Ok(_) => panic!("the plugin answered, and was to say it serves no such call"),
            Err(refusal) => assert!(
                refusal.code == Code::Unimplemented
                    && (refusal.reason == reason || refusal.reason == "unknown-method"),
                "refused, and not as {} unserved, which is UNIMPLEMENTED for the reason {reason}: {refusal:?}",
                self.method
            ),
        }
    }
}

/// A plugin asked as the host asks it: each call encoded, answered
/// through the SDK's dispatch, and the answer decoded.
pub struct TestPlugin<P> {
    plugin: P,
}

impl<P: Plugin> TestPlugin<P> {
    /// Wraps a plugin, usually `crate::served()`.
    pub fn new(plugin: P) -> Self {
        Self { plugin }
    }

    fn ask<Response: Message + Default>(
        &self,
        host: &TestHost,
        method: &str,
        request: impl Message,
    ) -> Answer<Response> {
        host.failed.take();
        let call = wire::Call {
            method: method.into(),
            request: request.encode_to_vec(),
        }
        .encode_to_vec();
        let reply = wire::Reply::decode(
            frontseat_plugin::abi::answer(&self.plugin, host, &call).as_slice(),
        )
        .expect("the plugin's reply decodes");
        // What the host could not serve fails the call, whatever the
        // plugin answered.
        if let Some(failure) = host.failed.take() {
            return Answer {
                result: Err(failure),
                method: method.into(),
                host_failed: true,
            };
        }
        Answer {
            method: method.into(),
            host_failed: false,
            result: match reply.result.expect("the plugin replied with something") {
                wire::reply::Result::Response(bytes) => {
                    Ok(Response::decode(bytes.as_slice()).expect("the plugin's response decodes"))
                }
                wire::reply::Result::Error(status) => Err(Error::from_status(status)),
            },
        }
    }

    /// What the plugin says it is. The host it is given fails any read,
    /// as the real one does.
    #[track_caller]
    pub fn describe(&self) -> DescribeResponse {
        self.ask(&unreadable(), "Describe", wire::DescribeRequest {})
            .answered()
    }

    /// A context with this profile and every option at its declared
    /// default, as the host fills it.
    #[track_caller]
    pub fn context(&self, profile: Profile) -> TestContext {
        TestContext(wire::Context {
            profile: profile as i32,
            options: self
                .describe()
                .options
                .into_iter()
                .map(|option| (option.name, option.default.unwrap_or_default()))
                .collect(),
            ..Default::default()
        })
    }

    /// Asks Analyze of these roots.
    pub fn analyze(
        &self,
        host: &TestHost,
        context: &TestContext,
        roots: &[&str],
    ) -> Answer<AnalyzeResponse> {
        self.ask(
            host,
            "Analyze",
            wire::AnalyzeRequest {
                context: Some(context.0.clone()),
                roots: roots.iter().map(|root| root.to_string()).collect(),
            },
        )
    }

    /// Asks Arrange of a project for the named actions, with what it was
    /// handed.
    pub fn arrange(
        &self,
        host: &TestHost,
        context: &TestContext,
        root: &str,
        actions: &[&str],
        handed: &[Handed],
    ) -> Answer<ArrangeResponse> {
        self.ask(
            host,
            "Arrange",
            wire::ArrangeRequest {
                context: Some(context.0.clone()),
                root: root.into(),
                actions: actions.iter().map(|action| action.to_string()).collect(),
                handed: handed.to_vec(),
            },
        )
    }

    /// Asks Report of what a project's check printed.
    pub fn report(
        &self,
        host: &TestHost,
        context: &TestContext,
        root: &str,
        action: &str,
        printed: &Printed,
    ) -> Answer<ReportResponse> {
        self.ask(
            host,
            "Report",
            wire::ReportRequest {
                context: Some(context.0.clone()),
                root: root.into(),
                action: action.into(),
                exit_code: printed.exit_code,
                stdout: printed.stdout.clone(),
                stderr: printed.stderr.clone(),
            },
        )
    }

    /// Asks Reconcile of the entities found beneath a root.
    pub fn reconcile(
        &self,
        host: &TestHost,
        context: &TestContext,
        root: &str,
        entities: &[Resolved],
    ) -> Answer<ReconcileResponse> {
        self.ask(
            host,
            "Reconcile",
            wire::ReconcileRequest {
                context: Some(context.0.clone()),
                root: root.into(),
                entities: entities.to_vec(),
            },
        )
    }

    /// Asks Branch of a branching strategy. It is given a host that fails
    /// any read.
    pub fn branch(&self, request: BranchRequest) -> Answer<BranchResponse> {
        self.ask(&unreadable(), "Branch", request)
    }

    /// Asks Version of a versioning strategy. It is given a host that
    /// fails any read.
    pub fn version(&self, request: VersionRequest) -> Answer<VersionResponse> {
        self.ask(&unreadable(), "Version", request)
    }

    /// Asks Service of a name the plugin claims, with what was written
    /// beside it. It is given a host that fails any read.
    pub fn service(&self, name: &str, written: Service) -> Answer<ServiceResponse> {
        self.ask(
            &unreadable(),
            "Service",
            wire::ServiceRequest {
                name: name.into(),
                written: Some(written),
            },
        )
    }
}

/// A host for a call that reads nothing: any read fails.
fn unreadable() -> TestHost {
    TestHost::empty().fail_everything(Error::new(
        Code::FailedPrecondition,
        "unreadable",
        "this call reads nothing of a repository",
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use frontseat_plugin::{Analysis, Description, PluginOption};

    fn unavailable() -> Error {
        Error::new(
            Code::Unavailable,
            "source-unavailable",
            "the source is unavailable",
        )
    }

    #[test]
    fn a_host_serves_its_files_records_what_is_read_and_fails_where_it_is_told() {
        let files = Files::new()
            .with("app/a.go", "a")
            .with("app/b.go", "b")
            .with("lib/c.go", "c");
        let host = TestHost::new(files.clone()).fail_read("app/b.go", unavailable());
        assert_eq!(host.read("app/a.go").unwrap(), Some(b"a".to_vec()));
        assert_eq!(host.read("absent").unwrap(), None);
        assert_eq!(
            host.read("app/b.go").unwrap_err().reason,
            "source-unavailable"
        );
        assert_eq!(
            host.glob("app", &["*.go"]).unwrap(),
            ["app/a.go", "app/b.go"]
        );
        assert!(host.glob("", &["*.rs"]).unwrap().is_empty());
        // What failed is not recorded, as the host records nothing of it.
        assert_eq!(
            host.reads(),
            [
                Read::File {
                    path: "app/a.go".into(),
                    found: true
                },
                Read::File {
                    path: "absent".into(),
                    found: false
                },
                Read::Glob {
                    directory: "app".into(),
                    patterns: vec!["*.go".into()],
                    found: true
                },
                Read::Glob {
                    directory: "".into(),
                    patterns: vec!["*.rs".into()],
                    found: false
                },
            ]
        );
        let host = TestHost::new(files.clone()).fail_glob("lib", unavailable());
        assert!(host.glob("lib", &["*.go"]).is_err());
        assert_eq!(host.glob("app", &["*.go"]).unwrap().len(), 2);
        let host = TestHost::new(files).fail_everything(unavailable());
        assert!(host.read("app/a.go").is_err() && host.glob("app", &["**"]).is_err());
        assert!(host.reads().is_empty());
    }

    #[test]
    fn files_are_read_from_a_directory_by_their_paths_beneath_it() {
        let files = Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/src")).unwrap();
        let host = TestHost::new(files);
        assert_eq!(host.glob("", &["**"]).unwrap(), ["glob.rs", "lib.rs"]);
    }

    /// A link is the entry it is, and is not followed: a directory
    /// linked to itself is not walked for ever, and what a link leads to
    /// is not read as a file of the directory.
    #[cfg(unix)]
    #[test]
    fn a_directory_is_read_without_following_its_links() {
        let top =
            std::env::temp_dir().join(format!("frontseat-plugin-test-{}", std::process::id()));
        // One left by a run that panicked is not this run's.
        let _ = std::fs::remove_dir_all(&top);
        let dir = top.join("read");
        std::fs::create_dir_all(dir.join("app")).unwrap();
        std::fs::write(dir.join("app/Makefile"), "test:\n").unwrap();
        std::fs::write(top.join("outside"), "secret\n").unwrap();
        std::os::unix::fs::symlink(&dir, dir.join("app/again")).unwrap();
        std::os::unix::fs::symlink(dir.join("app/Makefile"), dir.join("linked")).unwrap();
        std::os::unix::fs::symlink(top.join("outside"), dir.join("out")).unwrap();
        let read = Files::from_dir(&dir);
        std::fs::remove_dir_all(&top).unwrap();
        assert_eq!(
            read.unwrap(),
            Files::new()
                .with("app/Makefile", "test:\n")
                .with("linked", "test:\n")
        );
    }

    /// A name that is not UTF-8, which Linux allows, is an error that
    /// says where it is and not what it is.
    #[cfg(target_os = "linux")]
    #[test]
    fn a_name_that_is_not_text_is_an_error() {
        use std::os::unix::ffi::OsStrExt;
        let top =
            std::env::temp_dir().join(format!("frontseat-plugin-test-name-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&top);
        std::fs::create_dir_all(top.join("app")).unwrap();
        std::fs::write(
            top.join("app")
                .join(std::ffi::OsStr::from_bytes(b"\xffname")),
            "",
        )
        .unwrap();
        let read = Files::from_dir(&top);
        std::fs::remove_dir_all(&top).unwrap();
        let error = read.unwrap_err();
        assert_eq!(error.kind(), std::io::ErrorKind::InvalidData);
        assert_eq!(error.to_string(), "a file's name in app is not UTF-8");
    }

    struct Manifests;
    impl Plugin for Manifests {
        fn describe(&self) -> Description {
            Description {
                name: "manifests".into(),
                options: vec![PluginOption::new(
                    "strict",
                    "Whether a missing manifest is refused.",
                    true,
                )],
                ..Default::default()
            }
        }
        fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
            if repo.bool("strict")? && repo.read(root)?.is_none() {
                return Err(Error::new(
                    Code::FailedPrecondition,
                    "missing-manifest",
                    "there is no manifest",
                )
                .at_line(root, 3)
                .help("Write one."));
            }
            Ok(Analysis::default())
        }
    }

    #[test]
    fn a_plugin_is_asked_with_its_defaults_and_its_refusal_is_decoded_as_a_client_sees_it() {
        let plugin = TestPlugin::new(Manifests);
        let context = plugin.context(Profile::Development);
        let host = TestHost::empty();
        let refused = plugin
            .analyze(&host, &context, &["manifest"])
            .refused("missing-manifest");
        assert_eq!(
            refused,
            Error::new(
                Code::FailedPrecondition,
                "missing-manifest",
                "there is no manifest"
            )
            .at_line("manifest", 3)
            .help("Write one.")
        );
        let lenient = context.clone().option("strict", Value::Bool(false));
        let answered = plugin.analyze(&host, &lenient, &["manifest"]).answered();
        assert_eq!(answered.analyses[0].root, "manifest");
        // The SDK refuses an option of another kind before the plugin reads.
        let reads = host.reads().len();
        plugin
            .analyze(
                &host,
                &context.option("strict", Value::Int(1)),
                &["manifest"],
            )
            .refused("invalid-option");
        assert_eq!(host.reads().len(), reads);
        plugin.branch(BranchRequest::default()).unsupported();
        plugin.service("db", Service::default()).unsupported();
    }
}
