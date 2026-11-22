//! Tracked ecosystem documents. Libraries own syntax, aliases and duplicate
//! detection; the SDK owns resource policy and shared source diagnostics.

use super::Repository;
use crate::Error;
use serde::de::DeserializeOwned;
pub use toml::Table as TomlTable;

// Manifest limits are shared by all plugins, rather than accepting input that
// exhausts the Wasm instance before it can return a useful diagnostic.
const MAX_DOCUMENT_BYTES: usize = 4 * 1024 * 1024;
const MAX_YAML_EVENTS: usize = 100_000;
const MAX_YAML_DEPTH: usize = 64;

impl Repository<'_> {
    /// Read UTF-8 text, preserving absence and refusing invalid bytes.
    pub fn text(&self, path: &str) -> Result<Option<String>, Error> {
        self.read(path)?
            .map(|bytes| {
                String::from_utf8(bytes).map_err(|_| {
                    Error::invalid(
                        "invalid-text",
                        path,
                        "the file is not valid UTF-8",
                        "Save this file as UTF-8.",
                    )
                })
            })
            .transpose()
    }

    /// Read TOML through the parser family Cargo uses. Duplicate keys,
    /// malformed values and out-of-range integers are errors, not defaults.
    pub fn toml(&self, path: &str) -> Result<Option<TomlTable>, Error> {
        self.document_text(path)?
            .map(|text| {
                text.parse::<TomlTable>()
                    .map_err(|error| invalid_toml(path, &text, &error))
            })
            .transpose()
    }

    /// Read a typed TOML document through the same parser and policy as
    /// `toml`, with its text beside it, so that a plugin with
    /// `toml::Spanned` fields can say the line a value is on (`line_of`).
    /// Unknown keys are the struct's concern.
    pub fn toml_document<T: DeserializeOwned>(
        &self,
        path: &str,
    ) -> Result<Option<Typed<T>>, Error> {
        self.document_text(path)?
            .map(|text| {
                let value = toml::from_str::<T>(&text)
                    .map_err(|error| invalid_toml(path, &text, &error))?;
                Ok(Typed { value, text })
            })
            .transpose()
    }

    /// Read typed YAML documents using YAML 1.2 scalar rules and merge keys.
    /// Empty/null documents are skipped. Duplicates, unknown tags, malformed
    /// values, unresolved aliases and exceeded resource budgets are refused.
    /// Includes and environment/property expansion are disabled at compile time.
    ///
    /// Files are limited to 4 MiB, nesting to 64 levels and stream events to
    /// 100,000; alias replay is limited to 16,384 events per document. Plugins cannot
    /// weaken this policy. A struct can add `serde(deny_unknown_fields)` where
    /// its ecosystem schema requires that; extension fields remain its concern.
    pub fn yaml<T: DeserializeOwned>(&self, path: &str) -> Result<Option<Vec<T>>, Error> {
        self.document_text(path)?
            .map(|text| {
                validate_yaml_nulls(path, &text)?;
                let options = serde_saphyr::options! {
                    strict_indentation: true,
                    strict_booleans: true,
                    no_schema: true,
                    reject_unsupported_tags: true,
                    with_snippet: false,
                    budget: serde_saphyr::budget! {
                        max_events: MAX_YAML_EVENTS,
                        max_depth: MAX_YAML_DEPTH,
                        max_recorded_anchor_events: MAX_YAML_EVENTS,
                    },
                    alias_limits: serde_saphyr::alias_limits! {
                        max_total_replayed_events: 16_384,
                    },
                };
                serde_saphyr::from_multiple_with_options(&text, options).map_err(|error| {
                    let line = error.location().and_then(|location| i32::try_from(location.line()).ok()).unwrap_or(0);
                    Error::invalid("invalid-yaml", path, error.to_string(), "Correct the YAML syntax and value types, keep keys unique, and keep aliases within the document limits.")
                        .at_line(path, line)
                })
            })
            .transpose()
    }

    /// Read a manifest containing exactly one nonempty YAML document. Absence
    /// remains distinct from an empty file or an ambiguous document stream.
    pub fn yaml_document<T: DeserializeOwned>(&self, path: &str) -> Result<Option<T>, Error> {
        let Some(mut documents) = self.yaml::<T>(path)? else {
            return Ok(None);
        };
        if documents.len() != 1 {
            return Err(Error::invalid(
                "invalid-yaml",
                path,
                "the manifest must contain exactly one nonempty YAML document",
                "Keep one manifest document in this file.",
            ));
        }
        Ok(documents.pop())
    }

    /// Read one typed JSON document, as a lock a tool writes in JSON is.
    /// Malformed values, a value of another type, a key the struct knows
    /// given twice and anything after the document are errors, not
    /// defaults; a key the struct does not know is not looked at, twice or
    /// once;
    /// nesting is limited to the parser's 128 levels and the file to 4 MiB.
    /// Unknown keys are the struct's concern.
    pub fn json_document<T: DeserializeOwned>(&self, path: &str) -> Result<Option<T>, Error> {
        self.document_text(path)?
            .map(|text| {
                serde_json::from_str(&text).map_err(|error| {
                    Error::invalid(
                        "invalid-json",
                        path,
                        error.to_string(),
                        "Correct the JSON syntax and value types, keeping each key unique.",
                    )
                    .at_line(path, i32::try_from(error.line()).unwrap_or(0))
                })
            })
            .transpose()
    }

    fn document_text(&self, path: &str) -> Result<Option<String>, Error> {
        let text = self.text(path)?;
        if text
            .as_ref()
            .is_some_and(|text| text.len() > MAX_DOCUMENT_BYTES)
        {
            return Err(Error::invalid(
                "document-too-large",
                path,
                "the manifest exceeds the 4 MiB document limit",
                "Split the manifest into smaller ecosystem documents.",
            ));
        }
        Ok(text)
    }
}

