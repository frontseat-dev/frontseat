# Jest

The `jest` plugin runs one configured Jest suite as one test atom. It
consumes installed dependencies from a package-manager plugin; Node is a
mise tool. The first supported preparation adapter is npm.

```yaml
plugins:
  backstage: {}
  npm: {}
  jest: {}
```

Declare Jest as a direct dependency in a standalone package, with
`"scripts": {"test": "jest"}`. Keep options in native Jest configuration.
The inferred `//web:jest.test` action runs `npm run test -- --ci` with the
package source and installed dependencies. Native script variables and
installed commands on `PATH` remain available.

Jest owns discovery, configuration, setup/teardown, workers and retries.
JavaScript executes only on the worker. Native output and exit status
determine the result; reports are not normalized. Empty selections retain
Jest's native behavior. Source and configuration changes invalidate the atom.

The installation must belong to this package's own repository and root.
A missing package-manager plugin or a provider from elsewhere is refused.
Services come from the owning Component's Resource dependencies and consumed
APIs through the same runtime mechanism as other tests.

The adapter requires `test-shards: 1`. It refuses arbitrary test scripts,
script arguments, pretest/posttest hooks, npm workspaces and explicit
`packageManager` selection. pnpm, Yarn, JUnit and Playwright adapters and
smaller Jest atoms remain unimplemented.
