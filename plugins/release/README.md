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
is written as a version writes it, with no sign and no leading zero.
Every other branch releases nothing, the default branch among them, and
the plan says why. A release is made by cutting the branch.

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

GitHub Flow and GitLab Flow are not served: the plugin refuses a
strategy it does not say, by name. Release plans and a version a person
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