// The chosen deserializer treats any explicitly tagged null scalar as null,
// even `!!null banana`. The SDK validates that scalar's schema before values
// can disappear during typed deserialization (including ignored fields and
// empty-document handling). This is the dependency's own public parser, not a
// second grammar: it resolves tags/directives and decodes scalar escapes.
// Input is already size-limited; the event pass never expands aliases and
// bounds both parser nesting and total events before semantic conversion.
fn validate_yaml_nulls(path: &str, text: &str) -> Result<(), Error> {
    use serde_saphyr::granit_parser::{self, Event, Parser};
    let options = granit_parser::options! {
        strict_indentation: true,
        flow_nesting_limit: MAX_YAML_DEPTH,
        block_nesting_limit: MAX_YAML_DEPTH,
    };
    let invalid = |message: String, line: usize| {
        Error::invalid(
            "invalid-yaml",
            path,
            message,
            "Use valid YAML scalar values and keep the document within the shared parsing limits.",
        )
        .at_line(path, i32::try_from(line).unwrap_or(0))
    };
    for (count, event) in Parser::new_from_str_with_options(text, options).enumerate() {
        let (event, span) =
            event.map_err(|error| invalid(error.to_string(), error.marker().line()))?;
        if count >= MAX_YAML_EVENTS {
            return Err(invalid(
                "the YAML stream exceeds its event limit".into(),
                span.start.line(),
            ));
        }
        if let Event::Scalar(value, _, _, Some(tag)) = event {
            if tag.is_yaml_core_schema_tag("null")
                && !matches!(value.as_ref(), "" | "~" | "null" | "Null" | "NULL")
            {
                return Err(invalid(
                    "a null-tagged scalar must contain an empty value, ~, null, Null or NULL"
                        .into(),
                    span.start.line(),
                ));
            }
        }
    }
    Ok(())
}

/// TOML that does not read: where it stops reading, and why. Read as a
/// table or as a type, it is refused alike.
fn invalid_toml(path: &str, text: &str, error: &toml::de::Error) -> Error {
    Error::invalid(
        "invalid-toml",
        path,
        error.message(),
        "Correct the TOML, keeping each key unique and each value of its kind.",
    )
    .at_line(path, line_of(text, error.span().map(|span| span.start)))
}

/// A typed document with the text it was read from.
#[derive(Clone, Debug, PartialEq)]
pub struct Typed<T> {
    /// The document, read as its type.
    pub value: T,
    /// Its text, for the line a value is on ([`line_of`]).
    pub text: String,
}

/// The one-based line an offset of a text is on; zero for no offset.
pub fn line_of(text: &str, offset: Option<usize>) -> i32 {
    let Some(offset) = offset else {
        return 0;
    };
    let line = text.as_bytes()[..offset.min(text.len())]
        .iter()
        .filter(|&&byte| byte == b'\n')
        .count()
        + 1;
    i32::try_from(line).unwrap_or(0)
}
