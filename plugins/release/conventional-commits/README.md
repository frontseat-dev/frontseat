# Conventional commits

A parser of [Conventional Commits](https://www.conventionalcommits.org),
which the release plugin reads commit messages with.

This crate is a clone of
[git-conventional](https://github.com/crate-ci/git-conventional), at its
commit `442b31085999dd2f648c3a8de278d883f31430b6` (2026-11-01, after
v1.1.0), by its authors and under its licence, MIT or Apache-2.0
(`LICENSE-MIT`, `LICENSE-APACHE`). It is kept here, and not taken from
crates.io, for one reason: git-conventional takes `winnow` with its
default features, which turns on `winnow`'s `std` for every package of a
workspace that locks it, and frontseat's Cargo plugin builds each
package under the lock narrowed to that package, which cargo then
refuses for the packages that leave `std` off.

What differs from the original:

- `Cargo.toml` is this workspace's: the package is
  `frontseat-conventional-commits` and its library `conventional_commits`;
  `winnow` is taken without its default features, with `alloc`, `ascii`
  and `binary`.
- The crate's name in the examples of its documentation.
- What this repository's formatter and lints ask of every crate of the
  workspace, and nothing of what the parser does.

When git-conventional leaves `winnow`'s default features off, or the
Cargo plugin narrows a lock across such a feature, this crate goes and
the release plugin takes git-conventional again.
