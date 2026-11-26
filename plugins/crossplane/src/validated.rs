//! What `crossplane resource validate` found, at the file and line of the
//! example it found it in.
//!
//! The tool says which resource it found something wrong with, by its
//! apiVersion, kind, namespace and name, and nothing of where it read
//! it. So each is looked for in the examples, in the order of their
//! files and of the documents in each: the first that is that resource,
//! and was not found already, is where it was. One that is in none is
//! said of the package's metadata, by its name.
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::beneath;
use serde::Deserialize;
use std::collections::BTreeSet;

use crate::actions::{excluded, EXAMPLES};
use crate::manifest::FILE;

const VALIDATED: Finding = Finding {
    rule_id: "crossplane/validate",
    help: "Make the example what its CompositeResourceDefinition's schema says, or the schema what the example is; `crossplane resource validate` says why each is refused.",
};

/// What `--output json` prints: each resource, with what was wrong.
#[derive(Deserialize)]
struct Validation {
    #[serde(default)]
    resources: Vec<Validated>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Validated {
    #[serde(flatten)]
    resource: Resource,
    #[serde(default)]
    errors: Vec<Wrong>,
}

#[derive(Deserialize)]
struct Wrong {
    #[serde(default)]
    message: String,
}

/// A resource by what the tool names it by.
#[derive(Default, Deserialize, Clone, PartialEq, Eq, PartialOrd, Ord, Debug)]
#[serde(rename_all = "camelCase")]
struct Resource {
    #[serde(default)]
    api_version: String,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    namespace: String,
}

/// A document of an example as YAML holds one.
#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Document {
    #[serde(default)]
    api_version: String,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    metadata: Metadata,
}

#[derive(Default, Deserialize)]
struct Metadata {
    #[serde(default)]
    name: String,
    #[serde(default)]
    namespace: String,
}

/// Where each resource is: its file, relative to the repository, and
/// the line its document begins on, or 0 where that cannot be said.
struct Example {
    resource: Resource,
    path: String,
    line: i32,
}

/// The findings of what the check printed: one for each thing wrong
/// with each resource, at the example the resource is in.
pub(crate) fn findings(
    repo: &Repository,
    root: &str,
    printed: &Printed,
) -> Result<Vec<Diagnostic>, Error> {
    // What is no JSON the tool printed is nothing found: the check
    // failed, or passed, by its exit code, and says why where it printed.
    let Ok(validation) = serde_json::from_slice::<Validation>(&printed.stdout) else {
        return Ok(Vec::new());
    };
    if validation
        .resources
        .iter()
        .all(|validated| validated.errors.is_empty())
    {
        return Ok(Vec::new());
    }
    let examples = examples(repo, root)?;
    let mut taken = BTreeSet::new();
    let mut found = Vec::new();
    // The tool says every resource it read, valid or not, in the order
    // it read them: each takes its own document, so that one of an
    // identity another shares is where it is.
    for validated in &validation.resources {
        let resource = &validated.resource;
        let at = examples
            .iter()
            .enumerate()
            .find(|(index, example)| example.resource == *resource && !taken.contains(index));
        if let Some((index, _)) = at {
            taken.insert(index);
        }
        if validated.errors.is_empty() {
            continue;
        }
        let (file, line) = match at {
            Some((_, example)) => (
                beneath(root, &example.path)
                    .unwrap_or(&example.path)
                    .to_string(),
                example.line,
            ),
            None => (FILE.to_string(), 0),
        };
        for error in &validated.errors {
            found.extend(VALIDATED.at(
                root,
                &file,
                line,
                format!("{} {}: {}", resource.kind, named(resource), error.message),
            ));
        }
    }
    Ok(found)
}

/// A resource's name as it is written: its namespace first where it has one.
fn named(resource: &Resource) -> String {
    if resource.namespace.is_empty() {
        resource.name.clone()
    } else {
        format!("{}/{}", resource.namespace, resource.name)
    }
}

