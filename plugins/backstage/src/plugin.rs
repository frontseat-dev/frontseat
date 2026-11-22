use std::collections::BTreeMap;

use crate::catalog::{self, Document};
use frontseat_plugin::entity;
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;

pub(super) struct BackstagePlugin;

impl Plugin for BackstagePlugin {
    fn describe(&self) -> Description {
        Description {
            name: "backstage".into(),
            interests: vec!["**/catalog-info.yaml".into()],
            ..Default::default()
        }
    }

    // What each file should say of what the source says. The schema is
    // Backstage's: a component's dependencies are `spec.dependsOn`, its
    // dependents `spec.dependencyOf`, written only where the file states
    // it already; each entry names its kind, and one without a
    // namespace is of the entity's. An entry that is no component stays
    // where it was; a component the source still names stays as it was
    // written; the rest of what the source says is added, in canonical
    // order.
    fn reconcile(
        &self,
        repo: &Repository,
        _root: &str,
        entities: &[EntityDependencies],
    ) -> Result<Vec<Edit>, Error> {
        let mut read: BTreeMap<&str, Vec<Document>> = BTreeMap::new();
        let mut edits = Vec::new();
        for resolved in entities {
            let manifest = resolved.manifest.as_str();
            let Some(reference) = resolved.r#ref.as_ref() else {
                return Err(catalog::invalid(
                    manifest,
                    "an entity to reconcile has no reference",
                ));
            };
            if !read.contains_key(manifest) {
                read.insert(
                    manifest,
                    repo.yaml_stream::<Document>(manifest)
                        .map(|read| read.map(|document| document.value))?
                        .unwrap_or_default(),
                );
            }
            let documents = &read[manifest];
            let Some(index) = documents.iter().position(|document| document.is(reference)) else {
                return Err(catalog::invalid(
                    manifest,
                    format!("the file does not describe {}", entity::key(reference)),
                ));
            };
            let document = &documents[index];
            let index = i32::try_from(index).map_err(|_| {
                catalog::invalid(
                    manifest,
                    "the file holds more documents than can be counted",
                )
            })?;
            let spec = document.specification(manifest);
            edits.push(Edit {
                path: manifest.into(),
                document: index,
                keys: vec!["spec".into(), "dependsOn".into()],
                values: should_say(
                    spec.list("dependsOn")?,
                    &resolved.depends_on,
                    document.namespace(),
                ),
            });
            if document.states("dependencyOf") {
                edits.push(Edit {
                    path: manifest.into(),
                    document: index,
                    keys: vec!["spec".into(), "dependencyOf".into()],
                    values: should_say(
                        spec.list("dependencyOf")?,
                        &resolved.dependency_of,
                        document.namespace(),
                    ),
                });
            }
        }
        Ok(edits)
    }

    fn analyze(&self, repo: &Repository, root: &str) -> Result<Analysis, Error> {
        let manifest = join(root, "catalog-info.yaml");
        let documents = repo
            .yaml_stream::<Document>(&manifest)
            .map(|read| read.map(|document| document.value))?
            .unwrap_or_default();
        let mut entities = Vec::with_capacity(documents.len());
        for document in documents {
            entities.push(document.entity(&manifest, root)?);
        }
        Ok(Analysis {
            entities,
            ..Default::default()
        })
    }
}

/// A list as it should be, given what it says and the components the
/// source says: what is no component stays in its place, a component the
/// source still names stays as written, and what the source names beyond
/// that is added in canonical order. An entry names its kind, as
/// Backstage reads one, and is of the entity's namespace where it names
/// none.
fn should_say(written: Vec<&str>, components: &[EntityRef], namespace: &str) -> Vec<String> {
    let mut remaining: Vec<&EntityRef> = components.iter().collect();
    let mut out = Vec::with_capacity(written.len() + components.len());
    for entry in written {
        match entity::parse(entry, None, namespace) {
            Ok(reference) if reference.kind == "component" => {
                if let Some(i) = remaining.iter().position(|wanted| **wanted == reference) {
                    remaining.remove(i);
                    out.push(entry.to_string());
                }
            }
            _ => out.push(entry.to_string()),
        }
    }
    // What is added is written as the file writes a reference: bare where
    // it is of the entity's own namespace, and in full where it is not.
    let mut added: Vec<String> = remaining
        .iter()
        .map(|reference| {
            if reference.namespace == namespace {
                format!("{}:{}", reference.kind, reference.name)
            } else {
                entity::key(reference)
            }
        })
        .collect();
    added.sort();
    out.extend(added);
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn component(namespace: &str, name: &str) -> EntityRef {
        entity::parse(&format!("component:{namespace}/{name}"), None, "default").unwrap()
    }

    #[test]
    fn a_component_added_of_the_files_namespace_is_written_bare() {
        let said = should_say(Vec::new(), &[component("frontseat", "git")], "frontseat");
        assert_eq!(said, vec!["component:git".to_string()]);
    }

    #[test]
    fn a_component_added_of_another_namespace_is_written_in_full() {
        let said = should_say(Vec::new(), &[component("other", "git")], "frontseat");
        assert_eq!(said, vec!["component:other/git".to_string()]);
    }

    #[test]
    fn what_is_written_stays_as_it_was_written() {
        let said = should_say(
            vec!["component:frontseat/git", "resource:db"],
            &[component("frontseat", "git"), component("frontseat", "api")],
            "frontseat",
        );
        assert_eq!(
            said,
            vec![
                "component:frontseat/git".to_string(),
                "resource:db".to_string(),
                "component:api".to_string()
            ]
        );
    }
}
