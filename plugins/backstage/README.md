# Backstage plugin

The Rust plugin discovers every document in `catalog-info.yaml` through the
SDK's tracked, bounded YAML reader. `Analyze` returns catalog entities separately
from build projects. A component governs its descriptor's directory; organizational
and API entities acquire no source roots merely by sharing that file.

`catalog.rs` interprets the descriptor and built-in specification fields,
`metadata.rs` validates standard metadata, and `relations.rs` maps built-in fields
to Backstage relations and inverse names. Extension fields remain allowed.
Component, API, Resource, System, Domain, Group and User use Backstage's built-in
relation mappings; other kinds contribute their identity without invented relations.
A Location is held to Backstage's schema (a specification, whose `type` and
`target` are nonempty text, `targets` a list of them and `presence` required or
optional) and is otherwise kept as written, with no source and no relation: its
targets are Backstage's to read. Frontseat finds catalog files by their name and
follows no Location. This is catalog discovery, not a Backstage processor runtime:
Location targets, `$text`/`$json` substitutions and scaffolding are not executed
here. This repository's APIs name their definitions as paths, from the catalog
file's directory, and not as `$text` substitutions, since frontseat reads none:
Backstage shows such a definition as the text of the path, not the document it
names. The architecture test holds each path to what is in the repository.

The host validates all answers, refuses duplicate identities and ambiguous source
ownership, and assembles the graph. References are canonical and namespace relative.
External targets remain visible. Component `dependsOn` and `dependencyOf` are held
to source inference; authored catalog fields cannot create build edges. When a file
is out of step, the host asks the plugin what it should say (`Reconcile`), with the
components the source says each entity depends on and is depended on by. The
plugin answers edits: `spec.dependsOn` of the document that describes the entity,
and `spec.dependencyOf` only where the file states it. An entry that is no
component stays where it was, a component the source still names stays as it was
written, and the rest is added in canonical order. The host writes the edits
keeping comments and every other field, and refuses them where the plugin's own
reading of the result disagrees with the source.

`Arrange` has no work for catalog discovery. The SDK accepts an empty request and
refuses invented handed actions. The common real-Wasm harness covers the same
description, options, profiles, errors, tracked reads, cancellation, repetition and
concurrency checks used for Go. API specs live in
[`specs/build/catalog_test.go`](../../specs/build/catalog_test.go).

General plugin rule/rewrite results, CODEOWNERS inference and remote catalog
processing remain separate implementation work.

## The packages

The plugin also builds what the Backstage CLI builds, by the CLI's own
commands, as a repository's `package.json` scripts run them. A Backstage
repository is a Yarn install (`plugins/yarn`) whose root's `package.json`
depends on `@backstage/cli`; its packages are the workspaces that name a
role (`backstage.role`), one of the ten the CLI knows.

| Project | Action | What it runs |
|---|---|---|
| The repository, at the install's root (`backstage-repository`) | `compile/types` | `tsc --incremental false`: the one compilation over every package, as Backstage's `tsc:full` runs it, writing `dist-types/` |
| Each package (`backstage-package`) | `compile` | `backstage-cli package build`; a `backend` adds `--skip-build-dependencies` and packs each workspace package it links as that package's own build left it, into `dist/bundle.tar.gz` |
| | `lint` | `backstage-cli package lint`, a check |
| | `test` | `backstage-cli package test --no-watch --ci`, with `CI=true` |

Each runs the CLI the install laid out (`node_modules/@backstage/cli`) with
the Node `mise.toml` pins, and is handed, each where it was installed, what
its package requires: the install's `node_modules` (`javascript-install:`
the root's name), the repository's declarations and configuration
(`backstage-repository:`, its `package.json`, `tsconfig.json`, `yarn.lock`,
`backstage.json`, `app-config*.yaml` and ESLint's configuration), and each
workspace package it links (`backstage-package:`, its sources and `dist/`),
required at the line of its `package.json` that links it. A package
provides its sources and `dist/`.

What a test reads beyond its package and what it links, its Component's
catalog file names (`spec.frontseat.test.inputs`), as for any test.

The roles' builds read differently, and each is handed the same: a
library's or a plugin's build leaves every import to whoever depends on it
and reads its declarations from `dist-types/`; an app's bundles the
sources of every package it links; a backend's reads what each linked
package's build left. What a build reads that it is not handed fails it,
for want of the file.

A role the CLI does not know is refused `invalid-role`, at its line; a
package that names a role and that no Yarn install serves
`backstage-install-not-served`; and a Yarn install whose root does not
depend on the CLI `missing-cli`. A `package.json` that names no role is no
project of this plugin's.

Not held yet: an app's build is not reproduced byte for byte, since the
CLI writes the time it ran and `git describe` into its bundle
(`BUILD_INFO`), and nothing sets that time; lint's findings are printed,
not read back as findings; Jest sizes its workers by the machine's
processors, not the action's; and a backend's app, packaged to run, is
not built.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name.

```yaml
# frontseat.yaml
plugins:
  backstage: {}
```

## Catalog keys

The plugin reads each document of a `catalog-info.yaml` as Backstage
writes one, and nothing beneath `spec.frontseat`, which is the host's.

| Key | Read by | What it does |
|---|---|---|
| `apiVersion`, `kind` | the plugin | What the document is: a built-in kind is held to `backstage.io/v1alpha1` or `backstage.io/v1beta1`. |
| `metadata.name`, `metadata.namespace` | the plugin | The entity's identity; a namespace left out is `default`, as it is Backstage's. |
| `metadata` (`title`, `description`, `labels`, `annotations`, `tags`, `links`) | the plugin | Held to the shape Backstage gives each, and otherwise kept as written. |
| `spec.type`, `spec.lifecycle`, `spec.owner`, `spec.definition` | the plugin | Required of the kinds Backstage requires them of. |
| `spec.owner`, `spec.system`, `spec.domain`, `spec.subdomainOf`, `spec.subcomponentOf`, `spec.providesApis`, `spec.consumesApis`, `spec.dependsOn`, `spec.dependencyOf`, `spec.parent`, `spec.children`, `spec.members`, `spec.memberOf` | the plugin | The entity's relations, each with its inverse, as Backstage maps them. `dependsOn` and `dependencyOf` between Components are held to what the source says. |
| `metadata.uid`, `metadata.etag`, a Location's `spec.target`, `spec.targets` and `spec.presence`, a Group's `spec.profile`, among others | the plugin | Held to the shape Backstage gives each. |

```yaml
# catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: greet
spec:
  type: service
  lifecycle: production
  owner: team-a
  system: examples
```
