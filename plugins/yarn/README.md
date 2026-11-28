# yarn

The `yarn` plugin installs a Yarn workspace from its lock, offline, as the
node-modules linker lays it out. Yarn 4.18.1 and later are served; Node and Yarn
come from the repository's `mise.toml` and checked `mise.lock`. A tool on
the grid is held to the checksum its lock records, which mise's npm backend
records none of, so Yarn is pinned as its own npm package's archive, its
launcher made runnable, since an npm archive keeps no file's mode:

```toml
# mise.toml
[tools]
node = "22.23.1"

[tools."http:yarn"]
version = "4.19.0"
url = "https://registry.npmjs.org/@yarnpkg/cli-dist/-/cli-dist-{{ version }}.tgz"
strip_components = 1
bin_path = "bin"
postinstall = 'chmod +x "$MISE_TOOL_INSTALL_PATH/bin/yarn"'
```

An install is a directory whose `package.json` has a `yarn.lock` beside it.
Its workspaces are the directories holding a `package.json` that the root's
`workspaces` globs select, as Yarn selects them, beneath `testdata` too;
they are no projects of their own. A repository installs with one of npm,
Yarn and pnpm, and lists the one plugin: a package no Yarn install serves is
refused, `pnpm-not-served` where a pnpm install serves it (a
`pnpm-lock.yaml` beside it or above it),
`npm-not-served` where it has a `package-lock.json`, each saying which
plugin to list in place of yarn, and `invalid-lock` where no lock serves
it; the npm plugin refuses a package Yarn or pnpm installs alike (`javascript::install_of` in
the plugin SDK says which install serves a package). The actions of an install in `web/` are:

| Action | What it does |
|---|---|
| `//web:yarn.fetch` | `yarn install --immutable --mode=skip-build`, from the registry the worker names; retains only Yarn's cache, `.yarn/cache/` |
| `//web:yarn.install` | `yarn install --immutable --immutable-cache` with Yarn's network off; retains `node_modules/`, the root's and each workspace's |

The install provides `javascript-install:<root package name>` with the
root's `node_modules/` and each workspace's, as the npm plugin provides an
npm package's.

## How Yarn is run

Every action runs the Yarn `mise.toml` pins and ignores a release a
`.yarnrc.yml`'s `yarnPath` vendors (`YARN_IGNORE_PATH`); keeps Yarn's cache
in the install's own `.yarn/cache` rather than the user's; and fetches from
the registry the grid's worker names (`YARN_NPM_REGISTRY_SERVER` in its own
environment, which an action's would be set over): npm's on the local
stack, a mirror of it where the grid has one. Where the worker names none,
Yarn reads `.yarnrc.yml`, which may name only `registry.npmjs.org` or Yarn's
own name for it, `registry.yarnpkg.com`, the same registry; a Yarn lock
names no registry, and every archive is held to its checksum wherever it
came from. A mirror over plain HTTP is allowed by the worker too
(`YARN_UNSAFE_HTTP_WHITELIST`). Every action checks each archive against the lock's checksum
(`YARN_CHECKSUM_BEHAVIOR=throw`) and verifies every certificate
(`YARN_ENABLE_STRICT_SSL`): a variable of Yarn's own takes the place of what
`.yarnrc.yml` or `.env.yarn` say. The fetch builds nothing, so no
dependency's code runs with a network. The install builds what Yarn
builds with no network either: a native module is built from the source
its checked archive holds (`npm_config_build_from_source`), with the
worker's C toolchain, against the headers of the Node `mise.toml` pins,
which Yarn is started by so that node-gyp is told that Node's directory
(`npm_config_nodedir`), since a tool's directory is nothing an action can
name. What Yarn builds is what the repository lets it: a lock Yarn 4.14
or later wrote (its version 9 and later) runs no dependency's install
script unless the root's `package.json` allows that package
(`"dependenciesMeta": {"better-sqlite3": {"built": true}}`), so a native
module the repository needs is named there, and no other script runs. A
native module that is an optional dependency and fails to build is
left out, as Yarn leaves it. The install reads the manifests, the lock, `.yarnrc.yml` and
`.yarn/patches/`, runs with no network, refuses a lock that no longer
matches the manifests (`--immutable`) and a cache that would change
(`--immutable-cache`), and builds what Yarn builds.

Both outputs are optional: Yarn writes no cache and no `node_modules` where
there is nothing to install, and a workspace has a `node_modules` of its own
only where Yarn could not hoist what it needs.

## What is refused

What Yarn reads and the plugin does not build yet is refused
`UNIMPLEMENTED`, each by its own reason: a Yarn 1 lock, one with no
`__metadata`, or a pinned Yarn 1 (`yarn-classic-not-served`); a linker other
than `node-modules`, which is Yarn's default, so `.yarnrc.yml` must say
`nodeLinker: node-modules` (`linker-not-served`); Yarn plugins the
repository loads (`yarn-plugins-not-served`); a registry other than npm's
(`registry-not-served`); a setting of `.yarnrc.yml` that changes where or
how Yarn fetches or who it says it is: a proxy, a certificate, a credential,
`networkSettings`, `unsafeHttpWhitelist` or another registry
(`fetch-settings-not-served`); a `preinstall`, `install` or `postinstall`
script of a workspace (`install-hooks-not-served`); a workspace with
workspaces of its own (`nested-workspaces-not-served`); and a lock entry
resolved otherwise than from the npm registry by a version, the workspace,
a link to a directory within the install's root (as Backstage's template
links its app to its backend: `link:../app`, from the workspace the
locator Yarn adds names), or a patch of an npm package by a version with
Yarn's own patch or a file beneath `.yarn/patches`: git, a tarball, a file,
a link beyond the root, a command, or an
npm package with any parameter, `__archiveUrl` among them, which has Yarn
fetch it from the address it names (`resolution-not-served`). An npm
package locked with no checksum, and an entry with no resolution, are
refused as `invalid-lock`; one Yarn fetches on some platforms only is the
exception, since Yarn records no checksum for it: it is fetched from npm's
registry by its name and version, which npm never lets be published twice,
and nothing checks its bytes; a `packageManager` naming another than the
pinned Yarn as `invalid-package-manager`; a pinned Yarn before 4.18.1,
whose fetch can crash retrying a request that has already settled (Yarn's
#7249), as `unsupported-yarn`; a Yarn pinned by no exact version, whose
archive cannot be named, as `inexact-yarn`; and an unpinned Yarn as `unpinned-tool`. A refusal of the lock names its line and never repeats
what is written there.

The lock is read line by line, in the layout Yarn writes, rather than as a
YAML document: a Backstage workspace's lock holds more than the SDK reads of
one. It is not yet held to Yarn's own reading by a differential run.

Not served yet: a workspace's installation provided at its own directory,
so that a framework can test one workspace; private registries; and Plug'n'Play.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name.

```yaml
# frontseat.yaml
plugins:
  yarn: {}
```

## Catalog keys

None: nothing a catalog file says reaches the plugin.
