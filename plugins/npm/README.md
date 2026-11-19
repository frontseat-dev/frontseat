# npm

The `npm` plugin prepares locked dependencies independently of test runners.
Node, including its bundled npm, comes from the repository's
`mise.toml` and checked `mise.lock`. Jest comes from `package-lock.json`.

List `npm` and your catalog adapter in `frontseat.yaml`, pin Node through
mise, and commit a standalone package and its native v3 lock.
The actions of a package in `web/` are:

| Action | What it does |
|---|---|
| `//web:npm.fetch` | `npm ci --ignore-scripts --no-audit --no-fund`, through the registry proxy; retains only npm's content cache |
| `//web:npm.install` | `npm ci --offline --no-audit --no-fund`; retains `node_modules/` |

The project provides `javascript-install:<package name>` with the exact
`node_modules/` output of `install`. Consumers ask for that coordinate, naming
no npm actions. Frontseat schedules preparation and passes the output by digest.

The cache and installation are optional outputs: native npm creates neither
when there is nothing to install, including when it excludes every dependency
for the worker's platform. The same native commands still check the lock;
Frontseat preserves absence rather than manufacturing an empty directory.

The SDK first checks that each lock integrity is one canonical SHA-512 value;
npm verifies the downloaded bytes against it. npm itself accepts some malformed
integrity values as though no checksum were supplied, so its verifier alone
is insufficient. Dependency install hooks
run only during offline installation. Acquisition currently uses the whole
lock: npm's single-URL cache command cannot be given the lock's integrity,
and Frontseat's archive download contract does not yet support npm's SHA-512
tarball handoff. This costs extraction twice and invalidates acquisition when
the lock changes.

The first adapter accepts npm v3 locks of SHA-512-locked archives from
`registry.npmjs.org`. It explicitly refuses npm workspaces, other package
managers or `packageManager` selection, shrinkwrap, `.npmrc`, local/git/linked
or bundled dependencies, other checksum algorithms or SRI expressions,
implicit root `binding.gyp` builds, root install
hooks. Test-script semantics belong to the framework adapter.
Dependency hooks that require network access fail offline. Hooks that need
undeclared tools or outputs outside `node_modules/` are not supported.

pnpm and Yarn adapters, private registries, workspace linking and generated
application builds remain unimplemented. See `plugins/jest/README.md` for tests.
