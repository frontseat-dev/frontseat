use std::str;

use winnow::ascii::line_ending;
use winnow::combinator::alt;
use winnow::combinator::repeat;
use winnow::combinator::trace;
use winnow::combinator::{cut_err, eof, fail, opt, peek};
use winnow::combinator::{delimited, preceded, terminated};
use winnow::error::{AddContext, ErrMode, ParserError, StrContext};
use winnow::prelude::*;
use winnow::token::{take, take_till, take_while};

type CommitDetails<'a> = (
    &'a str,
    Option<&'a str>,
    bool,
    &'a str,
    Option<&'a str>,
    Vec<(&'a str, &'a str, &'a str)>,
);

pub(crate) fn parse<
    'a,
    E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug,
>(
    i: &mut &'a str,
) -> ModalResult<CommitDetails<'a>, E> {
    message.parse_next(i)
}

// <CR>              ::= "0x000D"
// <LF>              ::= "0x000A"
// <newline>         ::= [<CR>], <LF>
fn is_line_ending(c: char) -> bool {
    c == '\n' || c == '\r'
}

// <parens>          ::= "(" | ")"
fn is_parens(c: char) -> bool {
    c == '(' || c == ')'
}

// <ZWNBSP>          ::= "U+FEFF"
// <TAB>             ::= "U+0009"
// <VT>              ::= "U+000B"
// <FF>              ::= "U+000C"
// <SP>              ::= "U+0020"
// <NBSP>            ::= "U+00A0"
// /* See: https://www.ecma-international.org/ecma-262/11.0/index.html#sec-white-space */
// <USP>             ::= "Any other Unicode 'Space_Separator' code point"
// /* Any non-newline whitespace: */
// <whitespace>      ::= <ZWNBSP> | <TAB> | <VT> | <FF> | <SP> | <NBSP> | <USP>
fn is_whitespace(c: char) -> bool {
    c.is_whitespace()
}

fn whitespace<'a, E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    take_while(0.., is_whitespace).parse_next(i)
}

// <message>         ::= <summary>, <newline>+, <body>, (<newline>+, <footer>)*
//                    |  <summary>, (<newline>+, <footer>)*
//                    |  <summary>, <newline>*
pub(crate) fn message<
    'a,
    E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug,
>(
    i: &mut &'a str,
) -> ModalResult<CommitDetails<'a>, E> {
    trace("message", move |i: &mut &'a str| {
        let summary =
            terminated(trace("summary", summary), alt((line_ending, eof))).parse_next(i)?;
        let (type_, scope, breaking, description) = summary;

        // The body MUST begin one blank line after the description.
        let _ = alt((line_ending, eof))
            .context(StrContext::Label(BODY))
            .parse_next(i)?;

        let _extra: () = repeat(0.., line_ending).parse_next(i)?;

        let body = opt(body).parse_next(i)?;

        let footers = repeat(0.., footer).parse_next(i)?;

        let _: () = repeat(0.., line_ending).parse_next(i)?;

        Ok((type_, scope, breaking.is_some(), description, body, footers))
    })
    .parse_next(i)
}

// <type>            ::= <any UTF8-octets except newline or parens or ":" or "!:" or whitespace>+
pub(crate) fn type_<
    'a,
    E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug,
>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    trace(
        "type",
        take_while(1.., |c: char| {
            !is_line_ending(c) && !is_parens(c) && c != ':' && c != '!' && !is_whitespace(c)
        })
        .context(StrContext::Label(TYPE)),
    )
    .parse_next(i)
}

pub(crate) const TYPE: &str = "type";

// <scope>           ::= <any UTF8-octets except newline or parens>+
pub(crate) fn scope<
    'a,
    E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug,
>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    trace(
        "scope",
        take_while(1.., |c: char| !is_line_ending(c) && !is_parens(c))
            .context(StrContext::Label(SCOPE)),
    )
    .parse_next(i)
}

pub(crate) const SCOPE: &str = "scope";

// /* "!" should be added to the AST as a <breaking-change> node with the value "!" */
// <summary>         ::= <type>, "(", <scope>, ")", ["!"], ":", <whitespace>*, <text>
//                    |  <type>, ["!"], ":", <whitespace>*, <text>
#[expect(clippy::type_complexity, reason = "not worth abstracting")]
fn summary<'a, E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug>(
    i: &mut &'a str,
) -> ModalResult<(&'a str, Option<&'a str>, Option<&'a str>, &'a str), E> {
    trace(
        "summary",
        (
            type_,
            opt(delimited('(', cut_err(scope), ')')),
            opt(exclamation_mark),
            preceded(
                (':', whitespace),
                text.context(StrContext::Label(DESCRIPTION)),
            ),
        ),
    )
    .context(StrContext::Label(SUMMARY))
    .parse_next(i)
}

