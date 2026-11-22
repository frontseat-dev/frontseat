//! A length of time as the go command writes one, read as it reads one:
//! what a test program's `-test.timeout` takes. Ported from
//! time.ParseDuration in Go 1.27.3 (src/time/format.go; copyright and
//! license in ../LICENSE.go.txt), and held to the table Go holds its own
//! to (src/time/time_test.go).

/// One more than the most nanoseconds a duration holds: what the least
/// one holds, below nothing.
const MOST: u64 = 1 << 63;

/// The nanoseconds a duration written as the go command writes one
/// says, or none where the go command would refuse it: a signed
/// sequence of decimal numbers, each with a unit, as "90s", "1h30m" or
/// "1.5h"; and "0", which needs none.
pub(super) fn nanoseconds(written: &str) -> Option<i64> {
    let mut rest = written.as_bytes();
    let negative = rest.first() == Some(&b'-');
    if matches!(rest.first(), Some(b'-' | b'+')) {
        rest = &rest[1..];
    }
    if rest == b"0" {
        return Some(0);
    }
    if rest.is_empty() {
        return None;
    }
    let mut total: u64 = 0;
    while let Some(first) = rest.first() {
        if *first != b'.' && !first.is_ascii_digit() {
            return None;
        }
        let (mut value, after) = whole(rest)?;
        let before_point = after.len() != rest.len();
        rest = after;
        let (mut fraction, mut scale, mut after_point) = (0, 1.0, false);
        if let Some(digits) = rest.strip_prefix(b".") {
            let after;
            (fraction, scale, after) = part(digits);
            after_point = after.len() != digits.len();
            rest = after;
        }
        if !before_point && !after_point {
            return None;
        }
        let end = rest
            .iter()
            .position(|byte| *byte == b'.' || byte.is_ascii_digit())
            .unwrap_or(rest.len());
        let unit = unit(&rest[..end])?;
        rest = &rest[end..];
        if value > MOST / unit {
            return None;
        }
        value *= unit;
        if fraction > 0 {
            // A float, as Go has it: exact to the nanosecond for a part
            // of an hour.
            value += (fraction as f64 * (unit as f64 / scale)) as u64;
            if value > MOST {
                return None;
            }
        }
        total += value;
        if total > MOST {
            return None;
        }
    }
    let signed = i128::from(total) * if negative { -1 } else { 1 };
    i64::try_from(signed).ok()
}

/// The nanoseconds of a unit, or none where it is no unit of Go's.
fn unit(named: &[u8]) -> Option<u64> {
    Some(match named {
        b"ns" => 1,
        // "us", and the same with the micro sign (U+00B5) and with the
        // Greek letter mu (U+03BC).
        b"us" | b"\xc2\xb5s" | b"\xce\xbcs" => 1_000,
        b"ms" => 1_000_000,
        b"s" => 1_000_000_000,
        b"m" => 60_000_000_000,
        b"h" => 3_600_000_000_000,
        _ => return None,
    })
}

/// The number the leading digits say and what follows them, or none
/// where it is more than a duration holds (Go's leadingInt).
fn whole(text: &[u8]) -> Option<(u64, &[u8])> {
    let mut value: u64 = 0;
    let mut read = 0;
    for byte in text.iter().take_while(|byte| byte.is_ascii_digit()) {
        if value > MOST / 10 {
            return None;
        }
        value = value * 10 + u64::from(byte - b'0');
        if value > MOST {
            return None;
        }
        read += 1;
    }
    Some((value, &text[read..]))
}

/// The digits after a point, as a number over a scale, and what follows
/// them: digits past what a number holds add nothing (Go's
/// leadingFraction).
fn part(text: &[u8]) -> (u64, f64, &[u8]) {
    let (mut value, mut scale, mut full): (u64, f64, bool) = (0, 1.0, false);
    let mut read = 0;
    for byte in text.iter().take_while(|byte| byte.is_ascii_digit()) {
        read += 1;
        if full {
            continue;
        }
        if value > (MOST - 1) / 10 {
            full = true;
            continue;
        }
        let next = value * 10 + u64::from(byte - b'0');
        if next > MOST {
            full = true;
            continue;
        }
        value = next;
        scale *= 10.0;
    }
    (value, scale, &text[read..])
}

#[cfg(test)]
mod tests {
    use super::nanoseconds;
    use frontseat_plugin::prelude::*;
    use frontseat_plugin_test::{Files, TestHost, TestPlugin};

    const SECOND: i64 = 1_000_000_000;
    const MINUTE: i64 = 60 * SECOND;
    const HOUR: i64 = 60 * MINUTE;

