//! Strict checksums in native locks, written `algorithm-base64`.
//!
//! This is the single-checksum shape apko and npm write, not a parser of
//! arbitrary SRI expressions. Each caller chooses its algorithm and length.

use base64::engine::general_purpose::STANDARD;
use base64::Engine;

/// Decode one checksum of the expected algorithm and byte length. Unknown
/// algorithms, malformed base64, trailing data and wrong lengths are refused.
pub fn decode(checksum: &str, algorithm: &str, bytes: usize) -> Option<Vec<u8>> {
    let raw = STANDARD
        .decode(checksum.strip_prefix(algorithm)?.strip_prefix('-')?)
        .ok()?;
    (raw.len() == bytes).then_some(raw)
}
