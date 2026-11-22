//! Provider-neutral Frontseat entity identities shared by plugins.
//! References use `[kind:][namespace/]name`; adapters map provider-native IDs.

use crate::{EntityRef, Error};

/// Whether text is a name as an entity's parts are written: 1 to
/// `at_most` ASCII bytes, letters and digits at either end, and letters,
/// digits, hyphens, dots or underscores between. A catalog may hold a
/// name to fewer bytes than a reference does: Backstage holds one to 63.
pub fn is_name(text: &str, at_most: usize) -> bool {
    let bytes = text.as_bytes();
    !bytes.is_empty()
        && bytes.len() <= at_most
        && bytes[0].is_ascii_alphanumeric()
        && bytes[bytes.len() - 1].is_ascii_alphanumeric()
        && bytes
            .iter()
            .all(|c| c.is_ascii_alphanumeric() || b"-_.".contains(c))
}

/// Construct a canonical reference. Each part is 1–255 ASCII bytes with
/// alphanumeric endpoints and hyphens, dots or underscores inside. Output is
/// lower case. Provider-specific field validation belongs to the adapter.
pub fn reference(kind: &str, namespace: &str, name: &str) -> Result<EntityRef, Error> {
    let valid = |s: &str| is_name(s, 255);
    if !valid(kind) || !valid(namespace) || !valid(name) {
        return Err(Error::new(
            crate::Code::InvalidArgument,
            "invalid-entity-reference",
            format!("invalid entity reference {kind}:{namespace}/{name}"),
        )
        .help("Name an entity kind:namespace/name, each part 1 to 255 letters, digits, hyphens, dots or underscores, beginning and ending with a letter or digit."));
    }
    Ok(EntityRef {
        kind: kind.to_ascii_lowercase(),
        namespace: namespace.to_ascii_lowercase(),
        name: name.to_ascii_lowercase(),
    })
}

/// Resolve a reference in its source entity's namespace. A missing default
/// kind requires an explicit kind.
pub fn parse(
    written: &str,
    default_kind: Option<&str>,
    namespace: &str,
) -> Result<EntityRef, Error> {
    let (kind, rest) = match written.split_once(':') {
        Some(pair) => pair,
        None => (default_kind.unwrap_or(""), written),
    };
    let (namespace, name) = rest.split_once('/').unwrap_or((namespace, rest));
    reference(kind, namespace, name)
}

/// The canonical string identity used for ordering and deduplication.
pub fn key(reference: &EntityRef) -> String {
    format!(
        "{}:{}/{}",
        reference.kind, reference.namespace, reference.name
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn core_identity_does_not_require_a_provider_schema() {
        let entity = reference("service-blueprint", "tenant.scope", &"a".repeat(100)).unwrap();
        assert_eq!(parse(&key(&entity), None, "default").unwrap(), entity);
    }

    #[test]
    fn identity_parts_have_shared_ascii_and_length_boundaries() {
        for part in 0..3 {
            for value in [
                "a".into(),
                "A".into(),
                "1".into(),
                "a-b_c.d".into(),
                "a".repeat(255),
            ] {
                let mut parts = ["service-blueprint", "tenant.scope", "name"];
                parts[part] = &value;
                let result = reference(parts[0], parts[1], parts[2]).unwrap();
                assert_eq!(parse(&key(&result), None, "default").unwrap(), result);
            }
            for value in [
                "",
                "-a",
                "a-",
                "_a",
                "a_",
                ".a",
                "a.",
                "a b",
                "a/b",
                "a:b",
                "a\0b",
                "a\nb",
                "é",
                "K",
                "İ",
                "aKb",
                &"a".repeat(256),
                &"K".repeat(100),
            ] {
                let mut parts = ["component", "default", "name"];
                parts[part] = value;
                assert!(
                    reference(parts[0], parts[1], parts[2]).is_err(),
                    "accepted {parts:?}"
                );
                assert!(parse(
                    &format!("{}:{}/{}", parts[0], parts[1], parts[2]),
                    None,
                    "default"
                )
                .is_err());
            }
        }
    }
}
