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
role (`backstage.role`), one of the twelve the pinned CLI knows
(`@backstage/cli-node` 0.3.4).

| Project | Action | What it runs |
|---|---|---|
| The repository, at the install's root (`backstage-repository`) | `compile/types` | `tsc --incremental false`: the one compilation over every package, as Backstage's `tsc:full` runs it, writing `dist-types/` |
| Each package (`backstage-package`) | `compile` | `backstage-cli package build`; a `backend` adds `--skip-build-dependencies` and packs each workspace package it links as that package's own build left it, into `dist/bundle.tar.gz` |
| | `lint` | `backstage-cli package lint --format unix`, a check whose findings are reported at their files and lines |
| | `test` | `backstage-cli package test --no-watch --ci`, with `CI=true` |

Each runs the CLI the install laid out (`node_modules/@backstage/cli`) with
the Node `mise.toml` pins, and is handed only what its command reads, each
where it was installed:

| Action | Handed, besides its own package |
|---|---|
| every one | the install's `node_modules` (`javascript-install:`), and the repository's `package.json`, `tsconfig.json`, `yarn.lock`, `backstage.json`, `app-config*.yaml` and ESLint's configuration (`backstage-configuration:`) |
| a library's or a plugin's `compile` | the repository's declarations (`backstage-types:`); its imports are left to whoever depends on it |
| an app's `compile` (`frontend`, `frontend-dynamic-container`) | the sources of every workspace package it links, at any depth (`backstage-sources:`), which it bundles |
| a backend's `compile` | those sources, and what each one's build left (`backstage-built:`), which it packs |
| `test` | the sources of every workspace package it links, which its imports reach |

A package links a workspace by its name, as Yarn links one by any range
its version meets, and each link is required at the line of its
`package.json` that makes it. The repository's project provides every
workspace's sources and requires only the install, since what a project
requires is handed with everything its provider requires: so a package
takes another's sources and nothing else, and two packages that link each
other, a cycle Yarn allows, link sources alone, which no action makes. Only
a package's `dist/` is its own project's to provide.

What a test reads beyond its package and what it links, its Component's
catalog file names (`spec.frontseat.test.inputs`), as for any test.

A `package.json` that names no role, and is no repository's root, is no
concern of this plugin's, whatever else it says or leaves out, and the
catalog file beside it is read as before. A role the pinned CLI does not
know is refused `invalid-role`, at its line; a package that names a role
and that no Yarn install serves `backstage-install-not-served`; a Yarn
install whose root does not depend on the CLI `missing-cli`; and a
repository's root of no name `invalid-manifest`.

Not held yet: an app's build is not reproduced byte for byte, since the
CLI writes the time it ran and `git describe` into its bundle
(`BUILD_INFO`), and nothing sets that time; no app is built by a spec; a
library's build is handed the whole repository's declarations, so a change
to any package's source builds every library again, though nothing it
bundles changed; Jest sizes its workers by the machine's processors, not
the action's; and a backend's app, packaged to run, is not built.

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
