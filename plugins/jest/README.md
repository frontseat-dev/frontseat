# Jest

The `jest` plugin runs one configured Jest suite as one test piece. It
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
Jest's native behavior. Source and configuration changes invalidate the piece.

The installation must belong to this package's own repository and root.
A missing package-manager plugin or a provider from elsewhere is refused.
Services come from the owning Component's Resource dependencies and consumed
APIs through the same runtime mechanism as other tests.

The plugin takes no option: a suite is one piece, never cut into shards. It
refuses a test script that is not `jest`, or that passes arguments
(`invalid-manifest`), and a `package.json` whose name is missing, empty or
blank (`invalid-json`). Valid and not run yet, and refused `UNIMPLEMENTED` by
name: pretest/posttest hooks (`test-hooks-not-served`), npm workspaces
(`workspaces-not-served`) and an explicit `packageManager` selection
(`package-manager-not-served`). pnpm, Yarn, JUnit and Playwright adapters and
smaller Jest pieces remain unimplemented.