pub(crate) const SUMMARY: &str = "SUMMARY";
pub(crate) const DESCRIPTION: &str = "description";

// <text>            ::= <any UTF8-octets except newline>*
fn text<'a, E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    trace("text", take_till(1.., is_line_ending)).parse_next(i)
}

fn body<'a, E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    trace("body", move |i: &mut &'a str| {
        if i.is_empty() {
            let start = i.checkpoint();
            let err = E::from_input(i);
            let err = err.add_context(i, &start, StrContext::Label(BODY));
            return Err(ErrMode::Backtrack(err));
        }

        let mut offset = 0;
        let mut prior_is_empty = true;
        for line in crate::lines::LinesWithTerminator::new(i) {
            if prior_is_empty
                && peek::<_, _, ErrMode<E>, _>((token, separator))
                    .parse_peek(line.trim_end())
                    .is_ok()
            {
                break;
            }
            prior_is_empty = line.trim().is_empty();

            offset += line.chars().count();
        }
        if offset == 0 {
            fail::<_, (), _>(i)?;
        }

        take(offset).map(str::trim_end).parse_next(i)
    })
    .parse_next(i)
}

pub(crate) const BODY: &str = "body";

// <footer>          ::= <token>, <separator>, <whitespace>*, <value>
fn footer<'a, E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug>(
    i: &mut &'a str,
) -> ModalResult<(&'a str, &'a str, &'a str), E> {
    trace(
        "footer",
        (token, separator, whitespace, value).map(|(ft, s, _, fv)| (ft, s, fv)),
    )
    .parse_next(i)
}

// <token>           ::= <breaking-change>
//                    |  <type>
pub(crate) fn token<
    'a,
    E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug,
>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    trace("token", alt(("BREAKING CHANGE", type_))).parse_next(i)
}

// <separator>       ::= ":" | " #"
fn separator<'a, E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    trace("sep", alt((":", " #"))).parse_next(i)
}

pub(crate) fn value<
    'a,
    E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug,
>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    if i.is_empty() {
        let start = i.checkpoint();
        let err = E::from_input(i);
        let err = err.add_context(i, &start, StrContext::Label("value"));
        return Err(ErrMode::Cut(err));
    }

    let mut offset = 0;
    for (i, line) in crate::lines::LinesWithTerminator::new(i).enumerate() {
        if 0 < i
            && peek::<_, _, ErrMode<E>, _>((token, separator))
                .parse_peek(line.trim_end())
                .is_ok()
        {
            break;
        }

        offset += line.chars().count();
    }

    take(offset).map(str::trim_end).parse_next(i)
}

fn exclamation_mark<
    'a,
    E: ParserError<&'a str> + AddContext<&'a str, StrContext> + std::fmt::Debug,
>(
    i: &mut &'a str,
) -> ModalResult<&'a str, E> {
    "!".context(StrContext::Label(BREAKER)).parse_next(i)
}

pub(crate) const BREAKER: &str = "exclamation_mark";

#[cfg(test)]
mod tests {
    use super::*;

    use winnow::error::ContextError;

    /// Holds a parser to what it leaves and gives of each input it
    /// accepts, and to refusing each input it is given after.
    fn holds<'a, O: PartialEq + std::fmt::Debug>(
        mut p: impl Parser<&'a str, O, ErrMode<ContextError>>,
        accepted: Vec<(&'a str, &'a str, O)>,
        refused: &[&'a str],
    ) {
        for (input, left, given) in accepted {
            assert_eq!(p.parse_peek(input).unwrap(), (left, given), "{input:?}");
        }
        for input in refused {
            assert!(p.parse_peek(*input).is_err(), "{input:?}");
        }
    }