    /// Go's own table of what it reads (parseDurationTests), whole.
    #[test]
    fn a_duration_is_read_as_go_reads_it() {
        for (written, nanos) in [
            ("0", 0),
            ("5s", 5 * SECOND),
            ("30s", 30 * SECOND),
            ("1478s", 1478 * SECOND),
            ("-5s", -5 * SECOND),
            ("+5s", 5 * SECOND),
            ("-0", 0),
            ("+0", 0),
            ("5.0s", 5 * SECOND),
            ("5.6s", 5 * SECOND + 600_000_000),
            ("5.s", 5 * SECOND),
            (".5s", 500_000_000),
            ("1.0s", SECOND),
            ("1.00s", SECOND),
            ("1.004s", SECOND + 4_000_000),
            ("1.0040s", SECOND + 4_000_000),
            ("100.00100s", 100 * SECOND + 1_000_000),
            ("10ns", 10),
            ("11us", 11_000),
            ("12\u{b5}s", 12_000),
            ("12\u{3bc}s", 12_000),
            ("13ms", 13_000_000),
            ("14s", 14 * SECOND),
            ("15m", 15 * MINUTE),
            ("16h", 16 * HOUR),
            ("3h30m", 3 * HOUR + 30 * MINUTE),
            ("10.5s4m", 4 * MINUTE + 10 * SECOND + 500_000_000),
            ("-2m3.4s", -(2 * MINUTE + 3 * SECOND + 400_000_000)),
            (
                "1h2m3s4ms5us6ns",
                HOUR + 2 * MINUTE + 3 * SECOND + 4_005_006,
            ),
            (
                "39h9m14.425s",
                39 * HOUR + 9 * MINUTE + 14 * SECOND + 425_000_000,
            ),
            ("52763797000ns", 52_763_797_000),
            ("0.3333333333333333333h", 20 * MINUTE),
            ("9007199254740993ns", (1 << 53) + 1),
            ("9223372036854775807ns", i64::MAX),
            ("9223372036854775.807us", i64::MAX),
            ("9223372036s854ms775us807ns", i64::MAX),
            ("-9223372036854775808ns", i64::MIN),
            ("-9223372036854775.808us", i64::MIN),
            ("-9223372036s854ms775us808ns", i64::MIN),
            ("-2562047h47m16.854775808s", i64::MIN),
            ("0.100000000000000000000h", 6 * MINUTE),
            (
                "0.830103483285477580700h",
                49 * MINUTE + 48 * SECOND + 372_539_827,
            ),
        ] {
            assert_eq!(nanoseconds(written), Some(nanos), "{written}");
        }
    }

    /// Go's own table of what it refuses (parseDurationErrorTests), but
    /// the entries that are no text: an option's value is text.
    #[test]
    fn what_go_reads_as_no_duration_is_none() {
        for written in [
            "",
            "3",
            "-",
            "s",
            ".",
            "-.",
            ".s",
            "+.s",
            "1d",
            "\u{fffd}",
            "\u{fffd} hello \u{fffd} world",
            "9223372036854775810ns",
            "9223372036854775808ns",
            "-9223372036854775809ns",
            "9223372036854776us",
            "3000000h",
            "9223372036854775.808us",
            "9223372036854ms775us808ns",
            "ten minutes",
            "10 m",
        ] {
            assert_eq!(nanoseconds(written), None, "{written}");
        }
    }

    /// A test-timeout the go command would not read is refused where
    /// the module is read, by the option's name and without the value;
    /// the control is one it reads, which every test of the module is
    /// started with.
    #[test]
    fn a_test_timeout_that_is_no_duration_is_refused_by_name() {
        let plugin = TestPlugin::new(crate::served());
        let host = TestHost::new(
            Files::new()
                .with("app/go.mod", "module example.com/hello\n\ngo 1.27\n")
                .with("app/hello.go", "package hello\n")
                .with("app/hello_test.go", "package hello\nfunc TestA() {}\n"),
        );
        let timed = |timeout: &str| {
            let context = plugin
                .context(Profile::Development)
                .option("test-timeout", Value::String(timeout.into()));
            plugin.analyze(&host, &context, &["app"])
        };
        let answered = timed("1h30m").answered();
        let test = answered.analyses[0].projects[0]
            .actions
            .iter()
            .find(|action| action.name == "test/.")
            .expect("the module's own package has a test");
        assert!(test
            .command
            .as_ref()
            .unwrap()
            .arguments
            .contains(&"-test.timeout=1h30m".to_string()));
        for written in ["ten-minutes-of-secret", "10", ""] {
            let refused = timed(written).refused("invalid-test-timeout");
            assert_eq!(refused.path, "app/go.mod");
            assert!(refused.message.contains("test-timeout"));
            assert!(!refused.message.contains("secret") && !refused.help.contains("secret"));
        }
    }
}
