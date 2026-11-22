# github

What exists because GitHub hosts the repository: a GitHub release for
each unit a release tags.

```yaml
# frontseat.yaml
plugins:
  github: {}
```

```toml
# mise.toml
[tools]
gh = "2.105.0"
```

- **The plugin takes no options.** Listing it is what says the
  repository is GitHub's.
- **One project, at the repository's root**, found from `frontseat.yaml`.
  Where no Component owns the root, no entity owns the project, and its
  publish runs for each unit of the repository a release tags.
- **One action, `publish/release`**, which publishes
  (the action kind `Publish`): no build runs it. A release runs it once its tags
  are pushed, where the daemon is, as

  ```
  gh release create <tag> <each artifact> checksums.txt
     --repo <where the repository was brought from> --verify-tag
     --title <tag> --notes-file <the unit's notes> --prerelease=<true|false>
  ```

  GitHub's command line uploads every file to a draft and publishes it
  once all are there, so a release is never seen half made. The tag
  must be there already: the release pushed it, and `gh` makes none. A
  release there already for the tag is an error.
- **Its token is `GH_TOKEN`**, of where the daemon runs, given only
  where the daemon was told it may be (`frontseatd --publish-environment
  GH_TOKEN`). In a GitHub job that is the job's own token, with
  `contents: write`.
- **Its tool is `gh`, pinned by the repository.** A repository that
  lists the plugin and pins no `gh` is refused when it is analysed,
  before anything is built or tagged, as every plugin refuses one that
  pins none of what its actions run.
- **A repository that lists the plugin and is not GitHub's** fails its
  publish with what `gh` says of the address; nothing here reads the
  remote.

Not held: no test runs `gh` or reaches GitHub; `.github/` is not what
the project is found from, since Frontseat reads nothing beneath a
directory whose name begins with a dot.

`cargo test -p frontseat-plugin-github` runs its tests through the
plugin testkit; `TestTheGitHubPluginsPublishIsRunByARelease` runs it
through the host.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name. Listing it is what says the
repository is GitHub's.

```yaml
# frontseat.yaml
plugins:
  github: {}
```

## Catalog keys

None: nothing a catalog file says reaches the plugin.