    /// Each spelling of one word a parser of a word takes whole, and
    /// the cases it is held to besides.
    fn whole<'a, const N: usize>(
        besides: [(&'a str, &'a str, &'a str); N],
    ) -> Vec<(&'a str, &'a str, &'a str)> {
        let mut cases: Vec<_> = ["foo", "Foo", "FOO", "fOO"]
            .into_iter()
            .map(|word| (word, "", word))
            .collect();
        cases.extend(besides);
        cases
    }

    mod message {
        use super::*;
        #[test]
        fn errors() {
            let mut p = message::<ContextError>;

            for input in ["Hello World", "fix Improved error messages\n"] {
                let err = p.parse(input).unwrap_err();
                let err = crate::Error::with_nom(input, err);
                assert_eq!(
                    err.to_string(),
                    crate::ErrorKind::MissingType.to_string(),
                    "{input:?}"
                );
            }
        }
    }

    mod summary {
        use super::*;

        #[test]
        fn test_type() {
            holds(
                type_::<ContextError>,
                whole([
                    ("foo2bar", "", "foo2bar"),
                    ("foo-bar", "", "foo-bar"),
                    ("foo bar", " bar", "foo"),
                    ("foo: bar", ": bar", "foo"),
                    ("foo!: bar", "!: bar", "foo"),
                    ("foo(bar", "(bar", "foo"),
                    ("foo ", " ", "foo"),
                ]),
                &["", " ", "  ", ")", " feat", " feat "],
            );
        }

        #[test]
        fn test_scope() {
            holds(
                scope::<ContextError>,
                whole([
                    ("foo bar", "", "foo bar"),
                    ("foo-bar", "", "foo-bar"),
                    ("x86", "", "x86"),
                ]),
                &["", ")"],
            );
        }

        #[test]
        fn test_text() {
            holds(
                text::<ContextError>,
                whole([
                    ("foo bar", "", "foo bar"),
                    ("foo bar\n", "\n", "foo bar"),
                    ("foo\nbar\nbaz", "\nbar\nbaz", "foo"),
                ]),
                &[""],
            );
        }

        #[test]
        fn test_summary() {
            holds(
                summary::<ContextError>,
                vec![
                    ("foo: bar", "", ("foo", None, None, "bar")),
                    ("foo(bar): baz", "", ("foo", Some("bar"), None, "baz")),
                    ("foo(bar):     baz", "", ("foo", Some("bar"), None, "baz")),
                    (
                        "foo(bar-baz): qux",
                        "",
                        ("foo", Some("bar-baz"), None, "qux"),
                    ),
                    ("foo!: bar", "", ("foo", None, Some("!"), "bar")),
                ],
                &[
                    "",
                    " ",
                    "  ",
                    "foo",
                    "foo bar",
                    "foo : bar",
                    "foo bar: baz",
                    "foo(: bar",
                    "foo): bar",
                    "foo(): bar",
                    "foo(bar)",
                    "foo(bar):",
                    "foo(bar): ",
                    "foo(bar):  ",
                    "foo(bar) :baz",
                    "foo(bar) : baz",
                    "foo (bar): baz",
                    "foo bar(baz): qux",
                ],
            );
        }
    }

    mod body {
        use super::*;

        #[test]
        fn test_body() {
            holds(
                body::<ContextError>,
                whole([
                    ("    code block", "", "    code block"),
                    ("💃🏽", "", "💃🏽"),
                    ("foo bar", "", "foo bar"),
                    ("foo\nbar\n\nbaz", "", "foo\nbar\n\nbaz"),
                    (
                        "foo\n\nBREAKING CHANGE: oops!",
                        "BREAKING CHANGE: oops!",
                        "foo",
                    ),
                    ("foo\n\nBREAKING-CHANGE: bar", "BREAKING-CHANGE: bar", "foo"),
                    ("foo\n\nMy-Footer: bar", "My-Footer: bar", "foo"),
                    ("foo\n\nMy-Footer #bar", "My-Footer #bar", "foo"),
                ]),
                &[""],
            );
        }

        #[test]
        fn test_footer() {
            holds(
                footer::<ContextError>,
                vec![
                    ("hello: world", "", ("hello", ":", "world")),
                    (
                        "BREAKING CHANGE: woops!",
                        "",
                        ("BREAKING CHANGE", ":", "woops!"),
                    ),
                    (
                        "Co-Authored-By: Marge Simpson <marge@simpsons.com>",
                        "",
                        ("Co-Authored-By", ":", "Marge Simpson <marge@simpsons.com>"),
                    ),
                    ("Closes #12", "", ("Closes", " #", "12")),
                    (
                        "BREAKING-CHANGE: broken",
                        "",
                        ("BREAKING-CHANGE", ":", "broken"),
                    ),
                ],
                &[
                    "",
                    " ",
                    "  ",
                    "foo",
                    "foo:",
                    "foo: ",
                    "foo ",
                    "foo #",
                    "BREAKING CHANGE",
                    "BREAKING CHANGE:",
                    "Foo-Bar",
                    "Foo-Bar: ",
                    "foo",
                ],
            );
        }
    }
}