/// Every resource of the examples, in the order of their files and of
/// the documents in each. A file that does not read as YAML holds none
/// that is said: the tool read it otherwise, or failed.
fn examples(repo: &Repository, root: &str) -> Result<Vec<Example>, Error> {
    let patterns: Vec<String> = ["**/*.yaml", "**/*.yml"]
        .iter()
        .map(|pattern| format!("{EXAMPLES}/{pattern}"))
        .chain(excluded(repo, root)?)
        .collect();
    let patterns: Vec<&str> = patterns.iter().map(String::as_str).collect();
    let mut files = repo.glob(root, &patterns)?;
    // In the order the tool walks them (Go's filepath.Walk): by name
    // within each directory, so that "a/x.yaml" is before "a.yaml".
    files.sort_by(|a, b| a.split('/').cmp(b.split('/')));
    let mut out = Vec::new();
    for path in files {
        let Ok(Some(read)) = repo.yaml_stream::<Document>(&path) else {
            continue;
        };
        let starts = starts(&read.text);
        // The lines are said only where the documents read are the
        // documents found, one for one.
        let lines = (starts.len() == read.value.len()).then_some(starts);
        for (index, document) in read.value.into_iter().enumerate() {
            out.push(Example {
                resource: Resource {
                    api_version: document.api_version,
                    kind: document.kind,
                    name: document.metadata.name,
                    namespace: document.metadata.namespace,
                },
                path: path.clone(),
                line: lines.as_ref().map_or(0, |lines| lines[index]),
            });
        }
    }
    Ok(out)
}

/// The line each document of a YAML stream that holds anything begins
/// on, from 1: the first line of it that is no comment, no blank and no
/// marker. A document is ended by a line that begins `---` or `...`; what
/// follows `---` on its line is the next document's.
fn starts(text: &str) -> Vec<i32> {
    let mut starts = Vec::new();
    let mut current: Option<i32> = None;
    let content = |line: &str| {
        let line = line.trim();
        !line.is_empty() && !line.starts_with('#')
    };
    for (index, line) in text.lines().enumerate() {
        let number = i32::try_from(index + 1).unwrap_or(0);
        if let Some(rest) = line.strip_prefix("---") {
            if rest.is_empty() || rest.starts_with([' ', '\t']) {
                starts.extend(current.take());
                if content(rest) {
                    current = Some(number);
                }
                continue;
            }
        }
        if line.strip_prefix("...").is_some_and(|rest| !content(rest)) {
            starts.extend(current.take());
            continue;
        }
        if current.is_none() && content(line) {
            current = Some(number);
        }
    }
    starts.extend(current);
    starts
}

