//! Standard metadata and profile field policies from Backstage catalog-model.
//! Only declared fields are checked; extension fields retain their own shapes.
use crate::catalog::invalid;
use frontseat_plugin::prelude::*;
use serde_json::{Map, Value};

/// A name as Backstage holds one: the identity grammar, to 63 bytes.
fn name(value: &str) -> bool {
    frontseat_plugin::entity::is_name(value, 63)
}
fn dns_label(value: &str) -> bool {
    name(value)
        && value
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-')
}

pub(super) fn identity(
    kind: &str,
    namespace: &str,
    entity_name: &str,
    path: &str,
) -> Result<(), Error> {
    if kind.is_empty()
        || kind.len() > 63
        || !kind.as_bytes()[0].is_ascii_alphabetic()
        || !kind.bytes().all(|c| c.is_ascii_alphanumeric())
    {
        return Err(invalid(
            path,
            "kind must begin with a letter and contain at most 63 ASCII letters or digits",
        ));
    }
    if !dns_label(namespace) {
        return Err(invalid(
            path,
            "metadata.namespace must be a lower-case DNS label of at most 63 characters",
        ));
    }
    if !name(entity_name) {
        return Err(invalid(path, "metadata.name must have alphanumeric endpoints and at most 63 ASCII letters, digits, hyphens, dots or underscores"));
    }
    Ok(())
}
fn dns(value: &str) -> bool {
    value.len() <= 253 && value.split('.').all(dns_label)
}

fn qualified(value: &str, suffix: impl Fn(&str) -> bool) -> bool {
    match value.split_once('/') {
        Some((prefix, value)) => dns(prefix) && suffix(value),
        None => suffix(value),
    }
}

pub(super) fn api_version(value: &str) -> bool {
    qualified(value, |version| {
        !version.is_empty()
            && version.len() <= 63
            && version.bytes().all(|c| c.is_ascii_alphanumeric())
    })
}

fn string<'a>(value: &'a Value, path: &str, field: &str, empty: bool) -> Result<&'a str, Error> {
    value
        .as_str()
        .filter(|s| empty || !s.is_empty())
        .ok_or_else(|| {
            invalid(
                path,
                format!(
                    "{field} must be {}string",
                    if empty { "a " } else { "a nonempty " }
                ),
            )
        })
}

fn object<'a>(value: &'a Value, path: &str, field: &str) -> Result<&'a Map<String, Value>, Error> {
    value
        .as_object()
        .ok_or_else(|| invalid(path, format!("{field} must be an object")))
}

fn list<'a>(value: &'a Value, path: &str, field: &str) -> Result<&'a Vec<Value>, Error> {
    value
        .as_array()
        .ok_or_else(|| invalid(path, format!("{field} must be a list")))
}

pub(super) fn validate(fields: &Map<String, Value>, path: &str) -> Result<(), Error> {
    for field in ["uid", "etag", "title", "description"] {
        if let Some(value) = fields.get(field) {
            string(
                value,
                path,
                &format!("metadata.{field}"),
                field == "description",
            )?;
        }
    }
    for field in ["labels", "annotations"] {
        if let Some(value) = fields.get(field) {
            for (key, value) in object(value, path, &format!("metadata.{field}"))? {
                if !qualified(key, name) {
                    return Err(invalid(
                        path,
                        format!("metadata.{field} key {key:?} is invalid"),
                    ));
                }
                let text = string(value, path, &format!("metadata.{field}.{key}"), true)?;
                if field == "labels" && !text.is_empty() && !name(text) {
                    return Err(invalid(
                        path,
                        format!("metadata.labels.{key} has an invalid label value"),
                    ));
                }
            }
        }
    }
    if let Some(value) = fields.get("tags") {
        for value in list(value, path, "metadata.tags")? {
            let tag = string(value, path, "metadata.tags entry", false)?;
            if tag.len() > 63
                || tag.split('-').any(|part| {
                    part.is_empty()
                        || !part.bytes().all(|c| {
                            c.is_ascii_lowercase() || c.is_ascii_digit() || b":+#".contains(&c)
                        })
                })
            {
                return Err(invalid(
                    path,
                    format!("metadata.tags entry {tag:?} is invalid"),
                ));
            }
        }
    }
    if let Some(value) = fields.get("links") {
        for value in list(value, path, "metadata.links")? {
            let link = object(value, path, "metadata.links entry")?;
            let url = link
                .get("url")
                .ok_or_else(|| invalid(path, "metadata.links entry requires url"))?;
            let url = string(url, path, "metadata.links.url", false)?;
            url::Url::parse(url).map_err(|error| {
                invalid(
                    path,
                    format!("metadata.links.url is not an absolute URL: {error}"),
                )
            })?;
            for field in ["title", "icon", "type"] {
                if let Some(value) = link.get(field) {
                    let text = string(value, path, &format!("metadata.links.{field}"), false)?;
                    if matches!(field, "title" | "icon") && text.trim().is_empty() {
                        return Err(invalid(
                            path,
                            format!("metadata.links.{field} must not be blank"),
                        ));
                    }
                }
            }
        }
    }
    Ok(())
}

pub(super) fn profile(fields: &Map<String, Value>, path: &str) -> Result<(), Error> {
    if let Some(value) = fields.get("profile") {
        let profile = object(value, path, "spec.profile")?;
        for field in ["displayName", "email", "picture"] {
            if let Some(value) = profile.get(field) {
                string(value, path, &format!("spec.profile.{field}"), false)?;
            }
        }
    }
    Ok(())
}
