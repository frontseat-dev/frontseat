//! Catalog identity and the built-in fields used by Backstage's relation processor.
//! Extension fields remain allowed. This does not execute processors, follow
//! a Location's targets or fetch substitutions such as $text.

use frontseat_plugin::entity;
use frontseat_plugin::prelude::*;
use serde::Deserialize;
use serde_json::{Map, Value};
use std::sync::LazyLock;

/// What a document that states no specification is read as.
static UNSTATED: LazyLock<Map<String, Value>> = LazyLock::new(Map::new);

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Document {
    api_version: String,
    kind: String,
    metadata: Metadata,
    /// None where the document states no specification, which a
    /// Location must.
    #[serde(default)]
    spec: Option<Map<String, Value>>,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
    #[serde(default = "default_namespace")]
    namespace: String,
    #[serde(flatten)]
    fields: Map<String, Value>,
}

fn default_namespace() -> String {
    "default".into()
}

pub(super) fn invalid(path: &str, message: impl Into<String>) -> Error {
    Error::invalid(
        "invalid-catalog",
        path,
        message,
        "Correct the catalog identity and its built-in specification fields.",
    )
}

impl Document {
    /// Whether this document describes the entity: the same kind,
    /// namespace and name, as references are compared.
    pub fn is(&self, reference: &frontseat_plugin::EntityRef) -> bool {
        self.kind.eq_ignore_ascii_case(&reference.kind)
            && self
                .metadata
                .namespace
                .eq_ignore_ascii_case(&reference.namespace)
            && self.metadata.name.eq_ignore_ascii_case(&reference.name)
    }

    /// The entity's namespace, which a bare reference in its fields is in.
    pub fn namespace(&self) -> &str {
        &self.metadata.namespace
    }

    /// Whether the specification states a field at all.
    pub fn states(&self, field: &str) -> bool {
        self.fields().contains_key(field)
    }

    fn fields(&self) -> &Map<String, Value> {
        self.spec.as_ref().unwrap_or(&UNSTATED)
    }

    /// The specification's fields, read as the entity's are.
    pub fn specification<'a>(&'a self, manifest: &'a str) -> Specification<'a> {
        Specification {
            fields: self.fields(),
            path: manifest,
        }
    }

    pub fn entity(self, manifest: &str, root: &str) -> Result<Entity, Error> {
        crate::metadata::identity(
            &self.kind,
            &self.metadata.namespace,
            &self.metadata.name,
            manifest,
        )?;
        let reference =
            entity::reference(&self.kind, &self.metadata.namespace, &self.metadata.name)
                .map_err(|message| invalid(manifest, message))?;
        if !crate::metadata::api_version(&self.api_version) {
            return Err(invalid(
                manifest,
                "apiVersion must be an alphanumeric version with an optional DNS prefix",
            ));
        }
        crate::metadata::validate(&self.metadata.fields, manifest)?;
        let built_in = matches!(
            reference.kind.as_str(),
            "component" | "api" | "resource" | "system" | "domain" | "group" | "user" | "location"
        );
        if built_in
            && !matches!(
                self.api_version.as_str(),
                "backstage.io/v1alpha1" | "backstage.io/v1beta1"
            )
        {
            return Err(invalid(
                manifest,
                format!(
                    "unsupported apiVersion {} for {}",
                    self.api_version, self.kind
                ),
            ));
        }
        let mut result = Entity {
            r#ref: Some(reference.clone()),
            manifest: manifest.into(),
            roots: if reference.kind == "component" {
                vec![root.into()]
            } else {
                Vec::new()
            },
            ..Default::default()
        };
        if built_in {
            let spec = self.specification(manifest);
            if matches!(reference.kind.as_str(), "system" | "domain") {
                spec.text("type")?;
            }
            if matches!(reference.kind.as_str(), "group" | "user") {
                crate::metadata::profile(self.fields(), manifest)?;
            }
            if reference.kind == "location" {
                location(self.spec.is_some(), &spec)?;
            }
            for field in match reference.kind.as_str() {
                "component" => &["type", "lifecycle", "owner"][..],
                "api" => &["type", "lifecycle", "owner", "definition"][..],
                "resource" => &["type", "owner"][..],
                "system" | "domain" => &["owner"][..],
                "group" => &["type"][..],
                _ => &[],
            } {
                spec.required(field)?;
            }
            match reference.kind.as_str() {
                "group" => {
                    spec.required_list("children")?;
                }
                "user" => {
                    spec.required_list("memberOf")?;
                }
                _ => {}
            }
            result.relations = crate::relations::extract(&reference, &spec)?;
        }
        Ok(result)
    }
}

/// A Location as Backstage's schema has it: a specification, whose type
/// and targets are nonempty text and whose presence is required or
/// optional. Its targets are Backstage's to read; frontseat finds catalog
/// files by their name, and leaves a Location as it is written.
fn location(stated: bool, spec: &Specification) -> Result<(), Error> {
    if !stated {
        return Err(invalid(spec.path, "spec is required of a Location"));
    }
    spec.text("type")?;
    spec.text("target")?;
    spec.list("targets")?;
    match spec.text("presence")? {
        None | Some("required" | "optional") => Ok(()),
        Some(_) => Err(invalid(
            spec.path,
            "spec.presence must be required or optional",
        )),
    }
}

/// Built-in fields are checked only for the kind that owns them. A custom kind
/// may use the same spelling with another shape without inheriting those rules.
pub(super) struct Specification<'a> {
    fields: &'a Map<String, Value>,
    pub path: &'a str,
}