#[cfg(test)]
mod tests {
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    const INVALID: &str = r#"{
  "summary": {"total": 3, "valid": 1, "invalid": 2, "missingSchemas": 0},
  "resources": [
    {"apiVersion": "example.org/v1", "kind": "Network", "name": "eu", "namespace": "default", "status": "valid"},
    {"apiVersion": "example.org/v1", "kind": "Network", "name": "bad", "namespace": "default", "status": "invalid",
     "errors": [
       {"type": "schema", "field": "spec.region", "message": "spec.region: Unsupported value: \"mars\": supported values: \"eu\", \"us\"", "value": "mars"},
       {"type": "unknownField", "field": "spec.bogus", "message": "spec.bogus: Invalid value: \"bogus\": unknown field: \"bogus\"", "value": "bogus"}
     ]},
    {"apiVersion": "example.org/v1", "kind": "Network", "name": "elsewhere", "status": "invalid",
     "errors": [{"type": "schema", "field": "spec", "message": "spec: Required value", "value": null}]}
  ]
}
"#;

    fn host() -> TestHost {
        TestHost::new(
            Files::from_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/valid"))
                .unwrap()
                .with(
                    "app/examples/more/networks.yaml",
                    "# the networks of a team\n---\napiVersion: example.org/v1\nkind: Network\nmetadata:\n  name: eu\n  namespace: other\nspec: {region: eu}\n---\n\n# and one that is wrong\napiVersion: example.org/v1\nkind: Network\nmetadata:\n  name: bad\n  namespace: default\nspec: {region: mars, bogus: 1}\n",
                ),
        )
    }

    /// What validate finds is said at the example that holds the
    /// resource, at the line its document begins, one finding for each
    /// thing wrong; a resource in no example is said of the metadata.
    /// The control is a resource that was valid, which is no finding,
    /// and the package, which finds nothing.
    #[test]
    fn what_validate_finds_is_reported_at_the_example_that_holds_the_resource() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let printed = Printed::new(
            1,
            INVALID.as_bytes().to_vec(),
            b"crossplane: error: could not validate all resources\n".to_vec(),
        );
        let found = plugin
            .report(&host(), &context, "app", "validate", &printed)
            .answered()
            .diagnostics;
        let said: Vec<(&str, i32, &str)> = found
            .iter()
            .map(|finding| {
                let at = finding.source_location.as_ref().unwrap();
                (at.path.as_str(), at.line, finding.message.as_str())
            })
            .collect();
        assert_eq!(
            said,
            [
                ("app/examples/more/networks.yaml", 12, "Network default/bad: spec.region: Unsupported value: \"mars\": supported values: \"eu\", \"us\""),
                ("app/examples/more/networks.yaml", 12, "Network default/bad: spec.bogus: Invalid value: \"bogus\": unknown field: \"bogus\""),
                ("app/crossplane.yaml", 0, "Network elsewhere: spec: Required value"),
            ]
        );
        assert!(found
            .iter()
            .all(|finding| finding.rule_id == "crossplane/validate"));
        assert!(plugin
            .report(&host(), &context, "app", "package", &printed)
            .answered()
            .diagnostics
            .is_empty());
    }

    /// What is no JSON is nothing found: the check fails by its exit
    /// code, and says why in what it printed.
    #[test]
    fn what_is_no_json_is_nothing_found() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let printed = Printed::new(
            1,
            b"[x] schema validation error example.org/v1, Kind=Network, bad : spec.bogus\n"
                .to_vec(),
            Vec::new(),
        );
        assert!(plugin
            .report(&host(), &context, "app", "validate", &printed)
            .answered()
            .diagnostics
            .is_empty());
    }

    /// A resource of an identity another shares, as two of no name do,
    /// is at its own document: each the tool read takes one, valid or
    /// not, in the order it walks the examples, "a/x.yaml" before
    /// "a.yaml". The control is the same resources in one file.
    #[test]
    fn a_resource_of_an_identity_another_shares_is_at_its_own_document() {
        let plugin = TestPlugin::new(crate::served());
        let context = plugin.context(Profile::Development);
        let one = "apiVersion: example.org/v1\nkind: Network\nmetadata:\n  generateName: n-\nspec: {region: eu}\n";
        let host = TestHost::new(
            Files::new()
                .with("app/examples/a.yaml", one)
                .with("app/examples/a/x.yaml", format!("{one}---\n{one}")),
        );
        let printed = Printed::new(
            1,
            br#"{"resources": [
              {"apiVersion": "example.org/v1", "kind": "Network", "name": "", "status": "valid"},
              {"apiVersion": "example.org/v1", "kind": "Network", "name": "", "status": "invalid", "errors": [{"message": "second"}]},
              {"apiVersion": "example.org/v1", "kind": "Network", "name": "", "status": "invalid", "errors": [{"message": "third"}]}
            ]}"#.to_vec(),
            Vec::new(),
        );
        let said: Vec<(String, i32)> = plugin
            .report(&host, &context, "app", "validate", &printed)
            .answered()
            .diagnostics
            .into_iter()
            .map(|finding| {
                let at = finding.source_location.unwrap();
                (at.path, at.line)
            })
            .collect();
        assert_eq!(
            said,
            [
                ("app/examples/a/x.yaml".to_string(), 7),
                ("app/examples/a.yaml".to_string(), 1)
            ]
        );
    }

    /// Each document of a stream begins where its first line that says
    /// anything is; a stream of none begins nowhere.
    #[test]
    fn each_document_begins_at_its_first_line_that_says_anything() {
        assert_eq!(super::starts("a: 1\n"), [1]);
        assert_eq!(super::starts("# c\n---\n\na: 1\n---\nb: 2\n...\n"), [4, 6]);
        assert_eq!(
            super::starts("--- {a: 1}\n--- # nothing\n---\nb: 2\n"),
            [1, 4]
        );
        assert_eq!(
            super::starts("---\n# only a comment\n---\n"),
            Vec::<i32>::new()
        );
        assert_eq!(super::starts("----x: 1\n"), [1]);
    }
}
