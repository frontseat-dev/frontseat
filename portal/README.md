# The portal

Frontseat's open-source portal: a [Backstage](https://backstage.io)
repository of an app (`packages/app`, the Component `portal-app`) and a
backend (`packages/backend`, `portal-backend`), each a subcomponent of the
portal (`portal-repository`). Frontseat builds it: the yarn plugin installs it, and the
Backstage plugin compiles, lints and tests it with the Backstage CLI.

What a backend serves as dynamic plugins, the app loads as it starts
(`@backstage/frontend-dynamic-feature-loader`), and the backend loads the
plugins its image holds (`@backstage/backend-dynamic-feature-service`,
`dynamicPlugins` in `app-config.production.yaml`): none of the open-source
portal's, and Frontseat Enterprise's where its image adds them.

Run it by hand with the Node and Yarn `mise.toml` pins:

```sh
yarn install
yarn start
```

## Where it came from

It began as the app template of Backstage's own `create-app` 0.9.2, for
Backstage 1.55.0, rendered from its source at the release's tag, under the
Apache License 2.0 (see `NOTICE`). Changed from the template:

- `package.json`: named `portal`; every `@backstage/` package pinned to
  the 1.55.0 release (`resolutions`), as `backstage-cli versions:bump
  --release 1.55.0` pins them; new packages licensed Apache-2.0; no
  `build-image` script.
- `packages/app/package.json`, `packages/app/src/App.tsx`: the dynamic
  plugin loader.
- `packages/backend/package.json`, `packages/backend/src/index.ts`: the
  dynamic plugin service; no `build-image` script.
- `app-config.yaml`: no example Scaffolder template;
  `app-config.production.yaml`: where dynamic plugins are loaded from.
- `.yarnrc.yml`: no vendored Yarn release (`yarnPath`); Yarn is pinned in
  `mise.toml`.
- Left out: the vendored Yarn release, the CI workflow, the backend's
  `Dockerfile` and `.dockerignore`, the example Scaffolder template, and
  the template's catalog file; each package has a catalog file of its own.