impl Specification<'_> {
    pub fn text(&self, field: &str) -> Result<Option<&str>, Error> {
        self.fields
            .get(field)
            .map(|value| {
                value.as_str().filter(|s| !s.is_empty()).ok_or_else(|| {
                    invalid(self.path, format!("spec.{field} must be a nonempty string"))
                })
            })
            .transpose()
    }

    fn required(&self, field: &str) -> Result<&str, Error> {
        self.text(field)?
            .ok_or_else(|| invalid(self.path, format!("spec.{field} is required")))
    }

    pub fn list(&self, field: &str) -> Result<Vec<&str>, Error> {
        let Some(value) = self.fields.get(field) else {
            return Ok(Vec::new());
        };
        let values = value
            .as_array()
            .ok_or_else(|| invalid(self.path, format!("spec.{field} must be a list")))?;
        values
            .iter()
            .map(|v| {
                v.as_str().filter(|s| !s.is_empty()).ok_or_else(|| {
                    invalid(
                        self.path,
                        format!("spec.{field} entries must be nonempty strings"),
                    )
                })
            })
            .collect()
    }

    fn required_list(&self, field: &str) -> Result<Vec<&str>, Error> {
        if !self.fields.contains_key(field) {
            return Err(invalid(self.path, format!("spec.{field} is required")));
        }
        self.list(field)
    }
}

#[cfg(test)]
mod tests {
    use super::Document;
    use serde_json::{json, Value};

    fn read(document: Value) -> Result<frontseat_plugin::Entity, frontseat_plugin::Error> {
        serde_json::from_value::<Document>(document)
            .expect("a document")
            .entity("app/catalog-info.yaml", "app")
    }

    fn location(spec: Value) -> Value {
        json!({"apiVersion": "backstage.io/v1alpha1", "kind": "Location", "metadata": {"name": "app"}, "spec": spec})
    }

    #[test]
    fn a_location_is_an_entity_with_no_source_and_no_relations() {
        let entity = read(location(json!({
            "type": "url",
            "target": "./catalog-info.yaml",
            "targets": ["./api/catalog-info.yaml", "https://example.com/catalog-info.yaml"],
            "presence": "optional",
            "x-extension": {"kept": true},
        })))
        .expect("a sound Location");
        let reference = entity.r#ref.expect("a reference");
        assert_eq!(reference.kind, "location");
        assert!(entity.roots.is_empty());
        assert!(entity.relations.is_empty());
        assert!(read(location(json!({}))).is_ok(), "every field is optional");
    }

    #[test]
    fn a_malformed_location_is_refused_by_the_field_it_gets_wrong() {
        for (field, spec) in [
            ("spec.type", json!({"type": ""})),
            ("spec.type", json!({"type": ["url"]})),
            ("spec.target", json!({"target": ""})),
            ("spec.targets", json!({"targets": "./catalog-info.yaml"})),
            ("spec.targets", json!({"targets": [""]})),
            ("spec.presence", json!({"presence": "sometimes"})),
        ] {
            let error = read(location(spec.clone())).expect_err(&spec.to_string());
            assert_eq!(error.reason, "invalid-catalog");
            assert!(error.message.contains(field), "{spec}: {}", error.message);
        }
        let mut unstated = location(json!({}));
        unstated.as_object_mut().unwrap().remove("spec");
        let error = read(unstated).expect_err("a Location without a specification");
        assert!(
            error.message.contains("spec is required"),
            "{}",
            error.message
        );
        let mut foreign = location(json!({}));
        foreign["apiVersion"] = json!("example.com/v1");
        let error = read(foreign).expect_err("a Location of another apiVersion");
        assert!(error.message.contains("apiVersion"), "{}", error.message);
    }

    #[test]
    fn a_component_needs_a_type_and_a_kind_of_another_api_needs_no_specification() {
        let component = json!({"apiVersion": "backstage.io/v1alpha1", "kind": "Component", "metadata": {"name": "app"}});
        let error = read(component).expect_err("a component with no type");
        assert!(
            error.message.contains("spec.type is required"),
            "{}",
            error.message
        );
        let custom = json!({"apiVersion": "example.com/v1", "kind": "Template", "metadata": {"name": "app"}});
        assert!(read(custom).is_ok(), "a custom kind needs no specification");
    }

    fn component(spec: Value) -> Value {
        json!({"apiVersion": "backstage.io/v1alpha1", "kind": "Component", "metadata": {"name": "app"}, "spec": spec})
    }

    /// A dependency names its kind, as Backstage reads one: a bare entry
    /// is refused, saying how one is written and nothing of the entry.
    /// The control is the same entry with its kind.
    #[test]
    fn a_dependency_that_names_no_kind_is_told_how_one_is_written() {
        let spec = |entry: &str| json!({"type": "service", "lifecycle": "production", "owner": "team", "dependsOn": [entry]});
        assert!(read(component(spec("component:ledger"))).is_ok());
        assert!(read(component(spec("resource:other/ledger"))).is_ok());
        let refused = read(component(spec("ledger"))).expect_err("a bare dependency");
        let said = format!("{refused:?}");
        assert!(
            said.contains("spec.dependsOn: an entry names no kind: write its kind before it, as component:<name> or resource:<name>"),
            "{said}"
        );
        assert!(!said.contains("ledger"), "{said}");
    }
}
