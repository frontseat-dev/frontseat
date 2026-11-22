//! The SDK's readers of documents: what they take of a file, what they
//! refuse, and that they read the file asked for and list nothing.
use frontseat_plugin::Repository;
use frontseat_plugin_test::{Files, Read, TestHost};
use serde::Deserialize;

/// The paths the tests read a document from.
const PATHS: [&str; 4] = [
    "manifest.yaml",
    "Cargo.toml",
    "catalog-info.yaml",
    "lock.json",
];

/// A repository whose every document the tests read holds these bytes.
/// A reader of documents reads the one it is asked for and lists none,
/// which is checked as the host is dropped.
fn documents(bytes: impl Into<Vec<u8>>) -> Documents {
    let bytes = bytes.into();
    Documents(TestHost::new(
        PATHS
            .iter()
            .fold(Files::new(), |files, path| files.with(path, bytes.clone())),
    ))
}

struct Documents(TestHost);

impl std::ops::Deref for Documents {
    type Target = TestHost;
    fn deref(&self) -> &TestHost {
        &self.0
    }
}

impl Drop for Documents {
    fn drop(&mut self) {
        if std::thread::panicking() {
            return;
        }
        for read in self.0.reads() {
            assert!(
                matches!(read, Read::File { found: true, .. }),
                "document readers must read the file asked for and discover no other: {read:?}"
            );
        }
    }
}

#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct Manifest {
    name: String,
    enabled: bool,
    count: i64,
}

#[test]
fn typed_yaml_preserves_values_and_refuses_implicit_coercion() {
    for count in [i64::MIN, 9_007_199_254_740_993, i64::MAX] {
        let source =
            documents(format!("name: '${{HOME}}'\nenabled: true\ncount: {count}\n").into_bytes());
        let actual = Repository::new(&*source, None)
            .yaml_stream::<Manifest>("manifest.yaml")
            .unwrap()
            .unwrap()
            .value;
        assert_eq!(
            actual,
            [Manifest {
                name: "${HOME}".into(),
                enabled: true,
                count
            }]
        );
    }
    for text in [
        "name: 123\nenabled: true\ncount: 1\n",
        "name: example\nenabled: yes\ncount: 1\n",
        "name: example\nenabled: !!bool banana\ncount: 1\n",
        "name: example\nenabled: true\ncount: 9223372036854775808\n",
        "name: example\nenabled: true\ncount: 1\nunknown: value\n",
    ] {
        let source = documents(text.as_bytes().to_vec());
        let error = Repository::new(&*source, None)
            .yaml_stream::<Manifest>("manifest.yaml")
            .unwrap_err();
        assert_eq!(
            (error.reason.as_str(), error.path.as_str()),
            ("invalid-yaml", "manifest.yaml")
        );
        assert!(error.line > 0, "missing location for {text:?}: {error:?}");
    }
}

#[test]
fn manifest_size_limit_has_an_allowed_boundary_control() {
    for (size, allowed) in [(4 * 1024 * 1024, true), (4 * 1024 * 1024 + 1, false)] {
        let mut bytes = vec![b'x'; size];
        bytes[0] = b'#';
        let source = documents(bytes);
        let repo = Repository::new(&*source, None);
        let toml = repo.toml_table("Cargo.toml");
        let yaml = repo
            .yaml_stream::<Manifest>("catalog-info.yaml")
            .map(|read| read.map(|document| document.value));
        if allowed {
            assert!(toml.unwrap().unwrap().is_empty());
            assert!(yaml.unwrap().unwrap().is_empty());
        } else {
            assert_eq!(toml.unwrap_err().reason, "file-too-large");
            assert_eq!(yaml.unwrap_err().reason, "file-too-large");
        }
    }
}

#[test]
fn yaml_and_toml_errors_report_source_lines_after_unicode() {
    let source = documents("name: café\nname: duplicate\n".as_bytes().to_vec());
    let error = Repository::new(&*source, None)
        .yaml_stream::<Manifest>("catalog-info.yaml")
        .unwrap_err();
    assert_eq!(error.line, 2);
    let source = documents("name = 'café'\nname = 'duplicate'\n".as_bytes().to_vec());
    let error = Repository::new(&*source, None)
        .toml_table("Cargo.toml")
        .unwrap_err();
    assert_eq!(error.line, 2);
}

#[test]
fn single_yaml_document_requires_one_nonempty_manifest() {
    let valid = "name: sample\nenabled: true\ncount: 1\n";
    let source = documents(valid.as_bytes().to_vec());
    assert_eq!(
        Repository::new(&*source, None)
            .yaml::<Manifest>("manifest.yaml")
            .unwrap()
            .unwrap()
            .value
            .name,
        "sample"
    );
    for text in [
        String::new(),
        "null".into(),
        format!("{valid}---\n{valid}"),
        "name: [".into(),
    ] {
        let source = documents(text.into_bytes());
        let error = Repository::new(&*source, None)
            .yaml::<Manifest>("manifest.yaml")
            .unwrap_err();
        assert_eq!(error.reason, "invalid-yaml");
        assert_eq!(error.path, "manifest.yaml");
    }
}

#[test]
fn typed_json_preserves_values_and_refuses_what_is_not_the_document() {
    for count in [i64::MIN, 9_007_199_254_740_993, i64::MAX] {
        let source = documents(
            format!("{{\"name\": \"${{HOME}}\\n\", \"enabled\": true,\n \"count\": {count}}}\n")
                .into_bytes(),
        );
        let actual = Repository::new(&*source, None)
            .json::<Manifest>("lock.json")
            .unwrap()
            .unwrap()
            .value;
        assert_eq!(
            actual,
            Manifest {
                name: "${HOME}\n".into(),
                enabled: true,
                count
            }
        );
    }
    for text in [
        "",
        "null",
        "{\"name\": 123, \"enabled\": true, \"count\": 1}",
        "{\"name\": \"a\", \"enabled\": \"true\", \"count\": 1}",
        "{\"name\": \"a\", \"enabled\": true, \"count\": 9223372036854775808}",
        "{\"name\": \"a\", \"enabled\": true, \"count\": 1.0}",
        "{\"name\": \"a\", \"enabled\": true, \"count\": 1, \"unknown\": 1}",
        "{\"name\": \"a\", \"name\": \"b\", \"enabled\": true, \"count\": 1}",
        "{\"name\": \"a\", \"enabled\": true, \"count\": 1} {}",
        "{\"name\": \"a\", \"enabled\": true, \"count\": 1,}",
        "{'name': 'a', 'enabled': true, 'count': 1}",
        "name: a\nenabled: true\ncount: 1\n",
    ] {
        let source = documents(text.as_bytes().to_vec());
        let error = Repository::new(&*source, None)
            .json::<Manifest>("lock.json")
            .unwrap_err();
        assert_eq!(
            (error.reason.as_str(), error.path.as_str()),
            ("invalid-json", "lock.json"),
            "{text:?}"
        );
    }
    let source = documents("{\"name\": \"a\",\n \"enabled\": true,\n \"count\": []}".as_bytes());
    let error = Repository::new(&*source, None)
        .json::<Manifest>("lock.json")
        .unwrap_err();
    assert_eq!(error.line, 3);
    let mut large = b"{\"name\": \"".to_vec();
    large.resize(4 * 1024 * 1024 + 1, b'a');
    let error = Repository::new(&*documents(large), None)
        .json::<Manifest>("lock.json")
        .unwrap_err();
    assert_eq!(error.reason, "file-too-large");
}
