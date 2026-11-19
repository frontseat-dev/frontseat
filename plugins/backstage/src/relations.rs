//! The field-to-relation mapping from Backstage's BuiltinKindsEntityProcessor.
use crate::catalog::{invalid, Specification};
use frontseat_plugin::entity;
use frontseat_plugin::prelude::*;

pub(super) fn extract(
    source: &EntityRef,
    spec: &Specification,
) -> Result<Vec<EntityRelation>, Error> {
    let mut result = Vec::new();
    let mut emit = |field: &str,
                    list: bool,
                    default_kind,
                    outgoing: &str,
                    incoming: &str|
     -> Result<(), Error> {
        let targets = if list {
            spec.list(field)?
        } else {
            spec.text(field)?.into_iter().collect()
        };
        for target in targets {
            let target = entity::parse(target, default_kind, &source.namespace)
                .map_err(|message| invalid(spec.path, format!("spec.{field}: {message}")))?;
            result.push(EntityRelation {
                r#type: outgoing.into(),
                target: Some(target),
                inverse_type: incoming.into(),
            });
        }
        Ok(())
    };
    match source.kind.as_str() {
        "component" | "api" | "resource" | "system" | "domain" => {
            emit("owner", false, Some("group"), "ownedBy", "ownerOf")?
        }
        _ => {}
    }
    match source.kind.as_str() {
        "component" | "api" | "resource" => {
            emit("system", false, Some("system"), "partOf", "hasPart")?
        }
        "system" => emit("domain", false, Some("domain"), "partOf", "hasPart")?,
        "domain" => emit("subdomainOf", false, Some("domain"), "partOf", "hasPart")?,
        "user" => emit("memberOf", true, Some("group"), "memberOf", "hasMember")?,
        "group" => {
            emit("parent", false, Some("group"), "childOf", "parentOf")?;
            emit("children", true, Some("group"), "parentOf", "childOf")?;
            emit("members", true, Some("user"), "hasMember", "memberOf")?;
        }
        _ => {}
    }
    if source.kind == "component" {
        emit(
            "subcomponentOf",
            false,
            Some("component"),
            "partOf",
            "hasPart",
        )?;
        emit(
            "providesApis",
            true,
            Some("api"),
            "providesApi",
            "apiProvidedBy",
        )?;
        emit(
            "consumesApis",
            true,
            Some("api"),
            "consumesApi",
            "apiConsumedBy",
        )?;
    }
    if source.kind == "component" || source.kind == "resource" {
        emit("dependsOn", true, None, "dependsOn", "dependencyOf")?;
        emit("dependencyOf", true, None, "dependencyOf", "dependsOn")?;
    }
    result.sort_by_cached_key(|r| (r.r#type.clone(), r.target.as_ref().map(entity::key)));
    result.dedup();
    Ok(result)
}
