# Release strategies

The `release` plugin serves the strategies a release is planned with. It
reads no file and finds no project: the host builds the release graph,
from the catalog and the repository's history, and asks the plugin two
things of each unit.

A repository that releases lists it in `frontseat.yaml`:

```yaml
plugins:
  release: {}
```

A unit names its strategies in `spec.frontseat.release`, each as
`<plugin>:<name>`. Where it names none it has the defaults,
`release:release-flow` and `release:conventional-commits`.

## Branching: `release-flow`

A branch named `release/<major>.<minor>` releases, held to the line its
name gives: `release/0.77` releases 0.77.x and nothing else. Each number
is written as a version writes it, with no sign and no leading zero. A
release is made by cutting the branch.

Every other branch releases a prerelease of the version it is heading
for, of a channel its name gives (`feat/greet` is `feat-greet`):

| Branch | Is heading for |
|---|---|
| the default branch | a line not cut yet: the next minor at least, above everything the unit was released as |
| any other | whatever its commits call for, above everything the unit was released as |
| one beneath `release/` that names no line (`release/1`, `release/next`) | nothing: it is a release branch written wrongly, and the plan says so |
| any, where the repository's default branch is not known (a clone with no `origin/HEAD`) | nothing: where it is heading is not known, and the plan says so |

A prerelease is never a unit's last release: the release that follows
is planned from the last release, and its notes say everything since,
what the prereleases held too.

How a prerelease is spelled is no strategy's to say, and is the unit's
setting, whichever strategy says its branch releases one:

```yaml
spec:
  frontseat:
    release:
      prerelease: "{branch}-{timestamp}"   # the default: 0.78.0-main-20261121101500
      prerelease-tag: false                # the default: a prerelease is not tagged
```

`{branch}` is the branch, every character a semantic version does not
take in a prerelease made a dash, as far as its sixty-fourth; `{timestamp}` is when the commit the
release is planned at was made, in UTC, always fourteen digits, so that
a branch's prereleases are ordered as their commits were made;
`{epoch}` is the same time in seconds, and `{sha}` the commit as far as
its seventh character. The time is the commit's, never the clock's: a
commit has one prerelease version wherever it is planned.

A branch and a commit may each be written in digits alone, beginning
with a zero, which a semantic version takes for no number: a form that
makes `{branch}` or `{sha}` a part of its own between dots is refused
when the catalog is read, and joins it to another part with a dash.

A prerelease comes after the unit's highest release, wherever in the
repository its tag is, and says what moved it since: under Release Flow
that tag is on a release branch the default branch does not reach.

Not ordered: a form with no time, and an `{epoch}` that is not a part
of its own between dots. A commit's time is what whoever made it says
it is. Two commits of a branch made in the same second have one
version.

## Versioning: `conventional-commits`

A unit moves by the heaviest of its commits since its last release:

| Commit | From 1.0 on | Below 1.0 |
|---|---|---|
| `feat!:`, or a `BREAKING CHANGE:` footer | major | minor |
| `feat:` | minor | minor |
| `fix:`, `perf:` | patch | patch |
| anything else, or no conventional commit | none | none |

Below 1.0 no commit takes a unit to 1.0. A unit never released is
released at its initial version, whatever its commits say; on a release
branch the host holds that to the branch's line. Versions are read by [`semver`](https://crates.io/crates/semver), and
commits by `conventional-commits/`, this repository's clone of
[`git-conventional`](https://crates.io/crates/git-conventional): its
README says why it is a clone, and when it goes.

The answer names each commit that moved the unit, heaviest first, and
writes release notes from their descriptions.

## Not built yet

GitHub Flow and GitLab Flow are not served: a strategy the plugin does
not say is refused by the SDK before the plugin is asked
(`unclaimed-strategy`). Release plans and a version a person
gives are not in the contract.

A `revert:` commit moves nothing and takes nothing back: a feature that
was reverted still moves its unit. A feature on a release branch leaves
the branch's line, and its unit is refused there until the commit is
gone.

## Tests

`cargo test -p frontseat-plugin-release` runs the strategies on the
machine. The shared conformance harness asks the built module the same
through the host (`server/internal/pluginhost/release_test.go`):
a strategy plugin's fixture is the questions it is asked and the exact
answers, since it has no source to analyze.
