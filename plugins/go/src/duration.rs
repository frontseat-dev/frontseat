//! A length of time as the go command writes one, read as it reads one:
//! what a test program's `-test.timeout` takes. Ported from
//! time.ParseDuration in Go 1.27.3 (src/time/format.go; copyright and
//! license in ../LICENSE.go.txt), and held to the table Go holds its own
//! to (src/time/time_test.go) and to the go command itself
//! (`reads_every_duration_as_the_go_command_does`).
//!
//! It is a port because no crate the workspace locks reads this
//! syntax: prost-types reads protobuf's JSON form of a duration
//! (seconds alone, "3.5s"), and nothing else in Cargo.lock reads a
//! length of time.

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
        // Go's sum wraps where it is past what a number holds, and so
        // does this one, in every build.
        total = total.wrapping_add(value);
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

    const SECOND: i64 = 1_000_000_000;
    const MINUTE: i64 = 60 * SECOND;
    const HOUR: i64 = 60 * MINUTE;

    /// Go's own table of what it reads (parseDurationTests), whole.
    const READ: &[(&str, i64)] = &[
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
    ];

    /// Go's own table of what it refuses (parseDurationErrorTests), but
    /// the entries that are no text: an option's value is text.
    const REFUSED: &[&str] = &[
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
    ];

    /// Two numbers that are each the most a duration holds and one
    /// more: Go's sum of them wraps to nothing, which it reads as 0.
    const WRAPPED: &[&str] = &[
        "9223372036854775808ns9223372036854775808ns",
        "-9223372036854775808ns9223372036854775808ns",
    ];

    #[test]
    fn a_duration_is_read_as_go_reads_it() {
        for (written, nanos) in READ {
            assert_eq!(nanoseconds(written), Some(*nanos), "{written}");
        }
    }

    #[test]
    fn what_go_reads_as_no_duration_is_none() {
        for written in REFUSED {
            assert_eq!(nanoseconds(written), None, "{written}");
        }
    }

    #[test]
    fn a_sum_past_what_a_number_holds_wraps_as_gos_does() {
        for written in WRAPPED {
            assert_eq!(nanoseconds(written), Some(0), "{written}");
        }
    }

    /// The go program the differential run asks: for each line it is
    /// given, the nanoseconds time.ParseDuration reads, or "none".
    const ASKED: &str = r#"package main

import (
	"bufio"
	"fmt"
	"os"
	"time"
)

func main() {
	lines := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for lines.Scan() {
		if d, err := time.ParseDuration(lines.Text()); err != nil {
			fmt.Fprintln(out, "none")
		} else {
			fmt.Fprintln(out, int64(d))
		}
	}
}
"#;

    /// The differential run that holds the port to time.ParseDuration
    /// itself, of the go command the test is given
    /// (`spec.frontseat.test.tools` of the plugin's catalog file, and
    /// mise): both of Go's tables, the sums that wrap, and twenty
    /// thousand texts made of what a duration is written with, the same
    /// on every run.
    #[test]
    fn reads_every_duration_as_the_go_command_does() {
        use std::io::Write;
        const PIECES: &[&str] = &[
            "0",
            "1",
            "9",
            ".",
            "s",
            "m",
            "h",
            "n",
            "u",
            "\u{b5}",
            "\u{3bc}",
            "-",
            "+",
            " ",
            "5",
            "d",
            "922337203685477580",
            "8",
            "7",
        ];
        let mut written: Vec<String> = READ
            .iter()
            .map(|(written, _)| *written)
            .chain(REFUSED.iter().copied())
            .chain(WRAPPED.iter().copied())
            .map(str::to_string)
            .collect();
        // A linear congruential generator (Knuth's MMIX), so that every
        // run asks the same.
        let mut state: u64 = 1;
        let mut next = |below: usize| {
            state = state
                .wrapping_mul(6364136223846793005)
                .wrapping_add(1442695040888963407);
            (state >> 33) as usize % below
        };
        for _ in 0..20_000 {
            let pieces = 1 + next(8);
            written.push((0..pieces).map(|_| PIECES[next(PIECES.len())]).collect());
        }
        let module = std::env::temp_dir().join(format!("go-durations-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&module);
        std::fs::create_dir_all(&module).unwrap();
        std::fs::write(
            module.join("go.mod"),
            "module example.com/durations\n\ngo 1.21\n",
        )
        .unwrap();
        std::fs::write(module.join("main.go"), ASKED).unwrap();
        let mut go = crate::programs::go_command(&module.join("_kept"))
            .args(["run", "."])
            .current_dir(&module)
            .stdin(std::process::Stdio::piped())
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .spawn()
            .expect("the go command this test holds the plugin to is not found");
        let mut asked = go.stdin.take().unwrap();
        let lines = written.join("\n") + "\n";
        let writer = std::thread::spawn(move || asked.write_all(lines.as_bytes()));
        let answered = go.wait_with_output().unwrap();
        let _ = writer.join();
        let _ = std::fs::remove_dir_all(&module);
        assert!(
            answered.status.success(),
            "the go command reads nothing: {}",
            String::from_utf8_lossy(&answered.stderr)
        );
        let answers = String::from_utf8(answered.stdout).unwrap();
        let answers: Vec<&str> = answers.lines().collect();
        assert_eq!(answers.len(), written.len());
        let differences: Vec<String> = written
            .iter()
            .zip(answers)
            .filter_map(|(written, go)| {
                let here = nanoseconds(written).map_or("none".to_string(), |n| n.to_string());
                (here != go).then(|| format!("{written:?}: {here} here, {go} to the go command"))
            })
            .collect();
        assert!(differences.is_empty(), "{}", differences.join("\n"));
    }
}
