//! Strict checksums in native locks: written `algorithm-base64`, the
//! single-checksum shape apko and npm write (not a parser of arbitrary
//! SRI expressions), each caller choosing its algorithm and length; and
//! written in hex, as cargo's lock writes one.

use base64::engine::general_purpose::STANDARD;
use base64::Engine;

/// Decode one checksum of the expected algorithm and byte length. Unknown
/// algorithms, malformed base64, trailing data and wrong lengths are refused.
#[must_use]
pub fn decode(checksum: &str, algorithm: &str, bytes: usize) -> Option<Vec<u8>> {
    let raw = STANDARD
        .decode(checksum.strip_prefix(algorithm)?.strip_prefix('-')?)
        .ok()?;
    (raw.len() == bytes).then_some(raw)
}

/// Bytes written as lower-case hex, as a lock or a URL names a digest.
pub fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

/// Whether text is a digest of so many bytes written in lower-case hex.
pub fn is_hex(text: &str, bytes: usize) -> bool {
    text.len() == bytes * 2
        && text
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

#[cfg(test)]
mod tests {
    use super::{hex, is_hex};

    #[test]
    fn a_digest_is_written_and_read_in_lower_case_hex() {
        assert_eq!(hex(&[0x00, 0xab, 0x7f]), "00ab7f");
        assert!(is_hex("00ab7f", 3));
        for not in ["00AB7F", "00ab7", "00ab7f00", "00ab7g"] {
            assert!(!is_hex(not, 3), "{not}");
        }
    }
}
